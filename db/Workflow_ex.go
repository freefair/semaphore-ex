package db

const WorkflowDefinitionVersion = 1

// WorkflowValidationIssue is stable API data. Path points to the affected
// field while node_id and edge_id locate the graph element when applicable.
type WorkflowValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	NodeID  *int   `json:"node_id,omitempty"`
	EdgeID  *int   `json:"edge_id,omitempty"`
}

type WorkflowValidationResult struct {
	Valid  bool                      `json:"valid"`
	Issues []WorkflowValidationIssue `json:"issues"`
}
