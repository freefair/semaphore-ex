package db

import (
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

type RunnerState string

// RunnerStatus reports whether a runner is currently reachable.
type RunnerStatus string

// RunnerHeartbeatState distinguishes poll-based liveness from webhook delivery.
type RunnerHeartbeatState string

const (
	RunnerStatusOnline  RunnerStatus = "online"
	RunnerStatusOffline RunnerStatus = "offline"

	RunnerHeartbeatOnline  RunnerHeartbeatState = "online"
	RunnerHeartbeatOffline RunnerHeartbeatState = "offline"
	RunnerHeartbeatWebhook RunnerHeartbeatState = "webhook"
)

type RunnerTagFilterMode string

const (
	RunnerFilterTagCompleteMatch RunnerTagFilterMode = "complete_match"
	RunnerFilterHasAnyTag        RunnerTagFilterMode = "has_any_tag"
	RunnerFilterIgnoreTags       RunnerTagFilterMode = "ignore_tags"
	RunnerFilterIsDefault        RunnerTagFilterMode = "is_default"
)

type Runner struct {
	ID                int        `db:"id" json:"id"`
	Token             string     `db:"token" json:"-" backup:"-"`
	ProjectID         *int       `db:"project_id" json:"project_id"`
	Webhook           string     `db:"webhook" json:"webhook"`
	MaxParallelTasks  int        `db:"max_parallel_tasks" json:"max_parallel_tasks"`
	Active            bool       `db:"active" json:"active"`
	IsDefault         bool       `db:"is_default" json:"is_default"`
	Name              string     `db:"name" json:"name"`
	Tags              []string   `db:"-" json:"tags" backup:"tags"`
	Touched           *time.Time `db:"touched" json:"touched"`
	CleaningRequested *time.Time `db:"cleaning_requested" json:"cleaning_requested"`

	// StartedAt is the runner process's start time, reported by the runner on
	// every poll (X-Runner-Started-At header) and persisted next to Touched.
	// It changes on every restart, which is how the server detects that a
	// runner lost its in-memory job pool while still polling.
	StartedAt *time.Time `db:"started_at" json:"started_at"`
	Version   string     `db:"version" json:"version" backup:"-"`
	Platform  string     `db:"platform" json:"platform" backup:"-"`

	// CurrentLoad is the bounded number of jobs reported by the runner on its
	// latest poll. It is operational metadata, not an assignment authority.
	CurrentLoad int `db:"current_load" json:"current_load" backup:"-"`

	PublicKey *string `db:"public_key" json:"-"`

	// Registered is a transient flag (never persisted) used at creation time to
	// request a runner without an auth token. Such a runner gets a one-time,
	// short-lived registration token instead and must be registered later by
	// presenting that token to `semaphore runner register`.
	Registered bool `db:"-" json:"registered"`

	// Status is a transient field (never persisted) reporting whether the runner
	// is currently online or offline, derived from heartbeat liveness. It is
	// populated for API responses via FillStatus.
	Status RunnerStatus `db:"-" json:"status"`

	// RegistrationTokenHash is the stored SHA-256 hash of the one-time registration
	// token (the plaintext is never persisted). Project-runner creation and explicit
	// regeneration may issue the plaintext exactly once.
	RegistrationTokenHash      *string    `db:"registration_token" json:"-" backup:"-"`
	RegistrationTokenExpiresAt *time.Time `db:"registration_token_expires_at" json:"-" backup:"-"`
}

// IsRegistered reports whether the runner has been registered (has a token).
func (r Runner) IsRegistered() bool {
	return r.Token != ""
}

// IsCacheClearPending tolerates database timestamp precision that can store a
// cache-clear request and the preceding heartbeat at the same instant.
func (r Runner) IsCacheClearPending() bool {
	return r.CleaningRequested != nil &&
		(r.Touched == nil || !r.CleaningRequested.Before(*r.Touched))
}

// GenerateRunnerToken creates a new runner authentication token.
func GenerateRunnerToken() string {
	return base64.StdEncoding.EncodeToString(securecookie.GenerateRandomKey(32))
}

// HasTag reports whether the runner is tagged with the given tag.
func (r Runner) HasTag(tag string) bool {
	return slices.Contains(r.Tags, tag)
}

// IsOnline reports whether the runner is considered reachable for dispatch.
// A poll-based runner is online while its last poll (Touched) is within
// offlineTimeout. Webhook-driven runners do not poll, so heartbeat staleness
// does not apply to them — they are always dispatch candidates.
func (r Runner) IsOnline(now time.Time, offlineTimeout time.Duration) bool {
	if r.Webhook != "" {
		return true
	}
	return r.Touched != nil && now.Sub(*r.Touched) <= offlineTimeout
}

// FillStatus populates the transient Status field from heartbeat liveness.
func (r *Runner) FillStatus(now time.Time, offlineTimeout time.Duration) {
	if r.IsOnline(now, offlineTimeout) {
		r.Status = RunnerStatusOnline
	} else {
		r.Status = RunnerStatusOffline
	}
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

type RunnerTag struct {
	Tag             string `db:"-" json:"tag"`
	NumberOfRunners int    `db:"-" json:"number_of_runners"`
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
