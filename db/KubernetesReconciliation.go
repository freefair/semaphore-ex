package db

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxKubernetesReconciliationTokenLength = 128
const maxKubernetesReconciliationPageSize = 100

var (
	ErrKubernetesReconciliationSessionStale     = errors.New("Kubernetes reconciliation session is stale")
	ErrKubernetesReconciliationCoverageInvalid  = errors.New("Kubernetes reconciliation scan coverage is invalid")
	ErrKubernetesReconciliationRevisionConflict = errors.New("Kubernetes reconciliation revision conflict")
)

// KubernetesReconciliationSession is issued only after runner-token
// authentication. The runner may resume it but can never choose its identity,
// fence, cluster, namespace, or target snapshot.
type KubernetesReconciliationSession struct {
	SessionID                string                           `db:"session_id" json:"session_id"`
	Fence                    string                           `db:"-" json:"fence"`
	RunnerID                 int                              `db:"runner_id" json:"runner_id"`
	ClusterAlias             string                           `db:"cluster_alias" json:"cluster_alias"`
	Namespace                string                           `db:"namespace" json:"namespace"`
	Ready                    bool                             `db:"scan_complete" json:"ready"`
	Revision                 int64                            `db:"scan_revision" json:"revision"`
	TelemetryHighestSequence int64                            `db:"telemetry_highest_sequence" json:"telemetry_highest_sequence"`
	Targets                  []KubernetesReconciliationTarget `db:"-" json:"targets"`
}

type KubernetesReconciliationTarget struct {
	ProjectID         int        `db:"project_id" json:"project_id"`
	TaskID            int        `db:"task_id" json:"task_id"`
	Generation        int        `db:"generation" json:"generation"`
	JobName           string     `db:"job_name" json:"job_name"`
	JobUID            string     `db:"job_uid" json:"job_uid"`
	PodName           string     `db:"pod_name" json:"pod_name"`
	PodUID            string     `db:"pod_uid" json:"pod_uid"`
	SecretName        string     `db:"secret_name" json:"secret_name"`
	SecretUID         string     `db:"secret_uid" json:"secret_uid"`
	NetworkPolicyName string     `db:"network_policy_name" json:"network_policy_name"`
	NetworkPolicyUID  string     `db:"network_policy_uid" json:"network_policy_uid"`
	RetentionDeadline *time.Time `db:"retention_deadline" json:"retention_deadline,omitempty"`
	RetentionState    string     `db:"retention_state" json:"retention_state"`
}

type KubernetesReconciliationState string

const (
	KubernetesReconciliationObserved    KubernetesReconciliationState = "observed"
	KubernetesReconciliationAbsent      KubernetesReconciliationState = "absent"
	KubernetesReconciliationQuarantined KubernetesReconciliationState = "quarantined"
)

type KubernetesReconciliationObservation struct {
	ProjectID  int                           `json:"project_id"`
	TaskID     int                           `json:"task_id"`
	Generation int                           `json:"generation"`
	State      KubernetesReconciliationState `json:"state"`
	Reason     string                        `json:"reason,omitempty"`
	Revision   int64                         `json:"revision"`
}

type KubernetesReconciliationCandidate struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Reason   string `json:"reason"`
	Revision int64  `json:"revision"`
}

type KubernetesReconciliationScan struct {
	SessionID    string                                `json:"session_id"`
	Fence        string                                `json:"fence"`
	Revision     int64                                 `json:"revision"`
	Observations []KubernetesReconciliationObservation `json:"observations"`
	Candidates   []KubernetesReconciliationCandidate   `json:"candidates"`
}

// KubernetesReconciliationRemediationCommand has a deliberately closed action
// vocabulary; it can never be used to tunnel kubectl or cluster-scoped calls.
type KubernetesReconciliationRemediationCommand struct {
	CommandID        string                         `json:"command_id"`
	SessionID        string                         `json:"session_id"`
	Action           KubernetesReconciliationAction `json:"action"`
	ProjectID        int                            `json:"project_id"`
	TaskID           int                            `json:"task_id"`
	Generation       int                            `json:"generation"`
	ExpectedRevision int64                          `json:"expected_revision"`
	Target           KubernetesReconciliationTarget `json:"target"`
}

type KubernetesReconciliationAction string

const KubernetesReconciliationGarbageCollectExpired KubernetesReconciliationAction = "garbage_collect_expired"

type KubernetesReconciliationRemediationStatus string

