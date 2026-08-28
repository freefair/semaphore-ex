package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowDefinitionServiceStub struct {
	workflows  []db.WorkflowTemplate
	validation db.WorkflowValidationResult
	create     db.WorkflowTemplate
	update     db.WorkflowTemplate
	updateErr  error
	deletedID  int
	calls      int
}

type workflowServiceStub struct {
	pro_interfaces.WorkflowService
	run            db.WorkflowRun
	correlationIDs []string
	progressCalls  int
	stopCalls      int
	artifacts      map[string]any
}

func (s *workflowServiceStub) StartWorkflow(_ db.WorkflowTemplate, _ *db.User, correlationID string) (db.WorkflowRun, error) {
	s.correlationIDs = append(s.correlationIDs, correlationID)
	s.run.CorrelationID = correlationID
	return s.run, nil
}

func (s *workflowServiceStub) ProgressWorkflowRun(_ int, _ int, _ *db.User) error {
	s.progressCalls++
	return nil
}

func (s *workflowServiceStub) StopWorkflowRun(_ int, _ int, _ *db.User) (db.WorkflowRun, error) {
	s.stopCalls++
	return s.run, nil
}

func (s *workflowServiceStub) GetWorkflowRunArtifacts(_ int, _ int, _ *int) (map[string]any, error) {
	return s.artifacts, nil
}

type workflowManagerStub struct {
	db.WorkflowManager
	runs  []db.WorkflowRun
	run   db.WorkflowRun
	tasks []db.TaskWithTpl
}

func (s *workflowManagerStub) GetWorkflowRuns(_ int, _ int, _ db.RetrieveQueryParams) ([]db.WorkflowRun, error) {
	return s.runs, nil
}

func (s *workflowManagerStub) GetWorkflowRun(_ int, _ int, _ int) (db.WorkflowRun, error) {
	return s.run, nil
}

func (s *workflowManagerStub) GetWorkflowRunTasks(_ int, _ int, _ db.RetrieveQueryParams) ([]db.TaskWithTpl, error) {
	return s.tasks, nil
}

func (s *workflowDefinitionServiceStub) List(_ int, _ db.RetrieveQueryParams) ([]db.WorkflowTemplate, error) {
	return s.workflows, nil
}

func (s *workflowDefinitionServiceStub) Get(_ int, workflowID int) (db.WorkflowTemplate, error) {
	for _, workflow := range s.workflows {
		if workflow.ID == workflowID {
			return workflow, nil
		}
	}
	return db.WorkflowTemplate{}, db.ErrNotFound
}

func (s *workflowDefinitionServiceStub) Validate(_ int, _ db.WorkflowTemplate) (db.WorkflowValidationResult, error) {
	s.calls++
	return s.validation, nil
}

func (s *workflowDefinitionServiceStub) Create(_ int, _ db.WorkflowTemplate) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	s.calls++
	return s.create, s.validation, nil
}

func (s *workflowDefinitionServiceStub) Update(_ int, _ int, _ db.WorkflowTemplate) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	s.calls++
	return s.update, s.validation, s.updateErr
}

func (s *workflowDefinitionServiceStub) Delete(_ int, workflowID int) error {
	s.deletedID = workflowID
	return nil
}

func TestWorkflowControllerCRUDAndValidation(t *testing.T) {
	valid := db.WorkflowValidationResult{Valid: true, Issues: []db.WorkflowValidationIssue{}}
	created := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Deploy", DefinitionVersion: 1, Revision: 1}
	service := &workflowDefinitionServiceStub{workflows: []db.WorkflowTemplate{created}, validation: valid, create: created, update: created}
	controller := NewWorkflowController(nil, nil, service)

	listRecorder := httptest.NewRecorder()
	controller.GetWorkflows(listRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows", nil, nil))
	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), `"revision":1`)

	createRecorder := httptest.NewRecorder()
	controller.AddWorkflow(createRecorder, workflowRequest(http.MethodPost, "/api/project/7/workflows", created, nil))
	assert.Equal(t, http.StatusCreated, createRecorder.Code, createRecorder.Body.String())

	getRecorder := httptest.NewRecorder()
	controller.GetWorkflow(getRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows/41", nil, &created))
	assert.Equal(t, http.StatusOK, getRecorder.Code)

	updateRecorder := httptest.NewRecorder()
	controller.UpdateWorkflow(updateRecorder, workflowRequest(http.MethodPut, "/api/project/7/workflows/41", created, &created))
	assert.Equal(t, http.StatusOK, updateRecorder.Code, updateRecorder.Body.String())

	deleteRecorder := httptest.NewRecorder()
	controller.RemoveWorkflow(deleteRecorder, workflowRequest(http.MethodDelete, "/api/project/7/workflows/41", nil, &created))
	assert.Equal(t, http.StatusNoContent, deleteRecorder.Code)
	assert.Equal(t, 41, service.deletedID)

	validateRecorder := httptest.NewRecorder()
	controller.ValidateWorkflow(validateRecorder, workflowRequest(http.MethodPost, "/api/project/7/workflows/validate", created, nil))
	assert.Equal(t, http.StatusOK, validateRecorder.Code)
	assert.JSONEq(t, `{"valid":true,"issues":[]}`, validateRecorder.Body.String())
}

