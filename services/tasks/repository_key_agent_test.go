package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
)

func TestRepositorySSHKeyAgentEnvironment(t *testing.T) {
	executor := LocalExecutor{
		Repository:   db.Repository{SSHKey: db.AccessKey{Type: db.AccessKeySSH}},
		Inventory:    db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeyNone}},
		taskSSHAgent: &ssh.Agent{SocketFile: "/tmp/repository-task-agent.sock"},
	}

	assert.Equal(t, "SSH_AUTH_SOCK=/tmp/repository-task-agent.sock", executor.getSSHAgentEnv())
}

func TestTaskSSHAgentEnvironmentForEveryApp(t *testing.T) {
	for _, app := range []db.TemplateApp{db.AppAnsible, db.AppTerraform, db.AppTofu, db.AppTerragrunt, db.AppBash} {
		t.Run(string(app), func(t *testing.T) {
			executor := LocalExecutor{Template: db.Template{App: app}, taskSSHAgent: &ssh.Agent{SocketFile: "/tmp/task-agent.sock"}}
			assert.Equal(t, "SSH_AUTH_SOCK=/tmp/task-agent.sock", executor.getSSHAgentEnv())
		})
	}
}

func TestTaskSSHAgentEnvironmentIsAbsentWithoutAgent(t *testing.T) {
	assert.Empty(t, (&LocalExecutor{}).getSSHAgentEnv())
}

func TestTaskSSHAgentEnvironmentSelectsRepositoryForGitAndInventoryForAnsible(t *testing.T) {
	executor := LocalExecutor{
		taskSSHAgent:               &ssh.Agent{SocketFile: "/tmp/task-agent.sock"},
		repositorySSHIdentityFiles: []string{"/tmp/repository.pub"},
		inventorySSHIdentityFiles:  []string{"/tmp/inventory.pub"},
	}

	assert.Equal(t, []string{
		"SSH_AUTH_SOCK=/tmp/task-agent.sock",
		"GIT_SSH_COMMAND=ssh -o IdentitiesOnly=yes -o IdentityAgent='/tmp/task-agent.sock' -i '/tmp/repository.pub' -i '/tmp/inventory.pub'",
	}, executor.getTaskSSHAgentEnvironment(nil))
}

func TestTaskSSHAgentEnvironmentUsesInventoryForGitWhenRepositoryHasNoSSHKey(t *testing.T) {
	executor := LocalExecutor{
		taskSSHAgent:              &ssh.Agent{SocketFile: "/tmp/task-agent.sock"},
		inventorySSHIdentityFiles: []string{"/tmp/inventory.pub"},
	}

	assert.Contains(t, executor.getTaskSSHAgentEnvironment(nil), "GIT_SSH_COMMAND=ssh -o IdentitiesOnly=yes -o IdentityAgent='/tmp/task-agent.sock' -i '/tmp/inventory.pub'")
}

func TestTaskSSHAgentEnvironmentPreservesExplicitGitAndAnsibleSelectors(t *testing.T) {
	executor := LocalExecutor{taskSSHAgent: &ssh.Agent{SocketFile: "/tmp/task-agent.sock"}, repositorySSHIdentityFiles: []string{"/tmp/repository.pub"}, inventorySSHIdentityFiles: []string{"/tmp/inventory.pub"}}
	generated := executor.getTaskSSHAgentEnvironment([]string{"GIT_SSH_COMMAND=ssh -F /etc/ssh/custom", "ANSIBLE_PRIVATE_KEY_FILE=/etc/ssh/inventory.pub", "ANSIBLE_SSH_ARGS=-o ControlMaster=auto"})
	assert.Equal(t, []string{"SSH_AUTH_SOCK=/tmp/task-agent.sock"}, generated)
}

func TestEnvironmentHasNameRespectsAdminAndForwardedConfiguration(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	t.Setenv("TASK_TEST_FORWARDED", "forwarded")

	util.Config = &util.ConfigType{EnvVars: map[string]string{"TASK_TEST_ADMIN": ""}, ForwardedEnvVars: []string{"TASK_TEST_FORWARDED"}}
	assert.True(t, environmentHasName(nil, "TASK_TEST_ADMIN"))
	assert.True(t, environmentHasName(nil, "TASK_TEST_FORWARDED"))
	assert.False(t, environmentHasName(nil, "TASK_TEST_MISSING"))
	util.Config = nil
	assert.False(t, environmentHasName(nil, "TASK_TEST_ADMIN"))
}
