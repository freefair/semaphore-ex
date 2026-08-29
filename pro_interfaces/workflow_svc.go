package pro_interfaces

import (
	"encoding/json"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// WorkflowReconciliationLease is the durable ownership token for progressing
// one workflow run. Every correctness-sensitive progression write is bound to
// OwnerBootID and FencingToken; LeaseExpiresAt is derived from database time.
type WorkflowReconciliationLease struct {
	ProjectID              int        `json:"project_id"`
	WorkflowRunID          int        `json:"workflow_run_id"`
	OwnerBootID            string     `json:"owner_boot_id"`
	PreviousOwnerBootID    string     `json:"previous_owner_boot_id,omitempty"`
	FencingToken           int64      `json:"fencing_token"`
	LeaseExpiresAt         time.Time  `json:"lease_expires_at"`
	AcquiredAt             time.Time  `json:"acquired_at"`
	OwnershipTransferredAt *time.Time `json:"ownership_transferred_at,omitempty"`
	TransferCount          int        `json:"transfer_count"`
	LastReconciledAt       *time.Time `json:"last_reconciled_at,omitempty"`
}

// WorkflowReconciliationRepository persists workflow progression ownership.
// Implementations use database time for every lease comparison and expiry.
type WorkflowReconciliationRepository interface {
	ClaimWorkflowReconciliation(projectID int, runID int, ownerBootID string, ttl time.Duration) (WorkflowReconciliationLease, bool, error)
	RenewWorkflowReconciliation(lease WorkflowReconciliationLease, ttl time.Duration) (WorkflowReconciliationLease, bool, error)
	IsCurrentWorkflowReconciliation(lease WorkflowReconciliationLease) (bool, error)
	ReleaseWorkflowReconciliation(lease WorkflowReconciliationLease) (bool, error)
	RecordWorkflowReconciled(lease WorkflowReconciliationLease) (bool, error)
}

// WorkflowProgressionRepository contains Enhanced-only conditional writes
// whose atomic SQL predicates include the current reconciliation fence.
type WorkflowProgressionRepository interface {
	ClaimWorkflowRunNodeFenced(lease WorkflowReconciliationLease, nodeID int, queuedAt time.Time) (bool, error)
	FinalizeWorkflowRunNodeFenced(lease WorkflowReconciliationLease, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error)
	OpenWorkflowApprovalFenced(lease WorkflowReconciliationLease, approval db.WorkflowApproval) (db.WorkflowApproval, bool, error)
	FinalizeWorkflowRunApprovalNodeFenced(lease WorkflowReconciliationLease, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error)
	UpdateWorkflowRunStatusUnlessFenced(lease WorkflowReconciliationLease, run db.WorkflowRun, excluded []db.WorkflowRunStatus) (bool, error)
}

// WorkflowRunFairScanner returns active runs in project-interleaved order so
// one project's backlog cannot delay every other project on a reconciliation
// tick.
type WorkflowRunFairScanner interface {
	GetActiveWorkflowRunsFair() ([]db.WorkflowRun, error)
}

// WorkflowService orchestrates workflow runs: starting a run, progressing it as
// upstream tasks finish, resolving approvals and merging run artifacts. It is a
// Pro feature — the open-source build wires a no-op stub
// (pro/services/server/workflow_svc.go); the licensed build provides the real
// implementation (pro_impl/services/server/workflow_svc.go).
type WorkflowService interface {
	StartWorkflow(workflow db.WorkflowTemplate, user *db.User, correlationID string, input ...db.WorkflowRunInput) (db.WorkflowRun, error)
	ProgressWorkflowRun(projectID int, runID int, user *db.User) error
	// StopWorkflowRun stops a non-finished run: it signals every in-flight task
	// of the run to stop and marks the run as stopped (terminal).
	StopWorkflowRun(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	RequestWorkflowRunStop(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	ReconcileWorkflowRun(projectID int, runID int) (db.WorkflowRun, error)
	RetryWorkflowRunReconciliation(projectID int, runID int, user *db.User) (db.WorkflowRun, error)
	GetWorkflowApprovalInbox(projectID int, user *db.User) ([]db.WorkflowApproval, error)
	ResolveWorkflowApproval(projectID int, workflowID int, runID int, nodeID int, decision db.WorkflowApprovalDecision, user *db.User) (db.WorkflowApproval, error)
	HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error
	HandleWorkflowTaskCompletion(task db.Task) error
	GetWorkflowRunArtifacts(projectID int, runID int, currentTaskID *int) ([]db.WorkflowArtifactMetadata, error)
}

// WorkflowApprovalIdentityStore resolves the current project role used to
// authorize an approval decision. The request itself snapshots the required
// permission, so later definition edits cannot weaken the pending request.
type WorkflowApprovalIdentityStore interface {
	GetProjectUser(projectID int, userID int) (db.ProjectUser, error)
	GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error)
}

// WorkflowTaskEnqueuer is the slice of the task pool the workflow service needs
// to launch a node's task. It is implemented by *services/tasks.TaskPool.
// Declaring it here keeps the pro modules dependent only on pro_interfaces + db,
// avoiding an import of (and a cycle with) the services/tasks package.
type WorkflowTaskEnqueuer interface {
	AddTask(task db.Task, userID *int, username string, projectID int, needAlias bool) (db.Task, error)
	AddWorkflowTask(task db.Task, template db.Template, userID *int, username string, projectID int, needAlias bool) (db.Task, error)
	// StopTasksByWorkflowRun stops every active (queued or running) task that
	// belongs to the given workflow run. forceStop kills running tasks
	// immediately instead of letting them stop gracefully.
	StopTasksByWorkflowRun(projectID int, runID int, forceStop bool)
}

// WorkflowTaskFencedEnqueuer creates a workflow task only while the supplied
// reconciliation owner and node fence are still current.
type WorkflowTaskFencedEnqueuer interface {
	AddWorkflowTaskFenced(task db.Task, template db.Template, userID *int, username string, projectID int, needAlias bool, lease WorkflowReconciliationLease) (db.Task, error)
}

// WorkflowCredentialReader resolves an approved AccessKey immediately before
// a workflow task is created. Implementations must keep the value write-only.
type WorkflowCredentialReader interface {
	DeserializeSecret(key *db.AccessKey) error
}

// WorkflowRunLocker provides cluster-wide mutual exclusion for workflow run
// progression. In HA mode every progression trigger (task completion, API
// poll, reconciler tick) may fire on any node; the locker ensures only one
// node progresses a given run at a time. The lock is an optimization, not a
// correctness guarantee — conditional DB updates remain the source of truth.
type WorkflowRunLocker interface {
	// TryLockRun acquires durable progression ownership for a run. Returns
	// ok=false when another holder owns it; release must be called when ok.
	TryLockRun(projectID int, runID int) (lease WorkflowReconciliationLease, release func(), ok bool, err error)
	// TryLockStart acquires a short-lived lock serializing run creation per
	// workflow template (run version minting). Returns ok=false on contention.
	TryLockStart(projectID int, templateID int) (release func(), ok bool)
	RecordReconciled(lease WorkflowReconciliationLease) error
	Drain() error
	Resume()
}

// WorkflowReconciler periodically progresses every non-terminal workflow run
// so approval timeouts fire and run statuses converge without requiring an
// open browser or a task completion (and after a node crash mid-progression).
type WorkflowReconciler interface {
	Start()
	Stop()
}
