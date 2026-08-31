package pro_interfaces

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

var ErrWorkflowRevisionConflict = errors.New("workflow revision conflict")
var ErrWorkflowPermissionDenied = errors.New("workflow permission denied")

// WorkflowDefinitionService owns authoring use cases. The controller depends
// on this interface instead of reaching into persistence directly.
type WorkflowDefinitionService interface {
	List(projectID int, params db.RetrieveQueryParams, actors ...*db.User) ([]db.WorkflowTemplate, error)
	Get(projectID int, workflowID int, actors ...*db.User) (db.WorkflowTemplate, error)
	Validate(projectID int, workflow db.WorkflowTemplate, actors ...*db.User) (db.WorkflowValidationResult, error)
	Create(projectID int, workflow db.WorkflowTemplate, actors ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error)
	Update(projectID int, workflowID int, workflow db.WorkflowTemplate, actors ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error)
	Delete(projectID int, workflowID int, actors ...*db.User) error
	ListVersions(projectID int, workflowID int, params db.RetrieveQueryParams, actors ...*db.User) ([]db.WorkflowVersion, error)
	GetVersion(projectID int, workflowID int, versionNumber int, actors ...*db.User) (db.WorkflowVersion, error)
	DiffVersions(projectID int, workflowID int, beforeVersion int, afterVersion int, actors ...*db.User) (WorkflowDefinitionDiff, error)
	RestoreVersion(projectID int, workflowID int, versionNumber int, message string, actors ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error)
}
