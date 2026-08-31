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
	K8sClusterAlias        string               `db:"k8s_cluster_alias" json:"k8s_cluster_alias,omitempty"`
	K8sNamespace           string               `db:"k8s_namespace" json:"k8s_namespace,omitempty"`
	K8sJobName             string               `db:"k8s_job_name" json:"k8s_job_name,omitempty"`
	K8sJobUID              string               `db:"k8s_job_uid" json:"k8s_job_uid,omitempty"`
	K8sPodName             string               `db:"k8s_pod_name" json:"k8s_pod_name,omitempty"`
	K8sPodUID              string               `db:"k8s_pod_uid" json:"k8s_pod_uid,omitempty"`
	K8sContainerName       string               `db:"k8s_container_name" json:"k8s_container_name,omitempty"`
	K8sLifecycle           string               `db:"k8s_lifecycle" json:"k8s_lifecycle,omitempty"`
	K8sTerminalReason      string               `db:"k8s_terminal_reason" json:"k8s_terminal_reason,omitempty"`
	K8sPolicyRevision      int                  `db:"k8s_policy_revision" json:"k8s_policy_revision,omitempty"`
	K8sPolicyHash          string               `db:"k8s_policy_hash" json:"k8s_policy_hash,omitempty"`
	K8sDenialRuleID        string               `db:"k8s_denial_rule_id" json:"k8s_denial_rule_id,omitempty"`
	K8sServiceAccount      string               `db:"k8s_service_account" json:"k8s_service_account,omitempty"`
	K8sRuntimeClass        string               `db:"k8s_runtime_class" json:"k8s_runtime_class,omitempty"`
	K8sResourcePolicyID    string               `db:"k8s_resource_policy_id" json:"k8s_resource_policy_id,omitempty"`
	K8sResourcePolicyHash  string               `db:"k8s_resource_policy_hash" json:"k8s_resource_policy_hash,omitempty"`
	K8sNetworkProfile      string               `db:"k8s_network_profile" json:"k8s_network_profile,omitempty"`
	K8sNetworkEnforcement  string               `db:"k8s_network_enforcement" json:"k8s_network_enforcement,omitempty"`
	K8sSecretName          string               `db:"k8s_secret_name" json:"k8s_secret_name,omitempty"`
	K8sSecretUID           string               `db:"k8s_secret_uid" json:"k8s_secret_uid,omitempty"`
	K8sNetworkPolicyName   string               `db:"k8s_network_policy_name" json:"k8s_network_policy_name,omitempty"`
	K8sNetworkPolicyUID    string               `db:"k8s_network_policy_uid" json:"k8s_network_policy_uid,omitempty"`
	K8sRetentionDeadline   *time.Time           `db:"k8s_retention_deadline" json:"k8s_retention_deadline,omitempty"`
	K8sRetentionState      string               `db:"k8s_retention_state" json:"k8s_retention_state,omitempty"`
}

