package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	maxDockerTelemetryBatchEvents  = 100
	maxDockerTelemetryDroppedCount = 1_000_000_000
)

var (
	ErrDockerTelemetrySequenceConflict = fmt.Errorf("Docker telemetry sequence conflict")
	ErrDockerTelemetrySessionStale     = fmt.Errorf("Docker telemetry session is stale")
)

// DockerTelemetryKind is deliberately a closed vocabulary. In particular, it
// carries neither daemon errors nor task/container/image identities.
type DockerTelemetryKind string

const (
	DockerTelemetryResourceUsage  DockerTelemetryKind = "resource_usage"
	DockerTelemetryPolicyDenial   DockerTelemetryKind = "policy_denial"
	DockerTelemetryImagePull      DockerTelemetryKind = "image_pull"
	DockerTelemetryCleanupFailure DockerTelemetryKind = "cleanup_failure"
	DockerTelemetryReconciliation DockerTelemetryKind = "reconciliation"
	DockerTelemetryOrphan         DockerTelemetryKind = "orphan"
	DockerTelemetryDrop           DockerTelemetryKind = "telemetry_drop"
)

type DockerTelemetryRole string

const (
	DockerTelemetryRoleTask   DockerTelemetryRole = "task"
	DockerTelemetryRoleHelper DockerTelemetryRole = "helper"
)

type DockerTelemetryPullSource string

const (
	DockerTelemetryPullLocal  DockerTelemetryPullSource = "local"
	DockerTelemetryPullPulled DockerTelemetryPullSource = "pulled"
)

type DockerTelemetryCleanupResource string

const (
	DockerTelemetryCleanupTask   DockerTelemetryCleanupResource = "task"
	DockerTelemetryCleanupHelper DockerTelemetryCleanupResource = "helper"
	DockerTelemetryCleanupVolume DockerTelemetryCleanupResource = "volume"
)

type DockerTelemetryOrphanState string

const (
	DockerTelemetryOrphanDetected   DockerTelemetryOrphanState = "detected"
	DockerTelemetryOrphanRemoved    DockerTelemetryOrphanState = "removed"
	DockerTelemetryOrphanUnresolved DockerTelemetryOrphanState = "unresolved"
)

type DockerTelemetryDropReason string

const DockerTelemetryDropQueueFull DockerTelemetryDropReason = "queue_full"

// DockerTelemetryEvent is a compact process-local fact sent by an authenticated
// Docker runner. Its fields are interpreted solely by Kind and rejected unless
// every unused field remains its zero value.
type DockerTelemetryEvent struct {
	Sequence             int64                          `json:"sequence"`
	Kind                 DockerTelemetryKind            `json:"kind"`
	Role                 DockerTelemetryRole            `json:"role,omitempty"`
	PolicyRule           string                         `json:"policy_rule,omitempty"`
	PullSource           DockerTelemetryPullSource      `json:"pull_source,omitempty"`
	CleanupResource      DockerTelemetryCleanupResource `json:"cleanup_resource,omitempty"`
	ReconciliationState  DockerReconciliationState      `json:"reconciliation_state,omitempty"`
	OrphanState          DockerTelemetryOrphanState     `json:"orphan_state,omitempty"`
	DropReason           DockerTelemetryDropReason      `json:"drop_reason,omitempty"`
	CPUUsageNanoseconds  int64                          `json:"cpu_usage_nanoseconds,omitempty"`
	MemoryBytes          int64                          `json:"memory_bytes,omitempty"`
	PIDs                 int64                          `json:"pids,omitempty"`
	DurationMilliseconds int64                          `json:"duration_milliseconds,omitempty"`
	Count                int64                          `json:"count,omitempty"`
}

