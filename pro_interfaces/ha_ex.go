package pro_interfaces

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ClusterNodeIdentity separates the configured stable node identity from the
// boot identity that identifies one process lifetime.
type ClusterNodeIdentity struct {
	NodeID string `json:"node_id"`
	BootID string `json:"boot_id"`
}

// NewClusterNodeIdentity creates an identity for one server process.
// NodeID must come from stable operator configuration, while BootID is always
// newly generated so restarts remain observable even on the same host.
func NewClusterNodeIdentity(nodeID string) (ClusterNodeIdentity, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return ClusterNodeIdentity{}, errors.New("cluster node id is required")
	}
	bootBytes := make([]byte, 16)
	if _, err := rand.Read(bootBytes); err != nil {
		return ClusterNodeIdentity{}, err
	}
	return ClusterNodeIdentity{NodeID: nodeID, BootID: hex.EncodeToString(bootBytes)}, nil
}

// ScheduleOccurrenceKey returns a stable, non-secret identity for one
// intended schedule fire. The schedule revision prevents an edited schedule
// from sharing a claim with a prior definition, while UTC normalization makes
// every node derive the same key for the same instant.
func ScheduleOccurrenceKey(scheduleID int, revision string, intendedAt time.Time) (string, error) {
	if scheduleID <= 0 {
		return "", errors.New("schedule id is required")
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "", errors.New("schedule revision is required")
	}
	if intendedAt.IsZero() {
		return "", errors.New("schedule intended fire instant is required")
	}
	value := fmt.Sprintf("%d\x00%s\x00%s", scheduleID, revision, intendedAt.UTC().Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte("semaphore-schedule-occurrence:v1\x00" + value))
	return "sco_" + hex.EncodeToString(digest[:])[:60], nil
}

// ScheduleOccurrence identifies a single intended fire of one version of a
// schedule. Its key is durable and safe to use as a SQL uniqueness boundary.
type ScheduleOccurrence struct {
	Key        string    `json:"key"`
	ScheduleID int       `json:"schedule_id"`
	Revision   string    `json:"revision"`
	IntendedAt time.Time `json:"intended_at"`
}

func NewScheduleOccurrence(scheduleID int, revision string, intendedAt time.Time) (ScheduleOccurrence, error) {
	key, err := ScheduleOccurrenceKey(scheduleID, revision, intendedAt)
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	return ScheduleOccurrence{
		Key:        key,
		ScheduleID: scheduleID,
		Revision:   strings.TrimSpace(revision),
		IntendedAt: intendedAt.UTC(),
	}, nil
}

// ScheduleOccurrenceLease is a fenced, renewable ownership record. A caller
// must present its exact owner boot identity and fencing token for every
// follow-up action, so an expired owner cannot act after a successor claims
// the same occurrence.
type ScheduleOccurrenceLease struct {
	Occurrence   ScheduleOccurrence `json:"occurrence"`
	OwnerBootID  string             `json:"owner_boot_id"`
	FencingToken int64              `json:"fencing_token"`
	ExpiresAt    time.Time          `json:"expires_at"`
}

// ScheduleOccurrenceLeaseRepository persists the SQL authority for schedule
// claims. Redis may wake nodes but must never replace these CAS decisions.
type ScheduleOccurrenceLeaseRepository interface {
	ClaimScheduleOccurrence(occurrence ScheduleOccurrence, ownerBootID string, ttl time.Duration) (ScheduleOccurrenceLease, bool, error)
	IsCurrentScheduleLease(lease ScheduleOccurrenceLease) (bool, error)
	CompleteScheduleOccurrence(lease ScheduleOccurrenceLease, taskID int) (bool, error)
	ReleaseScheduleOccurrenceLease(lease ScheduleOccurrenceLease) (bool, error)
}

// TaskExecutionIdentity is the executor or runner identity bound to one
// dispatched task generation. Recovery may only act on evidence obtained for
// this exact identity, never on a missed node or runner heartbeat alone.
type TaskExecutionIdentity struct {
	RunnerID   int    `json:"runner_id"`
	Generation int    `json:"generation"`
	StableID   string `json:"stable_id"`
}

