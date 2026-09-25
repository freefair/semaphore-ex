package tasks

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/util"
)

func (t *LocalExecutor) prepareTaskSSHRouting() error {
	if t.taskSSHAgent == nil {
		return nil
	}
	identities, err := t.taskRoutingIdentities()
	if err != nil {
		return err
	}
	routing, err := ssh.BuildHostRouting(identities)
	if err != nil {
		return fmt.Errorf("building task SSH host routing: %w", err)
	}
	if !routing.RequiresRouting() && len(routing.RoutedHosts()) == 0 {
		if t.hostConfigInstallation == nil || t.hostConfigInstallation.SSHConfigPath() == "" {
			return nil
		}
	}
	if err := t.rejectHostConfigRoutingConflicts(routing.RoutedHosts()); err != nil {
		return err
	}
	return t.writeTaskSSHRoutingFiles(routing)
}

func (t *LocalExecutor) rejectHostConfigRoutingConflicts(taskHosts []string) error {
	for _, mapping := range t.HostConfigs {
		if mapping.Type == db.HostConfigHost {
			for _, host := range taskHosts {
				if strings.EqualFold(mapping.Name, host) {
					return fmt.Errorf("host mapping %q conflicts with an explicit task SSH route", mapping.Name)
				}
			}
		}
	}
	return nil
}

func (t *LocalExecutor) taskRoutingIdentities() ([]ssh.RoutingIdentity, error) {
	identities := make([]ssh.RoutingIdentity, 0, 2+len(t.Task.ResolvedSSHKeys))
	appendIdentity := func(key db.AccessKey, selector string, hosts, implicitHosts []string) error {
		if selector == "" || key.Type != db.AccessKeySSH {
			return nil
		}
		publicKey, err := ssh.PublicIdentity(key)
		if err != nil {
			return err
		}
		identities = append(identities, ssh.RoutingIdentity{PublicKey: publicKey, Selector: selector, Hosts: hosts, ImplicitHosts: implicitHosts})
		return nil
	}
	if err := appendIdentity(t.Inventory.SSHKey, firstSelector(t.inventorySSHIdentityFiles), nil, nil); err != nil {
		return nil, fmt.Errorf("inventory SSH identity: %w", err)
	}
	if err := appendIdentity(t.Repository.SSHKey, firstSelector(t.repositorySSHIdentityFiles), nil, repositorySSHHosts(t.Repository.GitURL)); err != nil {
		return nil, fmt.Errorf("repository SSH identity: %w", err)
	}
	for index, resolved := range t.Task.ResolvedSSHKeys {
		if err := appendIdentity(resolved.Key, firstSelector(t.extraSSHIdentityFiles[index:index+1]), resolved.Binding.Hosts, nil); err != nil {
			return nil, fmt.Errorf("task SSH identity %d: %w", resolved.Binding.AccessKeyID, err)
		}
	}
	return identities, nil
}

func firstSelector(selectors []string) string {
	if len(selectors) == 0 {
		return ""
	}
	return selectors[0]
}

func repositorySSHHosts(gitURL string) []string {
	if parsed, err := url.Parse(gitURL); err == nil && parsed.Hostname() != "" {
		return []string{strings.ToLower(parsed.Hostname())}
	}
	if at := strings.LastIndex(gitURL, "@"); at >= 0 {
		hostPath := gitURL[at+1:]
		if colon := strings.IndexByte(hostPath, ':'); colon > 0 {
			return []string{strings.ToLower(hostPath[:colon])}
		}
	}
	return nil
}

