package projects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const workflowDefinitionBodyLimit int64 = 8 * 1024 * 1024
const workflowRunCorrelationIDLimit = 64
const workflowRunBodyLimit int64 = 256 * 1024

type workflowController struct {
	definitionService pro_interfaces.WorkflowDefinitionService
	workflowService   pro_interfaces.WorkflowService
	workflowManager   db.WorkflowManager
}

type workflowRunDetails struct {
	Run       workflowRunView           `json:"run"`
	Workflow  workflowRunDefinitionView `json:"workflow"`
	Templates []workflowRunTemplateView `json:"templates"`
	Nodes     []workflowRunNodeDetails  `json:"nodes"`
}

type workflowRunNodeDetails struct {
	Node           workflowRunNodeView                `json:"node"`
	Status         db.WorkflowRunNodeStatus           `json:"status"`
	Reason         string                             `json:"reason,omitempty"`
	Result         *db.WorkflowNodeResult             `json:"result,omitempty"`
	ArtifactInputs []db.WorkflowArtifactInputSnapshot `json:"artifact_inputs,omitempty"`
	Overrides      db.WorkflowNodeOverride            `json:"overrides,omitempty"`
	Task           *workflowRunTaskView               `json:"task,omitempty"`
}

type workflowRunView struct {
	ID                 int                                     `json:"id"`
	ProjectID          int                                     `json:"project_id"`
	WorkflowTemplateID int                                     `json:"workflow_template_id"`
	Status             db.WorkflowRunStatus                    `json:"status"`
	Reason             string                                  `json:"reason,omitempty"`
	Version            *string                                 `json:"version,omitempty"`
	ActorUserID        int                                     `json:"actor_user_id"`
	DefinitionVersion  int                                     `json:"definition_version"`
	DefinitionRevision int                                     `json:"definition_revision"`
	CorrelationID      string                                  `json:"correlation_id"`
	Created            time.Time                               `json:"created"`
	Start              *time.Time                              `json:"start,omitempty"`
	End                *time.Time                              `json:"end,omitempty"`
	RootTaskID         *int                                    `json:"root_task_id,omitempty"`
	Parameters         map[string]db.WorkflowParameterSnapshot `json:"parameters,omitempty"`
	Trigger            *db.WorkflowTriggerSnapshot             `json:"trigger,omitempty"`
}

type workflowRunDefinitionView struct {
	ID                int                               `json:"id"`
	Name              string                            `json:"name"`
	DefinitionVersion int                               `json:"definition_version"`
	Revision          int                               `json:"revision"`
	MaxParallelTasks  int                               `json:"max_parallel_tasks"`
	Parameters        []db.WorkflowParameterDeclaration `json:"parameters,omitempty"`
	Nodes             []workflowRunNodeView             `json:"nodes"`
	Edges             []workflowRunEdgeView             `json:"edges"`
}

type workflowRunNodeView struct {
	ID              int                              `json:"id"`
	TemplateID      int                              `json:"template_id,omitempty"`
	DisplayName     string                           `json:"display_name,omitempty"`
	Kind            db.WorkflowNodeKind              `json:"kind,omitempty"`
	ConvergenceMode db.WorkflowConvergenceMode       `json:"convergence_mode,omitempty"`
	JoinMode        db.WorkflowJoinMode              `json:"join_mode,omitempty"`
	ApprovalTimeout *int                             `json:"approval_timeout,omitempty"`
	ApprovalMessage *string                          `json:"approval_message,omitempty"`
	Note            *string                          `json:"note,omitempty"`
	PositionX       int                              `json:"position_x"`
	PositionY       int                              `json:"position_y"`
	ArtifactOutputs []db.WorkflowArtifactDeclaration `json:"artifact_outputs,omitempty"`
	ArtifactInputs  []db.WorkflowArtifactReference   `json:"artifact_inputs,omitempty"`
	OverridePolicy  db.WorkflowNodeOverridePolicy    `json:"override_policy,omitempty"`
}

type workflowRunEdgeView struct {
	ID                int                      `json:"id"`
	SourceNodeID      int                      `json:"source_node_id"`
	DestinationNodeID int                      `json:"destination_node_id"`
	Condition         db.WorkflowEdgeCondition `json:"condition"`
	Expression        string                   `json:"condition_expression,omitempty"`
	Label             string                   `json:"label,omitempty"`
}

type workflowRunTemplateView struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type workflowRunTaskView struct {
	ID             int                    `json:"id"`
	Status         task_logger.TaskStatus `json:"status"`
	UsedRunnerID   *int                   `json:"used_runner_id,omitempty"`
	UsedRunnerName *string                `json:"used_runner_name,omitempty"`
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
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(workflow); err != nil {
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
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
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
	input := db.WorkflowRunInput{}
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, workflowRunBodyLimit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			helpers.WriteError(w, common_errors.NewValidationError("workflow run input is invalid"))
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			helpers.WriteError(w, common_errors.NewValidationError("workflow run input is invalid"))
			return
		}
	}
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
	run, err := c.workflowService.StartWorkflow(workflow, helpers.UserFromContext(r), correlationID, input)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.Header().Set("Idempotency-Key", correlationID)
	helpers.WriteJSON(w, http.StatusCreated, newWorkflowRunView(run))
}

