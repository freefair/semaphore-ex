package runners

import (
	"github.com/semaphoreui/semaphore/pkg/task_logger"
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
