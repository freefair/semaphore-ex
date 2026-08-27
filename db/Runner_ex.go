package db

import (
	"fmt"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

// IsCacheClearPending tolerates database timestamp precision that can store a
// cache-clear request and the preceding heartbeat at the same instant.
func (r Runner) IsCacheClearPending() bool {
	return r.CleaningRequested != nil &&
		(r.Touched == nil || !r.CleaningRequested.Before(*r.Touched))
}

// RunnerTaskAssignment identifies unfinished work that makes a destructive
// runner lifecycle transition unsafe.
type RunnerTaskAssignment struct {
	TaskID int                    `db:"task_id" json:"task_id"`
	Status task_logger.TaskStatus `db:"status" json:"status"`
}

// RunnerLifecycleConflictError reports every assignment that blocked a
// destructive runner lifecycle transition.
type RunnerLifecycleConflictError struct {
	RunnerID    int                    `json:"runner_id"`
	Assignments []RunnerTaskAssignment `json:"assignments"`
}

func (e *RunnerLifecycleConflictError) Error() string {
	return fmt.Sprintf("runner %d has %d unfinished task assignment(s)", e.RunnerID, len(e.Assignments))
}
