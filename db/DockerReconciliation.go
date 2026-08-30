package db

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxDockerReconciliationRunnerBootLength = 128
	maxDockerReconciliationResourceLength   = 32
	maxDockerReconciliationReasonLength     = 128
	maxDockerReconciliationQueryLimit       = 100
	maxDockerReconciliationCandidates       = 100
)

var (
	ErrDockerReconciliationRevisionConflict  = errors.New("Docker reconciliation revision conflict")
	ErrDockerReconciliationSequenceConflict  = errors.New("Docker reconciliation sequence conflict")
	ErrDockerReconciliationImmutableMutation = errors.New("Docker reconciliation immutable mutation")
	ErrDockerReconciliationSessionStale      = errors.New("Docker reconciliation session is stale")
	ErrDockerReconciliationCoverageInvalid   = errors.New("Docker reconciliation scan coverage is invalid")
	ErrDockerReconciliationCommandStale      = errors.New("Docker reconciliation remediation command is stale")
)

const maxDockerReconciliationSessionTokenLength = 128

// DockerReconciliationSession is issued by the server after runner-token
// authentication. SessionID and Fence authenticate the reporting process;
// TargetBoot is a separate server-issued label namespace for containers that
// process will create. A runner never chooses either authority.
type DockerReconciliationSession struct {
	SessionID       string `db:"session_id" json:"session_id"`
	Fence           string `db:"-" json:"fence"`
	RunnerID        int    `db:"runner_id" json:"runner_id"`
	TargetBoot      string `db:"target_boot" json:"target_boot"`
	Ready           bool   `db:"scan_complete" json:"ready"`
	HighestSequence int64  `db:"scan_highest_sequence" json:"highest_sequence"`
	// ScanCursor is issued by the server and identifies the only target page
	// the runner may submit next. It is protected by the session fence.
	ScanCursor      int64                            `db:"scan_cursor" json:"scan_cursor"`
	ScanTargetCount int                              `db:"scan_target_count" json:"scan_target_count"`
	ScanTargets     []DockerReconciliationScanTarget `db:"-" json:"scan_targets"`
}

func (s DockerReconciliationSession) ValidatePublic() error {
	if s.RunnerID <= 0 || !validDockerReconciliationToken(s.SessionID) || !validDockerReconciliationToken(s.TargetBoot) {
		return fmt.Errorf("invalid Docker reconciliation session")
	}
	return nil
}

func (s DockerReconciliationSession) ValidateCredentials() error {
	if err := s.ValidatePublic(); err != nil || !validDockerReconciliationToken(s.Fence) {
		return fmt.Errorf("invalid Docker reconciliation session credentials")
	}
	return nil
}

func validDockerReconciliationToken(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= maxDockerReconciliationSessionTokenLength
}

// ValidDockerReconciliationOpaqueHash accepts only a canonical SHA-256
// digest. It is used for volume creation identity and is safe to transport:
// it contains no daemon creation metadata.
func ValidDockerReconciliationOpaqueHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func NewDockerReconciliationToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// DockerReconciliationScanComplete is an explicit commit boundary. The
// covered resource tuples must exactly match the server's unfinished target set;
// accepting a boolean acknowledgement would leave unscanned containers hidden.
type DockerReconciliationScanComplete struct {
	SessionID       string                           `json:"session_id"`
	Fence           string                           `json:"fence"`
	CoveredTargets  []DockerReconciliationScanTarget `json:"covered_targets"`
	HighestSequence int64                            `json:"highest_sequence"`
	ScanCursor      int64                            `json:"scan_cursor"`
}

func (c DockerReconciliationScanComplete) Validate() error {
	if !validDockerReconciliationToken(c.SessionID) || !validDockerReconciliationToken(c.Fence) || c.HighestSequence < 0 || c.ScanCursor < 0 || len(c.CoveredTargets) > maxDockerReconciliationQueryLimit {
		return fmt.Errorf("invalid Docker reconciliation scan completion")
	}
	seen := make(map[string]struct{}, len(c.CoveredTargets))
	for _, target := range c.CoveredTargets {
		if target.Validate() != nil {
			return fmt.Errorf("invalid Docker reconciliation scan target")
		}
		key := target.ScanKey()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate Docker reconciliation scan target")
		}
		seen[key] = struct{}{}
	}
	return nil
}

