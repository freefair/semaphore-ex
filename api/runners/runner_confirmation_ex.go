package runners

import (
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
)

// The runner can report its last waiting snapshot before polling the user's
// decision. Preserve that decision without terminating the current assignment.
func runnerProgressStatusForConfirmation(current, reported task_logger.TaskStatus) task_logger.TaskStatus {
	if reported == task_logger.TaskWaitingConfirmation &&
		(current == task_logger.TaskConfirmed || current == task_logger.TaskRejected) {
		return current
	}
	return reported
}

// A failed conditional update refreshes the local task from durable storage.
// Retry only if another HA node recorded a decision for this same assignment.
func runnerProgressWasSupersededByConfirmation(tsk *tasks.TaskRunner, runnerID, generation int) bool {
	return tsk != nil &&
		tsk.Task.RunnerID != nil && *tsk.Task.RunnerID == runnerID &&
		tsk.Task.AssignmentGeneration == generation &&
		(tsk.Task.Status == task_logger.TaskConfirmed || tsk.Task.Status == task_logger.TaskRejected)
}
