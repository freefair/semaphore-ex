package db

import (
	"time"
)

type RunnerAttemptOutcome string

const (
	RunnerAttemptActive    RunnerAttemptOutcome = "active"
	RunnerAttemptRequeued  RunnerAttemptOutcome = "requeued"
	RunnerAttemptSucceeded RunnerAttemptOutcome = "succeeded"
	RunnerAttemptFailed    RunnerAttemptOutcome = "failed"
	RunnerAttemptStopped   RunnerAttemptOutcome = "stopped"
)

// RunnerAttempt records one immutable runner assignment and its terminal result.
type RunnerAttempt struct {
	ID         int                  `db:"id" json:"id"`
	ProjectID  int                  `db:"project_id" json:"project_id"`
	TaskID     int                  `db:"task_id" json:"task_id"`
	Generation int                  `db:"generation" json:"generation"`
	RunnerID   int                  `db:"runner_id" json:"runner_id"`
	RunnerName string               `db:"runner_name" json:"runner_name"`
	AssignedAt time.Time            `db:"assigned_at" json:"assigned_at"`
	EndedAt    *time.Time           `db:"ended_at" json:"ended_at,omitempty"`
	Outcome    RunnerAttemptOutcome `db:"outcome" json:"outcome"`
	Reason     string               `db:"reason" json:"reason,omitempty"`
}