// Fingerprint is an opaque digest of the fixed event fields. It lets the
// server acknowledge a retried sequence without accepting a changed payload.
func (e DockerTelemetryEvent) Fingerprint() string {
	value := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d", e.Sequence, e.Kind, e.Role, e.PolicyRule, e.PullSource, e.CleanupResource, e.ReconciliationState, e.OrphanState, e.DropReason, e.CPUUsageNanoseconds, e.MemoryBytes, e.PIDs, e.DurationMilliseconds, e.Count)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (e DockerTelemetryEvent) Validate() error {
	if e.Sequence <= 0 || e.Sequence > 1<<62 {
		return fmt.Errorf("invalid Docker telemetry sequence")
	}
	zeroNumbers := func() bool {
		return e.CPUUsageNanoseconds == 0 && e.MemoryBytes == 0 && e.PIDs == 0 && e.DurationMilliseconds == 0 && e.Count == 0
	}
	switch e.Kind {
	case DockerTelemetryResourceUsage:
		if (e.Role != DockerTelemetryRoleTask && e.Role != DockerTelemetryRoleHelper) || e.CPUUsageNanoseconds < 0 || e.MemoryBytes < 0 || e.PIDs < 0 || e.CPUUsageNanoseconds > 1<<62 || e.MemoryBytes > 1<<50 || e.PIDs > 1_000_000 || e.PolicyRule != "" || e.PullSource != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.OrphanState != "" || e.DropReason != "" || e.DurationMilliseconds != 0 || e.Count != 0 {
			return fmt.Errorf("invalid Docker resource telemetry")
		}
	case DockerTelemetryPolicyDenial:
		if !IsDockerPolicyRuleID(e.PolicyRule) || e.Role != "" || e.PullSource != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.OrphanState != "" || e.DropReason != "" || !zeroNumbers() {
			return fmt.Errorf("invalid Docker policy telemetry")
		}
	case DockerTelemetryImagePull:
		if (e.Role != DockerTelemetryRoleTask && e.Role != DockerTelemetryRoleHelper) || (e.PullSource != DockerTelemetryPullLocal && e.PullSource != DockerTelemetryPullPulled) || e.DurationMilliseconds < 0 || e.DurationMilliseconds > 3_600_000 || e.PolicyRule != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.OrphanState != "" || e.DropReason != "" || e.CPUUsageNanoseconds != 0 || e.MemoryBytes != 0 || e.PIDs != 0 || e.Count != 0 {
			return fmt.Errorf("invalid Docker image telemetry")
		}
	case DockerTelemetryCleanupFailure:
		if e.CleanupResource != DockerTelemetryCleanupTask && e.CleanupResource != DockerTelemetryCleanupHelper && e.CleanupResource != DockerTelemetryCleanupVolume || e.Role != "" || e.PolicyRule != "" || e.PullSource != "" || e.ReconciliationState != "" || e.OrphanState != "" || e.DropReason != "" || !zeroNumbers() {
			return fmt.Errorf("invalid Docker cleanup telemetry")
		}
	case DockerTelemetryReconciliation:
		switch e.ReconciliationState {
		case DockerReconciliationRunning, DockerReconciliationExited, DockerReconciliationAbsent, DockerReconciliationUnknown, DockerReconciliationDuplicate, DockerReconciliationLeaked, DockerReconciliationQuarantine:
		default:
			return fmt.Errorf("invalid Docker reconciliation state")
		}
		if e.Count <= 0 || e.Count > maxDockerTelemetryBatchEvents || e.Role != "" || e.PolicyRule != "" || e.PullSource != "" || e.CleanupResource != "" || e.OrphanState != "" || e.DropReason != "" || e.CPUUsageNanoseconds != 0 || e.MemoryBytes != 0 || e.PIDs != 0 || e.DurationMilliseconds != 0 {
			return fmt.Errorf("invalid Docker reconciliation telemetry")
		}
	case DockerTelemetryOrphan:
		if (e.OrphanState != DockerTelemetryOrphanDetected && e.OrphanState != DockerTelemetryOrphanRemoved && e.OrphanState != DockerTelemetryOrphanUnresolved) || e.Count <= 0 || e.Count > maxDockerTelemetryBatchEvents || e.Role != "" || e.PolicyRule != "" || e.PullSource != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DropReason != "" || e.CPUUsageNanoseconds != 0 || e.MemoryBytes != 0 || e.PIDs != 0 || e.DurationMilliseconds != 0 {
			return fmt.Errorf("invalid Docker orphan telemetry")
		}
	case DockerTelemetryDrop:
		if e.DropReason != DockerTelemetryDropQueueFull || e.Count <= 0 || e.Count > maxDockerTelemetryDroppedCount || e.Role != "" || e.PolicyRule != "" || e.PullSource != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.OrphanState != "" || e.CPUUsageNanoseconds != 0 || e.MemoryBytes != 0 || e.PIDs != 0 || e.DurationMilliseconds != 0 {
			return fmt.Errorf("invalid Docker telemetry drop")
		}
	default:
		return fmt.Errorf("unknown Docker telemetry kind")
	}
	return nil
}

type DockerTelemetryBatch struct {
	Events []DockerTelemetryEvent `json:"events"`
}

func (b DockerTelemetryBatch) Validate() error {
	if len(b.Events) == 0 || len(b.Events) > maxDockerTelemetryBatchEvents {
		return fmt.Errorf("invalid Docker telemetry batch size")
	}
	for index, event := range b.Events {
		if event.Validate() != nil || (index > 0 && event.Sequence != b.Events[index-1].Sequence+1) {
			return fmt.Errorf("invalid Docker telemetry batch")
		}
	}
	return nil
}

type DockerTelemetryAck struct {
	HighestSequence int64 `json:"highest_sequence"`
}

type DockerTelemetryRepository interface {
	IngestDockerTelemetry(authenticatedRunnerID int, sessionID string, fence string, batch DockerTelemetryBatch) (DockerTelemetryAck, []DockerTelemetryEvent, error)
}
