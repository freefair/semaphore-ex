package tasks

import (
	"sync"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/require"
)

type stopBeforeClaimStore struct {
	db.Store
}

func (s *stopBeforeClaimStore) ClaimTaskStart(projectID, taskID, expectedGeneration int) (db.Task, bool, error) {
	task, err := s.Store.GetTask(projectID, taskID)
	if err != nil {
		return db.Task{}, false, err
	}
	task.Status = task_logger.TaskStoppingStatus
	if err = s.Store.UpdateTask(task); err != nil {
		return db.Task{}, false, err
	}
	return s.Store.ClaimTaskStart(projectID, taskID, expectedGeneration)
}

func TestStopDuringClaimFinalizesStoppedTask(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	t.Cleanup(fixture.store.Close)
	fixture.pool.store = &stopBeforeClaimStore{Store: fixture.store}
	runner := TaskRunner{Task: fixture.task, Template: fixture.template, pool: &fixture.pool, keyInstaller: fixture.keyInstaller}

	runner.run()

	require.Equal(t, task_logger.TaskStoppedStatus, runner.Task.Status)
	require.NotNil(t, runner.Task.End)
	persisted, err := fixture.store.GetTaskByID(fixture.task.ID)
	require.NoError(t, err)
	require.Equal(t, task_logger.TaskStoppedStatus, persisted.Status)
	require.NotNil(t, persisted.End)
}

type swapStopRaceJob struct {
	entered    chan struct{}
	release    chan struct{}
	killCalled chan struct{}
	once       sync.Once
}

func (j *swapStopRaceJob) Run(string, *string, string) error { return nil }
func (j *swapStopRaceJob) Kill()                             { j.once.Do(func() { close(j.killCalled) }) }
func (j *swapStopRaceJob) IsKilled() bool {
	close(j.entered)
	<-j.release
	return false
}
func (j *swapStopRaceJob) Async() bool { return false }

func TestKillDuringGroupRunnerSwapRetainsKillIntent(t *testing.T) {
	runner := TaskRunner{}
	job := &swapStopRaceJob{entered: make(chan struct{}), release: make(chan struct{}), killCalled: make(chan struct{})}
	runner.job = job
	remote := &RemoteJob{}

	done := make(chan struct{})
	go func() { runner.replaceWithRemoteJob(remote); close(done) }()
	<-job.entered
	killed := make(chan struct{})
	go func() { runner.kill(); close(killed) }()
	close(job.release)
	<-done
	<-killed

	current, ok := runner.currentJob().(*RemoteJob)
	require.True(t, ok)
	require.Same(t, remote, current)
	require.True(t, remote.IsKilled())
}
