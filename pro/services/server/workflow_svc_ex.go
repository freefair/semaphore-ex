package server

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
)

func (s *workflowService) GetWorkflowApprovalInbox(projectID int, user *db.User) ([]db.WorkflowApproval, error) {
	return []db.WorkflowApproval{}, nil
}

func (s *workflowService) HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error {
	return nil
}