type DockerReconciliationScanTarget struct {
	TargetBoot    string                       `db:"target_boot" json:"target_boot"`
	ProjectID     int                          `db:"project_id" json:"project_id"`
	TaskID        int                          `db:"task_id" json:"task_id"`
	Generation    int                          `db:"generation" json:"generation"`
	Resource      DockerReconciliationResource `db:"resource" json:"resource"`
	ContainerName string                       `db:"container_name" json:"container_name"`
	// Sequence is allocated by the server per target boot when the immutable
	// session snapshot is made. It makes retries/restarts deterministic without
	// allowing the runner to choose an append cursor.
	Sequence int64 `db:"sequence" json:"sequence"`
}

type DockerReconciliationScan struct {
	SessionID    string                            `json:"session_id"`
	Fence        string                            `json:"fence"`
	Observations []DockerReconciliationObservation `json:"observations"`
	Complete     DockerReconciliationScanComplete  `json:"complete"`
}

// DockerReconciliationQuarantine is a runner report for a bound Docker
// attempt whose stop state could not be proven. Sequence allocation is server
// owned so this report cannot forge or overwrite reconciliation history.
type DockerReconciliationStopQuarantine struct {
	ProjectID     int                          `json:"project_id"`
	TaskID        int                          `json:"task_id"`
	Generation    int                          `json:"generation"`
	Resource      DockerReconciliationResource `json:"resource"`
	ContainerName string                       `json:"container_name"`
	Reason        string                       `json:"reason"`
}

func (t DockerReconciliationScanTarget) ScanKey() string {
	return fmt.Sprintf("%s/%d/%d/%d/%s/%s", t.TargetBoot, t.ProjectID, t.TaskID, t.Generation, t.Resource, t.ContainerName)
}
func (t DockerReconciliationScanTarget) Validate() error {
	if ValidateDockerRunnerBoot(t.TargetBoot) != nil || t.ProjectID <= 0 || t.TaskID <= 0 || t.Generation <= 0 || len(t.ContainerName) == 0 || len(t.ContainerName) > MaxRunnerContainerIdentityLength || (t.Resource != DockerReconciliationResourceTask && t.Resource != DockerReconciliationResourceHelper) {
		return fmt.Errorf("invalid Docker reconciliation scan target")
	}
	return nil
}

type DockerReconciliationState string

const (
	DockerReconciliationRunning    DockerReconciliationState = "running"
	DockerReconciliationExited     DockerReconciliationState = "exited"
	DockerReconciliationAbsent     DockerReconciliationState = "absent"
	DockerReconciliationUnknown    DockerReconciliationState = "unknown"
	DockerReconciliationDuplicate  DockerReconciliationState = "duplicate"
	DockerReconciliationLeaked     DockerReconciliationState = "leaked"
	DockerReconciliationQuarantine DockerReconciliationState = "quarantined"
)

type DockerReconciliationResource string

const (
	DockerReconciliationResourceTask   DockerReconciliationResource = "task"
	DockerReconciliationResourceHelper DockerReconciliationResource = "helper"
)

type DockerReconciliationQuarantineStatus string

const (
	DockerReconciliationQuarantineNone       DockerReconciliationQuarantineStatus = "none"
	DockerReconciliationQuarantinePending    DockerReconciliationQuarantineStatus = "pending"
	DockerReconciliationQuarantineRemediated DockerReconciliationQuarantineStatus = "remediated"
)

type DockerReconciliationRemediation string

const (
	DockerReconciliationRemediationNone    DockerReconciliationRemediation = "none"
	DockerReconciliationRemediationInspect DockerReconciliationRemediation = "inspect"
	DockerReconciliationRemediationStop    DockerReconciliationRemediation = "stop"
	DockerReconciliationRemediationRemove  DockerReconciliationRemediation = "remove"
)

// DockerReconciliationOwner identifies one immutable runner process. Every
// repository read and write is scoped by this pair, preventing a restarted or
// reused runner row from observing a previous boot's reconciliation history.
type DockerReconciliationOwner struct {
	RunnerID   int    `json:"runner_id"`
	RunnerBoot string `json:"runner_boot"`
}

