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

func workflowRequest(method, target string, body any, workflow *db.WorkflowTemplate) *http.Request {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}

func workflowRawRequest(method, target string, body []byte, workflow *db.WorkflowTemplate) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7, Name: "Project"})
	if workflow != nil {
		request = helpers.SetContextValue(request, "workflow", *workflow)
	}
	return request
}