func TestWorkflowControllerReturnsLocatedValidationErrors(t *testing.T) {
	nodeID := -2
	service := &workflowDefinitionServiceStub{validation: db.WorkflowValidationResult{
		Valid: false,
		Issues: []db.WorkflowValidationIssue{{
			Code: "WORKFLOW_TEMPLATE_NOT_IN_PROJECT", Message: "Template is not available in this project.",
			Path: "nodes[1].template_id", NodeID: &nodeID,
		}},
	}}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.AddWorkflow(recorder, workflowRequest(http.MethodPost, "/api/project/7/workflows", db.WorkflowTemplate{Name: "Invalid"}, nil))

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "WORKFLOW_VALIDATION_FAILED", response["code"])
	issues := response["issues"].([]any)
	issue := issues[0].(map[string]any)
	assert.Equal(t, "WORKFLOW_TEMPLATE_NOT_IN_PROJECT", issue["code"])
	assert.Equal(t, "nodes[1].template_id", issue["path"])
	assert.Equal(t, float64(-2), issue["node_id"])
}

func TestWorkflowControllerReturnsCurrentDefinitionOnRevisionConflict(t *testing.T) {
	current := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Newer", Revision: 3}
	service := &workflowDefinitionServiceStub{
		workflows: []db.WorkflowTemplate{current}, validation: db.WorkflowValidationResult{Valid: true},
		updateErr: pro_interfaces.ErrWorkflowRevisionConflict,
	}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.UpdateWorkflow(recorder, workflowRequest(http.MethodPut, "/api/project/7/workflows/41", db.WorkflowTemplate{Revision: 2}, &current))

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"WORKFLOW_REVISION_CONFLICT"`)
	assert.Contains(t, recorder.Body.String(), `"name":"Newer"`)
	assert.Contains(t, recorder.Body.String(), `"revision":3`)
}

func TestWorkflowControllerRejectsOversizedDefinitionsBeforeServiceCalls(t *testing.T) {
	current := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Current", Revision: 1}
	body := []byte(`{"name":"` + strings.Repeat("a", int(workflowDefinitionBodyLimit)) + `"}`)
	tests := []struct {
		name   string
		method string
		target string
		call   func(pro_interfaces.WorkflowController, http.ResponseWriter, *http.Request)
	}{
		{name: "create", method: http.MethodPost, target: "/api/project/7/workflows", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.AddWorkflow(w, r)
		}},
		{name: "validate", method: http.MethodPost, target: "/api/project/7/workflows/validate", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.ValidateWorkflow(w, r)
		}},
		{name: "update", method: http.MethodPut, target: "/api/project/7/workflows/41", call: func(controller pro_interfaces.WorkflowController, w http.ResponseWriter, r *http.Request) {
			controller.UpdateWorkflow(w, r)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &workflowDefinitionServiceStub{}
			controller := NewWorkflowController(nil, nil, service)
			recorder := httptest.NewRecorder()
			request := workflowRawRequest(test.method, test.target, body, &current)

			test.call(controller, recorder, request)

			assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
			assert.JSONEq(t, `{"code":"WORKFLOW_DEFINITION_TOO_LARGE","message":"Workflow definition exceeds the 8 MiB request limit."}`, recorder.Body.String())
			assert.Zero(t, service.calls)
		})
	}
}

func TestWorkflowControllerAcceptsMaximumShapeWithinBodyLimit(t *testing.T) {
	nodes := make([]db.WorkflowNode, 200)
	for index := range nodes {
		nodes[index] = db.WorkflowNode{ID: index + 1, TemplateID: index + 1}
	}
	edges := make([]db.WorkflowEdge, 0, 1000)
	for source := 1; source < len(nodes) && len(edges) < cap(edges); source++ {
		for destination := source + 1; destination <= len(nodes) && len(edges) < cap(edges); destination++ {
			edges = append(edges, db.WorkflowEdge{ID: len(edges) + 1, SourceNodeID: source, DestinationNodeID: destination})
		}
	}
	workflow := db.WorkflowTemplate{Name: "Maximum shape", DefinitionVersion: 1, Nodes: nodes, Edges: edges}
	data, err := json.Marshal(workflow)
	require.NoError(t, err)
	require.Less(t, int64(len(data)), workflowDefinitionBodyLimit)
	service := &workflowDefinitionServiceStub{validation: db.WorkflowValidationResult{Valid: true}}
	controller := NewWorkflowController(nil, nil, service)
	recorder := httptest.NewRecorder()

	controller.AddWorkflow(recorder, workflowRawRequest(http.MethodPost, "/api/project/7/workflows", data, nil))

	assert.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, 1, service.calls)
}

func TestWorkflowRunControllerStartStatusListStopAndArtifacts(t *testing.T) {
	taskID := 301
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Edited live definition", Revision: 4}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: workflow.ID, ActorUserID: 12,
		Status: db.WorkflowRunQueued, DefinitionRevision: 3,
		DefinitionSnapshot: db.WorkflowTemplate{
			ID: 41, ProjectID: 7, Name: "Original snapshot", Revision: 3,
			Nodes: []db.WorkflowNode{{ID: 201, TemplateID: 51, DisplayName: "Deploy"}},
		},
		Nodes: []db.WorkflowRunNode{{
			ID: 101, ProjectID: 7, WorkflowRunID: 91, WorkflowNodeID: 201,
			TemplateID: 51, Status: db.WorkflowRunNodeQueued, TaskID: &taskID,
			TemplateSnapshot: db.Template{ID: 51, Name: "Deploy template"},
		}},
	}
	service := &workflowServiceStub{run: run, artifacts: map[string]any{}}
	workflowNodeID := 201
	manager := &workflowManagerStub{
		runs: []db.WorkflowRun{run}, run: run,
		tasks: []db.TaskWithTpl{{Task: db.Task{ID: taskID, ProjectID: 7, WorkflowNodeID: &workflowNodeID}}},
	}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})

	start := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	start.Header.Set("Idempotency-Key", "deploy-once")
	startRecorder := httptest.NewRecorder()
	controller.RunWorkflow(startRecorder, start)
	assert.Equal(t, http.StatusCreated, startRecorder.Code, startRecorder.Body.String())
	assert.Equal(t, "deploy-once", startRecorder.Header().Get("Idempotency-Key"))
	assert.Contains(t, startRecorder.Body.String(), `"correlation_id":"deploy-once"`)

	duplicateRecorder := httptest.NewRecorder()
	controller.RunWorkflow(duplicateRecorder, start)
	assert.Equal(t, http.StatusCreated, duplicateRecorder.Code)
	assert.Equal(t, []string{"deploy-once", "deploy-once"}, service.correlationIDs)

	listRecorder := httptest.NewRecorder()
	controller.GetWorkflowRuns(listRecorder, workflowRequest(http.MethodGet, "/api/project/7/workflows/41/runs", nil, &workflow))
	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.Contains(t, listRecorder.Body.String(), `"id":91`)

	statusRecorder := httptest.NewRecorder()
	controller.GetWorkflowRun(statusRecorder, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run))
	assert.Equal(t, http.StatusOK, statusRecorder.Code)
	assert.Equal(t, 1, service.progressCalls)
	assert.Contains(t, statusRecorder.Body.String(), `"workflow":{"id":41`, "the status API must return the frozen definition")
	assert.Contains(t, statusRecorder.Body.String(), `"name":"Original snapshot"`)
	assert.Contains(t, statusRecorder.Body.String(), `"task":{"id":301`)

	stopRecorder := httptest.NewRecorder()
	controller.StopWorkflowRun(stopRecorder, workflowRunRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/stop", workflow, run))
	assert.Equal(t, http.StatusOK, stopRecorder.Code)
	assert.Equal(t, 1, service.stopCalls)

	artifactsRecorder := httptest.NewRecorder()
	controller.GetWorkflowRunArtifacts(artifactsRecorder, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91/artifacts", workflow, run))
	assert.Equal(t, http.StatusOK, artifactsRecorder.Code)
	assert.JSONEq(t, `{}`, artifactsRecorder.Body.String())
}

func TestWorkflowRunControllerRejectsOversizedIdempotencyKey(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	service := &workflowServiceStub{}
	controller := NewWorkflowController(service, &workflowManagerStub{}, &workflowDefinitionServiceStub{})
	request := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	request.Header.Set("Idempotency-Key", strings.Repeat("a", workflowRunCorrelationIDLimit+1))
	recorder := httptest.NewRecorder()

	controller.RunWorkflow(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Empty(t, service.correlationIDs)
}

func TestWorkflowRunResponsesExposeOnlyRunPresentationData(t *testing.T) {
	taskArguments := "sensitive-task-arguments"
	vaultScript := "sensitive-vault-script"
	usedRunnerID := 88
	usedRunnerName := "runner-east"
	workflowNodeID := 201
	taskID := 301
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "Edited live definition", Revision: 4}
	run := db.WorkflowRun{
		ID: 91, ProjectID: 7, WorkflowTemplateID: workflow.ID, ActorUserID: 12,
		Status: db.WorkflowRunRunning, DefinitionVersion: 1, DefinitionRevision: 3,
		CorrelationID: "deploy-once",
		DefinitionSnapshot: db.WorkflowTemplate{
			ID: 41, ProjectID: 7, Name: "Original snapshot", DefinitionVersion: 1, Revision: 3,
			Nodes: []db.WorkflowNode{{
				ID: workflowNodeID, WorkflowTemplateID: 41, TemplateID: 51,
				DisplayName: "Deploy application", Kind: db.WorkflowNodeTaskKind,
				PositionX: 120, PositionY: 240,
				TaskParams: &db.TaskParams{
					Environment: "sensitive-workflow-environment",
					Arguments:   &taskArguments,
				},
			}},
			Edges: []db.WorkflowEdge{},
		},
		Nodes: []db.WorkflowRunNode{{
			ID: 101, ProjectID: 7, WorkflowRunID: 91, WorkflowNodeID: workflowNodeID,
			TemplateID: 51, Status: db.WorkflowRunNodeRunning, TaskID: &taskID,
			TemplateSnapshot: db.Template{
				ID: 51, ProjectID: 7, Name: "Deploy template", Playbook: "sensitive-playbook.yml",
				Vaults: []db.TemplateVault{{Type: db.TemplateVaultScript, Script: &vaultScript}},
			},
		}},
	}
	service := &workflowServiceStub{run: run}
	manager := &workflowManagerStub{
		runs: []db.WorkflowRun{run}, run: run,
		tasks: []db.TaskWithTpl{{
			Task: db.Task{
				ID: taskID, ProjectID: 7, WorkflowNodeID: &workflowNodeID,
				Status:      task_logger.TaskRunningStatus,
				Environment: "sensitive-task-environment", Arguments: &taskArguments,
			},
			UsedRunnerID: &usedRunnerID, UsedRunnerName: &usedRunnerName,
		}},
	}
	controller := NewWorkflowController(service, manager, &workflowDefinitionServiceStub{})

	responses := map[string]*httptest.ResponseRecorder{}

	startRequest := workflowRequest(http.MethodPost, "/api/project/7/workflows/41/run", nil, &workflow)
	startRequest.Header.Set("Idempotency-Key", "deploy-once")
	responses["start"] = httptest.NewRecorder()
	controller.RunWorkflow(responses["start"], startRequest)

	responses["list"] = httptest.NewRecorder()
	controller.GetWorkflowRuns(responses["list"], workflowRequest(http.MethodGet, "/api/project/7/workflows/41/runs", nil, &workflow))

	responses["details"] = httptest.NewRecorder()
	controller.GetWorkflowRun(responses["details"], workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run))

	responses["stop"] = httptest.NewRecorder()
	controller.StopWorkflowRun(responses["stop"], workflowRunRequest(http.MethodPost, "/api/project/7/workflows/41/runs/91/stop", workflow, run))

	for name, recorder := range responses {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, []int{http.StatusOK, http.StatusCreated}, recorder.Code, recorder.Body.String())
			body := recorder.Body.String()
			assert.NotContains(t, body, "sensitive-workflow-environment")
			assert.NotContains(t, body, "sensitive-task-arguments")
			assert.NotContains(t, body, "sensitive-vault-script")
			assert.NotContains(t, body, "sensitive-playbook.yml")
			assert.NotContains(t, body, "sensitive-task-environment")
		})
	}

	details := responses["details"].Body.String()
	assert.Contains(t, details, `"name":"Original snapshot"`)
	assert.Contains(t, details, `"display_name":"Deploy application"`)
	assert.Contains(t, details, `"name":"Deploy template"`)
	assert.Contains(t, details, `"id":301`)
	assert.Contains(t, details, `"status":"running"`)
	assert.Contains(t, details, `"used_runner_id":88`)
}

func workflowRequest(method, target string, body any, workflow *db.WorkflowTemplate) *http.Request {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 12, Username: "workflow-user"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}

func workflowRawRequest(method, target string, body []byte, workflow *db.WorkflowTemplate) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 12, Username: "workflow-user"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}

func workflowRunRequest(method, target string, workflow db.WorkflowTemplate, run db.WorkflowRun) *http.Request {
	request := workflowRequest(method, target, nil, &workflow)
	return helpers.SetContextValue(request, "workflow_run", run)
}