// DockerReconciliationCandidateResource deliberately has a smaller vocabulary
// than an observation. Candidates are managed daemon objects which cannot be
// tied to one server-issued attempt tuple, so they are quarantined separately
// and never participate in scan-complete readiness.
type DockerReconciliationCandidateResource string

const (
	DockerReconciliationCandidateContainer DockerReconciliationCandidateResource = "container"
	DockerReconciliationCandidateVolume    DockerReconciliationCandidateResource = "volume"
)

type DockerReconciliationCandidateReason string

const (
	DockerReconciliationCandidateMalformed   DockerReconciliationCandidateReason = "malformed"
	DockerReconciliationCandidateConflicting DockerReconciliationCandidateReason = "conflicting"
	DockerReconciliationCandidateDuplicate   DockerReconciliationCandidateReason = "duplicate"
	DockerReconciliationCandidateExtra       DockerReconciliationCandidateReason = "extra"
)

// DockerReconciliationOrphanCandidate is a bounded, read-only quarantine
// report. It intentionally carries no Docker labels or daemon error text.
// Server-side authentication supplies RunnerID and session ownership.
type DockerReconciliationOrphanCandidate struct {
	RunnerID     int                                   `db:"runner_id" json:"runner_id,omitempty"`
	Resource     DockerReconciliationCandidateResource `db:"resource" json:"resource"`
	Identifier   string                                `db:"identifier" json:"identifier"`
	Name         string                                `db:"name" json:"name"`
	Reason       DockerReconciliationCandidateReason   `db:"reason" json:"reason"`
	Fingerprint  string                                `db:"fingerprint" json:"fingerprint"`
	Identity     string                                `db:"identity" json:"identity,omitempty"`
	ObservedAt   time.Time                             `db:"observed_at" json:"observed_at"`
	Revision     int64                                 `db:"revision" json:"revision"`
	Status       DockerReconciliationCandidateStatus   `db:"status" json:"status"`
	RemediatedAt *time.Time                            `db:"remediated_at" json:"remediated_at,omitempty"`
}

type DockerReconciliationCandidateStatus string

const (
	DockerReconciliationCandidatePending  DockerReconciliationCandidateStatus = "pending"
	DockerReconciliationCandidateResolved DockerReconciliationCandidateStatus = "resolved"
)

func (c DockerReconciliationOrphanCandidate) Validate() error {
	if c.RunnerID < 0 || len(c.Identifier) == 0 || len(c.Identifier) > MaxRunnerContainerIdentityLength || len(c.Name) > MaxRunnerContainerIdentityLength || len(c.Identity) > MaxRunnerContainerIdentityLength || c.ObservedAt.IsZero() {
		return fmt.Errorf("invalid Docker reconciliation orphan candidate")
	}
	switch c.Resource {
	case DockerReconciliationCandidateContainer, DockerReconciliationCandidateVolume:
	default:
		return fmt.Errorf("invalid Docker reconciliation candidate resource")
	}
	switch c.Reason {
	case DockerReconciliationCandidateMalformed, DockerReconciliationCandidateConflicting, DockerReconciliationCandidateDuplicate, DockerReconciliationCandidateExtra:
		return nil
	default:
		return fmt.Errorf("invalid Docker reconciliation candidate reason")
	}
}

// DockerReconciliationCandidateQuery is a stable, bounded page over the
// immutable candidate fingerprint. Candidates are scoped to both a runner and
// its authenticated reconciliation session; a similarly named object from a
// later runner process can never be selected accidentally.
type DockerReconciliationCandidateQuery struct {
	AfterFingerprint string `json:"after_fingerprint,omitempty"`
	Limit            int    `json:"limit"`
}

type DockerReconciliationStatePage struct {
	States     []DockerReconciliationStateRecord `json:"states"`
	NextCursor *int64                            `json:"next_cursor,omitempty"`
}

type DockerReconciliationCandidatePage struct {
	Candidates []DockerReconciliationOrphanCandidate `json:"candidates"`
	NextCursor *string                               `json:"next_cursor,omitempty"`
}

func (q DockerReconciliationCandidateQuery) Validate() error {
	if q.Limit <= 0 || q.Limit > maxDockerReconciliationQueryLimit || len(q.AfterFingerprint) > 64 {
		return fmt.Errorf("invalid Docker reconciliation candidate query")
	}
	return nil
}

type DockerReconciliationRemediationAction string