const (
	KubernetesReconciliationRemediationPending   KubernetesReconciliationRemediationStatus = "pending"
	KubernetesReconciliationRemediationSucceeded KubernetesReconciliationRemediationStatus = "succeeded"
	KubernetesReconciliationRemediationBlocked   KubernetesReconciliationRemediationStatus = "blocked"
)

type KubernetesReconciliationRemediationEvidence string

const (
	KubernetesReconciliationEvidenceRemoved          KubernetesReconciliationRemediationEvidence = "removed"
	KubernetesReconciliationEvidenceAlreadyAbsent    KubernetesReconciliationRemediationEvidence = "already_absent"
	KubernetesReconciliationEvidenceIdentityMismatch KubernetesReconciliationRemediationEvidence = "identity_mismatch"
	KubernetesReconciliationEvidenceNameReused       KubernetesReconciliationRemediationEvidence = "name_reused"
	KubernetesReconciliationEvidenceUnsafeTarget     KubernetesReconciliationRemediationEvidence = "unsafe_target"
)

// KubernetesReconciliationRemediationRequest is an administrator's narrow,
// optimistic request. It cannot carry object names, labels, namespaces, or
// Kubernetes verbs: the server reconstructs those solely from its snapshot.
type KubernetesReconciliationRemediationRequest struct {
	Action           KubernetesReconciliationAction `json:"action"`
	IdempotencyKey   string                         `json:"idempotency_key"`
	ProjectID        int                            `json:"project_id"`
	TaskID           int                            `json:"task_id"`
	Generation       int                            `json:"generation"`
	ExpectedRevision int64                          `json:"expected_revision"`
}

// KubernetesReconciliationRemediationDescriptor is server-built diagnostic
// capability data. The browser may add only an idempotency key before posting
// the matching request; no Kubernetes object identity reaches that boundary.
type KubernetesReconciliationRemediationDescriptor struct {
	Action           KubernetesReconciliationAction `json:"action"`
	ProjectID        int                            `json:"project_id"`
	TaskID           int                            `json:"task_id"`
	Generation       int                            `json:"generation"`
	ExpectedRevision int64                          `json:"expected_revision"`
}

type KubernetesReconciliationRemediationResult struct {
	CommandID string                                      `json:"command_id"`
	Status    KubernetesReconciliationRemediationStatus   `json:"status"`
	Evidence  KubernetesReconciliationRemediationEvidence `json:"evidence"`
}

// KubernetesReconciliationDiagnosticTarget is the browser-safe task tuple.
// Kubernetes object names and UIDs stay in the server-side command snapshot.
type KubernetesReconciliationDiagnosticTarget struct {
	ProjectID         int        `json:"project_id"`
	TaskID            int        `json:"task_id"`
	Generation        int        `json:"generation"`
	RetentionDeadline *time.Time `json:"retention_deadline,omitempty"`
	RetentionState    string     `json:"retention_state"`
}

// KubernetesReconciliationDiagnostic is an administrator-only, sanitized
// status record. Only Remediation is actionable, and it is server-built.
type KubernetesReconciliationDiagnostic struct {
	SessionID   string                                         `db:"session_id" json:"-"`
	Target      *KubernetesReconciliationDiagnosticTarget      `json:"target,omitempty"`
	Candidate   *KubernetesReconciliationCandidate             `json:"candidate,omitempty"`
	State       KubernetesReconciliationState                  `db:"state" json:"state"`
	Reason      string                                         `db:"reason" json:"reason"`
	Remediation *KubernetesReconciliationRemediationDescriptor `json:"remediation,omitempty"`
}

