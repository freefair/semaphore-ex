package runners

import (
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
)

// normalizedRunnerProgressStatus preserves a concurrent stop or confirmation
// decision while applying a runner's reported progress.
func normalizedRunnerProgressStatus(current, reported task_logger.TaskStatus) task_logger.TaskStatus {
	return runnerProgressStatusForConfirmation(current, runnerProgressStatusForStoppingTask(current, reported))
}

// runnerProgressWasBenignlyAdvanced permits one retry after a failed
// runner/generation CAS only when another HA node changed the same active
// assignment. Assignment replacement and terminal completion stay fenced.
func runnerProgressWasBenignlyAdvanced(
	tsk *tasks.TaskRunner,
	runnerID, generation int,
	previous task_logger.TaskStatus,
) bool {
	return tsk != nil &&
		tsk.Task.RunnerID != nil && *tsk.Task.RunnerID == runnerID &&
		tsk.Task.AssignmentGeneration == generation &&
		!tsk.Task.Status.IsFinished() &&
		tsk.Task.Status != previous
}
