package projects

import (
	"encoding/json"

	workflowTasks "github.com/semaphoreui/semaphore/pro/services/tasks"
)

const maxWorkflowPublicOutputViewBytes = 256 * 1024

// workflowPublicOutputs projects only the public, bounded envelope stored for
// a workflow task. Internal EX callback payloads never reach the run API.
func workflowPublicOutputs(document *string) *string {
	if document == nil || len(*document) > maxWorkflowPublicOutputViewBytes {
		return nil
	}
	var envelope struct {
		Values  map[string]json.RawMessage `json:"values"`
		Skipped map[string]string          `json:"skipped,omitempty"`
	}
	if json.Unmarshal([]byte(*document), &envelope) != nil {
		return nil
	}
	delete(envelope.Values, "semaphore_workflow_outputs")
	delete(envelope.Skipped, "semaphore_workflow_outputs")
	if len(envelope.Values) == 0 && len(envelope.Skipped) == 0 {
		return nil
	}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > maxWorkflowPublicOutputViewBytes {
		return nil
	}
	collector := workflowTasks.NewTaskOutputsCollector()
	if collector == nil {
		return nil
	}
	result, err := collector.Validate(string(encoded))
	if err != nil {
		return nil
	}
	return result
}