func (c *workflowController) StopWorkflowRun(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	stopped, err := c.workflowService.StopWorkflowRun(project.ID, run.ID, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, newWorkflowRunView(stopped))
}

func (c *workflowController) GetWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	runs, err := c.workflowManager.GetWorkflowRuns(project.ID, workflow.ID, helpers.QueryParams(r.URL))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	result := make([]workflowRunView, len(runs))
	for index := range runs {
		result[index] = newWorkflowRunView(runs[index])
	}
	helpers.WriteJSON(w, http.StatusOK, result)
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
	templates := make([]workflowRunTemplateView, 0, len(run.Nodes))
	for _, node := range run.Nodes {
		statesByNode[node.WorkflowNodeID] = node
		templates = append(templates, workflowRunTemplateView{
			ID: node.TemplateSnapshot.ID, Name: node.TemplateSnapshot.Name,
		})
	}
	nodes := make([]workflowRunNodeDetails, 0, len(run.DefinitionSnapshot.Nodes))
	for _, node := range run.DefinitionSnapshot.Nodes {
		state, exists := statesByNode[node.ID]
		if !exists {
			continue
		}
		detail := workflowRunNodeDetails{
			Node: newWorkflowRunNodeView(node), Status: state.Status, Reason: state.Reason,
			ArtifactInputs: state.ArtifactInputs, Overrides: state.OverrideSnapshot,
		}
		if state.ResultJSON != "" && state.ResultJSON != "{}" {
			result := state.Result
			detail.Result = &result
		}
		if task, taskExists := tasksByNode[node.ID]; taskExists {
			detail.Task = &workflowRunTaskView{
				ID: task.ID, Status: task.Status,
				UsedRunnerID: task.UsedRunnerID, UsedRunnerName: task.UsedRunnerName,
			}
		}
		nodes = append(nodes, detail)
	}
	return workflowRunDetails{
		Run:       newWorkflowRunView(run),
		Workflow:  newWorkflowRunDefinitionView(run.DefinitionSnapshot),
		Templates: templates,
		Nodes:     nodes,
	}, nil
}

func newWorkflowRunView(run db.WorkflowRun) workflowRunView {
	view := workflowRunView{
		ID:                 run.ID,
		ProjectID:          run.ProjectID,
		WorkflowTemplateID: run.WorkflowTemplateID,
		Status:             run.Status,
		Reason:             run.Reason,
		Version:            run.Version,
		ActorUserID:        run.ActorUserID,
		DefinitionVersion:  run.DefinitionVersion,
		DefinitionRevision: run.DefinitionRevision,
		CorrelationID:      run.CorrelationID,
		Created:            run.Created,
		Start:              run.Start,
		End:                run.End,
		RootTaskID:         run.RootTaskID,
		Parameters:         run.ParameterSnapshot,
	}
	if run.TriggerSnapshot.ID > 0 {
		trigger := run.TriggerSnapshot
		view.Trigger = &trigger
	}
	return view
}

func newWorkflowRunDefinitionView(workflow db.WorkflowTemplate) workflowRunDefinitionView {
	nodes := make([]workflowRunNodeView, len(workflow.Nodes))
	for index := range workflow.Nodes {
		nodes[index] = newWorkflowRunNodeView(workflow.Nodes[index])
	}
	edges := make([]workflowRunEdgeView, len(workflow.Edges))
	for index, edge := range workflow.Edges {
		edges[index] = workflowRunEdgeView{
			ID: edge.ID, SourceNodeID: edge.SourceNodeID,
			DestinationNodeID: edge.DestinationNodeID,
			Condition:         edge.Condition, Expression: edge.Expression, Label: edge.Label,
		}
	}
	return workflowRunDefinitionView{
		ID: workflow.ID, Name: workflow.Name,
		DefinitionVersion: workflow.DefinitionVersion, Revision: workflow.Revision,
		MaxParallelTasks: workflow.MaxParallelTasks,
		Parameters:       workflow.ParameterDefinitions,
		Nodes:            nodes, Edges: edges,
	}
}

func newWorkflowRunNodeView(node db.WorkflowNode) workflowRunNodeView {
	return workflowRunNodeView{
		ID:              node.ID,
		TemplateID:      node.TemplateID,
		DisplayName:     node.DisplayName,
		Kind:            node.Kind,
		ConvergenceMode: node.ConvergenceMode,
		JoinMode:        node.JoinMode,
		ApprovalTimeout: node.ApprovalTimeout,
		ApprovalMessage: node.ApprovalMessage,
		Note:            node.Note,
		PositionX:       node.PositionX,
		PositionY:       node.PositionY,
		ArtifactOutputs: node.ArtifactOutputs,
		ArtifactInputs:  node.ArtifactInputs,
		OverridePolicy:  node.OverridePolicy,
	}
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
