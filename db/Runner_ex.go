package db

import (
	"fmt"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"time"
)

// RunnerHeartbeatState distinguishes poll-based liveness from webhook delivery.
type RunnerHeartbeatState string

// IsCacheClearPending tolerates database timestamp precision that can store a
// cache-clear request and the preceding heartbeat at the same instant.
func (r Runner) IsCacheClearPending() bool {
	return r.CleaningRequested != nil &&
		(r.Touched == nil || !r.CleaningRequested.Before(*r.Touched))
}

// HeartbeatState reports whether a runner is live, stale, or webhook-driven.
func (r Runner) HeartbeatState(now time.Time, offlineTimeout time.Duration) RunnerHeartbeatState {
	if r.Webhook != "" {
		return RunnerHeartbeatWebhook
	}
	if r.IsOnline(now, offlineTimeout) {
		return RunnerHeartbeatOnline
	}
	return RunnerHeartbeatOffline
}

// RunnerHealth is the non-secret operational projection shown to operators.
type RunnerHealth struct {
	RunnerID                int                  `json:"runner_id"`
	Name                    string               `json:"name"`
	Version                 string               `json:"version"`
	Platform                string               `json:"platform"`
	StartedAt               *time.Time           `json:"started_at,omitempty"`
	LastHeartbeat           *time.Time           `json:"last_heartbeat,omitempty"`
	UptimeSeconds           *int64               `json:"uptime_seconds,omitempty"`
	HeartbeatAgeSeconds     *int64               `json:"heartbeat_age_seconds,omitempty"`
	HeartbeatTimeoutSeconds int64                `json:"heartbeat_timeout_seconds"`
	HeartbeatState          RunnerHeartbeatState `json:"heartbeat_state"`
	CurrentLoad             int                  `json:"current_load"`
	MaxParallelTasks        int                  `json:"max_parallel_tasks"`
}

// Health derives an operator projection from persisted runner report metadata.
func (r Runner) Health(now time.Time, offlineTimeout time.Duration) RunnerHealth {
	health := RunnerHealth{
		RunnerID:                r.ID,
		Name:                    r.Name,
		Version:                 r.Version,
		Platform:                r.Platform,
		StartedAt:               r.StartedAt,
		LastHeartbeat:           r.Touched,
		HeartbeatTimeoutSeconds: int64(offlineTimeout / time.Second),
		HeartbeatState:          r.HeartbeatState(now, offlineTimeout),
		CurrentLoad:             r.CurrentLoad,
		MaxParallelTasks:        r.MaxParallelTasks,
	}
	if r.StartedAt != nil {
		seconds := int64(now.Sub(*r.StartedAt) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		health.UptimeSeconds = &seconds
	}
	if r.Touched != nil {
		seconds := int64(now.Sub(*r.Touched) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		health.HeartbeatAgeSeconds = &seconds
	}
	return health
}

// RunnerTaskAssignment identifies unfinished work that makes a destructive
// runner lifecycle transition unsafe.
type RunnerTaskAssignment struct {
	TaskID int                    `db:"task_id" json:"task_id"`
	Status task_logger.TaskStatus `db:"status" json:"status"`
}

// RunnerTaskHistoryItem is a bounded, non-secret completed-assignment projection.
type RunnerTaskHistoryItem struct {
	TaskID       int                    `db:"task_id" json:"task_id"`
	TemplateID   int                    `db:"template_id" json:"template_id"`
	TemplateName string                 `db:"template_name" json:"template_name"`
	Status       task_logger.TaskStatus `db:"status" json:"status"`
	RunnerID     int                    `db:"runner_id" json:"runner_id"`
	RunnerName   string                 `db:"runner_name" json:"runner_name"`
	Created      time.Time              `db:"created" json:"created"`
	Start        *time.Time             `db:"start" json:"start,omitempty"`
	End          *time.Time             `db:"end" json:"end,omitempty"`
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
