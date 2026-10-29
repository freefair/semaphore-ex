package tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskWithoutSSHKeysGetsPrivateAnsibleControlPath(t *testing.T) {
	configureRepositoryAgentTest(t)
	executor := &LocalExecutor{Template: db.Template{ProjectID: 1}, Logger: task_logger.NopLogger{}}

	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)

	require.NotEmpty(t, executor.taskSSHControlPathDir)
	environment := executor.getTaskSSHAgentEnvironment(nil)
	assert.Equal(t, []string{
		"ANSIBLE_SSH_CONTROL_PATH_DIR=" + executor.taskSSHControlPathDir,
		"ANSIBLE_SSH_CONTROL_PATH=%(directory)s/%%C",
	}, environment)
}

func TestTaskSSHKeyRoutingForcesPrivateControlPath(t *testing.T) {
	configureRepositoryAgentTest(t)
	util.Config.Ssh = &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}
	repositoryPrivateKey, _ := repositoryAgentTestPrivateKey(t)
	extraPrivateKey, _ := repositoryAgentTestPrivateKey(t)
	executor := repositoryAgentTestExecutor(repositoryPrivateKey, "")
	executor.Task.ResolvedSSHKeys = []db.ResolvedTaskSSHKey{{
		Binding: db.SSHKeyBinding{AccessKeyID: 17, Hosts: []string{"web-1.example.test"}},
		Key:     db.AccessKey{ID: 17, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: extraPrivateKey}},
	}}
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	require.NotEmpty(t, executor.taskSSHRoutingCommand)

	// Ansible or a user configuration asks for the shared master socket.
	output, err := exec.Command(executor.taskSSHRoutingCommand, "-G", "-o", "ControlPath=/shared/%C", "web-1.example.test").Output()

	require.NoError(t, err)
	assert.Contains(t, string(output), "\ncontrolpath "+executor.taskSSHControlPathDir+"/")
	assert.NotContains(t, string(output), "/shared/")
}

func TestTaskSSHAgentOverridesInheritedAnsibleControlPath(t *testing.T) {
	configureRepositoryAgentTest(t)
	repositoryPrivateKey, _ := repositoryAgentTestPrivateKey(t)
	executor := repositoryAgentTestExecutor(repositoryPrivateKey, "")
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	inherited := []string{"ANSIBLE_SSH_CONTROL_PATH_DIR=/shared", "ANSIBLE_SSH_CONTROL_PATH=/shared/%%h"}

	environment := append(append([]string(nil), inherited...), executor.getTaskSSHAgentEnvironment(inherited)...)

	assert.Equal(t, executor.taskSSHControlPathDir, lastEnvironmentValue(environment, "ANSIBLE_SSH_CONTROL_PATH_DIR"))
	assert.Equal(t, "%(directory)s/%%C", lastEnvironmentValue(environment, "ANSIBLE_SSH_CONTROL_PATH"))
}

func TestTaskSSHControlPathIsPrivateShortDistinctAndRemoved(t *testing.T) {
	configureRepositoryAgentTest(t)
	first := &LocalExecutor{Template: db.Template{ProjectID: 1}, Logger: task_logger.NopLogger{}}
	second := &LocalExecutor{Template: db.Template{ProjectID: 1}, Logger: task_logger.NopLogger{}}
	require.NoError(t, first.startTaskSSHAgent())
	require.NoError(t, second.startTaskSSHAgent())
	t.Cleanup(second.destroyKeys)
	directory := first.taskSSHControlPathDir
	info, err := os.Stat(directory)
	require.NoError(t, err)

	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.Less(t, len(filepath.Join(directory, ""))+1+40, 104, "ControlPath with %C must stay below OpenSSH's limit")
	assert.NotEqual(t, directory, second.taskSSHControlPathDir)

	first.destroyKeys()
	_, err = os.Stat(directory)
	assert.True(t, os.IsNotExist(err))
	assert.Empty(t, first.taskSSHControlPathDir)
}

func TestContainerEnvironmentDropsRunnerHostControlPath(t *testing.T) {
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previousConfig })
	executor := LocalExecutor{Template: db.Template{ID: 1}, taskSSHControlPathDir: "/tmp/sxcp123"}
	executor.preparedEnv = append([]string{"ANSIBLE_SSH_CONTROL_PATH_DIR=/custom/cp"}, executor.getTaskSSHAgentEnvironment()...)

	environment, err := executor.containerEnvironment()

	require.NoError(t, err)
	assert.Contains(t, environment, "ANSIBLE_SSH_CONTROL_PATH_DIR=/custom/cp")
	assert.NotContains(t, environment, "ANSIBLE_SSH_CONTROL_PATH_DIR=/tmp/sxcp123")
	assert.NotContains(t, environment, "ANSIBLE_SSH_CONTROL_PATH=%(directory)s/%%C")
}

func TestTaskSSHControlPathIsRemovedWhenOwnershipCannotBeSet(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may change ownership to any user")
	}
	configureRepositoryAgentTest(t)
	foreign := uint32(os.Geteuid() + 4242)
	util.Config.Process = &util.ConfigProcess{UID: &foreign, GID: &foreign}
	before, err := filepath.Glob(filepath.Join(taskSSHControlPathParent(), "sxcp*"))
	require.NoError(t, err)
	executor := &LocalExecutor{Template: db.Template{ProjectID: 1}, Logger: task_logger.NopLogger{}}

	err = executor.prepareTaskSSHControlPath()

	assert.ErrorContains(t, err, "securing task SSH control path directory")
	assert.Empty(t, executor.taskSSHControlPathDir)
	after, globErr := filepath.Glob(filepath.Join(taskSSHControlPathParent(), "sxcp*"))
	require.NoError(t, globErr)
	assert.ElementsMatch(t, before, after, "the directory is removed when chown fails")
}
