package pro_interfaces

import (
	"github.com/semaphoreui/semaphore/db"
	"time"
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

// WorkflowApprovalIdentityStore resolves the current project role used to
// authorize an approval decision. The request itself snapshots the required
// permission, so later definition edits cannot weaken the pending request.
type WorkflowApprovalIdentityStore interface {
	GetProjectUser(projectID int, userID int) (db.ProjectUser, error)
	GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error)
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
