package server

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
)

func (s *workflowService) RequestWorkflowRunStop(projectID int, runID int, user *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}

func (s *workflowService) ReconcileWorkflowRun(projectID int, runID int) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}

func (s *workflowService) RetryWorkflowRunReconciliation(projectID int, runID int, user *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}

func (s *workflowService) GetWorkflowApprovalInbox(projectID int, user *db.User) ([]db.WorkflowApproval, error) {
	return []db.WorkflowApproval{}, nil
}

func (s *workflowService) HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error {
	return nil
}
