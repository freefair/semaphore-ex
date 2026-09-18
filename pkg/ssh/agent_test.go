package ssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sshcrypto "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestAgentKeysComposesRepositoryAndInventoryKeys(t *testing.T) {
	sshKey := func(id int, privateKey string) db.AccessKey {
		return db.AccessKey{ID: id, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: privateKey}}
	}

	tests := []struct {
		name     string
		keys     []db.AccessKey
		expected []AgentKey
	}{
		{"repository key", []db.AccessKey{sshKey(2, "repository")}, []AgentKey{{Key: []byte("repository"), Passphrase: []byte{}}}},
		{"inventory and repository keys", []db.AccessKey{sshKey(1, "inventory"), sshKey(2, "repository")}, []AgentKey{{Key: []byte("inventory"), Passphrase: []byte{}}, {Key: []byte("repository"), Passphrase: []byte{}}}},
		{"inventory key", []db.AccessKey{sshKey(1, "inventory")}, []AgentKey{{Key: []byte("inventory"), Passphrase: []byte{}}}},
		{"none and login password keys", []db.AccessKey{{Type: db.AccessKeyNone}, {Type: db.AccessKeyLoginPassword}}, []AgentKey{}},
		{"same key is offered once", []db.AccessKey{sshKey(1, "repository"), sshKey(2, "repository")}, []AgentKey{{Key: []byte("repository"), Passphrase: []byte{}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, AgentKeys(test.keys...))
		})
	}
}

func TestTaskGitSSHCommandPreservesHostPolicyAndQuotesPaths(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })

	tests := []struct {
		name     string
		policy   util.SshStrictHostKeyChecking
		expected string
	}{
		{"strict", util.SshStrictHostKeyCheckingYes, "-o StrictHostKeyChecking=yes -o UserKnownHostsFile='/tmp/known hosts $(literal)'"},
		{"accept new", util.SshStrictHostKeyCheckingAcceptNew, "-o StrictHostKeyChecking=accept-new -o UserKnownHostsFile='/tmp/known hosts $(literal)'"},
		{"disabled", util.SshStrictHostKeyCheckingNo, "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			util.Config = &util.ConfigType{Ssh: &util.SshConfig{StrictHostKeyChecking: test.policy, KnownHostsFile: "/tmp/known hosts $(literal)", ConfigPath: "/tmp/config ' $()"}}
			command := TaskGitSSHCommand("/tmp/agent $sock", []string{"/tmp/repository'`key`"})
			assert.Contains(t, command, "ssh -o IdentitiesOnly=yes -o IdentityAgent='/tmp/agent $sock' -i '/tmp/repository'\"'\"'`key`'")
			assert.Contains(t, command, test.expected)
			assert.Contains(t, command, "-F '/tmp/config '\"'\"' $()'")
			assert.NotContains(t, command, "ssh ssh")

			fakeSSHDirectory := t.TempDir()
			fakeSSH := filepath.Join(fakeSSHDirectory, "ssh")
			require.NoError(t, os.WriteFile(fakeSSH, []byte("#!/bin/sh\nprintf '%s\\0' \"$@\"\n"), 0o700))
			run := exec.Command("sh", "-c", command+" fixture-host")
			run.Env = append(os.Environ(), "PATH="+fakeSSHDirectory+":"+os.Getenv("PATH"))
			output, err := run.Output()
			require.NoError(t, err)
			arguments := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
			assert.Contains(t, arguments, "IdentityAgent=/tmp/agent $sock")
			assert.Contains(t, arguments, "/tmp/repository'`key`")
			if test.policy != util.SshStrictHostKeyCheckingNo {
				assert.Contains(t, arguments, "UserKnownHostsFile=/tmp/known hosts $(literal)")
			}
			assert.Contains(t, arguments, "/tmp/config ' $()")
		})
	}
}

func TestAgentCloseTerminatesActiveConnectionsAndRemovesPublicIdentities(t *testing.T) {
	setupShortAgentTestConfig(t)

	privateKey := testPrivateKey(t)
	sshAgent, err := StartSSHAgentWithKeys([]AgentKey{{Key: privateKey}}, nil, task_logger.NopLogger{})
	require.NoError(t, err)

	connection, err := net.Dial("unix", sshAgent.SocketFile)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	client := agent.NewClient(connection)
	_, err = client.List()
	require.NoError(t, err)
	require.NoError(t, sshAgent.Close())
	_, err = client.List()
	assert.Error(t, err)
	_, err = os.Stat(sshAgent.SocketFile)
	assert.True(t, os.IsNotExist(err))
}

func testPrivateKey(t *testing.T) []byte {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}

