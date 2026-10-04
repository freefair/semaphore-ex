package tasks

import (
	"os"
	"os/exec"
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

func TestWorkflowIdentitySurvivesExecutionPreparation(t *testing.T) {
	for _, app := range []db.TemplateApp{db.AppBash, db.AppAnsible, db.AppTerraform} {
		for _, container := range []bool{false, true} {
			name := string(app) + "/local"
			if container {
				name = string(app) + "/container"
			}
			t.Run(name, func(t *testing.T) {
				previous := util.Config
				util.Config = &util.ConfigType{TmpPath: t.TempDir(), WebHost: "https://semaphore.example.test", Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingAcceptNew}, Process: &util.ConfigProcess{}}
				t.Cleanup(func() { util.Config = previous })
				repository := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repository, "run.sh"), []byte("#!/bin/sh\n"), 0600))
				runID, workflowID := 42, 7
				executor := &LocalExecutor{
					Task:         db.Task{ID: 11, ProjectID: 3, WorkflowRunID: &runID, WorkflowTemplateID: &workflowID},
					Template:     db.Template{ID: 5, ProjectID: 3, App: app, Playbook: "run.sh"},
					Repository:   db.Repository{ID: 6, ProjectID: 3, GitURL: repository},
					Inventory:    db.Inventory{ID: 8, ProjectID: 3, Type: db.InventoryStatic, Inventory: "localhost ansible_connection=local"},
					KeyInstaller: ssh.KeyInstaller{}, Logger: task_logger.NopLogger{}, App: &sshLifecycleApp{},
				}
				t.Cleanup(executor.Cleanup)
				expected := []string{"SEMAPHORE_PROJECT_ID=3", "SEMAPHORE_TASK_ID=11", "SEMAPHORE_WORKFLOW_RUN_ID=42", "SEMAPHORE_WORKFLOW_ID=7", "SEMAPHORE_WORKFLOW_URL=https://semaphore.example.test/project/3/workflows/7/runs/42"}
				if container {
					plan, err := executor.PrepareContainerTask("", nil, "")
					require.NoError(t, err)
					entries := readTaskBundle(t, plan.Bundle)
					require.NoError(t, plan.Bundle.Close())
					script := string(entries["credentials/environment.sh"]) + "\nenv\n"
					command := exec.Command("/bin/sh", "-c", script)
					command.Env = []string{"PATH=/usr/bin:/bin"}
					output, err := command.CombinedOutput()
					require.NoError(t, err, string(output))
					for _, entry := range expected {
						assert.Contains(t, strings.Split(string(output), "\n"), entry)
					}
				} else {
					require.NoError(t, executor.Prepare("", nil, ""))
					for _, entry := range expected {
						assert.Contains(t, executor.preparedEnv, entry)
					}
				}
			})
		}
	}
}
