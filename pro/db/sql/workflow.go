package sql

import (
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// WorkflowStoreImpl is the open-source no-op stub for the Pro workflow store.
// Workflows are a Pro feature; the real implementation lives in
// pro_impl/db/sql/workflow.go. The stub keeps the open build compiling while
// the feature is disabled via the Workflows feature flag.
type WorkflowStoreImpl struct {
}

var _ db.WorkflowManager = (*WorkflowStoreImpl)(nil)

func (d *WorkflowStoreImpl) GetWorkflowRunTasks(projectID int, runID int, params db.RetrieveQueryParams) (res []db.TaskWithTpl, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowTemplates(projectID int, params db.RetrieveQueryParams) (res []db.WorkflowTemplate, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowTemplate(projectID int, workflowID int) (res db.WorkflowTemplate, err error) {
	return
}

func (d *WorkflowStoreImpl) CreateWorkflowTemplate(workflow db.WorkflowTemplate) (res db.WorkflowTemplate, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowTemplate(workflow db.WorkflowTemplate) (res db.WorkflowTemplate, err error) {
	return
}

func (d *WorkflowStoreImpl) DeleteWorkflowTemplate(projectID int, workflowID int) (err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRuns(projectID int, workflowTemplateID int, params db.RetrieveQueryParams) (res []db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRun(projectID int, workflowTemplateID int, runID int) (res db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunByID(projectID int, runID int) (res db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunByCorrelationID(projectID int, workflowTemplateID int, correlationID string) (res db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunNodeTask(projectID int, runID int, nodeID int) (res db.Task, err error) {
	return
}

func (d *WorkflowStoreImpl) GetActiveWorkflowRuns() (res []db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) CreateWorkflowRun(run db.WorkflowRun) (res db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowRun(run db.WorkflowRun) (err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunReconciliation(run db.WorkflowRun) (err error) {
	return
}

func (d *WorkflowStoreImpl) RequestWorkflowRunStop(projectID int, runID int) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunNode(projectID int, runID int, nodeID int) (res db.WorkflowRunNode, err error) {
	return
}

func (d *WorkflowStoreImpl) ClaimWorkflowRunNode(projectID int, runID int, nodeID int, queuedAt time.Time) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) AttachWorkflowRunNodeTask(projectID int, runID int, nodeID int, taskID int) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunNodeFromTask(projectID int, runID int, nodeID int, taskID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunNode(projectID int, runID int, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) BlockWorkflowRunNode(projectID int, runID int, nodeID int, reason string, at time.Time) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunNodeArtifactInputs(projectID int, runID int, nodeID int, inputsJSON string) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) ReplaceWorkflowTaskArtifacts(projectID int, runID int, nodeID int, taskID int, attempt int, artifacts []db.WorkflowArtifact) (err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunArtifacts(projectID int, runID int) (res []db.WorkflowArtifact, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunStatusUnless(run db.WorkflowRun, excluded []db.WorkflowRunStatus) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) SetWorkflowRunRootTask(projectID int, runID int, taskID int) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowApprovals(projectID int, runID int) (res []db.WorkflowApproval, err error) {
	return
}

func (d *WorkflowStoreImpl) GetPendingWorkflowApprovals(projectID int) (res []db.WorkflowApproval, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowApproval(projectID int, runID int, nodeID int) (res db.WorkflowApproval, err error) {
	return
}

func (d *WorkflowStoreImpl) OpenWorkflowApproval(approval db.WorkflowApproval) (res db.WorkflowApproval, opened bool, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowApproval(approval db.WorkflowApproval) (err error) {
	return
}

func (d *WorkflowStoreImpl) ResolveWorkflowApprovalIfPending(approval db.WorkflowApproval) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowDelays(projectID int, runID int) (res []db.WorkflowDelay, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowDelay(projectID int, runID int, nodeID int) (res db.WorkflowDelay, err error) {
	return
}

func (d *WorkflowStoreImpl) CreateWorkflowDelay(delay db.WorkflowDelay) (res db.WorkflowDelay, err error) {
	return
}

func (d *WorkflowStoreImpl) UpdateWorkflowDelay(delay db.WorkflowDelay) (err error) {
	return
}

func (d *WorkflowStoreImpl) ResolveWorkflowDelayIfWaiting(delay db.WorkflowDelay) (ok bool, err error) {
	return
}

func (d *WorkflowStoreImpl) GetExpiredWorkflowDelays() (res []db.WorkflowDelay, err error) {
	return
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunApprovalNode(projectID int, runID int, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (ok bool, err error) {
	return
}
