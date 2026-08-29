package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"time"
)

func (d *WorkflowStoreImpl) GetWorkflowRunByCorrelationID(projectID int, workflowTemplateID int, correlationID string) (res db.WorkflowRun, err error) {
	return
}

func (d *WorkflowStoreImpl) GetWorkflowRunNodeTask(projectID int, runID int, nodeID int) (res db.Task, err error) {
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

func (d *WorkflowStoreImpl) GetPendingWorkflowApprovals(projectID int) (res []db.WorkflowApproval, err error) {
	return
}

func (d *WorkflowStoreImpl) OpenWorkflowApproval(approval db.WorkflowApproval) (res db.WorkflowApproval, opened bool, err error) {
	return
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunApprovalNode(projectID int, runID int, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (ok bool, err error) {
	return
}
