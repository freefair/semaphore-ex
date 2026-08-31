package db

import "time"

type WorkflowManager interface {
	GetWorkflowRunTasks(projectID int, runID int, params RetrieveQueryParams) ([]TaskWithTpl, error)

	GetWorkflowTemplates(projectID int, params RetrieveQueryParams) ([]WorkflowTemplate, error)
	GetWorkflowTemplate(projectID int, workflowID int) (WorkflowTemplate, error)
	CreateWorkflowTemplate(workflow WorkflowTemplate) (WorkflowTemplate, error)
	UpdateWorkflowTemplate(workflow WorkflowTemplate) (WorkflowTemplate, error)
	DeleteWorkflowTemplate(projectID int, workflowID int) error

	GetWorkflowRuns(projectID int, workflowTemplateID int, params RetrieveQueryParams) ([]WorkflowRun, error)
	GetWorkflowRun(projectID int, workflowTemplateID int, runID int) (WorkflowRun, error)
	GetWorkflowRunByID(projectID int, runID int) (WorkflowRun, error)
	GetWorkflowRunByCorrelationID(projectID int, workflowTemplateID int, correlationID string) (WorkflowRun, error)
	GetWorkflowRunNodeTask(projectID int, runID int, nodeID int) (Task, error)

	GetActiveWorkflowRuns() ([]WorkflowRun, error)
	CreateWorkflowRun(run WorkflowRun) (WorkflowRun, error)
	UpdateWorkflowRun(run WorkflowRun) error
	UpdateWorkflowRunReconciliation(run WorkflowRun) error
	RequestWorkflowRunStop(projectID int, runID int) (bool, error)
	GetWorkflowRunNode(projectID int, runID int, nodeID int) (WorkflowRunNode, error)
	ClaimWorkflowRunNode(projectID int, runID int, nodeID int, queuedAt time.Time) (bool, error)
	AttachWorkflowRunNodeTask(projectID int, runID int, nodeID int, taskID int) (bool, error)
	UpdateWorkflowRunNodeFromTask(projectID int, runID int, nodeID int, taskID int, status WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error)
	FinalizeWorkflowRunNode(projectID int, runID int, nodeID int, status WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error)
	BlockWorkflowRunNode(projectID int, runID int, nodeID int, reason string, at time.Time) (bool, error)
	UpdateWorkflowRunNodeArtifactInputs(projectID int, runID int, nodeID int, inputsJSON string) (bool, error)
	ReplaceWorkflowTaskArtifacts(projectID int, runID int, nodeID int, taskID int, attempt int, artifacts []WorkflowArtifact) error
	GetWorkflowRunArtifacts(projectID int, runID int) ([]WorkflowArtifact, error)

	UpdateWorkflowRunStatusUnless(run WorkflowRun, excluded []WorkflowRunStatus) (bool, error)

	SetWorkflowRunRootTask(projectID int, runID int, taskID int) (bool, error)

	GetWorkflowApprovals(projectID int, runID int) ([]WorkflowApproval, error)
	GetPendingWorkflowApprovals(projectID int) ([]WorkflowApproval, error)
	GetWorkflowApproval(projectID int, runID int, nodeID int) (WorkflowApproval, error)
	OpenWorkflowApproval(approval WorkflowApproval) (WorkflowApproval, bool, error)
	UpdateWorkflowApproval(approval WorkflowApproval) error
	ResolveWorkflowApprovalIfPending(approval WorkflowApproval) (bool, error)

	GetWorkflowDelays(projectID int, runID int) ([]WorkflowDelay, error)
	GetWorkflowDelay(projectID int, runID int, nodeID int) (WorkflowDelay, error)
	CreateWorkflowDelay(delay WorkflowDelay) (WorkflowDelay, error)
	UpdateWorkflowDelay(delay WorkflowDelay) error
	ResolveWorkflowDelayIfWaiting(delay WorkflowDelay) (bool, error)
	GetExpiredWorkflowDelays() ([]WorkflowDelay, error)
	FinalizeWorkflowRunApprovalNode(projectID int, runID int, nodeID int, status WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error)
}

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
