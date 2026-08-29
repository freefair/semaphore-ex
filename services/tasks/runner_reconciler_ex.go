package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// ApplyOrphanRecovery applies an evidence-derived recovery decision through
// the existing runner CAS/finalization paths. The caller is responsible for
// holding a current fenced task-control lease before invoking this method.
func (p *TaskPool) ApplyOrphanRecovery(tsk *TaskRunner, assessment pro_interfaces.TaskRecoveryAssessment) {
	if tsk == nil || tsk.Task.Status.IsFinished() {
		return
	}
	switch assessment.Decision {
	case pro_interfaces.TaskRecoveryObserve:
		return
	case pro_interfaces.TaskRecoveryQuarantine:
		p.quarantineOrphanedTask(tsk, assessment.Reason)
		return
	case pro_interfaces.TaskRecoveryRecover:
		if !assessment.SafeReplacement {
			p.quarantineOrphanedTask(tsk, "recovery was not proven safe")
			return
		}
		runnerID := runnerIDFromSnapshot(tsk.Task)
		switch tsk.Task.Status {
		case task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus:
			if runnerID == 0 {
				p.requeueUndispatchedTask(tsk)
				return
			}
			p.requeueTaskRunnerOffline(tsk, runnerID, assessment.Reason)
		case task_logger.TaskStoppingStatus, task_logger.TaskRejected:
			p.stopTaskRunnerLost(tsk, nil, assessment.Reason)
		default:
			p.failTaskRunnerLost(tsk, nil, assessment.Reason)
		}
	default:
		p.quarantineOrphanedTask(tsk, "recovery decision is invalid")
	}
}

func (p *TaskPool) quarantineOrphanedTask(tsk *TaskRunner, reason string) {
	reason = "Recovery quarantined: " + reason
	candidate := tsk.Task
	candidate.RecoveryReason = reason
	candidate.Message = reason
	if err := p.store.UpdateTask(candidate); err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist task recovery quarantine")
		return
	}
	tsk.Task = candidate
	p.state.UpdateRuntimeFields(tsk)
	tsk.publishStatus()
	tsk.Log(reason)
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