const DockerReconciliationRemediationRetryStopAndCleanup DockerReconciliationRemediationAction = "retry_stop_and_cleanup"

type DockerReconciliationRemediationTarget string

const (
	DockerReconciliationRemediationTargetQuarantine DockerReconciliationRemediationTarget = "quarantine"
	DockerReconciliationRemediationTargetCandidate  DockerReconciliationRemediationTarget = "candidate"
)

type DockerReconciliationRemediationStatus string

const (
	DockerReconciliationRemediationPending   DockerReconciliationRemediationStatus = "pending"
	DockerReconciliationRemediationSucceeded DockerReconciliationRemediationStatus = "succeeded"
	DockerReconciliationRemediationErrored   DockerReconciliationRemediationStatus = "error"
)

// DockerReconciliationRemediationRequest is an admin intent only. It contains
// no daemon endpoint or arbitrary operation: the runner later executes the
// sole allow-listed action after independently re-inspecting the resource.
type DockerReconciliationRemediationRequest struct {
	Action           DockerReconciliationRemediationAction `json:"action"`
	IdempotencyKey   string                                `json:"idempotency_key"`
	ExpectedRevision int64                                 `json:"expected_revision"`
	Target           DockerReconciliationRemediationTarget `json:"target"`
	Quarantine       *DockerReconciliationKey              `json:"quarantine,omitempty"`
	SessionID        string                                `json:"session_id,omitempty"`
	Fingerprint      string                                `json:"fingerprint,omitempty"`
}

func (r DockerReconciliationRemediationRequest) Validate() error {
	if r.Action != DockerReconciliationRemediationRetryStopAndCleanup || !validDockerReconciliationToken(r.IdempotencyKey) || r.ExpectedRevision <= 0 {
		return fmt.Errorf("invalid Docker reconciliation remediation request")
	}
	switch r.Target {
	case DockerReconciliationRemediationTargetQuarantine:
		if r.Quarantine == nil || r.Quarantine.Validate() != nil || r.SessionID != "" || r.Fingerprint != "" {
			return fmt.Errorf("invalid Docker reconciliation quarantine target")
		}
	case DockerReconciliationRemediationTargetCandidate:
		if r.Quarantine != nil || !validDockerReconciliationToken(r.SessionID) || len(r.Fingerprint) != 64 {
			return fmt.Errorf("invalid Docker reconciliation candidate target")
		}
	default:
		return fmt.Errorf("invalid Docker reconciliation remediation target")
	}
	return nil
}

// DockerReconciliationRemediationCommand is a server-issued, session-fenced
// work item. Fingerprint and DaemonID are immutable identity evidence; raw
// Docker messages, labels, and inspect objects never cross the runner API.
type DockerReconciliationRemediationCommand struct {
	CommandID          string                                `db:"command_id" json:"command_id"`
	SessionID          string                                `db:"session_id" json:"session_id"`
	RunnerID           int                                   `db:"runner_id" json:"runner_id"`
	Action             DockerReconciliationRemediationAction `db:"action" json:"action"`
	Target             DockerReconciliationRemediationTarget `db:"target" json:"target"`
	ExpectedRevision   int64                                 `db:"expected_revision" json:"expected_revision"`
	Fingerprint        string                                `db:"fingerprint" json:"fingerprint"`
	DaemonID           string                                `db:"daemon_id" json:"daemon_id"`
	CandidateResource  DockerReconciliationCandidateResource `db:"candidate_resource" json:"candidate_resource,omitempty"`
	CandidateSessionID string                                `db:"candidate_session_id" json:"-"`
	CandidateIdentity  string                                `db:"candidate_identity" json:"candidate_identity,omitempty"`
	RunnerBoot         string                                `db:"runner_boot" json:"-"`
	ProjectID          int                                   `db:"project_id" json:"-"`
	TaskID             int                                   `db:"task_id" json:"-"`
	Generation         int                                   `db:"generation" json:"-"`
	Resource           DockerReconciliationResource          `db:"resource" json:"-"`
	Quarantine         *DockerReconciliationKey              `db:"-" json:"quarantine,omitempty"`
	Status             DockerReconciliationRemediationStatus `db:"status" json:"status"`
}