// NewTaskExecutionIdentity derives the same non-secret execution identity on
// every node. Assignment generation prevents a recovered replacement from
// sharing identity with the execution it supersedes.
func NewTaskExecutionIdentity(taskID int, runnerID int, generation int) (TaskExecutionIdentity, error) {
	if taskID <= 0 || runnerID <= 0 || generation <= 0 {
		return TaskExecutionIdentity{}, errors.New("task, runner, and assignment generation are required")
	}
	return TaskExecutionIdentity{
		RunnerID: runnerID, Generation: generation,
		StableID: fmt.Sprintf("runner:%d:task:%d:generation:%d", runnerID, taskID, generation),
	}, nil
}

// TaskControlLease identifies the server process currently responsible for
// reconciling a task. FencingToken increases on every expired-owner takeover
// so a former owner cannot persist a recovery decision after replacement.
type TaskControlLease struct {
	TaskID       int                   `json:"task_id"`
	OwnerBootID  string                `json:"owner_boot_id"`
	FencingToken int64                 `json:"fencing_token"`
	ExpiresAt    time.Time             `json:"expires_at"`
	Execution    TaskExecutionIdentity `json:"execution"`
}

// TaskControlLeaseRepository is the durable ownership boundary for HA task
// reconciliation. Claim and renewal are server-time based; every mutation
// after a claim must re-check the exact boot owner and fencing token.
type TaskControlLeaseRepository interface {
	ClaimTaskControl(taskID int, execution TaskExecutionIdentity, ownerBootID string, ttl time.Duration) (TaskControlLease, bool, error)
	IsCurrentTaskControlLease(lease TaskControlLease) (bool, error)
	ReleaseTaskControlLease(lease TaskControlLease) (bool, error)
}

// TaskExecutionState is the evidence returned by a runner or executor for a
// stable execution identity. Unknown is intentionally not treated as absent.
type TaskExecutionState string

const (
	TaskExecutionRunning  TaskExecutionState = "running"
	TaskExecutionTerminal TaskExecutionState = "terminal"
	TaskExecutionAbsent   TaskExecutionState = "absent"
	TaskExecutionUnknown  TaskExecutionState = "unknown"
)

// TaskExecutionEvidence is an intentionally narrow, value-free recovery
// observation. Reason is an operator-safe diagnostic; it must not contain
// executor output or credentials.
type TaskExecutionEvidence struct {
	State          TaskExecutionState `json:"state"`
	TerminalStatus string             `json:"terminal_status,omitempty"`
	Reason         string             `json:"reason,omitempty"`
}

type TaskRecoveryDecision string

const (
	TaskRecoveryObserve    TaskRecoveryDecision = "observe"
	TaskRecoveryRecover    TaskRecoveryDecision = "recover"
	TaskRecoveryQuarantine TaskRecoveryDecision = "quarantine"
)

// TaskRecoveryAssessment makes recovery conservative by construction. A
// replacement is permitted only after evidence proves the exact prior
// execution absent or terminal; unknown evidence is quarantined.
type TaskRecoveryAssessment struct {
	Decision        TaskRecoveryDecision `json:"decision"`
	SafeReplacement bool                 `json:"safe_replacement"`
	EvidenceState   TaskExecutionState   `json:"evidence_state"`
	TerminalStatus  string               `json:"terminal_status,omitempty"`
	Reason          string               `json:"reason"`
}

// DecideTaskRecovery translates stable execution evidence into an action. It
// does not claim or mutate a lease; callers must still use their current
// fenced lease for any follow-up write.
func DecideTaskRecovery(_ TaskControlLease, evidence TaskExecutionEvidence) TaskRecoveryAssessment {
	switch evidence.State {
	case TaskExecutionRunning:
		return TaskRecoveryAssessment{Decision: TaskRecoveryObserve, EvidenceState: evidence.State, Reason: "original execution is still running"}
	case TaskExecutionAbsent:
		return TaskRecoveryAssessment{Decision: TaskRecoveryRecover, SafeReplacement: true, EvidenceState: evidence.State, Reason: "original execution is absent"}
	case TaskExecutionTerminal:
		return TaskRecoveryAssessment{Decision: TaskRecoveryRecover, EvidenceState: evidence.State, TerminalStatus: evidence.TerminalStatus, Reason: "original execution reported a terminal result"}
	default:
		reason := strings.TrimSpace(evidence.Reason)
		if reason == "" {
			reason = "execution evidence is unavailable"
		}
		return TaskRecoveryAssessment{Decision: TaskRecoveryQuarantine, EvidenceState: evidence.State, Reason: reason}
	}
}

