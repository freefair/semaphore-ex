package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type delayedApp struct {
	runCh     chan struct{}
	releaseCh chan struct{}
}

func (a *delayedApp) SetLogger(logger task_logger.Logger) task_logger.Logger {
	return logger
}

func (a *delayedApp) InstallRequirements(db_lib.LocalAppInstallingArgs) error {
	return nil
}

func (a *delayedApp) Run(args db_lib.LocalAppRunningArgs) error {
	close(a.runCh)
	<-a.releaseCh

	select {
	case <-args.StopCh:
		return nil
	case <-time.After(time.Second):
		return errors.New("stop request was not delivered")
	}
}

func (a *delayedApp) Clear() {}

// Test_KillBeforeRun_PreventsAppStart asserts that an existing stop request
// prevents the app from starting.
func Test_KillBeforeRun_PreventsAppStart(t *testing.T) {
	setupExecutorConfig(t)

	app := &delayedApp{
		runCh:     make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	executor := &LocalExecutor{
		App:      app,
		Logger:   task_logger.NopLogger{},
		prepared: true,
	}

	executor.Kill()
	close(app.releaseCh)
	err := executor.Run("", nil, "")

	assert.NoError(t, err)
	select {
	case <-app.runCh:
		assert.Fail(t, "app started after termination was requested")
	default:
	}
}

func Test_RunCanOnlyBeCalledOnce(t *testing.T) {
	setupExecutorConfig(t)

	app := &delayedApp{
		runCh:     make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	executor := &LocalExecutor{
		App:      app,
		Logger:   task_logger.NopLogger{},
		prepared: true,
	}
	firstRunCh := make(chan error, 1)
	go func() {
		firstRunCh <- executor.Run("", nil, "")
	}()

	<-app.runCh

	err := executor.Run("", nil, "")
	assert.ErrorContains(t, err, "local executor has already been run")

	executor.Kill()
	close(app.releaseCh)
	select {
	case err := <-firstRunCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "first LocalExecutor.Run did not return")
	}

	err = executor.Run("", nil, "")
	assert.ErrorContains(t, err, "local executor has already been run")
}

func TestMonorepoCheckoutLocksAcrossTemplatesUntilCleanup(t *testing.T) {
	setupExecutorConfig(t)

	repositoryRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repositoryRoot, "run.sh"), []byte("#!/bin/sh\n"), 0o700))
	lock := &KeyLock{}
	newExecutor := func(templateID int) *LocalExecutor {
		return &LocalExecutor{
			Template:   db.Template{ID: templateID, ProjectID: 1, Type: db.TemplateTask, App: db.AppBash, Playbook: "run.sh"},
			Inventory:  db.Inventory{ID: 1, ProjectID: 1, Type: db.InventoryStatic},
			Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repositoryRoot, Monorepo: true},
			App:        &delayedApp{runCh: make(chan struct{}), releaseCh: make(chan struct{})},
			Logger:     task_logger.NopLogger{},
			RepoLock:   lock,
		}
	}

	first := newExecutor(1)
	require.NoError(t, first.Prepare("", nil, ""))
	t.Cleanup(first.Cleanup)

	second := newExecutor(2)
	prepared := make(chan error, 1)
	go func() { prepared <- second.Prepare("", nil, "") }()
	select {
	case err := <-prepared:
		assert.Fail(t, "second template prepared while the shared checkout was leased", "%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	first.Cleanup()
	require.NoError(t, <-prepared)
	second.Cleanup()
}

func TestMonorepoCheckoutLockWaitStopsWithTask(t *testing.T) {
	setupExecutorConfig(t)

	repositoryRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repositoryRoot, "run.sh"), []byte("#!/bin/sh\n"), 0o700))
	lock := &KeyLock{}
	newExecutor := func(templateID int) *LocalExecutor {
		return &LocalExecutor{
			Template:   db.Template{ID: templateID, ProjectID: 1, Type: db.TemplateTask, App: db.AppBash, Playbook: "run.sh"},
			Inventory:  db.Inventory{ID: 1, ProjectID: 1, Type: db.InventoryStatic},
			Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repositoryRoot, Monorepo: true},
			App:        &delayedApp{runCh: make(chan struct{}), releaseCh: make(chan struct{})},
			Logger:     task_logger.NopLogger{},
			RepoLock:   lock,
		}
	}

	first := newExecutor(1)
	require.NoError(t, first.Prepare("", nil, ""))
	t.Cleanup(first.Cleanup)

	second := newExecutor(2)
	runResult := make(chan error, 1)
	go func() { runResult <- second.Run("", nil, "") }()
	select {
	case err := <-runResult:
		assert.Fail(t, "task returned before waiting for the monorepo checkout", "%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	second.Kill()
	select {
	case err := <-runResult:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "stopped task remained blocked on the monorepo checkout")
	}
}

func TestPrepareThenRunUsesTheExistingCancellationChannel(t *testing.T) {
	setupExecutorConfig(t)
	repositoryRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repositoryRoot, "run.sh"), []byte("#!/bin/sh\n"), 0o700))
	app := &delayedApp{runCh: make(chan struct{}), releaseCh: make(chan struct{})}
	executor := &LocalExecutor{
		Template:   db.Template{ID: 1, ProjectID: 1, Type: db.TemplateTask, App: db.AppBash, Playbook: "run.sh"},
		Inventory:  db.Inventory{Type: db.InventoryStatic},
		Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repositoryRoot},
		App:        app,
		Logger:     task_logger.NopLogger{},
		RepoLock:   &KeyLock{},
	}
	require.NoError(t, executor.Prepare("", nil, ""))
	result := make(chan error, 1)
	go func() { result <- executor.Run("", nil, "") }()
	<-app.runCh
	executor.Kill()
	close(app.releaseCh)
	require.NoError(t, <-result)
}