func (c DockerReconciliationRemediationCommand) Validate() error {
	if !validDockerReconciliationToken(c.CommandID) || !validDockerReconciliationToken(c.SessionID) || c.RunnerID <= 0 || c.Action != DockerReconciliationRemediationRetryStopAndCleanup || !ValidDockerReconciliationOpaqueHash(c.Fingerprint) || c.DaemonID == "" {
		return fmt.Errorf("invalid Docker reconciliation remediation command")
	}
	switch c.Target {
	case DockerReconciliationRemediationTargetQuarantine:
		if c.Quarantine == nil || c.Quarantine.Validate() != nil {
			return fmt.Errorf("invalid Docker reconciliation quarantine command")
		}
	case DockerReconciliationRemediationTargetCandidate:
		switch c.CandidateResource {
		case DockerReconciliationCandidateContainer:
		case DockerReconciliationCandidateVolume:
			if !ValidDockerReconciliationOpaqueHash(c.CandidateIdentity) {
				return fmt.Errorf("invalid Docker volume remediation identity")
			}
		default:
			return fmt.Errorf("invalid Docker reconciliation candidate command")
		}
	default:
		return fmt.Errorf("invalid Docker reconciliation remediation command target")
	}
	return nil
}

type DockerReconciliationRemediationEvidence string

const (
	DockerReconciliationEvidenceRemoved           DockerReconciliationRemediationEvidence = "removed"
	DockerReconciliationEvidenceAlreadyAbsent     DockerReconciliationRemediationEvidence = "already_absent"
	DockerReconciliationEvidenceIdentityMismatch  DockerReconciliationRemediationEvidence = "identity_mismatch"
	DockerReconciliationEvidenceStillRunning      DockerReconciliationRemediationEvidence = "still_running"
	DockerReconciliationEvidenceVolumeInUse       DockerReconciliationRemediationEvidence = "volume_in_use"
	DockerReconciliationEvidenceDaemonUnavailable DockerReconciliationRemediationEvidence = "daemon_unavailable"
)

type DockerReconciliationRemediationResult struct {
	CommandID   string                                  `json:"command_id"`
	Fingerprint string                                  `json:"fingerprint"`
	Status      DockerReconciliationRemediationStatus   `json:"status"`
	Evidence    DockerReconciliationRemediationEvidence `json:"evidence"`
}

func (r DockerReconciliationRemediationResult) Validate() error {
	if !validDockerReconciliationToken(r.CommandID) || len(r.Fingerprint) != 64 {
		return fmt.Errorf("invalid Docker reconciliation remediation result")
	}
	switch r.Status {
	case DockerReconciliationRemediationSucceeded:
		if r.Evidence != DockerReconciliationEvidenceRemoved && r.Evidence != DockerReconciliationEvidenceAlreadyAbsent {
			return fmt.Errorf("invalid successful Docker remediation evidence")
		}
	case DockerReconciliationRemediationPending, DockerReconciliationRemediationErrored:
		switch r.Evidence {
		case DockerReconciliationEvidenceIdentityMismatch, DockerReconciliationEvidenceStillRunning, DockerReconciliationEvidenceVolumeInUse, DockerReconciliationEvidenceDaemonUnavailable:
		default:
			return fmt.Errorf("invalid unresolved Docker remediation evidence")
		}
	default:
		return fmt.Errorf("invalid Docker reconciliation remediation result")
	}
	return nil
}

func (o DockerReconciliationOwner) Validate() error {
	if o.RunnerID <= 0 || o.RunnerBoot == "" || strings.TrimSpace(o.RunnerBoot) != o.RunnerBoot || len(o.RunnerBoot) > maxDockerReconciliationRunnerBootLength {
		return fmt.Errorf("invalid Docker reconciliation owner")
	}
	return nil
}

func ValidateDockerRunnerBoot(boot string) error {
	return DockerReconciliationOwner{RunnerID: 1, RunnerBoot: boot}.Validate()
}

