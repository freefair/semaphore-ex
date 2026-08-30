package db

import (
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"strings"
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
	ID                     int                  `db:"id" json:"id"`
	ProjectID              int                  `db:"project_id" json:"project_id"`
	TaskID                 int                  `db:"task_id" json:"task_id"`
	Generation             int                  `db:"generation" json:"generation"`
	RunnerID               int                  `db:"runner_id" json:"runner_id"`
	RunnerName             string               `db:"runner_name" json:"runner_name"`
	AssignedAt             time.Time            `db:"assigned_at" json:"assigned_at"`
	EndedAt                *time.Time           `db:"ended_at" json:"ended_at,omitempty"`
	Outcome                RunnerAttemptOutcome `db:"outcome" json:"outcome"`
	Reason                 string               `db:"reason" json:"reason,omitempty"`
	RequestedTags          StringArrayField     `db:"requested_tags" json:"requested_tags,omitempty"`
	MatchMode              RunnerTagMatchMode   `db:"match_mode" json:"match_mode,omitempty"`
	PlacementReason        string               `db:"placement_reason" json:"placement_reason,omitempty"`
	RequestedExecutorImage *string              `db:"requested_executor_image" json:"requested_executor_image,omitempty"`
	ResolvedExecutorImage  *string              `db:"resolved_executor_image" json:"resolved_executor_image,omitempty"`
	ExecutorType           RunnerExecutorType   `db:"executor_type" json:"executor_type,omitempty"`
	ContainerID            string               `db:"container_id" json:"container_id,omitempty"`
	ContainerName          string               `db:"container_name" json:"container_name,omitempty"`
	DockerRequestedImage   string               `db:"docker_requested_image" json:"docker_requested_image,omitempty"`
	DockerResolvedImage    string               `db:"docker_resolved_image" json:"docker_resolved_image,omitempty"`
	DockerPolicyRevision   int                  `db:"docker_policy_revision" json:"docker_policy_revision,omitempty"`
	DockerPolicyHash       string               `db:"docker_policy_hash" json:"docker_policy_hash,omitempty"`
	DockerNanoCPUs         int64                `db:"docker_nano_cpus" json:"docker_nano_cpus,omitempty"`
	DockerMemoryBytes      int64                `db:"docker_memory_bytes" json:"docker_memory_bytes,omitempty"`
	DockerPidsLimit        int64                `db:"docker_pids_limit" json:"docker_pids_limit,omitempty"`
	DenialRuleID           string               `db:"denial_rule_id" json:"denial_rule_id,omitempty"`
}

// RunnerExecutorMetadata is the bounded runtime identity a runner may attach
// to its current assignment. Task arguments, environment, labels, mounts, and
// daemon details intentionally never cross this API boundary.
type RunnerExecutorMetadata struct {
	ExecutorType   RunnerExecutorType `json:"executor_type"`
	ContainerID    string             `json:"container_id,omitempty"`
	ContainerName  string             `json:"container_name,omitempty"`
	RequestedImage string             `json:"requested_image,omitempty"`
	ResolvedImage  string             `json:"resolved_image,omitempty"`
	PolicyRevision int                `json:"policy_revision,omitempty"`
	PolicyHash     string             `json:"policy_hash,omitempty"`
	NanoCPUs       int64              `json:"nano_cpus,omitempty"`
	MemoryBytes    int64              `json:"memory_bytes,omitempty"`
	PidsLimit      int64              `json:"pids_limit,omitempty"`
	DenialRuleID   string             `json:"denial_rule_id,omitempty"`
}

const MaxRunnerContainerIdentityLength = 128

// Validate checks the small runner-owned identity contract before metadata is
// persisted or shown to project users.
func (m RunnerExecutorMetadata) Validate(expected RunnerExecutorType) error {
	executorType, err := NormalizeRunnerExecutorType(m.ExecutorType)
	if err != nil {
		return err
	}
	if executorType != expected {
		return fmt.Errorf("executor metadata type %q does not match runner type %q", executorType, expected)
	}
	if executorType != RunnerExecutorDocker {
		if m.ContainerID != "" || m.ContainerName != "" {
			return errors.New("container identity is only valid for Docker executor metadata")
		}
		return nil
	}
	if m.DenialRuleID != "" {
		if !IsDockerPolicyRuleID(m.DenialRuleID) || m.ContainerID != "" || m.ContainerName != "" {
			return errors.New("Docker executor metadata contains an invalid denial rule")
		}
		return nil
	}
	if m.ContainerName == "" {
		return errors.New("Docker executor metadata requires a container name")
	}
	for field, value := range map[string]string{"container ID": m.ContainerID, "container name": m.ContainerName} {
		if len(value) > MaxRunnerContainerIdentityLength {
			return fmt.Errorf("%s must contain at most %d bytes", field, MaxRunnerContainerIdentityLength)
		}
		for _, character := range value {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
				continue
			}
			return fmt.Errorf("%s contains an unsupported character", field)
		}
	}
	if m.ResolvedImage != "" && (m.RequestedImage == "" || !strings.Contains(m.ResolvedImage, "@sha256:")) {
		return errors.New("Docker executor metadata requires requested and immutable resolved images")
	}
	if m.PolicyHash != "" && (len(m.PolicyHash) != 64 || m.PolicyRevision < 0) {
		return errors.New("Docker executor metadata contains an invalid policy acknowledgement")
	}
	if m.ResolvedImage != "" && (m.NanoCPUs <= 0 || m.MemoryBytes <= 0 || m.PidsLimit <= 0) {
		return errors.New("Docker executor metadata requires positive policy resource limits")
	}
	return nil
}

// TaskExecutionEvidenceState is a value-free observation from a complete
// runner snapshot. Unknown must never be treated as evidence of absence.
type TaskExecutionEvidenceState string

const (
	TaskExecutionEvidenceRunning  TaskExecutionEvidenceState = "running"
	TaskExecutionEvidenceTerminal TaskExecutionEvidenceState = "terminal"
	TaskExecutionEvidenceAbsent   TaskExecutionEvidenceState = "absent"
	TaskExecutionEvidenceUnknown  TaskExecutionEvidenceState = "unknown"
)

// TaskExecutionEvidence identifies one runner execution generation without
// carrying task output, secrets, or executor metadata.
type TaskExecutionEvidence struct {
	TaskID     int                        `json:"task_id"`
	Generation int                        `json:"generation"`
	State      TaskExecutionEvidenceState `json:"state"`
	Status     task_logger.TaskStatus     `json:"status,omitempty"`
}

// TaskExecutionEvidenceRecorder is an optional store capability. Community
// stores need not implement it; HA code uses it only when available.
type TaskExecutionEvidenceRecorder interface {
	RecordTaskExecutionSnapshot(runnerID int, evidence []TaskExecutionEvidence) error
}
