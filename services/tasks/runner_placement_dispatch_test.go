package tasks

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskControlLifecycleSpy struct {
	registered atomic.Bool
	released   atomic.Int32
	err        error
}

func (s *taskControlLifecycleSpy) RegisterTaskControl(db.Task) error {
	s.registered.Store(true)
	return s.err
}

func (s *taskControlLifecycleSpy) ReleaseTaskControl(int) {
	s.released.Add(1)
}

func createPlacementDispatchRunner(
	t *testing.T,
	store *sql.SqlDb,
	projectID *int,
	name string,
	tags []string,
) db.Runner {
	t.Helper()
	runner, err := store.CreateRunner(db.Runner{
		ProjectID: projectID, Name: name, Tags: tags,
		Token: db.GenerateRunnerToken(), Active: true, MaxParallelTasks: 2,
	})
	require.NoError(t, err)
	touched := time.Now().UTC()
	_, err = store.Sql().Exec(
		store.PrepareQuery("update runner set touched=? where id=?"), touched, runner.ID,
	)
	require.NoError(t, err)
	runner.Touched = &touched
	return runner
}

func TestRemoteJobDoesNotCallWebhookBeforeCapacityClaim(t *testing.T) {
	setupReconcilerConfig(t)
	var webhookCalls atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webhookCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)

	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	firstTask := createReconcilerTestTaskNoRunner(t, store, task_logger.TaskStartingStatus)
	runner := createPlacementDispatchRunner(t, store, &firstTask.ProjectID, "full webhook", nil)
	runner.MaxParallelTasks = 1
	runner.Webhook = webhook.URL
	runner.IsDefault = true
	require.NoError(t, store.UpdateRunner(runner))
	_, assigned, err := store.AssignTaskRunner(
		firstTask.ProjectID, firstTask.ID, runner.ID, runner.Name, time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, assigned)

	secondTask, err := store.CreateTask(db.Task{
		ProjectID: firstTask.ProjectID, TemplateID: firstTask.TemplateID,
		Status: task_logger.TaskStartingStatus, Created: time.Now(),
	}, 0)
	require.NoError(t, err)
	tsk := &TaskRunner{Task: secondTask, pool: &pool}
	state.SetRunning(tsk)
	job := RemoteJob{Task: secondTask, taskPool: &pool}

	err = job.Run("tester", nil, "")

	require.ErrorIs(t, err, ErrAllRunnersBusy)
	assert.Zero(t, webhookCalls.Load(), "a runner may only be notified after capacity is reserved")
}

func TestRemoteJobRegistersTaskControlBeforeRunnerCanObserveAssignment(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	lifecycle := &taskControlLifecycleSpy{}
	pool.SetTaskControlLifecycle(lifecycle)

	webhookObservedRegistration := atomic.Bool{}
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webhookObservedRegistration.Store(lifecycle.registered.Load())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)

	task := createReconcilerTestTaskNoRunner(t, store, task_logger.TaskStartingStatus)
	runner := createPlacementDispatchRunner(t, store, &task.ProjectID, "controlled", nil)
	runner.Webhook = webhook.URL
	runner.IsDefault = true
	require.NoError(t, store.UpdateRunner(runner))
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)
	job := RemoteJob{Task: task, taskPool: &pool}

	require.NoError(t, job.Run("tester", nil, ""))
	assert.True(t, lifecycle.registered.Load())
	assert.True(t, webhookObservedRegistration.Load())

	pool.onTaskStop(tsk)
	assert.EqualValues(t, 1, lifecycle.released.Load())
}

func TestRemoteJobDoesNotNotifyRunnerWhenTaskControlRegistrationFails(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	lifecycle := &taskControlLifecycleSpy{err: errors.New("task control unavailable")}
	pool.SetTaskControlLifecycle(lifecycle)

	var webhookCalls atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webhookCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)

	task := createReconcilerTestTaskNoRunner(t, store, task_logger.TaskStartingStatus)
	runner := createPlacementDispatchRunner(t, store, &task.ProjectID, "controlled", nil)
	runner.Webhook = webhook.URL
	runner.IsDefault = true
	require.NoError(t, store.UpdateRunner(runner))
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)
	job := RemoteJob{Task: task, taskPool: &pool}

	err := job.Run("tester", nil, "")
	require.ErrorContains(t, err, "task control unavailable")
	assert.Zero(t, webhookCalls.Load())
}

func TestRemoteJobPersistsDeterministicProjectPlacement(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	task := createReconcilerTestTaskNoRunner(t, store, task_logger.TaskStartingStatus)
	global := createPlacementDispatchRunner(t, store, nil, "global", []string{"linux", "gpu"})
	project := createPlacementDispatchRunner(t, store, &task.ProjectID, "project", []string{"GPU", " linux "})
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)
	job := RemoteJob{
		RunnerTags: []string{"linux", "gpu"}, RunnerTagMatchMode: db.RunnerTagMatchAll,
		Task: task, taskPool: &pool,
	}

	err := job.Run("tester", nil, "")

	require.NoError(t, err)
	require.NotNil(t, tsk.Task.RunnerID)
	assert.Equal(t, project.ID, *tsk.Task.RunnerID)
	assert.NotEqual(t, global.ID, *tsk.Task.RunnerID)
	require.NotNil(t, tsk.Task.PlacementDecision)
	assert.Equal(t, db.RunnerPlacementProject, tsk.Task.PlacementDecision.SelectedScope)
	attempts, err := store.GetTaskRunnerAttempts(task.ProjectID, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.StringArrayField{"gpu", "linux"}, attempts[0].RequestedTags)
	assert.Contains(t, attempts[0].PlacementReason, "selected project runner")
}

func TestRemoteJobPersistsActionableNoRunnerDecision(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	task := createReconcilerTestTaskNoRunner(t, store, task_logger.TaskStartingStatus)
	createPlacementDispatchRunner(t, store, &task.ProjectID, "wrong architecture", []string{"amd64"})
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)
	job := RemoteJob{
		RunnerTags: []string{"arm64"}, RunnerTagMatchMode: db.RunnerTagMatchAll,
		Task: task, taskPool: &pool,
	}

	err := job.Run("tester", nil, "")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAllRunnersBusy))
	require.NotNil(t, tsk.Task.PlacementDecision)
	assert.Nil(t, tsk.Task.PlacementDecision.SelectedRunnerID)
	assert.Equal(t, "no runner matched requested tags: arm64", tsk.Task.PlacementDecision.Reason)
	assert.NotEmpty(t, tsk.Task.PlacementDecision.ActionHint)
	stored, err := store.GetTask(task.ProjectID, task.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.PlacementDecision)
	assert.Equal(t, tsk.Task.PlacementDecision.ActionHint, stored.PlacementDecision.ActionHint)
}