func validKubernetesReconciliationToken(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= maxKubernetesReconciliationTokenLength
}
func NewKubernetesReconciliationToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
func (s KubernetesReconciliationSession) ValidateCredentials() error {
	if s.RunnerID <= 0 || !validKubernetesReconciliationToken(s.SessionID) || !validKubernetesReconciliationToken(s.Fence) || ValidateKubernetesClusterAlias(s.ClusterAlias) != nil || !safeDNSLabel(s.Namespace) {
		return fmt.Errorf("invalid Kubernetes reconciliation session")
	}
	return nil
}
func (t KubernetesReconciliationTarget) Validate() error {
	if t.ProjectID <= 0 || t.TaskID <= 0 || t.Generation <= 0 || !safeDNSLabel(t.JobName) || t.JobUID == "" || (t.PodName == "") != (t.PodUID == "") || (t.PodName != "" && (!safeDNSLabel(t.PodName) || len(t.PodUID) > 128)) || !safeDNSLabel(t.SecretName) || t.SecretUID == "" || !safeDNSLabel(t.NetworkPolicyName) || t.NetworkPolicyUID == "" || len(t.JobUID) > 128 || len(t.SecretUID) > 128 || len(t.NetworkPolicyUID) > 128 {
		return fmt.Errorf("invalid Kubernetes reconciliation target")
	}
	return nil
}
func (r KubernetesReconciliationRemediationRequest) Validate() error {
	if r.Action != KubernetesReconciliationGarbageCollectExpired || !validKubernetesReconciliationToken(r.IdempotencyKey) || r.ProjectID <= 0 || r.TaskID <= 0 || r.Generation <= 0 || r.ExpectedRevision < 0 {
		return fmt.Errorf("invalid Kubernetes reconciliation remediation request")
	}
	return nil
}
func (c KubernetesReconciliationRemediationCommand) Validate() error {
	if !validKubernetesReconciliationToken(c.CommandID) || !validKubernetesReconciliationToken(c.SessionID) || c.Action != KubernetesReconciliationGarbageCollectExpired || c.ExpectedRevision < 0 || c.ProjectID != c.Target.ProjectID || c.TaskID != c.Target.TaskID || c.Generation != c.Target.Generation || c.Target.Validate() != nil {
		return fmt.Errorf("invalid Kubernetes reconciliation remediation command")
	}
	return nil
}
func (r KubernetesReconciliationRemediationResult) Validate() error {
	if !validKubernetesReconciliationToken(r.CommandID) {
		return fmt.Errorf("invalid Kubernetes reconciliation remediation result")
	}
	if r.Status == KubernetesReconciliationRemediationSucceeded {
		if r.Evidence != KubernetesReconciliationEvidenceRemoved && r.Evidence != KubernetesReconciliationEvidenceAlreadyAbsent {
			return fmt.Errorf("invalid Kubernetes reconciliation success evidence")
		}
		return nil
	}
	if r.Status == KubernetesReconciliationRemediationBlocked && (r.Evidence == KubernetesReconciliationEvidenceIdentityMismatch || r.Evidence == KubernetesReconciliationEvidenceNameReused || r.Evidence == KubernetesReconciliationEvidenceUnsafeTarget) {
		return nil
	}
	return fmt.Errorf("invalid Kubernetes reconciliation remediation result")
}
func (o KubernetesReconciliationObservation) Validate() error {
	if o.ProjectID <= 0 || o.TaskID <= 0 || o.Generation <= 0 || o.Revision < 0 || len(o.Reason) > 128 {
		return fmt.Errorf("invalid Kubernetes reconciliation observation")
	}
	if o.State != KubernetesReconciliationObserved && o.State != KubernetesReconciliationAbsent && o.State != KubernetesReconciliationQuarantined {
		return fmt.Errorf("invalid Kubernetes reconciliation state")
	}
	if o.State == KubernetesReconciliationQuarantined && o.Reason == "" {
		return fmt.Errorf("Kubernetes quarantine needs a reason")
	}
	return nil
}
func (c KubernetesReconciliationCandidate) Validate() error {
	if (c.Resource != "job" && c.Resource != "pod" && c.Resource != "secret" && c.Resource != "network_policy") || !safeDNSLabel(c.Name) || c.UID == "" || len(c.UID) > 128 || len(c.Reason) == 0 || len(c.Reason) > 128 || c.Revision < 0 {
		return fmt.Errorf("invalid Kubernetes reconciliation candidate")
	}
	return nil
}

type KubernetesReconciliationSessionRepository interface {
	OpenKubernetesReconciliationSession(authenticatedRunnerID int, clusterAlias, namespace, resumeSessionID, resumeFence string) (KubernetesReconciliationSession, error)
	IngestKubernetesReconciliationScan(authenticatedRunnerID int, scan KubernetesReconciliationScan) error
	GetKubernetesReconciliationCommands(authenticatedRunnerID int, sessionID, fence string, limit int) ([]KubernetesReconciliationRemediationCommand, error)
	ReportKubernetesReconciliationRemediation(authenticatedRunnerID int, sessionID, fence string, result KubernetesReconciliationRemediationResult) error
}

type KubernetesReconciliationRepository interface {
	KubernetesReconciliationSessionRepository
	GetKubernetesReconciliationPendingDiagnostics(runnerID int, limit int) ([]KubernetesReconciliationDiagnostic, error)
	RequestKubernetesReconciliationRemediation(runnerID int, request KubernetesReconciliationRemediationRequest) (KubernetesReconciliationRemediationCommand, error)
}
