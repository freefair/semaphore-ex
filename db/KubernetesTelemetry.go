package db

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	maxKubernetesTelemetryBatchEvents  = 100
	maxKubernetesTelemetryCount        = int64(1_000_000_000)
	maxKubernetesTelemetryMilliseconds = int64(3_600_000)
)

var (
	ErrKubernetesTelemetrySequenceConflict = errors.New("Kubernetes telemetry sequence conflict")
	ErrKubernetesTelemetrySessionStale     = errors.New("Kubernetes telemetry session is stale")
)

// KubernetesTelemetryKind is a closed, value-free telemetry vocabulary.
type KubernetesTelemetryKind string

const (
	KubernetesTelemetryAPILatency     KubernetesTelemetryKind = "api_latency"
	KubernetesTelemetryWatchReconnect KubernetesTelemetryKind = "watch_reconnect"
	KubernetesTelemetryLogReconnect   KubernetesTelemetryKind = "log_reconnect"
	KubernetesTelemetryDenial         KubernetesTelemetryKind = "denial"
	KubernetesTelemetryCleanupFailure KubernetesTelemetryKind = "cleanup_failure"
	KubernetesTelemetryReconciliation KubernetesTelemetryKind = "reconciliation"
	KubernetesTelemetryOrphan         KubernetesTelemetryKind = "orphan"
	KubernetesTelemetryQuarantine     KubernetesTelemetryKind = "quarantine"
	KubernetesTelemetryDrop           KubernetesTelemetryKind = "telemetry_drop"
)

// KubernetesTelemetryOperation identifies one namespaced Kubernetes API shape.
type KubernetesTelemetryOperation string

const (
	KubernetesTelemetryOperationCreateSecret        KubernetesTelemetryOperation = "create_secret"
	KubernetesTelemetryOperationCreateNetworkPolicy KubernetesTelemetryOperation = "create_network_policy"
	KubernetesTelemetryOperationCreateJob           KubernetesTelemetryOperation = "create_job"
	KubernetesTelemetryOperationGetPod              KubernetesTelemetryOperation = "get_pod"
	KubernetesTelemetryOperationListPods            KubernetesTelemetryOperation = "list_pods"
	KubernetesTelemetryOperationWatchPods           KubernetesTelemetryOperation = "watch_pods"
	KubernetesTelemetryOperationStreamPodLogs       KubernetesTelemetryOperation = "stream_pod_logs"
	KubernetesTelemetryOperationGetJob              KubernetesTelemetryOperation = "get_job"
	KubernetesTelemetryOperationWatchJobs           KubernetesTelemetryOperation = "watch_jobs"
	KubernetesTelemetryOperationDeleteJob           KubernetesTelemetryOperation = "delete_job"
	KubernetesTelemetryOperationDeleteNetworkPolicy KubernetesTelemetryOperation = "delete_network_policy"
	KubernetesTelemetryOperationDeleteSecret        KubernetesTelemetryOperation = "delete_secret"
	KubernetesTelemetryOperationListJobs            KubernetesTelemetryOperation = "list_jobs"
	KubernetesTelemetryOperationListSecrets         KubernetesTelemetryOperation = "list_secrets"
	KubernetesTelemetryOperationListNetworkPolicies KubernetesTelemetryOperation = "list_network_policies"
)

// KubernetesTelemetryResource is a closed cleanup resource vocabulary.
type KubernetesTelemetryResource string

const (
	KubernetesTelemetryResourceJob           KubernetesTelemetryResource = "job"
	KubernetesTelemetryResourceNetworkPolicy KubernetesTelemetryResource = "network_policy"
	KubernetesTelemetryResourceSecret        KubernetesTelemetryResource = "secret"
)

type KubernetesTelemetryReconciliationState string

const (
	KubernetesTelemetryReconciliationObserved KubernetesTelemetryReconciliationState = "observed"
	KubernetesTelemetryReconciliationAbsent   KubernetesTelemetryReconciliationState = "absent"
)

type KubernetesTelemetryDropReason string

const KubernetesTelemetryDropQueueFull KubernetesTelemetryDropReason = "queue_full"

// KubernetesTelemetryEvent carries no Kubernetes object name, namespace,
// error text, request payload, label, token, or other unbounded value.
type KubernetesTelemetryEvent struct {
	Sequence             int64                                  `json:"sequence"`
	Kind                 KubernetesTelemetryKind                `json:"kind"`
	Operation            KubernetesTelemetryOperation           `json:"operation,omitempty"`
	PolicyRule           string                                 `json:"policy_rule,omitempty"`
	CleanupResource      KubernetesTelemetryResource            `json:"cleanup_resource,omitempty"`
	ReconciliationState  KubernetesTelemetryReconciliationState `json:"reconciliation_state,omitempty"`
	DropReason           KubernetesTelemetryDropReason          `json:"drop_reason,omitempty"`
	DurationMilliseconds int64                                  `json:"duration_milliseconds,omitempty"`
	Count                int64                                  `json:"count,omitempty"`
}

