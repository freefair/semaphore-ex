package db

import (
	"time"
)

// WorkflowVersionStore is an optional Enhanced persistence capability. It
// keeps definition mutation and append-only version insertion in one database
// transaction; Community does not implement it.
type WorkflowVersionStore interface {
	CreateWorkflowTemplateVersioned(workflow WorkflowTemplate, mutation WorkflowVersionMutation) (WorkflowTemplate, WorkflowVersion, error)
	UpdateWorkflowTemplateVersioned(workflow WorkflowTemplate, mutation WorkflowVersionMutation) (WorkflowTemplate, WorkflowVersion, error)
	GetWorkflowVersions(projectID int, workflowID int, params RetrieveQueryParams) ([]WorkflowVersion, error)
	GetWorkflowVersion(projectID int, workflowID int, versionNumber int) (WorkflowVersion, error)
	EnsureCurrentWorkflowVersion(projectID int, workflowID int) (WorkflowVersion, error)
}

// TemplateVersionStore is the optional Enhanced persistence seam for immutable
// owner template snapshots. Community continues to use its existing mutable
// template CRUD contract without implementing this capability.
type TemplateVersionStore interface {
	PublishTemplateVersion(template Template, authorUserID int, created time.Time) (TemplateVersion, bool, error)
	GetTemplateVersions(ownerProjectID int, templateID int, params RetrieveQueryParams) ([]TemplateVersion, error)
	GetTemplateVersion(ownerProjectID int, templateID int, versionNumber int) (TemplateVersion, error)
	DeleteTemplateVersion(ownerProjectID int, templateID int, versionNumber int) error
}

// CrossProjectTemplateGrantStore persists explicit owner-to-consumer grants.
// All authorisation decisions remain above this seam; the store enforces
// lifecycle, scope, optimistic revision, and pagination invariants.
type CrossProjectTemplateGrantStore interface {
	CreateCrossProjectTemplateGrant(grant CrossProjectTemplateGrant) (CrossProjectTemplateGrant, error)
	UpdateCrossProjectTemplateGrant(ownerProjectID int, grantID int, update CrossProjectTemplateGrantUpdate, expectedRevision int) (CrossProjectTemplateGrant, error)
	DeleteCrossProjectTemplateGrant(ownerProjectID int, grantID int, expectedRevision int) error
	GetCrossProjectTemplateGrant(projectID int, grantID int) (CrossProjectTemplateGrant, error)
	GetCrossProjectTemplateGrants(projectID int, params RetrieveQueryParams) ([]CrossProjectTemplateGrant, error)
	AcceptCrossProjectTemplateGrant(consumerProjectID int, grantID int, actorUserID int, expectedRevision int, acceptedAt time.Time) (CrossProjectTemplateGrant, error)
	RevokeCrossProjectTemplateGrant(projectID int, grantID int, actorUserID int, expectedRevision int, reason string, revokedAt time.Time) (CrossProjectTemplateGrant, error)
	HasActiveCrossProjectTemplateGrants(ownerProjectID int, templateID int) (bool, error)
	HasUnrevokedCrossProjectTemplateGrants(ownerProjectID int, templateID int) (bool, error)
	ResolveActiveCrossProjectTemplateGrant(consumerProjectID int, reference CrossProjectTemplateReference, operation CrossProjectTemplateGrantOperation) (CrossProjectTemplateReference, TemplateVersion, error)
	GetMappedCrossProjectTemplateVersions(consumerProjectID int, grantID int, params RetrieveQueryParams) ([]TemplateVersion, CrossProjectTemplateGrant, error)
}

// CrossProjectWorkflowReferenceStore keeps a grant recheck in the same
// transaction as an immutable workflow or run snapshot write. It is optional
// so Community builds retain the established workflow contracts.
type CrossProjectWorkflowReferenceStore interface {
	CreateWorkflowTemplateVersionedWithCrossProjectReferences(WorkflowTemplate, WorkflowVersionMutation) (WorkflowTemplate, WorkflowVersion, error)
	UpdateWorkflowTemplateVersionedWithCrossProjectReferences(WorkflowTemplate, WorkflowVersionMutation) (WorkflowTemplate, WorkflowVersion, error)
	CreateWorkflowRunWithCrossProjectReferences(WorkflowRun) (WorkflowRun, error)
}

type CrossProjectTemplateDeletionStore interface {
	DeleteTemplateWithCrossProjectGrantGuard(ownerProjectID int, templateID int) error
}

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
