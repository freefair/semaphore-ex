package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"time"
)

type taskRecoveryFencedStore interface {
	UpdateTaskFenced(task db.Task, expectedFencingToken int64) (bool, error)
	UpdateTaskRunnerFenced(
		task db.Task,
		expectedStatus task_logger.TaskStatus,
		expectedRunnerID int,
		expectedGeneration int,
		expectedFencingToken int64,
		outcome db.RunnerAttemptOutcome,
		attemptReason string,
		transitionedAt time.Time,
	) (bool, error)
}

// ApplyOrphanRecovery applies an evidence-derived recovery decision through a
// task-row mutation guarded by the same fencing token as the control lease.
// This closes the pause-after-decision race: once a successor installs a newer
// fence, the former owner cannot change task state even if it resumes later.
func (p *TaskPool) ApplyOrphanRecovery(
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	assessment pro_interfaces.TaskRecoveryAssessment,
) bool {
	if tsk == nil || tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	store, ok := p.store.(taskRecoveryFencedStore)
	if !ok {
		log.WithField("task_id", tsk.Task.ID).Error("task store does not support fenced recovery writes")
		return false
	}
	switch assessment.Decision {
	case pro_interfaces.TaskRecoveryObserve:
		return true
	case pro_interfaces.TaskRecoveryQuarantine:
		return p.quarantineOrphanedTask(store, tsk, lease, assessment.Reason)
	case pro_interfaces.TaskRecoveryRecover:
		if assessment.EvidenceState == pro_interfaces.TaskExecutionTerminal {
			status := task_logger.TaskStatus(assessment.TerminalStatus)
			if !status.IsFinished() || !taskStatusTransitionAllowed(tsk.Task.Status, status) {
				return p.quarantineOrphanedTask(store, tsk, lease, "terminal execution evidence could not be reconciled")
			}
			return p.applyFencedRecoveryTransition(store, tsk, lease, status,
				runnerAttemptOutcomeForStatus(status), assessment.Reason, true, false)
		}
		if !assessment.SafeReplacement {
			return p.quarantineOrphanedTask(store, tsk, lease, "recovery was not proven safe")
		}
		switch tsk.Task.Status {
		case task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskWaitingStatus,
				db.RunnerAttemptActive, assessment.Reason, false, true)
		case task_logger.TaskStoppingStatus, task_logger.TaskRejected:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskStoppedStatus,
				db.RunnerAttemptStopped, assessment.Reason, true, false)
		default:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskFailStatus,
				db.RunnerAttemptFailed, assessment.Reason, true, false)
		}
	default:
		return p.quarantineOrphanedTask(store, tsk, lease, "recovery decision is invalid")
	}
}

