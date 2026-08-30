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
	task, runnerID := createGenerationAwareReconcilerTask(t, store, task_logger.TaskStartingStatus)
	lease := installRecoveryFence(t, store, &task, 7)
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)

	pool.ApplyOrphanRecovery(tsk, lease, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryObserve})
	assert.Equal(t, task_logger.TaskStartingStatus, tsk.Task.Status)
	assert.NotNil(t, tsk.Task.RunnerID)

	pool.ApplyOrphanRecovery(tsk, lease, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryQuarantine, Reason: "runner query timed out"})
	assert.Equal(t, task_logger.TaskStartingStatus, tsk.Task.Status)
	assert.Contains(t, tsk.Task.RecoveryReason, "quarantined")
	assert.Equal(t, 0, state.QueueLen())

	pool.ApplyOrphanRecovery(tsk, lease, pro_interfaces.TaskRecoveryAssessment{Decision: pro_interfaces.TaskRecoveryRecover, SafeReplacement: true, EvidenceState: pro_interfaces.TaskExecutionAbsent, Reason: "execution absent"})
	assert.Equal(t, task_logger.TaskWaitingStatus, tsk.Task.Status)
	assert.Nil(t, tsk.Task.RunnerID)
	assert.Equal(t, 1, state.QueueLen())
	row, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskWaitingStatus, row.Status)
	assert.Nil(t, row.RunnerID)
	require.NotNil(t, row.RunnerSnapshotID)
	assert.Equal(t, runnerID, *row.RunnerSnapshotID)
}

func TestRevokeOrphanedTaskAssignmentWaitsBeforeRequeue(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	task, _ := createGenerationAwareReconcilerTask(t, store, task_logger.TaskStartingStatus)
	lease := installRecoveryFence(t, store, &task, 7)
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)
	assert.True(t, pool.RevokeOrphanedTaskAssignment(tsk, lease, "owner boot expired"))
	assert.Nil(t, tsk.Task.RunnerID)
	assert.Equal(t, task_logger.TaskStartingStatus, tsk.Task.Status)
	assert.Zero(t, state.QueueLen(), "revocation alone must not launch a replacement")
	persisted, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Nil(t, persisted.RunnerID)
	attempts, err := store.GetTaskRunnerAttempts(task.ProjectID, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.RunnerAttemptRequeued, attempts[0].Outcome)

	pool.ApplyOrphanRecovery(tsk, lease, pro_interfaces.TaskRecoveryAssessment{
		Decision: pro_interfaces.TaskRecoveryRecover, SafeReplacement: true,
		EvidenceState: pro_interfaces.TaskExecutionAbsent, Reason: "post-revocation snapshot reports absence",
	})
	assert.Equal(t, task_logger.TaskWaitingStatus, tsk.Task.Status)
	assert.Equal(t, 1, state.QueueLen())
}

func TestApplyOrphanRecoveryReconcilesExactTerminalResult(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	task, runnerID := createGenerationAwareReconcilerTask(t, store, task_logger.TaskStartingStatus)
	lease := installRecoveryFence(t, store, &task, 7)
	started := time.Now().UTC()
	running := task
	running.Status = task_logger.TaskRunningStatus
	running.Start = &started
	updated, err := store.UpdateTaskRunner(
		running, task_logger.TaskStartingStatus, runnerID, task.AssignmentGeneration,
		db.RunnerAttemptActive, "", started,
	)
	require.NoError(t, err)
	require.True(t, updated)
	task = running
	tsk := &TaskRunner{Task: task, pool: &pool}
	state.SetRunning(tsk)

	pool.ApplyOrphanRecovery(tsk, lease, pro_interfaces.TaskRecoveryAssessment{
		Decision: pro_interfaces.TaskRecoveryRecover, EvidenceState: pro_interfaces.TaskExecutionTerminal,
		TerminalStatus: string(task_logger.TaskSuccessStatus), Reason: "runner reported success",
	})

	assert.Equal(t, task_logger.TaskSuccessStatus, tsk.Task.Status)
	require.NotNil(t, tsk.Task.End)
	persisted, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, persisted.Status)
	require.NotNil(t, persisted.End)
}