func TestAgentPublicIdentitySelectsKeyWithOpenSSHIdentitiesOnly(t *testing.T) {
	setupShortAgentTestConfig(t)

	clientPrivateKey := testPrivateKey(t)
	clientSigner, err := sshcrypto.ParsePrivateKey(clientPrivateKey)
	require.NoError(t, err)
	listener, address := startTestSSHServer(t, clientSigner.PublicKey())
	t.Cleanup(func() { _ = listener.Close() })

	sshAgent, err := StartSSHAgentWithKeys([]AgentKey{{Key: clientPrivateKey}}, nil, task_logger.NopLogger{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sshAgent.Close()) })
	publicIdentity := filepath.Join(util.Config.TmpPath, "repository-key.pub")
	require.NoError(t, os.WriteFile(publicIdentity, sshcrypto.MarshalAuthorizedKey(clientSigner.PublicKey()), 0o600))

	command := exec.Command("ssh", "-F", "none", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent="+sshAgent.SocketFile, "-i", publicIdentity, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-p", strconv.Itoa(address.Port), "git@127.0.0.1", "true")
	command.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sshAgent.SocketFile)
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "OpenSSH did not offer the agent key selected by its public identity: %s", output)
}

func setupShortAgentTestConfig(t *testing.T) {
	t.Helper()
	tmp, err := os.MkdirTemp("/tmp", "ssh")
	require.NoError(t, err)
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: tmp}
	t.Cleanup(func() {
		util.Config = previousConfig
		_ = os.RemoveAll(tmp)
	})
}

func startTestSSHServer(t *testing.T, allowedKey sshcrypto.PublicKey) (net.Listener, *net.TCPAddr) {
	t.Helper()
	hostSigner, err := sshcrypto.ParsePrivateKey(testPrivateKey(t))
	require.NoError(t, err)
	serverConfig := &sshcrypto.ServerConfig{
		PublicKeyCallback: func(_ sshcrypto.ConnMetadata, key sshcrypto.PublicKey) (*sshcrypto.Permissions, error) {
			if bytes.Equal(key.Marshal(), allowedKey.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unexpected client public key")
		},
	}
	serverConfig.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveTestSSHConnection(connection, serverConfig)
		}
	}()
	return listener, address
}

func serveTestSSHConnection(connection net.Conn, config *sshcrypto.ServerConfig) {
	serverConnection, channels, requests, err := sshcrypto.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer serverConnection.Close()
	go sshcrypto.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(sshcrypto.UnknownChannelType, "only session channels are supported")
			continue
		}
		session, requests, err := channel.Accept()
		if err != nil {
			continue
		}
		for request := range requests {
			if request.Type == "exec" {
				_ = request.Reply(true, nil)
				_, _ = session.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
				_ = session.Close()
				break
			}
			_ = request.Reply(false, nil)
		}
	}
}

// TestAgent_Listen_CreatesSocketDir tests that Listen() creates the socket's
// parent directory if it doesn't exist (e.g. project tmp dir not yet created).
func TestAgent_Listen_CreatesSocketDir(t *testing.T) {
	// Not t.TempDir(): its path exceeds the ~104-byte unix socket limit on macOS.
	tmp, err := os.MkdirTemp("/tmp", "ssh")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmp) }) //nolint:errcheck

	agent := Agent{
		SocketFile: filepath.Join(tmp, "project_2", "agent.sock"),
	}

	err = agent.Listen()
	require.NoError(t, err)
	require.NoError(t, agent.Close())
}

// TestAgent_Close_WithNilListener tests that Close() doesn't panic when listener is nil
func TestAgent_Close_WithNilListener(t *testing.T) {
	// Create agent with nil listener (simulates failed initialization)
	agent := Agent{}

	// This should not panic
	err := agent.Close()
	if err != nil {
		t.Errorf("Expected no error when closing agent with nil listener, got: %v", err)
	}
}

// TestAgent_Close_WithNilDone tests that Close() doesn't panic when done channel is nil
func TestAgent_Close_WithNilDone(t *testing.T) {
	// Create agent with nil done channel
	agent := Agent{}

	// This should not panic
	err := agent.Close()
	if err != nil {
		t.Errorf("Expected no error when closing agent with nil done channel, got: %v", err)
	}
}

// TestAgent_Close_WithAllNil tests that Close() doesn't panic when both fields are nil
func TestAgent_Close_WithAllNil(t *testing.T) {
	// Create completely empty agent (simulates NewAgent() result)
	agent := NewAgent()

	// This should not panic
	err := agent.Close()
	if err != nil {
		t.Errorf("Expected no error when closing empty agent, got: %v", err)
	}
}

// TestAgent_Close_FailedInitialization simulates the exact scenario from issue #3232
// where agent initialization fails but the agent is still assigned to installation
func TestAgent_Close_FailedInitialization(t *testing.T) {
	// Simulate the scenario described in the issue:
	// 1. StartSSHAgent() fails during Listen() but returns incomplete agent
	// 2. Install() method assigns the incomplete agent to installation.SSHAgent
	// 3. Later, destroyKeys() calls Destroy() which calls Close() on incomplete agent

	// Create an agent that would be returned by StartSSHAgent() if Listen() failed
	incompleteAgent := Agent{
		Keys: []AgentKey{
			{
				Key:        []byte("test-private-key"),
				Passphrase: []byte(""),
			},
		},
		SocketFile: "/tmp/test-socket.sock",
		// listener and done are nil because Listen() failed
	}

	// This simulates the destroyKeys() -> Destroy() -> Close() call chain
	// that was causing the panic
	err := incompleteAgent.Close()
	if err != nil {
		t.Errorf("Expected no error when closing incomplete agent, got: %v", err)
	}
}
