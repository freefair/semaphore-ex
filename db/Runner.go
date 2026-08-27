package db

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
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

// RunnerExecutorType identifies the runner-side execution strategy.
type RunnerExecutorType string

// RunnerTagMatchMode controls how a task's requested tags are matched.
type RunnerTagMatchMode string

const (
	MaxRunnerTags      = 32
	MaxRunnerTagLength = 255
)

const (
	RunnerFilterTagCompleteMatch RunnerTagFilterMode = "complete_match"
	RunnerFilterHasAnyTag        RunnerTagFilterMode = "has_any_tag"
	RunnerFilterIgnoreTags       RunnerTagFilterMode = "ignore_tags"
	RunnerFilterIsDefault        RunnerTagFilterMode = "is_default"

	RunnerTagMatchAll RunnerTagMatchMode = "all"
	RunnerTagMatchAny RunnerTagMatchMode = "any"

	RunnerExecutorLocal  RunnerExecutorType = "local"
	RunnerExecutorDocker RunnerExecutorType = "docker"
	RunnerExecutorK8s    RunnerExecutorType = "k8s"
)

// NormalizeRunnerTags returns the canonical lower-case, trimmed, sorted tag set.
func NormalizeRunnerTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" {
			seen[tag] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for tag := range seen {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

// ValidateRunnerTags bounds placement policy and runner metadata persisted in SQL and API responses.
func ValidateRunnerTags(tags []string) error {
	normalized := NormalizeRunnerTags(tags)
	if len(normalized) > MaxRunnerTags {
		return fmt.Errorf("runner tags must contain at most %d entries", MaxRunnerTags)
	}
	for _, tag := range normalized {
		if len(tag) > MaxRunnerTagLength {
			return fmt.Errorf("runner tag must contain at most %d bytes", MaxRunnerTagLength)
		}
	}
	return nil
}

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

	ExecutorType RunnerExecutorType `db:"executor_type" json:"executor_type" backup:"-"`

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

// EffectiveExecutorType preserves compatibility with runners predating executor reports.
func (r Runner) EffectiveExecutorType() RunnerExecutorType {
	if r.ExecutorType == "" {
		return RunnerExecutorLocal
	}
	return r.ExecutorType
}

// SupportsExecutorImage reports whether this runner executes tasks in containers.
func (r Runner) SupportsExecutorImage() bool {
	switch r.EffectiveExecutorType() {
	case RunnerExecutorDocker, RunnerExecutorK8s:
		return true
	default:
		return false
	}
}

// NormalizeRunnerExecutorType validates a runner report and maps omitted legacy values to local.
func NormalizeRunnerExecutorType(value RunnerExecutorType) (RunnerExecutorType, error) {
	if value == "" {
		return RunnerExecutorLocal, nil
	}
	switch value {
	case RunnerExecutorLocal, RunnerExecutorDocker, RunnerExecutorK8s:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported runner executor type %q", value)
	}
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
	normalized := NormalizeRunnerTags([]string{tag})
	if len(normalized) == 0 {
		return false
	}
	return slices.Contains(NormalizeRunnerTags(r.Tags), normalized[0])
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

type RunnerPlacementScope string

const (
	RunnerPlacementProject RunnerPlacementScope = "project"
	RunnerPlacementGlobal  RunnerPlacementScope = "global"
)

// RunnerPlacementEvaluation is a redacted explanation for one considered runner.
type RunnerPlacementEvaluation struct {
	RunnerID         int                  `json:"runner_id"`
	RunnerName       string               `json:"runner_name"`
	Scope            RunnerPlacementScope `json:"scope"`
	Eligible         bool                 `json:"eligible"`
	AcceptedCriteria []string             `json:"accepted_criteria"`
	RejectedCriteria []string             `json:"rejected_criteria"`
}

// RunnerPlacementDecision explains the deterministic result of one placement attempt.
type RunnerPlacementDecision struct {
	RequestedTags    []string                    `json:"requested_tags"`
	MatchMode        RunnerTagMatchMode          `json:"match_mode"`
	RequestedImage   *string                     `json:"requested_executor_image,omitempty"`
	ResolvedImage    *string                     `json:"resolved_executor_image,omitempty"`
	SelectedRunnerID *int                        `json:"selected_runner_id,omitempty"`
	SelectedName     string                      `json:"selected_runner_name,omitempty"`
	SelectedScope    RunnerPlacementScope        `json:"selected_scope,omitempty"`
	Reason           string                      `json:"reason"`
	ActionHint       string                      `json:"action_hint,omitempty"`
	Evaluations      []RunnerPlacementEvaluation `json:"evaluations"`
}

// Scan implements sql.Scanner for persisted placement decisions.
func (d *RunnerPlacementDecision) Scan(value any) error {
	if value == nil {
		*d = RunnerPlacementDecision{}
		return nil
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return errors.New("unsupported type for RunnerPlacementDecision")
	}
	if len(data) == 0 {
		*d = RunnerPlacementDecision{}
		return nil
	}
	return json.Unmarshal(data, d)
}

// Value implements driver.Valuer for persisted placement decisions.
func (d *RunnerPlacementDecision) Value() (driver.Value, error) {
	if d == nil {
		return nil, nil
	}
	return json.Marshal(d)
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
