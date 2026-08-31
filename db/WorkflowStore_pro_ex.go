package db

import (
	"time"
)

// WorkflowApprovalContributionStore persists immutable approval decisions.
// The Enhanced implementation serializes each submission with a database
// transaction; Community does not implement this optional capability.
type WorkflowApprovalContributionStore interface {
	GetWorkflowApprovalContributions(approvalID int) ([]WorkflowApprovalContribution, error)
	CreateWorkflowApprovalContribution(WorkflowApprovalContribution) error
	SubmitWorkflowApprovalContribution(WorkflowApprovalContributionSubmission) (WorkflowApprovalContributionResult, error)
}

// WorkflowApprovalContributionSubmission is the full server-derived command
// for a user decision. No caller-controlled metadata or role selection is
// accepted beyond the already validated contribution evidence.
type WorkflowApprovalContributionSubmission struct {
	ProjectID      int
	WorkflowRunID  int
	WorkflowNodeID int
	ActorUserID    int
	Decision       WorkflowApprovalDecision
	CorrelationID  string
	LegacySnapshot *WorkflowApprovalRolePolicySnapshot
	At             time.Time
}

// WorkflowApprovalContributionResult returns the committed state after one
// atomic decision attempt. TimedOut and Duplicate are explicit outcomes so a
// caller can audit the result without reinterpreting a database error.
type WorkflowApprovalContributionResult struct {
	Approval     WorkflowApproval
	Committed    bool
	Terminal     bool
	TimedOut     bool
	Denied       bool
	Duplicate    bool
	Contribution *WorkflowApprovalContribution
}
