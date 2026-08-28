package projects

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const workflowDefinitionBodyLimit int64 = 8 * 1024 * 1024
const workflowRunCorrelationIDLimit = 64

type workflowController struct {
	definitionService pro_interfaces.WorkflowDefinitionService
	workflowService   pro_interfaces.WorkflowService
	workflowManager   db.WorkflowManager
}

type workflowRunDetails struct {
	Run       db.WorkflowRun           `json:"run"`
	Workflow  db.WorkflowTemplate      `json:"workflow"`
	Templates []db.Template            `json:"templates"`
	Nodes     []workflowRunNodeDetails `json:"nodes"`
}

type workflowRunNodeDetails struct {
	Node   db.WorkflowNode          `json:"node"`
	Status db.WorkflowRunNodeStatus `json:"status"`
	Reason string                   `json:"reason,omitempty"`
	Task   *db.TaskWithTpl          `json:"task,omitempty"`
}

var _ pro_interfaces.WorkflowController = (*workflowController)(nil)

func NewWorkflowController(
	workflowService pro_interfaces.WorkflowService,
	workflowManager db.WorkflowManager,
	definitionService pro_interfaces.WorkflowDefinitionService,
) pro_interfaces.WorkflowController {
	return &workflowController{
		definitionService: definitionService,
		workflowService:   workflowService,
		workflowManager:   workflowManager,
	}
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

func (c *workflowController) RunWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	correlationID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if correlationID == "" {
		correlationID = helpers.CorrelationID(r.Context())
	}
	if correlationID == "" {
		correlationID = random.String(32)
	}
	if len(correlationID) > workflowRunCorrelationIDLimit {
		helpers.WriteError(w, common_errors.NewValidationError("workflow run idempotency key must not exceed 64 characters"))
		return
	}
	run, err := c.workflowService.StartWorkflow(workflow, helpers.UserFromContext(r), correlationID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.Header().Set("Idempotency-Key", correlationID)
	helpers.WriteJSON(w, http.StatusCreated, run)
}

func (c *workflowController) StopWorkflowRun(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	stopped, err := c.workflowService.StopWorkflowRun(project.ID, run.ID, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, stopped)
}

func (c *workflowController) GetWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	runs, err := c.workflowManager.GetWorkflowRuns(project.ID, workflow.ID, helpers.QueryParams(r.URL))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, runs)
}

func (c *workflowController) GetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	if err := c.workflowService.ProgressWorkflowRun(project.ID, run.ID, helpers.UserFromContext(r)); err != nil {
		helpers.WriteError(w, err)
		return
	}
	current, err := c.workflowManager.GetWorkflowRun(project.ID, workflow.ID, run.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	details, err := c.workflowRunDetails(current)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, details)
}

func (c *workflowController) workflowRunDetails(run db.WorkflowRun) (workflowRunDetails, error) {
	tasks, err := c.workflowManager.GetWorkflowRunTasks(run.ProjectID, run.ID, db.RetrieveQueryParams{})
	if err != nil {
		return workflowRunDetails{}, err
	}
	tasksByNode := make(map[int]db.TaskWithTpl, len(tasks))
	for _, task := range tasks {
		if task.WorkflowNodeID != nil {
			tasksByNode[*task.WorkflowNodeID] = task
		}
	}
	statesByNode := make(map[int]db.WorkflowRunNode, len(run.Nodes))
	templates := make([]db.Template, 0, len(run.Nodes))
	for _, node := range run.Nodes {
		statesByNode[node.WorkflowNodeID] = node
		templates = append(templates, node.TemplateSnapshot)
	}
	nodes := make([]workflowRunNodeDetails, 0, len(run.DefinitionSnapshot.Nodes))
	for _, node := range run.DefinitionSnapshot.Nodes {
		state, exists := statesByNode[node.ID]
		if !exists {
			continue
		}
		detail := workflowRunNodeDetails{Node: node, Status: state.Status, Reason: state.Reason}
		if task, taskExists := tasksByNode[node.ID]; taskExists {
			taskCopy := task
			detail.Task = &taskCopy
		}
		nodes = append(nodes, detail)
	}
	return workflowRunDetails{
		Run: run, Workflow: run.DefinitionSnapshot, Templates: templates, Nodes: nodes,
	}, nil
}

func (c *workflowController) GetWorkflowRunArtifacts(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	artifacts, err := c.workflowService.GetWorkflowRunArtifacts(project.ID, run.ID, nil)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifacts)
}

func (c *workflowController) GetWorkflowApprovals(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}

func (c *workflowController) ResolveWorkflowApproval(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