// RevokeOrphanedTaskAssignment conditionally removes a not-yet-running runner
// assignment without enqueueing a replacement. Recovery must then wait for a
// complete runner snapshot observed after this revocation before requeueing.
func (p *TaskPool) RevokeOrphanedTaskAssignment(tsk *TaskRunner, lease pro_interfaces.TaskControlLease, reason string) bool {
	if tsk == nil || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	store, ok := p.store.(taskRecoveryFencedStore)
	if !ok {
		return false
	}
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status != task_logger.TaskStartingStatus && tsk.Task.Status != task_logger.TaskWaitingStatus {
		return false
	}
	if !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	if tsk.Task.RunnerID == nil {
		return true
	}
	if *tsk.Task.RunnerID != lease.Execution.RunnerID {
		return false
	}

	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.RunnerID = nil
	candidate.RunnerAssignedAt = nil
	candidate.RecoveryReason = reason
	candidate.Message = reason
	updated, err := store.UpdateTaskRunnerFenced(
		candidate, oldStatus, lease.Execution.RunnerID, lease.Execution.Generation, lease.FencingToken,
		db.RunnerAttemptRequeued, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to revoke orphaned task assignment")
		return false
	}
	if !updated {
		p.refreshTaskStatusFromDB(tsk)
		return false
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Log("Recovery revoked the runner assignment; waiting for post-revocation execution evidence.")
	return true
}

func (p *TaskPool) applyFencedRecoveryTransition(
	store taskRecoveryFencedStore,
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	status task_logger.TaskStatus,
	outcome db.RunnerAttemptOutcome,
	reason string,
	finalize bool,
	requeue bool,
) bool {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	if !requeue && !taskStatusTransitionAllowed(tsk.Task.Status, status) {
		return false
	}
	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.Status = status
	candidate.RecoveryReason = reason
	candidate.Message = reason
	if requeue {
		candidate.RunnerID = nil
		candidate.RunnerAssignedAt = nil
	}
	updated, err := store.UpdateTaskRunnerFenced(
		candidate, oldStatus, lease.Execution.RunnerID, lease.Execution.Generation, lease.FencingToken,
		outcome, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist fenced task recovery")
		return false
	}
	if !updated {
		return false
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	if requeue {
		tsk.Log("Recovery proved the revoked execution absent; returning task to queue.")
		p.state.Enqueue(tsk)
		p.queueEvents <- PoolEvent{EventTypeRequeued, tsk}
	} else if finalize {
		tsk.Log("Recovery reconciled the execution as " + string(status) + ".")
		p.finalizeRemoteTaskLocked(tsk, nil)
	}
	return true
}

func (p *TaskPool) quarantineOrphanedTask(
	store taskRecoveryFencedStore,
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	reason string,
) bool {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	reason = "Recovery quarantined: " + reason
	candidate := tsk.Task
	candidate.RecoveryReason = reason
	candidate.Message = reason
	updated, err := store.UpdateTaskFenced(candidate, lease.FencingToken)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist task recovery quarantine")
		return false
	}
	if !updated {
		return false
	}
	tsk.Task = candidate
	p.state.UpdateRuntimeFields(tsk)
	tsk.publishStatus()
	tsk.Log(reason)
	return true
}

func taskMatchesRecoveryLease(task db.Task, lease pro_interfaces.TaskControlLease) bool {
	return lease.TaskID == task.ID && lease.FencingToken > 0 &&
		lease.Execution.RunnerID == runnerIDFromSnapshot(task) &&
		lease.Execution.Generation == task.AssignmentGeneration
}

func runnerIDFromSnapshot(task db.Task) int {
	if task.RunnerID != nil {
		return *task.RunnerID
	}
	if task.RunnerSnapshotID != nil {
		return *task.RunnerSnapshotID
	}
	return 0
}

func (p *TaskPool) applyPersistedRunnerStatus(tsk *TaskRunner, oldStatus task_logger.TaskStatus) {
	p.state.UpdateRuntimeFields(tsk)
	tsk.publishStatus()
	tsk.afterStatusChange(oldStatus, tsk.Task.Status)
}

// finalizeConcurrentRunnerWinner handles the CAS-loser side of a terminal
// runner-report race. The reconciler already owns the finalize lock here; if
// the runner won SQL first, its own FinalizeRemoteTask call cannot acquire that
// lock. The lock owner must therefore finish the persisted winner instead.
func (p *TaskPool) finalizeConcurrentRunnerWinner(tsk *TaskRunner, runner *db.Runner) {
	p.refreshTaskStatusFromDB(tsk)
	if !tsk.Task.Status.IsFinished() {
		return
	}
	if tsk.Task.End != nil {
		p.onTaskStop(tsk)
		return
	}
	p.finalizeRemoteTaskLocked(tsk, runner)
}

func (p *TaskPool) stopTaskRunnerLost(tsk *TaskRunner, runner *db.Runner, reason string) {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() {
		return
	}
	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.Status = task_logger.TaskStoppedStatus
	candidate.Message = reason
	candidate.RecoveryReason = reason
	if runner == nil {
		candidate.RunnerID = nil
	}
	updated, err := p.store.UpdateTaskRunner(
		candidate, oldStatus, runnerIDFromSnapshot(tsk.Task), tsk.Task.AssignmentGeneration,
		db.RunnerAttemptStopped, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist lost-runner cancellation")
		return
	}
	if !updated {
		p.finalizeConcurrentRunnerWinner(tsk, runner)
		return
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Log("Runner cancellation completed: " + reason)
	p.finalizeRemoteTaskLocked(tsk, runner)
}