// TaskControlRecoveryRecord is the SQL-authoritative recovery snapshot for one
// controlled execution. DatabaseNow and all observation timestamps originate
// from the database server rather than a node's local clock.
type TaskControlRecoveryRecord struct {
	Lease                  TaskControlLease        `json:"lease"`
	PreviousOwnerBootID    string                  `json:"previous_owner_boot_id,omitempty"`
	OwnershipTransferredAt *time.Time              `json:"ownership_transferred_at,omitempty"`
	Evidence               TaskExecutionEvidence   `json:"evidence"`
	EvidenceObservedAt     *time.Time              `json:"evidence_observed_at,omitempty"`
	AssignmentRevokedAt    *time.Time              `json:"assignment_revoked_at,omitempty"`
	LastAssessment         *TaskRecoveryAssessment `json:"last_assessment,omitempty"`
	RecoveryDecidedAt      *time.Time              `json:"recovery_decided_at,omitempty"`
	DatabaseNow            time.Time               `json:"database_now"`
}

// TaskControlRecoveryRepository extends task-control leases with the evidence
// and fenced diagnostic writes needed by an orphan recovery worker.
type TaskControlRecoveryRepository interface {
	TaskControlLeaseRepository
	ListExpiredTaskControls(limit int) ([]TaskControlRecoveryRecord, error)
	GetTaskControlRecovery(taskID int) (TaskControlRecoveryRecord, bool, error)
	RecordTaskAssignmentRevoked(lease TaskControlLease) (bool, error)
	RecordTaskRecoveryDecision(lease TaskControlLease, assessment TaskRecoveryAssessment) (bool, error)
}

// ClusterHeartbeatRedisClient is the narrow Redis dependency of the cluster
// registry. ServerTime is authoritative for observed timestamps and key TTLs
// are authoritative for liveness.
type ClusterHeartbeatRedisClient interface {
	ServerTime(ctx context.Context) (time.Time, error)
	SetWithTTL(ctx context.Context, key string, value string, ttl time.Duration) error
	Exists(ctx context.Context, key string) (bool, error)
	Delete(ctx context.Context, key string) error
}

type ClusterRedisDiagnosticsClient interface {
	RedisDiagnostics(ctx context.Context) (RedisInfo, error)
}

// ClusterHeartbeatStore is the live Redis projection of a durable SQL node
// registration. It does not use a node's local clock to determine liveness.
type ClusterHeartbeatStore interface {
	Publish(ctx context.Context, identity ClusterNodeIdentity, ttl time.Duration) (time.Time, error)
	IsLive(ctx context.Context, identity ClusterNodeIdentity) (bool, time.Time, error)
	Remove(ctx context.Context, identity ClusterNodeIdentity) error
}

type ClusterNodeCompatibilityState string

const (
	ClusterNodeCompatible               ClusterNodeCompatibilityState = "compatible"
	ClusterNodeDraining                 ClusterNodeCompatibilityState = "draining"
	ClusterNodeIncompatibleSchema       ClusterNodeCompatibilityState = "incompatible_schema"
	ClusterNodeIncompatibleProtocol     ClusterNodeCompatibilityState = "incompatible_protocol"
	ClusterNodeIncompatibleCapabilities ClusterNodeCompatibilityState = "incompatible_capabilities"
	ClusterNodeStale                    ClusterNodeCompatibilityState = "stale"
)

