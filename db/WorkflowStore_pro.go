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
	GetWorkflowApproval(projectID int, runID int, nodeID int) (WorkflowApproval, error)
	CreateWorkflowApproval(approval WorkflowApproval) (WorkflowApproval, error)
	UpdateWorkflowApproval(approval WorkflowApproval) error
	ResolveWorkflowApprovalIfPending(approval WorkflowApproval) (bool, error)

	GetWorkflowDelays(projectID int, runID int) ([]WorkflowDelay, error)
	GetWorkflowDelay(projectID int, runID int, nodeID int) (WorkflowDelay, error)
	CreateWorkflowDelay(delay WorkflowDelay) (WorkflowDelay, error)
	UpdateWorkflowDelay(delay WorkflowDelay) error
	ResolveWorkflowDelayIfWaiting(delay WorkflowDelay) (bool, error)
	GetExpiredWorkflowDelays() ([]WorkflowDelay, error)
}