// DockerReconciliationObservation records one durable, ordered observation of
// a container labeled for a runner boot. It is immutable audit evidence; the
// separate DockerReconciliationStateRecord is the only mutable authority.
type DockerReconciliationObservation struct {
	ID            int                          `db:"id" json:"id"`
	Revision      int                          `db:"revision" json:"revision"`
	Sequence      int64                        `db:"sequence" json:"sequence"`
	RunnerID      int                          `db:"runner_id" json:"runner_id"`
	RunnerBoot    string                       `db:"runner_boot" json:"runner_boot"`
	TaskID        int                          `db:"task_id" json:"task_id"`
	ProjectID     int                          `db:"project_id" json:"project_id"`
	Generation    int                          `db:"generation" json:"generation"`
	Resource      DockerReconciliationResource `db:"resource" json:"resource"`
	ContainerID   string                       `db:"container_id" json:"container_id,omitempty"`
	ContainerName string                       `db:"container_name" json:"container_name,omitempty"`
	State         DockerReconciliationState    `db:"state" json:"state"`
	Reason        string                       `db:"reason" json:"reason,omitempty"`
	ObservedAt    time.Time                    `db:"observed_at" json:"observed_at"`

	QuarantineStatus  DockerReconciliationQuarantineStatus `db:"quarantine_status" json:"quarantine_status"`
	Remediation       DockerReconciliationRemediation      `db:"remediation" json:"remediation"`
	RemediationReason string                               `db:"remediation_reason" json:"remediation_reason,omitempty"`
	QuarantinedAt     *time.Time                           `db:"quarantined_at" json:"quarantined_at,omitempty"`
	RemediatedAt      *time.Time                           `db:"remediated_at" json:"remediated_at,omitempty"`
}

// DockerReconciliationKey identifies the canonical state for one labeled
// Docker resource. It intentionally includes the immutable task attempt
// identity, so a restarted runner or a later generation cannot overwrite it.
type DockerReconciliationKey struct {
	DockerReconciliationOwner
	ProjectID  int                          `json:"project_id"`
	TaskID     int                          `json:"task_id"`
	Generation int                          `json:"generation"`
	Resource   DockerReconciliationResource `json:"resource"`
}

func (k DockerReconciliationKey) Validate() error {
	if err := k.DockerReconciliationOwner.Validate(); err != nil {
		return err
	}
	if k.ProjectID <= 0 || k.TaskID <= 0 || k.Generation <= 0 {
		return fmt.Errorf("invalid Docker reconciliation key")
	}
	switch k.Resource {
	case DockerReconciliationResourceTask, DockerReconciliationResourceHelper:
		return nil
	default:
		return fmt.Errorf("invalid Docker reconciliation resource")
	}
}

// DockerReconciliationStateRecord is the authoritative state used for
// quarantine/remediation decisions. Observations remain immutable audit
// evidence; mutations apply only to this revision-fenced record.
type DockerReconciliationStateRecord struct {
	RunnerID       int                          `db:"runner_id" json:"runner_id"`
	RunnerBoot     string                       `db:"runner_boot" json:"runner_boot"`
	ProjectID      int                          `db:"project_id" json:"project_id"`
	TaskID         int                          `db:"task_id" json:"task_id"`
	Generation     int                          `db:"generation" json:"generation"`
	Resource       DockerReconciliationResource `db:"resource" json:"resource"`
	Revision       int64                        `db:"revision" json:"revision"`
	LatestSequence int64                        `db:"latest_sequence" json:"latest_sequence"`
	ContainerID    string                       `db:"container_id" json:"container_id,omitempty"`
	ContainerName  string                       `db:"container_name" json:"container_name,omitempty"`
	State          DockerReconciliationState    `db:"state" json:"state"`
	Reason         string                       `db:"reason" json:"reason,omitempty"`
	UpdatedAt      time.Time                    `db:"updated_at" json:"updated_at"`

	QuarantineStatus  DockerReconciliationQuarantineStatus `db:"quarantine_status" json:"quarantine_status"`
	Remediation       DockerReconciliationRemediation      `db:"remediation" json:"remediation"`
	RemediationReason string                               `db:"remediation_reason" json:"remediation_reason,omitempty"`
	QuarantinedAt     *time.Time                           `db:"quarantined_at" json:"quarantined_at,omitempty"`
	RemediatedAt      *time.Time                           `db:"remediated_at" json:"remediated_at,omitempty"`
}

func (s DockerReconciliationStateRecord) Key() DockerReconciliationKey {
	return DockerReconciliationKey{DockerReconciliationOwner: DockerReconciliationOwner{RunnerID: s.RunnerID, RunnerBoot: s.RunnerBoot}, ProjectID: s.ProjectID, TaskID: s.TaskID, Generation: s.Generation, Resource: s.Resource}
}

