package db_lib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type terraformAgentInstaller struct {
	calls int
	agent ssh.Agent
}

func (i *terraformAgentInstaller) Install(_ db.AccessKey, _ db.AccessKeyRole, _ task_logger.Logger) (ssh.AccessKeyInstallation, error) {
	i.calls++
	return ssh.AccessKeyInstallation{SSHAgent: &i.agent}, nil
}

func newTerraformAgentInstaller(t *testing.T) *terraformAgentInstaller {
	t.Helper()
	shortDirectory, err := os.MkdirTemp("/tmp", "tfa")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(shortDirectory) })
	installer := &terraformAgentInstaller{agent: ssh.Agent{SocketFile: filepath.Join(shortDirectory, "agent.sock"), Logger: task_logger.NopLogger{}}}
	require.NoError(t, installer.agent.Listen())
	t.Cleanup(func() { _ = installer.agent.Close() })
	return installer
}

func TestTerraformInitPreservesTaskAgentEnvironment(t *testing.T) {
	fixture := t.TempDir()
	output := filepath.Join(fixture, "environment")
	binary := filepath.Join(fixture, "terraform")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$SSH_AUTH_SOCK\" \"$GIT_SSH_COMMAND\" \"$GIT_TERMINAL_PROMPT\" > \"$TASK_AGENT_CAPTURE_FILE\"\n"), 0o700))
	previous := util.Config
	util.Config = &util.ConfigType{Apps: map[string]util.App{"terraform": {AppPath: binary}}, TmpPath: fixture, Process: &util.ConfigProcess{}, EnvVars: map[string]string{"TASK_AGENT_CAPTURE_FILE": output}, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previous })

	app := TerraformApp{Logger: task_logger.NopLogger{}, Name: "terraform", Template: db.Template{ID: 1}, Repository: db.Repository{GitURL: fixture}, Inventory: db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH}}}
	app.reader.EOF = true
	installer := &terraformAgentInstaller{}
	err := app.init([]string{"SSH_AUTH_SOCK=/tmp/task-agent.sock", "GIT_SSH_COMMAND=ssh -o IdentityAgent=/tmp/task-agent.sock"}, installer, &db.TerraformTaskParams{}, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, installer.calls)
	contents, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/task-agent.sock\nssh -o IdentityAgent=/tmp/task-agent.sock\n0\n", string(contents))
}

func TestTerraformInitFallsBackToInventoryAgent(t *testing.T) {
	fixture := t.TempDir()
	output := filepath.Join(fixture, "environment")
	binary := filepath.Join(fixture, "terraform")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n%s' \"$SSH_AUTH_SOCK\" \"$GIT_TERMINAL_PROMPT\" > \"$TASK_AGENT_CAPTURE_FILE\"\n"), 0o700))
	previous := util.Config
	util.Config = &util.ConfigType{Apps: map[string]util.App{"terraform": {AppPath: binary}}, TmpPath: fixture, Process: &util.ConfigProcess{}, EnvVars: map[string]string{"TASK_AGENT_CAPTURE_FILE": output}, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previous })
	app := TerraformApp{Logger: task_logger.NopLogger{}, Name: "terraform", Template: db.Template{ID: 1}, Repository: db.Repository{GitURL: fixture}, Inventory: db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH}}}
	app.reader.EOF = true
	installer := newTerraformAgentInstaller(t)
	require.NoError(t, app.init(nil, installer, &db.TerraformTaskParams{}, nil))
	assert.Equal(t, 1, installer.calls)
	contents, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(contents), installer.agent.SocketFile+"\n0"))
	_, statErr := os.Stat(installer.agent.SocketFile)
	assert.True(t, os.IsNotExist(statErr), "legacy agent socket must be removed by Terraform init cleanup")
}

func TestHasEnvironmentVariableUsesEffectiveNonemptyValue(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment []string
		expected    bool
	}{
		{"missing", nil, false}, {"empty", []string{"SSH_AUTH_SOCK="}, false},
		{"last empty", []string{"SSH_AUTH_SOCK=/task", "SSH_AUTH_SOCK="}, false},
		{"last nonempty", []string{"SSH_AUTH_SOCK=", "SSH_AUTH_SOCK=/task"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, hasEnvironmentVariable(test.environment, "SSH_AUTH_SOCK"))
		})
	}
}
