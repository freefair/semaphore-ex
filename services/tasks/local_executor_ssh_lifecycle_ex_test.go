package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingSSHLifecycleInstaller struct{}

func (failingSSHLifecycleInstaller) Install(db.AccessKey, db.AccessKeyRole, task_logger.Logger) (ssh.AccessKeyInstallation, error) {
	return ssh.AccessKeyInstallation{}, errors.New("inventory installation failed")
}

type sshLifecycleApp struct {
	runErr  error
	cleared bool
}

func TestPrepareContainerTaskCleansTaskAgentWhenInventoryPreparationFails(t *testing.T) {
	configureRepositoryAgentTest(t)
	util.Config.HomeDirMode = util.HomeDirModeProjectHome
	privateKey, _ := repositoryAgentTestPrivateKey(t)
	repositoryPath := t.TempDir()
	becomeKeyID := 1
	executor := &LocalExecutor{
		Task:         db.Task{ID: 1},
		Template:     db.Template{ID: 1, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"},
		Repository:   db.Repository{ID: 1, ProjectID: 1, GitURL: repositoryPath, SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: privateKey}}},
		Inventory:    db.Inventory{ID: 1, ProjectID: 1, Type: db.InventoryStatic, BecomeKeyID: &becomeKeyID, BecomeKey: db.AccessKey{Type: db.AccessKeyLoginPassword}},
		App:          &sshLifecycleApp{},
		KeyInstaller: failingSSHLifecycleInstaller{},
		Logger:       task_logger.NopLogger{},
	}
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "run.sh"), []byte("#!/bin/sh\n"), 0o700))

	_, err := executor.PrepareContainerTask("", nil, "")
	require.ErrorContains(t, err, "inventory installation failed")
	assert.Nil(t, executor.taskSSHAgent)
	assert.Empty(t, executor.taskSSHIdentityFiles)
}

func (a *sshLifecycleApp) SetLogger(logger task_logger.Logger) task_logger.Logger  { return logger }
func (a *sshLifecycleApp) InstallRequirements(db_lib.LocalAppInstallingArgs) error { return nil }
func (a *sshLifecycleApp) Run(db_lib.LocalAppRunningArgs) error                    { return a.runErr }
func (a *sshLifecycleApp) Clear()                                                  { a.cleared = true }

func TestLocalExecutorRunCleansTaskAgentAfterFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		killed bool
		runErr error
	}{
		{name: "failure", runErr: errors.New("task failed")},
		{name: "cancellation", killed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configureRepositoryAgentTest(t)
			privateKey, _ := repositoryAgentTestPrivateKey(t)
			app := &sshLifecycleApp{runErr: test.runErr}
			executor := repositoryAgentTestExecutor(privateKey, "")
			executor.App = app
			executor.Template = db.Template{ProjectID: 1}
			executor.prepared = true
			executor.killed = test.killed
			require.NoError(t, executor.startTaskSSHAgent())
			socketFile := executor.taskSSHAgent.SocketFile
			selectorFiles := append([]string(nil), executor.taskSSHIdentityFiles...)

			err := executor.Run("", nil, "")
			if test.runErr != nil {
				assert.ErrorIs(t, err, test.runErr)
			} else {
				assert.NoError(t, err)
			}
			assert.True(t, app.cleared)
			assert.Nil(t, executor.taskSSHAgent)
			assertFileDoesNotExist(t, socketFile)
			for _, filename := range selectorFiles {
				assertFileDoesNotExist(t, filename)
			}
		})
	}
}

func assertFileDoesNotExist(t *testing.T, filename string) {
	t.Helper()
	_, err := os.Stat(filename)
	assert.True(t, os.IsNotExist(err))
}
