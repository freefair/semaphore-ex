package server

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
)

func (s *workflowService) HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error {
	return nil
}
