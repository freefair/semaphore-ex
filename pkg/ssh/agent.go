package ssh

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/util"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type AgentKey struct {
	Key        []byte
	Passphrase []byte
}

type Agent struct {
	Keys       []AgentKey
	Logger     task_logger.Logger
	SocketFile string
	runtime    *agentRuntime
}

type agentRuntime struct {
	listener      net.Listener
	done          chan struct{}
	closeOnce     sync.Once
	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
}

func NewAgent() Agent {
	return Agent{}
}

func (a *Agent) Listen() error {
	if err := os.MkdirAll(path.Dir(a.SocketFile), 0o755); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	keyring := agent.NewKeyring()

	for _, k := range a.Keys {
		var (
			key any
			err error
		)

		if len(k.Passphrase) == 0 {
			key, err = ssh.ParseRawPrivateKey(k.Key)
		} else {
			key, err = ssh.ParseRawPrivateKeyWithPassphrase(k.Key, k.Passphrase)
		}

		if err != nil {
			return fmt.Errorf("parsing private key: %w", err)
		}

		if err := keyring.Add(agent.AddedKey{
			PrivateKey: key,
		}); err != nil {
			return fmt.Errorf("adding private key: %w", err)
		}

	}

	l, err := net.ListenUnix(
		"unix",
		&net.UnixAddr{
			Net:  "unix",
			Name: a.SocketFile,
		},
	)
	if err != nil {
		return fmt.Errorf("listening on socket %q: %w", a.SocketFile, err)
	}

	l.SetUnlinkOnClose(true)
	runtime := &agentRuntime{listener: l, done: make(chan struct{}), connections: make(map[net.Conn]struct{})}
	a.runtime = runtime

	go func() {
		for {
			conn, err := runtime.listener.Accept()
			if err != nil {
				select {
				case <-runtime.done:
					return
				default:
					a.Logger.Logf("error accepting socket connection: %w", err)
					return
				}
			}

			runtime.connectionsMu.Lock()
			select {
			case <-runtime.done:
				runtime.connectionsMu.Unlock()
				_ = conn.Close()
				return
			default:
				runtime.connections[conn] = struct{}{}
				runtime.connectionsMu.Unlock()
			}
			go func(conn net.Conn) {
				defer func() {
					runtime.connectionsMu.Lock()
					delete(runtime.connections, conn)
					runtime.connectionsMu.Unlock()
					_ = conn.Close()
				}()

				// ServeAgent only returns once the connection breaks; io.EOF just
				// means the client went away. staticcheck knows ServeAgent never
				// returns nil, so the defensive nil check needs the nolint.
				if err := agent.ServeAgent(keyring, conn); err != nil && !errors.Is(err, io.EOF) { //nolint:staticcheck // SA4023
					a.Logger.Logf("error serving SSH agent listener: %w", err)
				}
			}(conn)
		}
	}()

	return nil
}

func (a *Agent) Close() error {
	if a.runtime == nil {
		return nil
	}
	var closeErr error
	a.runtime.closeOnce.Do(func() {
		close(a.runtime.done)
		if a.runtime.listener != nil {
			closeErr = a.runtime.listener.Close()
		}
		a.runtime.connectionsMu.Lock()
		for connection := range a.runtime.connections {
			if err := connection.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		a.runtime.connectionsMu.Unlock()
	})
	return closeErr
}

func StartSSHAgent(key db.AccessKey, logger task_logger.Logger) (Agent, error) {
	return StartSSHAgentWithKeys(AgentKeys(key), key.ProjectID, logger)
}

// AgentKeys returns SSH key material suitable for one in-process agent. It skips
// non-SSH access keys and keeps the first occurrence of duplicate key material so
// callers can safely compose credentials from independent task bindings.
func AgentKeys(keys ...db.AccessKey) []AgentKey {
	result := make([]AgentKey, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key.Type != db.AccessKeySSH {
			continue
		}
		identity := key.SshKey.PrivateKey + "\x00" + key.SshKey.Passphrase
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, AgentKey{Key: []byte(key.SshKey.PrivateKey), Passphrase: []byte(key.SshKey.Passphrase)})
	}
	return result
}