func (e KubernetesTelemetryEvent) Fingerprint() string {
	value := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d", e.Sequence, e.Kind, e.Operation, e.PolicyRule, e.CleanupResource, e.ReconciliationState, e.DropReason, e.DurationMilliseconds, e.Count)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (e KubernetesTelemetryEvent) Validate() error {
	if e.Sequence <= 0 || e.Sequence > 1<<62 {
		return fmt.Errorf("invalid Kubernetes telemetry sequence")
	}
	validOperation := func() bool {
		switch e.Operation {
		case KubernetesTelemetryOperationCreateSecret, KubernetesTelemetryOperationCreateNetworkPolicy, KubernetesTelemetryOperationCreateJob, KubernetesTelemetryOperationGetPod, KubernetesTelemetryOperationListPods, KubernetesTelemetryOperationWatchPods, KubernetesTelemetryOperationStreamPodLogs, KubernetesTelemetryOperationGetJob, KubernetesTelemetryOperationWatchJobs, KubernetesTelemetryOperationDeleteJob, KubernetesTelemetryOperationDeleteNetworkPolicy, KubernetesTelemetryOperationDeleteSecret, KubernetesTelemetryOperationListJobs, KubernetesTelemetryOperationListSecrets, KubernetesTelemetryOperationListNetworkPolicies:
			return true
		default:
			return false
		}
	}
	zeroFields := func() bool {
		return e.Operation == "" && e.PolicyRule == "" && e.CleanupResource == "" && e.ReconciliationState == "" && e.DropReason == "" && e.DurationMilliseconds == 0
	}
	switch e.Kind {
	case KubernetesTelemetryAPILatency:
		if !validOperation() || e.DurationMilliseconds < 0 || e.DurationMilliseconds > maxKubernetesTelemetryMilliseconds || e.PolicyRule != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DropReason != "" || e.Count != 0 {
			return fmt.Errorf("invalid Kubernetes API latency telemetry")
		}
	case KubernetesTelemetryWatchReconnect:
		if (e.Operation != KubernetesTelemetryOperationWatchPods && e.Operation != KubernetesTelemetryOperationWatchJobs) || e.PolicyRule != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DropReason != "" || e.DurationMilliseconds != 0 || e.Count != 1 {
			return fmt.Errorf("invalid Kubernetes watch reconnect telemetry")
		}
	case KubernetesTelemetryLogReconnect:
		if e.Operation != KubernetesTelemetryOperationStreamPodLogs || e.PolicyRule != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DropReason != "" || e.DurationMilliseconds != 0 || e.Count != 1 {
			return fmt.Errorf("invalid Kubernetes log reconnect telemetry")
		}
	case KubernetesTelemetryDenial:
		if !IsKubernetesPolicyRuleID(e.PolicyRule) || e.Operation != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DropReason != "" || e.DurationMilliseconds != 0 || e.Count != 0 {
			return fmt.Errorf("invalid Kubernetes denial telemetry")
		}
	case KubernetesTelemetryCleanupFailure:
		if (e.CleanupResource != KubernetesTelemetryResourceJob && e.CleanupResource != KubernetesTelemetryResourceNetworkPolicy && e.CleanupResource != KubernetesTelemetryResourceSecret) || e.Operation != "" || e.PolicyRule != "" || e.ReconciliationState != "" || e.DropReason != "" || e.DurationMilliseconds != 0 || e.Count != 0 {
			return fmt.Errorf("invalid Kubernetes cleanup telemetry")
		}
	case KubernetesTelemetryReconciliation:
		if (e.ReconciliationState != KubernetesTelemetryReconciliationObserved && e.ReconciliationState != KubernetesTelemetryReconciliationAbsent) || e.Count <= 0 || e.Count > maxKubernetesTelemetryCount || e.Operation != "" || e.PolicyRule != "" || e.CleanupResource != "" || e.DropReason != "" || e.DurationMilliseconds != 0 {
			return fmt.Errorf("invalid Kubernetes reconciliation telemetry")
		}
	case KubernetesTelemetryOrphan, KubernetesTelemetryQuarantine:
		if e.Count <= 0 || e.Count > maxKubernetesTelemetryCount || !zeroFields() {
			return fmt.Errorf("invalid Kubernetes count telemetry")
		}
	case KubernetesTelemetryDrop:
		if e.DropReason != KubernetesTelemetryDropQueueFull || e.Count <= 0 || e.Count > maxKubernetesTelemetryCount || e.Operation != "" || e.PolicyRule != "" || e.CleanupResource != "" || e.ReconciliationState != "" || e.DurationMilliseconds != 0 {
			return fmt.Errorf("invalid Kubernetes telemetry drop")
		}
	default:
		return fmt.Errorf("unknown Kubernetes telemetry kind")
	}
	return nil
}

type KubernetesTelemetryBatch struct {
	Events []KubernetesTelemetryEvent `json:"events"`
}

func (b KubernetesTelemetryBatch) Validate() error {
	if len(b.Events) == 0 || len(b.Events) > maxKubernetesTelemetryBatchEvents {
		return fmt.Errorf("invalid Kubernetes telemetry batch size")
	}
	for index, event := range b.Events {
		if event.Validate() != nil || (index > 0 && event.Sequence != b.Events[index-1].Sequence+1) {
			return fmt.Errorf("invalid Kubernetes telemetry batch")
		}
	}
	return nil
}

// KubernetesTelemetryAck identifies the active authenticated session without
// echoing its fence secret back to the runner.
type KubernetesTelemetryAck struct {
	SessionID       string `json:"session_id"`
	HighestSequence int64  `json:"highest_sequence"`
}

func (a KubernetesTelemetryAck) Validate() error {
	if !validKubernetesReconciliationToken(a.SessionID) || a.HighestSequence < 0 {
		return fmt.Errorf("invalid Kubernetes telemetry acknowledgement")
	}
	return nil
}

type KubernetesTelemetryRepository interface {
	IngestKubernetesTelemetry(authenticatedRunnerID int, sessionID, fence string, batch KubernetesTelemetryBatch) (KubernetesTelemetryAck, []KubernetesTelemetryEvent, error)
}
