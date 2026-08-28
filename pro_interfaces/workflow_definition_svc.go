package pro_interfaces

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

var ErrWorkflowRevisionConflict = errors.New("workflow revision conflict")

// WorkflowDefinitionService owns authoring use cases. The controller depends
// on this interface instead of reaching into persistence directly.
type WorkflowDefinitionService interface {
	List(projectID int, params db.RetrieveQueryParams) ([]db.WorkflowTemplate, error)
	Get(projectID int, workflowID int) (db.WorkflowTemplate, error)
	Validate(projectID int, workflow db.WorkflowTemplate) (db.WorkflowValidationResult, error)
	Create(projectID int, workflow db.WorkflowTemplate) (db.WorkflowTemplate, db.WorkflowValidationResult, error)
	Update(projectID int, workflowID int, workflow db.WorkflowTemplate) (db.WorkflowTemplate, db.WorkflowValidationResult, error)
	Delete(projectID int, workflowID int) error
}