// PublicIdentity returns a stable identity derived from the public half of an
// SSH access key. It deliberately does not use an access-key ID or PEM bytes:
// the same public credential can appear through repository, inventory and task
// bindings and must count only once for routing.
func PublicIdentity(key db.AccessKey) (string, error) {
	if key.Type != db.AccessKeySSH {
		return "", nil
	}
	var (
		privateKey any
		err        error
	)
	if key.SshKey.Passphrase == "" {
		privateKey, err = ssh.ParseRawPrivateKey([]byte(key.SshKey.PrivateKey))
	} else {
		privateKey, err = ssh.ParseRawPrivateKeyWithPassphrase([]byte(key.SshKey.PrivateKey), []byte(key.SshKey.Passphrase))
	}
	if err != nil {
		return "", fmt.Errorf("parsing SSH public identity: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("creating SSH public identity: %w", err)
	}
	return ssh.FingerprintSHA256(signer.PublicKey()), nil
}

// StartSSHAgentWithKeys starts one task-scoped SSH agent for a composed key set.
// The private material remains only in the in-process keyring after Listen parses it.
func StartSSHAgentWithKeys(keys []AgentKey, projectID *int, logger task_logger.Logger) (Agent, error) {
	if len(keys) == 0 {
		return Agent{}, nil
	}

	socketFilename := fmt.Sprintf("ssh-agent-%s.sock", random.String(10))

	var socketFile string

	if projectID == nil {
		socketFile = path.Join(util.Config.TmpPath, socketFilename)
	} else {
		socketFile = path.Join(util.Config.GetProjectTmpDir(*projectID), socketFilename)
	}

	sshAgent := Agent{
		Logger:     logger,
		Keys:       keys,
		SocketFile: socketFile,
	}

	err := sshAgent.Listen()
	return sshAgent, err
}

// TaskGitSSHCommand preserves the configured host-key policy while selecting
// task-scoped agent identities explicitly.
func TaskGitSSHCommand(socketFile string, identityFiles []string) string {
	command := "ssh -o IdentitiesOnly=yes -o IdentityAgent=" + shellQuote(socketFile)
	for _, identityFile := range identityFiles {
		command += " -i " + shellQuote(identityFile)
	}
	if util.Config == nil || util.Config.Ssh == nil {
		return command
	}
	command += " " + gitHostKeyCheckingOpts()
	if util.Config.GetSshConfigPath() != "" {
		command += " -F " + shellQuote(util.Config.GetSshConfigPath())
	}
	return command
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type AccessKeyInstallation struct {
	SSHAgent *Agent
	Login    string
	Password string
	Script   string
}

func (key *AccessKeyInstallation) GetGitEnv() (env []string) {
	return key.GetGitEnvWithHostConfigs(nil)
}

func (key *AccessKeyInstallation) GetGitEnvWithHostConfigs(hostConfigs *HostConfigInstallation) (env []string) {
	env = make([]string, 0)

	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if key.SSHAgent != nil {
		env = append(env, fmt.Sprintf("SSH_AUTH_SOCK=%s", key.SSHAgent.SocketFile))
	}
	if key.SSHAgent != nil || hostConfigs.SSHConfigPath() != "" {
		configPath := hostConfigs.SSHConfigPath()
		if configPath == "" {
			configPath = util.Config.GetSshConfigPath()
		}
		sshCmd := "ssh " + gitHostKeyCheckingOpts()
		if configPath != "" {
			sshCmd += " -F " + shellQuote(configPath)
		}
		env = append(env, fmt.Sprintf("GIT_SSH_COMMAND=%s", sshCmd))
	}
	if params := hostConfigs.GitConfigParameters(); params != "" {
		env = append(env, "GIT_CONFIG_PARAMETERS="+params)
	}

	return env
}

// gitHostKeyCheckingOpts returns shell-quoted host-key policy arguments, without
// an ssh executable prefix, for Git SSH commands. An empty known_hosts setting
// uses the TmpPath fallback. The configured policy is preserved: yes remains
// strict, accept-new pins a first-seen key, and no explicitly disables checking.
func gitHostKeyCheckingOpts() string {
	knownHostsFile := util.Config.Ssh.KnownHostsFile
	if knownHostsFile == "" {
		knownHostsFile = path.Join(util.Config.TmpPath, "known_hosts")
	}

	switch util.Config.Ssh.StrictHostKeyChecking {
	case util.SshStrictHostKeyCheckingYes:
		return "-o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + shellQuote(knownHostsFile)
	case util.SshStrictHostKeyCheckingNo:
		return "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
	case util.SshStrictHostKeyCheckingAcceptNew:
		return "-o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + shellQuote(knownHostsFile)
	default:
		panic("Unknown SSH strict host key check option")
	}
}

func (key *AccessKeyInstallation) Destroy() error {
	if key.SSHAgent != nil {
		return key.SSHAgent.Close()
	}
	return nil
}

type KeyInstaller struct{}

func (KeyInstaller) Install(key db.AccessKey, usage db.AccessKeyRole, logger task_logger.Logger) (installation AccessKeyInstallation, err error) {

	switch usage {
	case db.AccessKeyRoleGit:
		switch key.Type {
		case db.AccessKeySSH:
			var agent Agent
			agent, err = StartSSHAgent(key, logger)
			installation.SSHAgent = &agent
			installation.Login = key.SshKey.Login
		}
	case db.AccessKeyRoleAnsiblePasswordVault:
		switch key.Type {
		case db.AccessKeyLoginPassword:
			installation.Password = key.LoginPassword.Password
		default:
			err = fmt.Errorf("access key type not supported for ansible password vault")
		}
	case db.AccessKeyRoleAnsibleBecomeUser:
		if key.Type != db.AccessKeyLoginPassword {
			err = fmt.Errorf("access key type not supported for ansible become user")
		}
		installation.Login = key.LoginPassword.Login
		installation.Password = key.LoginPassword.Password
	case db.AccessKeyRoleAnsibleUser:
		switch key.Type {
		case db.AccessKeySSH:
			var agent Agent
			agent, err = StartSSHAgent(key, logger)
			installation.SSHAgent = &agent
			installation.Login = key.SshKey.Login
		case db.AccessKeyLoginPassword:
			installation.Login = key.LoginPassword.Login
			installation.Password = key.LoginPassword.Password
		case db.AccessKeyNone:
			// No SSH agent or password needed for ansible user with no access key.
		default:
			err = fmt.Errorf("access key type not supported for ansible user")
		}
	}

	return
}
