package db

import (
	"encoding/base64"
	"github.com/gorilla/securecookie"
	"slices"
	"time"
)

type RunnerState string

// RunnerStatus reports whether a runner is currently reachable.
type RunnerStatus string

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

	RunnerTagMatchAll RunnerTagMatchMode = "all"
	RunnerTagMatchAny RunnerTagMatchMode = "any"

	RunnerExecutorLocal  RunnerExecutorType = "local"
	RunnerExecutorDocker RunnerExecutorType = "docker"
	RunnerExecutorK8s    RunnerExecutorType = "k8s"
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

	ExecutorType RunnerExecutorType `db:"executor_type" json:"executor_type" backup:"-"`

	DockerPolicyRevision int    `db:"docker_policy_revision" json:"docker_policy_revision" backup:"-"`
	DockerPolicyHash     string `db:"docker_policy_hash" json:"docker_policy_hash" backup:"-"`
	K8sClusterAlias      string `db:"k8s_cluster_alias" json:"k8s_cluster_alias" backup:"-"`
	K8sNamespace         string `db:"k8s_namespace" json:"k8s_namespace" backup:"-"`
	K8sPolicyRevision    int    `db:"k8s_policy_revision" json:"k8s_policy_revision" backup:"-"`
	K8sPolicyHash        string `db:"k8s_policy_hash" json:"k8s_policy_hash" backup:"-"`

	PublicKey *string `db:"public_key" json:"-" backup:"-"`

	RegistrationPolicy      RunnerRegistrationPolicy `db:"registration_policy" json:"registration_policy"`
	RegistrationKind        RunnerRegistrationKind   `db:"registration_kind" json:"registration_kind" backup:"-"`
	SecurityCompliant       bool                     `db:"security_compliant" json:"security_compliant" backup:"-"`
	SecurityReason          string                   `db:"security_reason" json:"security_reason" backup:"-"`
	SecurityRemediation     string                   `db:"security_remediation" json:"security_remediation" backup:"-"`
	TransportTrust          RunnerTransportTrust     `db:"transport_trust" json:"transport_trust" backup:"-"`
	SecurityProtocolVersion int                      `db:"security_protocol_version" json:"security_protocol_version" backup:"-"`
	SecurityCheckedAt       *time.Time               `db:"security_checked_at" json:"security_checked_at" backup:"-"`

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

// HasFreeCapacity reports whether the runner can take one more task.
// MaxParallelTasks == 0 means unlimited.
func (r Runner) HasFreeCapacity(runningTasks int) bool {
	return r.MaxParallelTasks == 0 || runningTasks < r.MaxParallelTasks
}

// FillStatus populates the transient Status field from heartbeat liveness.
func (r *Runner) FillStatus(now time.Time, offlineTimeout time.Duration) {
	if r.IsOnline(now, offlineTimeout) {
		r.Status = RunnerStatusOnline
	} else {
		r.Status = RunnerStatusOffline
	}
}

type RunnerTag struct {
	Tag             string `db:"-" json:"tag"`
	NumberOfRunners int    `db:"-" json:"number_of_runners"`
}
