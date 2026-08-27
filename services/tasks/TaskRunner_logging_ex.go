package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	log "github.com/sirupsen/logrus"
)

func taskStatusTransitionAllowed(current task_logger.TaskStatus, next task_logger.TaskStatus) bool {
	if current == next {
		return true
	}
	switch current {
	case task_logger.TaskConfirmed:
		return next != task_logger.TaskWaitingConfirmation
	case task_logger.TaskRunningStatus:
		return next != task_logger.TaskWaitingStatus
	case task_logger.TaskStoppingStatus, task_logger.TaskRejected:
		return next == task_logger.TaskStoppedStatus || next == task_logger.TaskFailStatus
	case task_logger.TaskSuccessStatus, task_logger.TaskFailStatus, task_logger.TaskStoppedStatus:
		return false
	default:
		return true
	}
}

func runnerAttemptOutcomeForStatus(status task_logger.TaskStatus) db.RunnerAttemptOutcome {
	switch status {
	case task_logger.TaskSuccessStatus:
		return db.RunnerAttemptSucceeded
	case task_logger.TaskFailStatus:
		return db.RunnerAttemptFailed
	case task_logger.TaskStoppedStatus:
		return db.RunnerAttemptStopped
	default:
		return db.RunnerAttemptActive
	}
}

// ApplyRunnerProgress atomically accepts one report for the current assignment.
// A false result means the runner must terminate its stale local job.
func (t *TaskRunner) ApplyRunnerProgress(
	status task_logger.TaskStatus,
	runnerID int,
	generation int,
	commitHash *string,
	commitMessage string,
) bool {
	if generation == 0 {
		if t.Task.AssignmentGeneration != 0 || t.Task.RunnerID == nil || *t.Task.RunnerID != runnerID {
			return false
		}
		return t.setStatus(status, commitHash, &commitMessage, 0, 0)
	}
	return t.setStatus(status, commitHash, &commitMessage, runnerID, generation)
}

func (t *TaskRunner) setStatus(
	status task_logger.TaskStatus,
	commitHash *string,
	commitMessage *string,
	expectedRunnerID int,
	expectedGeneration int,
) bool {
	oldStatus := t.Task.Status
	if !taskStatusTransitionAllowed(oldStatus, status) {
		return false
	}
	candidate := t.Task
	candidate.Status = status
	if status == task_logger.TaskRunningStatus && oldStatus != task_logger.TaskRunningStatus {
		now := tz.Now()
		candidate.Start = &now
	}
	if commitHash != nil {
		candidate.CommitHash = commitHash
		candidate.CommitMessage = *commitMessage
	}

	conditionalRunnerUpdate := candidate.RunnerID != nil && candidate.AssignmentGeneration > 0
	if expectedRunnerID > 0 || expectedGeneration > 0 {
		if candidate.RunnerID == nil || *candidate.RunnerID != expectedRunnerID ||
			candidate.AssignmentGeneration != expectedGeneration {
			return false
		}
		conditionalRunnerUpdate = true
	}
	if conditionalRunnerUpdate {
		runnerID := *candidate.RunnerID
		generation := candidate.AssignmentGeneration
		if expectedRunnerID > 0 {
			runnerID = expectedRunnerID
			generation = expectedGeneration
		}
		updated, err := t.pool.store.UpdateTaskRunner(
			candidate, oldStatus, runnerID, generation,
			runnerAttemptOutcomeForStatus(status), "", tz.Now(),
		)
		if err != nil {
			t.panicOnError(err, "Failed to conditionally update runner task status")
			return false
		}
		if !updated {
			t.pool.refreshTaskStatusFromDB(t)
			return false
		}
		t.Task = candidate
		t.pool.state.UpdateRuntimeFields(t)
		if status != oldStatus {
			t.publishStatus()
			t.afterStatusChange(oldStatus, status)
		}
		return true
	}

	if status == oldStatus && commitHash == nil {
		return true
	}
	t.Task = candidate
	t.saveStatus()
	if status != oldStatus {
		t.afterStatusChange(oldStatus, status)
	}
	return true
}

func (t *TaskRunner) afterStatusChange(oldStatus task_logger.TaskStatus, status task_logger.TaskStatus) {
	if t.pool != nil {
		t.pool.metrics.RecordTaskStatusChange(oldStatus, status)
	}

	if localJob, ok := t.job.(*LocalExecutor); ok {
		localJob.SetStatus(status)
	}

	if status == task_logger.TaskFailStatus {
		t.sendMailAlert()
	}

	if status.IsNotifiable() {
		t.sendTelegramAlert()
		t.sendSlackAlert()
		t.sendRocketChatAlert()
		t.sendMicrosoftTeamsAlert()
		t.sendDingTalkAlert()
		t.sendGotifyAlert()
	}

	for _, l := range t.statusListeners {
		l(status)
	}

	log.WithFields(log.Fields{
		"task_id": t.Task.ID,
		"context": "task_logger",
		"status":  status,
	}).Info("Task status updated")
}