// ClusterNodeRegistration is the durable SQL record for one process lifetime.
// A new BootID creates a new row even when the stable NodeID is unchanged.
type ClusterNodeRegistration struct {
	ClusterNodeIdentity
	Edition         string    `json:"edition"`
	Version         string    `json:"version"`
	Build           string    `json:"build"`
	ProtocolVersion int       `json:"protocol_version"`
	SchemaVersion   string    `json:"schema_version"`
	Capabilities    []string  `json:"capabilities"`
	StartedAt       time.Time `json:"started_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	Draining        bool      `json:"draining"`
}

type ClusterCompatibilityRequirements struct {
	ProtocolVersion      int
	SchemaVersion        string
	RequiredCapabilities []string
}

type ClusterNodeCompatibility struct {
	State  ClusterNodeCompatibilityState `json:"state"`
	Ready  bool                          `json:"ready"`
	Reason string                        `json:"reason,omitempty"`
}

// ClusterNodeRepository persists durable membership history separately from
// the Redis live-heartbeat projection.
type ClusterNodeRepository interface {
	UpsertClusterNode(node ClusterNodeRegistration) error
	ListClusterNodes() ([]ClusterNodeRegistration, error)
	SetClusterNodeDraining(bootID string, draining bool) error
	DeleteClusterNodesLastSeenBefore(before time.Time) (int, error)
}

// EvaluateClusterNodeCompatibility is deterministic and deliberately separate
// from liveness: a Redis-live node can still be unsafe for coordinated work.
func EvaluateClusterNodeCompatibility(node ClusterNodeRegistration, required ClusterCompatibilityRequirements) ClusterNodeCompatibility {
	if node.ProtocolVersion != required.ProtocolVersion {
		return ClusterNodeCompatibility{State: ClusterNodeIncompatibleProtocol, Reason: "cluster protocol version differs"}
	}
	if node.SchemaVersion != required.SchemaVersion {
		return ClusterNodeCompatibility{State: ClusterNodeIncompatibleSchema, Reason: "database schema version differs"}
	}
	available := make(map[string]struct{}, len(node.Capabilities))
	for _, capability := range node.Capabilities {
		available[capability] = struct{}{}
	}
	for _, requiredCapability := range required.RequiredCapabilities {
		if _, exists := available[requiredCapability]; !exists {
			return ClusterNodeCompatibility{State: ClusterNodeIncompatibleCapabilities, Reason: "required cluster capability is unavailable"}
		}
	}
	if node.Draining {
		return ClusterNodeCompatibility{State: ClusterNodeDraining, Reason: "node is draining"}
	}
	return ClusterNodeCompatibility{State: ClusterNodeCompatible, Ready: true}
}

type TaskRecoveryDiagnostics struct {
	Controlled             bool                 `json:"controlled"`
	OwnerBootID            string               `json:"owner_boot_id"`
	PreviousOwnerBootID    string               `json:"previous_owner_boot_id,omitempty"`
	FencingToken           int64                `json:"fencing_token"`
	LeaseExpiresAt         time.Time            `json:"lease_expires_at"`
	OwnershipTransferredAt *time.Time           `json:"ownership_transferred_at,omitempty"`
	RunnerID               int                  `json:"runner_id"`
	AssignmentGeneration   int                  `json:"assignment_generation"`
	EvidenceState          TaskExecutionState   `json:"evidence_state"`
	EvidenceTerminalStatus string               `json:"evidence_terminal_status,omitempty"`
	EvidenceObservedAt     *time.Time           `json:"evidence_observed_at,omitempty"`
	AssignmentRevokedAt    *time.Time           `json:"assignment_revoked_at,omitempty"`
	RecoveryDecision       TaskRecoveryDecision `json:"recovery_decision,omitempty"`
	RecoveryReason         string               `json:"recovery_reason,omitempty"`
	RecoveryDecidedAt      *time.Time           `json:"recovery_decided_at,omitempty"`
	Quarantined            bool                 `json:"quarantined"`
	SafeAction             string               `json:"safe_action,omitempty"`
}

type ClusterCoordinatorHealth struct {
	SQLAuthoritative bool      `json:"sql_authoritative"`
	LiveEvents       string    `json:"live_events"`
	Reason           string    `json:"reason,omitempty"`
	ObservedAt       time.Time `json:"observed_at"`
}

type ClusterCoordinatorHealthSource interface {
	CoordinatorHealth() ClusterCoordinatorHealth
}