// RunnerExecutorMetadata is the bounded runtime identity a runner may attach
// to its current assignment. Task arguments, environment, labels, mounts, and
// daemon details intentionally never cross this API boundary.
type RunnerExecutorMetadata struct {
	ExecutorType          RunnerExecutorType `json:"executor_type"`
	ContainerID           string             `json:"container_id,omitempty"`
	ContainerName         string             `json:"container_name,omitempty"`
	RequestedImage        string             `json:"requested_image,omitempty"`
	ResolvedImage         string             `json:"resolved_image,omitempty"`
	PolicyRevision        int                `json:"policy_revision,omitempty"`
	PolicyHash            string             `json:"policy_hash,omitempty"`
	NanoCPUs              int64              `json:"nano_cpus,omitempty"`
	MemoryBytes           int64              `json:"memory_bytes,omitempty"`
	PidsLimit             int64              `json:"pids_limit,omitempty"`
	DenialRuleID          string             `json:"denial_rule_id,omitempty"`
	K8sClusterAlias       string             `json:"k8s_cluster_alias,omitempty"`
	K8sNamespace          string             `json:"k8s_namespace,omitempty"`
	K8sJobName            string             `json:"k8s_job_name,omitempty"`
	K8sJobUID             string             `json:"k8s_job_uid,omitempty"`
	K8sPodName            string             `json:"k8s_pod_name,omitempty"`
	K8sPodUID             string             `json:"k8s_pod_uid,omitempty"`
	K8sContainerName      string             `json:"k8s_container_name,omitempty"`
	K8sLifecycle          string             `json:"k8s_lifecycle,omitempty"`
	K8sTerminalReason     string             `json:"k8s_terminal_reason,omitempty"`
	K8sPolicyRevision     int                `json:"k8s_policy_revision,omitempty"`
	K8sPolicyHash         string             `json:"k8s_policy_hash,omitempty"`
	K8sDenialRuleID       string             `json:"k8s_denial_rule_id,omitempty"`
	K8sServiceAccount     string             `json:"k8s_service_account,omitempty"`
	K8sRuntimeClass       string             `json:"k8s_runtime_class,omitempty"`
	K8sResourcePolicyID   string             `json:"k8s_resource_policy_id,omitempty"`
	K8sResourcePolicyHash string             `json:"k8s_resource_policy_hash,omitempty"`
	K8sNetworkProfile     string             `json:"k8s_network_profile,omitempty"`
	K8sNetworkEnforcement string             `json:"k8s_network_enforcement,omitempty"`
	K8sSecretName         string             `json:"k8s_secret_name,omitempty"`
	K8sSecretUID          string             `json:"k8s_secret_uid,omitempty"`
	K8sNetworkPolicyName  string             `json:"k8s_network_policy_name,omitempty"`
	K8sNetworkPolicyUID   string             `json:"k8s_network_policy_uid,omitempty"`
	K8sRetentionDeadline  *time.Time         `json:"k8s_retention_deadline,omitempty"`
	K8sRetentionState     string             `json:"k8s_retention_state,omitempty"`
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
	if executorType == RunnerExecutorK8s {
		return m.validateKubernetes()
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

func (m RunnerExecutorMetadata) validateKubernetes() error {
	if m.ContainerID != "" || m.ContainerName != "" || m.PolicyRevision != 0 || m.PolicyHash != "" ||
		m.NanoCPUs != 0 || m.MemoryBytes != 0 || m.PidsLimit != 0 || m.DenialRuleID != "" {
		return errors.New("Kubernetes executor metadata contains Docker-only fields")
	}
	if !safeExecutorIdentity(m.K8sClusterAlias, 128, false) {
		return errors.New("Kubernetes executor metadata contains an invalid cluster alias")
	}
	if !safeDNSLabel(m.K8sNamespace) {
		return errors.New("Kubernetes executor metadata contains an invalid namespace")
	}
	provenanceSupplied := m.hasKubernetesProvenance()
	if provenanceSupplied && (!safeDNSLabel(m.K8sServiceAccount) || m.K8sServiceAccount == "default" ||
		(m.K8sRuntimeClass != "" && !safeDNSLabel(m.K8sRuntimeClass)) ||
		!safeExecutorIdentity(m.K8sResourcePolicyID, 128, false) || !IsKubernetesPolicyHash(m.K8sResourcePolicyHash) ||
		m.K8sNetworkProfile != string(KubernetesNetworkProfileDenyAll) ||
		(m.K8sNetworkEnforcement != string(KubernetesNetworkPolicyEnforcementNetworkPolicy) && !(m.K8sDenialRuleID != "" && m.K8sNetworkEnforcement == string(KubernetesNetworkPolicyEnforcementUnsupported)))) {
		return errors.New("Kubernetes executor metadata contains invalid declared execution provenance")
	}
	if m.K8sContainerName != "task" {
		return errors.New("Kubernetes executor metadata contains an invalid main container")
	}
	if !immutableSHA256Image(m.RequestedImage) || m.ResolvedImage != m.RequestedImage {
		return errors.New("Kubernetes executor metadata requires one immutable requested and resolved image")
	}
	if (m.K8sPolicyRevision != 0 || m.K8sPolicyHash != "") && (m.K8sPolicyRevision <= 0 || !IsKubernetesPolicyHash(m.K8sPolicyHash)) {
		return errors.New("Kubernetes executor metadata requires a policy acknowledgement")
	}
	if m.K8sDenialRuleID != "" && !IsKubernetesPolicyRuleID(m.K8sDenialRuleID) {
		return errors.New("Kubernetes executor metadata contains an invalid policy denial")
	}
	if m.K8sDenialRuleID != "" {
		if m.K8sLifecycle != "failed" || m.K8sTerminalReason != "PolicyDenied" || m.K8sRetentionState != "terminal" || m.K8sRetentionDeadline == nil {
			return errors.New("Kubernetes policy denial requires terminal bounded provenance")
		}
		return nil
	}
	switch m.K8sLifecycle {
	case "starting":
		if m.K8sJobName != "" || m.K8sJobUID != "" || m.K8sPodName != "" || m.K8sPodUID != "" || m.K8sSecretName != "" || m.K8sSecretUID != "" || m.K8sNetworkPolicyName != "" || m.K8sNetworkPolicyUID != "" || m.K8sTerminalReason != "" || m.K8sDenialRuleID != "" {
			return errors.New("starting Kubernetes metadata cannot claim runtime identities")
		}
		return nil
	case "pending":
		if !safeDNSLabel(m.K8sJobName) || !safeExecutorIdentity(m.K8sJobUID, 128, false) || (provenanceSupplied && (!safeDNSLabel(m.K8sSecretName) || !safeExecutorIdentity(m.K8sSecretUID, 128, false) || !safeDNSLabel(m.K8sNetworkPolicyName) || !safeExecutorIdentity(m.K8sNetworkPolicyUID, 128, false))) || m.K8sPodName != "" || m.K8sPodUID != "" || m.K8sTerminalReason != "" || m.K8sDenialRuleID != "" {
			return errors.New("pending Kubernetes metadata requires only a valid Job identity")
		}
		return nil
	case "running", "succeeded", "failed", "canceling", "stopped":
	default:
		return errors.New("Kubernetes executor metadata contains an invalid lifecycle")
	}
	if !safeDNSLabel(m.K8sJobName) || !safeExecutorIdentity(m.K8sJobUID, 128, false) ||
		!safeDNSLabel(m.K8sPodName) || !safeExecutorIdentity(m.K8sPodUID, 128, false) ||
		(provenanceSupplied && (!safeDNSLabel(m.K8sSecretName) || !safeExecutorIdentity(m.K8sSecretUID, 128, false) || !safeDNSLabel(m.K8sNetworkPolicyName) || !safeExecutorIdentity(m.K8sNetworkPolicyUID, 128, false))) {
		return errors.New("Kubernetes executor metadata requires valid Job and Pod identities")
	}
	_, allowedReason := kubernetesTerminalReasons[m.K8sTerminalReason]
	switch m.K8sLifecycle {
	case "running", "canceling", "succeeded":
		if m.K8sTerminalReason != "" {
			return errors.New("Kubernetes metadata lifecycle cannot contain a terminal reason")
		}
	case "failed":
		if !allowedReason {
			return errors.New("failed Kubernetes metadata requires an allow-listed terminal reason")
		}
		if m.K8sDenialRuleID != "" && m.K8sTerminalReason != "PolicyDenied" {
			return errors.New("Kubernetes policy denial requires the policy denied reason")
		}
	case "stopped":
		if m.K8sTerminalReason != "Canceled" {
			return errors.New("stopped Kubernetes metadata requires the canceled reason")
		}
	}
	if provenanceSupplied && (m.K8sLifecycle == "succeeded" || m.K8sLifecycle == "failed" || m.K8sLifecycle == "stopped") {
		if m.K8sRetentionState != "terminal" || m.K8sRetentionDeadline == nil || !m.K8sRetentionDeadline.After(time.Unix(0, 0)) {
			return errors.New("terminal Kubernetes metadata requires a bounded retention deadline")
		}
	} else if provenanceSupplied && (m.K8sRetentionState != "active" || m.K8sRetentionDeadline != nil) {
		return errors.New("active Kubernetes metadata requires active retention state")
	}
	return nil
}

func (m RunnerExecutorMetadata) hasKubernetesProvenance() bool {
	return m.K8sServiceAccount != "" || m.K8sRuntimeClass != "" || m.K8sResourcePolicyID != "" || m.K8sResourcePolicyHash != "" ||
		m.K8sNetworkProfile != "" || m.K8sNetworkEnforcement != "" || m.K8sSecretName != "" || m.K8sSecretUID != "" ||
		m.K8sNetworkPolicyName != "" || m.K8sNetworkPolicyUID != "" || m.K8sRetentionDeadline != nil || m.K8sRetentionState != ""
}

func immutableSHA256Image(value string) bool {
	digest := strings.LastIndex(value, "@sha256:")
	if digest <= 0 || len(value)-digest != len("@sha256:")+64 {
		return false
	}
	for _, character := range value[digest+len("@sha256:"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func safeDNSLabel(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func safeExecutorIdentity(value string, limit int, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if len(value) > limit {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
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
