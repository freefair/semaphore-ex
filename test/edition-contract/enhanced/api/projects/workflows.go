package projects

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const workflowDefinitionBodyLimit int64 = 8 * 1024 * 1024

type workflowController struct {
	definitionService pro_interfaces.WorkflowDefinitionService
}

var _ pro_interfaces.WorkflowController = (*workflowController)(nil)

func NewWorkflowController(
	_ pro_interfaces.WorkflowService,
	_ db.WorkflowManager,
	definitionService pro_interfaces.WorkflowDefinitionService,
) pro_interfaces.WorkflowController {
	return &workflowController{definitionService: definitionService}
}

func (c *workflowController) GetWorkflows(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflows, err := c.definitionService.List(project.ID, helpers.QueryParams(r.URL))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, workflows)
}

func (c *workflowController) AddWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	created, validation, err := c.definitionService.Create(project.ID, workflow)
	if err != nil {
		writeWorkflowError(w, err, c.definitionService, project.ID, 0)
		return
	}
	if !validation.Valid {
		writeWorkflowValidation(w, validation)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *workflowController) ValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	result, err := c.definitionService.Validate(project.ID, workflow)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	helpers.WriteJSON(w, http.StatusOK, workflow)
}

func (c *workflowController) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	current := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	updated, validation, err := c.definitionService.Update(project.ID, current.ID, workflow)
	if err != nil {
		writeWorkflowError(w, err, c.definitionService, project.ID, current.ID)
		return
	}
	if !validation.Valid {
		writeWorkflowValidation(w, validation)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func bindWorkflowDefinition(w http.ResponseWriter, r *http.Request, workflow *db.WorkflowTemplate) bool {
	r.Body = http.MaxBytesReader(w, r.Body, workflowDefinitionBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(workflow); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			helpers.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"code":    "WORKFLOW_DEFINITION_TOO_LARGE",
				"message": "Workflow definition exceeds the 8 MiB request limit.",
			})
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func (c *workflowController) RemoveWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	if err := c.definitionService.Delete(project.ID, workflow.ID); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeWorkflowValidation(w http.ResponseWriter, result db.WorkflowValidationResult) {
	helpers.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"code": "WORKFLOW_VALIDATION_FAILED", "valid": false, "issues": result.Issues,
	})
}

func writeWorkflowError(
	w http.ResponseWriter,
	err error,
	service pro_interfaces.WorkflowDefinitionService,
	projectID int,
	workflowID int,
) {
	if errors.Is(err, pro_interfaces.ErrWorkflowRevisionConflict) {
		response := map[string]any{
			"code":    "WORKFLOW_REVISION_CONFLICT",
			"message": "The workflow was changed by another editor. Reload the current definition before saving again.",
		}
		if current, getErr := service.Get(projectID, workflowID); getErr == nil {
			response["current"] = current
		}
		helpers.WriteJSON(w, http.StatusConflict, response)
		return
	}
	helpers.WriteError(w, err)
}

func (c *workflowController) RunWorkflow(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) StopWorkflowRun(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) GetWorkflowRuns(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}

func (c *workflowController) GetWorkflowRun(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) GetWorkflowRunArtifacts(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) GetWorkflowApprovals(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}

func (c *workflowController) ResolveWorkflowApproval(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
