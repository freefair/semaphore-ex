package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestApplyOrphanRecoveryOnlyReplacesExecutionWhenEvidenceProvesItAbsent(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskStartingStatus, nil)
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)

	pool.ApplyOrphanRecovery(tsk, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryObserve})
	assert.Equal(t, task_logger.TaskStartingStatus, tsk.Task.Status)
	assert.NotNil(t, tsk.Task.RunnerID)

	pool.ApplyOrphanRecovery(tsk, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryQuarantine, Reason: "runner query timed out"})
	assert.Equal(t, task_logger.TaskStartingStatus, tsk.Task.Status)
	assert.Contains(t, tsk.Task.RecoveryReason, "quarantined")
	assert.Equal(t, 0, state.QueueLen())

	pool.ApplyOrphanRecovery(tsk, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryRecover, SafeReplacement: true, Reason: "execution absent"})
	assert.Equal(t, task_logger.TaskWaitingStatus, tsk.Task.Status)
	assert.Nil(t, tsk.Task.RunnerID)
	assert.Equal(t, 1, state.QueueLen())
	row, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskWaitingStatus, row.Status)
	assert.Nil(t, row.RunnerID)
	assert.Nil(t, row.RunnerSnapshotID)
}

func (s *reconcilerStoreStub) UpdateTaskRunner(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
) (bool, error) {
	if s.updateTaskErr != nil {
		return false, s.updateTaskErr
	}
	return s.Store.UpdateTaskRunner(
		task, expectedStatus, expectedRunnerID, expectedGeneration, outcome, attemptReason, transitionedAt,
	)
}

func TestFailTaskRunnerLost_FinalizesConcurrentTerminalWinner(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	now := time.Now()
	newTask, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}
	state.SetRunning(tsk)

	winner := newTask
	winner.Status = task_logger.TaskSuccessStatus
	require.NoError(t, store.UpdateTask(winner))

	pool.failTaskRunnerLost(tsk, nil, "runner stopped responding")

	assert.Equal(t, task_logger.TaskSuccessStatus, tsk.Task.Status)
	assert.NotNil(t, tsk.Task.End)
	stored, err := store.GetTaskByID(newTask.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, stored.Status)
	assert.NotNil(t, stored.End)
	select {
	case event := <-pool.queueEvents:
		assert.Equal(t, EventTypeFinished, event.eventType)
	default:
		t.Fatal("expected the persisted runner winner to be finalized")
	}
}

func TestStopTaskRunnerLost(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	now := time.Now()
	newTask, _ := createReconcilerTestTask(t, store, task_logger.TaskStoppingStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}
	state.SetRunning(tsk)

	pool.stopTaskRunnerLost(tsk, nil, "runner disappeared during cancellation")

	assert.Equal(t, task_logger.TaskStoppedStatus, tsk.Task.Status)
	assert.Equal(t, "runner disappeared during cancellation", tsk.Task.RecoveryReason)
	assert.NotNil(t, tsk.Task.End)
	row, err := store.GetTaskByID(newTask.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppedStatus, row.Status)
	assert.Equal(t, tsk.Task.RecoveryReason, row.RecoveryReason)
	assert.NotNil(t, row.End)
	select {
	case event := <-pool.queueEvents:
		assert.Equal(t, EventTypeFinished, event.eventType)
	default:
		t.Fatal("expected EventTypeFinished in queueEvents")
	}
}
