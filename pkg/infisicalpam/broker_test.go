package infisicalpam_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// privateSocketPath returns a short path: macOS limits Unix socket paths to
// 104 bytes, which t.TempDir names can exceed.
func privateSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "pam")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	require.NoError(t, os.Chmod(directory, 0o700))
	return filepath.Join(directory, "broker.sock")
}

func startBroker(t *testing.T, env *pamtest.Environment, logs *[]string) *infisicalpam.Broker {
	t.Helper()
	broker, err := infisicalpam.StartBroker(infisicalpam.BrokerConfig{
		Client: newTestClient(t, env, pamtest.ClientSecret),
		Routes: []infisicalpam.Route{
			{Host: "web-1.example.com", AccountPath: "servers/web-1"},
			{Host: "db-1", AccountPath: "servers/db-1"},
		},
		Duration:   time.Hour,
		Reason:     "Semaphore task 42",
		SocketPath: privateSocketPath(t),
		Log: func(line string) {
			if logs != nil {
				*logs = append(*logs, line)
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(broker.Close)
	return broker
}

// pipeConn joins the two pipes Connect uses into the net.Conn the SSH client
// expects, as OpenSSH does with a ProxyCommand's stdin and stdout.
type pipeConn struct {
	io.Reader
	io.WriteCloser
}

func (pipeConn) LocalAddr() net.Addr              { return &net.UnixAddr{Name: "proxy", Net: "unix"} }
func (pipeConn) RemoteAddr() net.Addr             { return &net.UnixAddr{Name: "proxy", Net: "unix"} }
func (pipeConn) SetDeadline(time.Time) error      { return nil }
func (pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (pipeConn) SetWriteDeadline(time.Time) error { return nil }
func (c pipeConn) Close() error                   { return c.WriteCloser.Close() }

func proxyExec(t *testing.T, socket, host, command string) (string, error) {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	connectErr := make(chan error, 1)
	go func() {
		err := infisicalpam.Connect(context.Background(), socket, host, stdinReader, stdoutWriter)
		// A ProxyCommand that exits closes both pipes; without this the SSH
		// client blocks writing its version banner after a refused connect.
		_ = stdinReader.CloseWithError(io.ErrClosedPipe)
		_ = stdoutWriter.CloseWithError(io.EOF)
		connectErr <- err
	}()
	sshConn, channels, requests, err := ssh.NewClientConn(pipeConn{Reader: stdoutReader, WriteCloser: stdinWriter}, host, &ssh.ClientConfig{
		User:            "ignored",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		_ = stdinWriter.Close()
		return "", <-connectErr
	}
	client := ssh.NewClient(sshConn, channels, requests)
	session, err := client.NewSession()
	require.NoError(t, err)
	output, err := session.Output(command)
	require.NoError(t, err)
	_ = client.Close()
	return string(output), <-connectErr
}

func TestBrokerOpensSessionsLazilyAndReusesThem(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	var logs []string
	broker := startBroker(t, env, &logs)
	assert.Empty(t, env.Accesses(), "no session before the first connection")

	first, err := proxyExec(t, broker.SocketPath(), "WEB-1.example.com", "hostname")
	require.NoError(t, err)
	second, err := proxyExec(t, broker.SocketPath(), "web-1.example.com", "id")
	require.NoError(t, err)

	assert.Equal(t, "ok hostname\n", first)
	assert.Equal(t, "ok id\n", second)
	require.Len(t, env.Accesses(), 1)
	assert.Equal(t, "servers/web-1", env.Accesses()[0].Path)
	assert.Equal(t, "Semaphore task 42", env.Accesses()[0].Reason)
	assert.Equal(t, []string{"hostname", "id"}, env.Commands())
	assert.Contains(t, logs, "Infisical PAM session opened for servers/web-1")
}

func TestBrokerCloseEndsSessionsAndRemovesSocket(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	broker := startBroker(t, env, nil)
	_, err := proxyExec(t, broker.SocketPath(), "db-1", "true")
	require.NoError(t, err)

	broker.Close()
	broker.Close()

	assert.Equal(t, []string{"session-1"}, env.Ended())
	assert.Eventually(t, func() bool { return len(env.Cancelled()) == 1 }, 2*time.Second, 10*time.Millisecond)
	_, statErr := os.Stat(broker.SocketPath())
	assert.True(t, os.IsNotExist(statErr))
}

func TestBrokerRejectsUnroutedHost(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	broker := startBroker(t, env, nil)

	_, err := proxyExec(t, broker.SocketPath(), "other.example.com", "true")

	assert.Equal(t, infisicalpam.ErrorCategory("not_routed"), infisicalpam.CategoryOf(err))
	assert.Empty(t, env.Accesses())
}

func TestBrokerReportsAccessCategory(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{AccessError: "PAM_APPROVAL_REQUIRED"})
	var logs []string
	broker := startBroker(t, env, &logs)

	_, err := proxyExec(t, broker.SocketPath(), "db-1", "true")

	assert.Equal(t, infisicalpam.ErrorApprovalRequired, infisicalpam.CategoryOf(err))
	assert.Contains(t, logs, "Infisical PAM connection to db-1 via servers/db-1 failed: approval_required")
}

func TestBrokerRequiresPrivateSocketDirectory(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	socket := privateSocketPath(t)
	require.NoError(t, os.Chmod(filepath.Dir(socket), 0o755))

	_, err := infisicalpam.StartBroker(infisicalpam.BrokerConfig{
		Client:     newTestClient(t, env, pamtest.ClientSecret),
		Routes:     []infisicalpam.Route{{Host: "db-1", AccountPath: "servers/db-1"}},
		Duration:   time.Hour,
		SocketPath: socket,
	})

	assert.Equal(t, infisicalpam.ErrorValidation, infisicalpam.CategoryOf(err))
}

func TestBrokerSocketIsOwnerOnly(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	broker := startBroker(t, env, nil)

	info, err := os.Stat(broker.SocketPath())

	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestValidateRoutes(t *testing.T) {
	tests := []struct {
		name   string
		routes []infisicalpam.Route
		valid  bool
	}{
		{"valid", []infisicalpam.Route{{Host: "web-1", AccountPath: "a/b"}}, true},
		{"duplicate host ignores case", []infisicalpam.Route{{Host: "web", AccountPath: "a/b"}, {Host: "WEB", AccountPath: "a/c"}}, false},
		{"config syntax in host", []infisicalpam.Route{{Host: "web\n  ProxyCommand x", AccountPath: "a/b"}}, false},
		{"wildcard host", []infisicalpam.Route{{Host: "*.example.com", AccountPath: "a/b"}}, false},
		{"bad path", []infisicalpam.Route{{Host: "web", AccountPath: "b"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, infisicalpam.ValidateRoutes(tt.routes) == nil)
		})
	}
}
