package runners

import "github.com/semaphoreui/semaphore/pkg/task_logger"

// Terminal reports first prove their generation without making completion visible.
// Every retry must recompute the target from the authoritative refreshed state.
func deferredRunnerProgressStatus(current, reported task_logger.TaskStatus) (guard, terminal task_logger.TaskStatus) {
	terminal = runnerProgressStatusForConfirmation(current, runnerProgressStatusForStoppingTask(current, reported))
	guard = terminal
	if terminal.IsFinished() {
		guard = current
	}
	return
}