func (o DockerReconciliationObservation) Key() DockerReconciliationKey {
	return DockerReconciliationKey{
		DockerReconciliationOwner: o.Owner(), ProjectID: o.ProjectID, TaskID: o.TaskID,
		Generation: o.Generation, Resource: o.Resource,
	}
}

func (s DockerReconciliationStateRecord) Validate() error {
	if err := s.Key().Validate(); err != nil {
		return err
	}
	observation := DockerReconciliationObservation{
		Revision: 0, Sequence: s.LatestSequence, RunnerID: s.RunnerID, RunnerBoot: s.RunnerBoot,
		ProjectID: s.ProjectID, TaskID: s.TaskID, Generation: s.Generation, Resource: s.Resource,
		ContainerID: s.ContainerID, ContainerName: s.ContainerName, State: s.State, Reason: s.Reason,
		ObservedAt: s.UpdatedAt, QuarantineStatus: s.QuarantineStatus, Remediation: s.Remediation,
		RemediationReason: s.RemediationReason, QuarantinedAt: s.QuarantinedAt, RemediatedAt: s.RemediatedAt,
	}
	if s.Revision <= 0 {
		return fmt.Errorf("invalid Docker reconciliation state revision")
	}
	return observation.Validate()
}

func (o DockerReconciliationObservation) Owner() DockerReconciliationOwner {
	return DockerReconciliationOwner{RunnerID: o.RunnerID, RunnerBoot: o.RunnerBoot}
}

func (o DockerReconciliationObservation) Validate() error {
	if err := o.Owner().Validate(); err != nil {
		return err
	}
	if o.ID < 0 || o.Revision < 0 || o.Sequence <= 0 || o.TaskID <= 0 || o.ProjectID <= 0 || o.Generation <= 0 || o.ObservedAt.IsZero() {
		return fmt.Errorf("invalid Docker reconciliation observation")
	}
	if len(o.Resource) == 0 || len(o.Resource) > maxDockerReconciliationResourceLength || len(o.ContainerID) > MaxRunnerContainerIdentityLength || len(o.ContainerName) > MaxRunnerContainerIdentityLength || len(o.Reason) > maxDockerReconciliationReasonLength || len(o.RemediationReason) > maxDockerReconciliationReasonLength {
		return fmt.Errorf("Docker reconciliation observation exceeds bounds")
	}
	switch o.Resource {
	case DockerReconciliationResourceTask, DockerReconciliationResourceHelper:
	default:
		return fmt.Errorf("invalid Docker reconciliation resource")
	}
	switch o.State {
	case DockerReconciliationRunning, DockerReconciliationExited, DockerReconciliationAbsent, DockerReconciliationUnknown, DockerReconciliationDuplicate, DockerReconciliationLeaked, DockerReconciliationQuarantine:
	default:
		return fmt.Errorf("invalid Docker reconciliation state")
	}
	switch o.QuarantineStatus {
	case DockerReconciliationQuarantineNone:
		if o.State == DockerReconciliationQuarantine || o.Remediation != DockerReconciliationRemediationNone || o.RemediationReason != "" || o.QuarantinedAt != nil || o.RemediatedAt != nil {
			return fmt.Errorf("invalid non-quarantined Docker reconciliation observation")
		}
	case DockerReconciliationQuarantinePending:
		if o.State != DockerReconciliationQuarantine || o.QuarantinedAt == nil || o.RemediatedAt != nil {
			return fmt.Errorf("invalid pending Docker reconciliation quarantine")
		}
	case DockerReconciliationQuarantineRemediated:
		if o.State != DockerReconciliationQuarantine || o.QuarantinedAt == nil || o.RemediatedAt == nil || o.RemediatedAt.Before(*o.QuarantinedAt) {
			return fmt.Errorf("invalid remediated Docker reconciliation quarantine")
		}
	default:
		return fmt.Errorf("invalid Docker reconciliation quarantine status")
	}
	switch o.Remediation {
	case DockerReconciliationRemediationNone:
		if o.QuarantineStatus != DockerReconciliationQuarantineNone {
			return fmt.Errorf("Docker reconciliation quarantine requires remediation")
		}
	case DockerReconciliationRemediationInspect, DockerReconciliationRemediationStop, DockerReconciliationRemediationRemove:
		if o.QuarantineStatus == DockerReconciliationQuarantineNone || o.RemediationReason == "" {
			return fmt.Errorf("invalid Docker reconciliation remediation")
		}
	default:
		return fmt.Errorf("invalid Docker reconciliation remediation")
	}
	return nil
}