func (t *LocalExecutor) writeTaskSSHRoutingFiles(routing ssh.HostRouting) error {
	directory := filepath.Dir(t.taskSSHAgent.SocketFile)
	prefix := filepath.Base(t.taskSSHAgent.SocketFile) + ".routing"
	routingDirectory := filepath.Join(directory, prefix)
	if err := os.Mkdir(routingDirectory, 0o700); err != nil {
		return fmt.Errorf("creating task SSH routing directory: %w", err)
	}
	config := filepath.Join(routingDirectory, "routing.conf")
	wrapper := filepath.Join(routingDirectory, "ssh")
	sshBinary, err := exec.LookPath("ssh")
	if err != nil {
		_ = os.RemoveAll(routingDirectory)
		return fmt.Errorf("locating system SSH client: %w", err)
	}
	sshBinary, err = filepath.Abs(sshBinary)
	if err != nil {
		_ = os.RemoveAll(routingDirectory)
		return fmt.Errorf("resolving system SSH client path: %w", err)
	}
	configContents := ""
	if t.hostConfigInstallation != nil && t.hostConfigInstallation.SSHConfigPath() != "" {
		mapped, readErr := os.ReadFile(t.hostConfigInstallation.SSHConfigPath())
		if readErr != nil {
			_ = os.RemoveAll(routingDirectory)
			return fmt.Errorf("reading host mapping SSH config: %w", readErr)
		}
		// OpenSSH uses the first value it reads. Mappings must precede the
		// task routing's Host * defaults so their agent socket is effective.
		configContents += string(mapped) + "\n"
	}
	configContents += routing.Config(t.taskSSHAgent.SocketFile)
	if err := os.WriteFile(config, []byte(taskSSHRoutingConfig(configContents)), 0o600); err != nil {
		_ = os.RemoveAll(routingDirectory)
		return fmt.Errorf("writing task SSH routing config: %w", err)
	}
	if err := os.WriteFile(wrapper, []byte(taskSSHRoutingWrapper(sshBinary, config)), 0o700); err != nil {
		_ = os.RemoveAll(routingDirectory)
		return fmt.Errorf("writing task SSH routing wrapper: %w", err)
	}
	t.taskSSHRoutingFiles = []string{routingDirectory}
	t.taskSSHRoutingCommand = wrapper
	return nil
}

func taskSSHRoutingConfig(config string) string {
	var policy strings.Builder
	policy.WriteString(config)
	configPath := ""
	if util.Config != nil {
		configPath = util.Config.GetSshConfigPath()
	}
	if util.Config != nil && util.Config.Ssh != nil {
		policy.WriteString("Host *\n")
		switch util.Config.Ssh.StrictHostKeyChecking {
		case util.SshStrictHostKeyCheckingYes:
			policy.WriteString("  StrictHostKeyChecking yes\n  UserKnownHostsFile ")
			policy.WriteString(sshConfigPathValue(util.Config.Ssh.KnownHostsFile))
			policy.WriteString("\n")
		case util.SshStrictHostKeyCheckingNo:
			policy.WriteString("  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n")
		case util.SshStrictHostKeyCheckingAcceptNew:
			policy.WriteString("  StrictHostKeyChecking accept-new\n  UserKnownHostsFile ")
			policy.WriteString(sshConfigPathValue(util.Config.Ssh.KnownHostsFile))
			policy.WriteString("\n")
		}
	}
	if configPath != "" {
		policy.WriteString(taskSSHConfigIncludes(configPath))
	} else {
		// ssh -F suppresses OpenSSH's normal system and user configuration. Keep
		// that inheritance so HostName, Port and ProxyJump defaults still work;
		// routing options precede these includes and remain authoritative.
		policy.WriteString(taskSSHConfigIncludes("~/.ssh/config", "/etc/ssh/ssh_config"))
	}
	return policy.String()
}

func taskSSHConfigIncludes(paths ...string) string {
	var includes strings.Builder
	for _, configPath := range paths {
		includes.WriteString("Include ")
		includes.WriteString(sshConfigPathValue(configPath))
		includes.WriteString("\n")
	}
	return includes.String()
}

func sshConfigPathValue(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), `"`, `\"`) + `"`
}

func taskSSHRoutingWrapper(sshBinary, config string) string {
	return "#!/bin/sh\n" +
		"set -eu\n" +
		"exec " + posixQuote(sshBinary) + " -F " + posixQuote(config) + " \"$@\"\n"
}

func (t *LocalExecutor) removeTaskSSHRoutingFiles() {
	for _, filename := range t.taskSSHRoutingFiles {
		_ = os.RemoveAll(filename)
	}
	t.taskSSHRoutingFiles = nil
	t.taskSSHRoutingCommand = ""
}
