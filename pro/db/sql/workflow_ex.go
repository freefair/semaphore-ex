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