func TestApplyOrphanRecoveryStateMatrix(t *testing.T) {
	tests := []struct {
		name       string
		initial    task_logger.TaskStatus
		assessment pro_interfaces.TaskRecoveryAssessment
		expected   task_logger.TaskStatus
	}{
		{
			name: "running execution is observed", initial: task_logger.TaskRunningStatus,
			assessment: pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryObserve, EvidenceState: pro_interfaces.TaskExecutionRunning,
				Reason: "original execution is still running",
			}, expected: task_logger.TaskRunningStatus,
		},
		{
			name: "absent running execution fails without replacement", initial: task_logger.TaskRunningStatus,
			assessment: pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryRecover, SafeReplacement: true,
				EvidenceState: pro_interfaces.TaskExecutionAbsent, Reason: "execution absent",
			}, expected: task_logger.TaskFailStatus,
		},
		{
			name: "absent canceling execution converges to stopped", initial: task_logger.TaskStoppingStatus,
			assessment: pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryRecover, SafeReplacement: true,
				EvidenceState: pro_interfaces.TaskExecutionAbsent, Reason: "execution absent",
			}, expected: task_logger.TaskStoppedStatus,
		},
		{
			name: "terminal completion preserves runner failure", initial: task_logger.TaskRunningStatus,
			assessment: pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryRecover, EvidenceState: pro_interfaces.TaskExecutionTerminal,
				TerminalStatus: string(task_logger.TaskFailStatus), Reason: "runner reported failure",
			}, expected: task_logger.TaskFailStatus,
		},
		{
			name: "ambiguous running execution remains nonterminal", initial: task_logger.TaskRunningStatus,
			assessment: pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryQuarantine, EvidenceState: pro_interfaces.TaskExecutionUnknown,
				Reason: "runner query timed out",
			}, expected: task_logger.TaskRunningStatus,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupReconcilerConfig(t)
			store := sql.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			state := NewMemoryTaskStateStore()
			pool := newReconcilerTestPool(store, state)
			task, runnerID := createGenerationAwareReconcilerTask(t, store, task_logger.TaskStartingStatus)
			lease := installRecoveryFence(t, store, &task, 7)
			if test.initial != task_logger.TaskStartingStatus {
				candidate := task
				candidate.Status = test.initial
				started := time.Now().UTC()
				candidate.Start = &started
				updated, err := store.UpdateTaskRunner(
					candidate, task_logger.TaskStartingStatus, runnerID, task.AssignmentGeneration,
					db.RunnerAttemptActive, "", started,
				)
				require.NoError(t, err)
				require.True(t, updated)
				task = candidate
			}
			tsk := &TaskRunner{Task: task, pool: &pool}
			state.SetRunning(tsk)

			pool.ApplyOrphanRecovery(tsk, lease, test.assessment)

			assert.Equal(t, test.expected, tsk.Task.Status)
			persisted, err := store.GetTaskByID(task.ID)
			require.NoError(t, err)
			assert.Equal(t, test.expected, persisted.Status)
			if test.assessment.Decision == pro_interfaces.TaskRecoveryQuarantine {
				assert.Contains(t, persisted.RecoveryReason, "quarantined")
			}
		})
	}
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

func installRecoveryFence(t *testing.T, store *sql.SqlDb, task *db.Task, token int64) pro_interfaces.TaskControlLease {
	t.Helper()
	require.NotNil(t, task.RunnerSnapshotID)
	_, err := store.GetConnection().Exec(
		"update task set task_control_fencing_token=? where id=?", token, task.ID,
	)
	require.NoError(t, err)
	task.TaskControlFencingToken = token
	execution, err := pro_interfaces.NewTaskExecutionIdentity(
		task.ID, *task.RunnerSnapshotID, task.AssignmentGeneration,
	)
	require.NoError(t, err)
	return pro_interfaces.TaskControlLease{
		TaskID: task.ID, OwnerBootID: "recovery-test", FencingToken: token,
		Execution: execution, ExpiresAt: time.Now().Add(time.Minute),
	}
}

// createGenerationAwareReconcilerTask uses the production assignment CAS so
// recovery tests exercise the durable runner-attempt generation contract.
func createGenerationAwareReconcilerTask(
	t *testing.T,
	store *sql.SqlDb,
	status task_logger.TaskStatus,
) (db.Task, int) {
	t.Helper()
	task := createReconcilerTestTaskNoRunner(t, store, status)
	runner := createPlacementDispatchRunner(t, store, &task.ProjectID, "recovery runner", nil)
	assigned, ok, err := store.AssignTaskRunner(
		task.ProjectID, task.ID, runner.ID, runner.Name, time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Positive(t, assigned.AssignmentGeneration)
	return assigned, runner.ID
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

func TestStopTaskRunnerLostQuarantinesDockerWithoutDaemonEvidence(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	state := NewMemoryTaskStateStore()
	pool := newReconcilerTestPool(store, state)
	now := time.Now()
	newTask, runnerID := createReconcilerTestTask(t, store, task_logger.TaskStoppingStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}
	state.SetRunning(tsk)

	pool.stopTaskRunnerLost(tsk, &db.Runner{ID: runnerID, ExecutorType: db.RunnerExecutorDocker}, "HA lost runner during cancellation")

	assert.Equal(t, task_logger.TaskStoppingStatus, tsk.Task.Status)
	assert.Nil(t, tsk.Task.End)
	assert.Contains(t, tsk.Task.RecoveryReason, "quarantined")
	stored, err := store.GetTaskByID(newTask.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppingStatus, stored.Status)
	assert.Nil(t, stored.End)
	assert.Empty(t, pool.queueEvents)
}