// DockerReconciliationQuery is bounded and always applies to one owner.
// AfterSequence provides stable forward pagination over the boot-local log.
type DockerReconciliationQuery struct {
	AfterSequence int64 `json:"after_sequence,omitempty"`
	Limit         int   `json:"limit"`
}

func (q DockerReconciliationQuery) Validate() error {
	if q.AfterSequence < 0 || q.Limit <= 0 || q.Limit > maxDockerReconciliationQueryLimit {
		return fmt.Errorf("invalid Docker reconciliation query")
	}
	return nil
}

type DockerReconciliationSessionRepository interface {
	OpenDockerReconciliationSession(authenticatedRunnerID int, resumeSessionID string, resumeFence string) (DockerReconciliationSession, error)
	CompleteDockerReconciliationScan(authenticatedRunnerID int, complete DockerReconciliationScanComplete) error
	BindDockerReconciliationAttempt(authenticatedRunnerID int, session DockerReconciliationSession, target DockerReconciliationScanTarget) error
	BindDockerReconciliationAttempts(authenticatedRunnerID int, session DockerReconciliationSession, targets []DockerReconciliationScanTarget) error
	QuarantineDockerReconciliationAttempt(authenticatedRunnerID int, sessionID string, fence string, quarantine DockerReconciliationStopQuarantine) error
	IngestDockerReconciliationScan(authenticatedRunnerID int, scan DockerReconciliationScan) error
	RecordDockerReconciliationOrphanCandidates(authenticatedRunnerID int, sessionID string, fence string, candidates []DockerReconciliationOrphanCandidate) error
	CreateDockerReconciliationSessionObservation(authenticatedRunnerID int, sessionID string, fence string, observation DockerReconciliationObservation) (state DockerReconciliationStateRecord, replayed bool, err error)
}

type DockerReconciliationRepository interface {
	DockerReconciliationSessionRepository
	CreateDockerReconciliationObservation(authenticatedRunnerID int, observation DockerReconciliationObservation) (state DockerReconciliationStateRecord, replayed bool, err error)
	GetDockerReconciliationObservation(owner DockerReconciliationOwner, sequence int64) (DockerReconciliationObservation, error)
	GetDockerReconciliationObservations(owner DockerReconciliationOwner, query DockerReconciliationQuery) ([]DockerReconciliationObservation, error)
	GetDockerReconciliationState(key DockerReconciliationKey) (DockerReconciliationStateRecord, error)
	GetDockerReconciliationStates(owner DockerReconciliationOwner, query DockerReconciliationQuery) ([]DockerReconciliationStateRecord, error)
	UpdateDockerReconciliationState(state DockerReconciliationStateRecord, expectedRevision int) (DockerReconciliationStateRecord, error)
	GetDockerReconciliationCandidates(runnerID int, sessionID string, query DockerReconciliationCandidateQuery) ([]DockerReconciliationOrphanCandidate, error)
	GetDockerReconciliationPendingStates(owner DockerReconciliationOwner, query DockerReconciliationQuery) (DockerReconciliationStatePage, error)
	GetDockerReconciliationPendingCandidates(runnerID int, sessionID string, query DockerReconciliationCandidateQuery) (DockerReconciliationCandidatePage, error)
	RequestDockerReconciliationRemediation(runnerID int, request DockerReconciliationRemediationRequest) (DockerReconciliationRemediationCommand, error)
	GetDockerReconciliationRemediationCommands(authenticatedRunnerID int, sessionID string, fence string, limit int) ([]DockerReconciliationRemediationCommand, error)
	ReportDockerReconciliationRemediation(authenticatedRunnerID int, sessionID string, fence string, result DockerReconciliationRemediationResult) error

	// RecordDockerReconciliationObservation retains the initial append-only
	// contract for callers that do not need the idempotency result.
	RecordDockerReconciliationObservation(authenticatedRunnerID int, observation DockerReconciliationObservation) error
}
