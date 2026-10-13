package projects

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkflowRunPresentsPublicOutputsAndFrozenEdgeMappings(t *testing.T) {
	workflow := db.WorkflowTemplate{ID: 41, ProjectID: 7}
	nodeID := 201
	document := `{"values":{"tag":"v1.2","replicas":3,"semaphore_workflow_outputs":{"token":"private-structured-output"}},"unknown":"private-extra-field"}`
	run := db.WorkflowRun{ID: 91, ProjectID: 7, WorkflowTemplateID: 41, ActorUserID: 12, WorkflowVersionID: 9,
		DefinitionSnapshot: db.WorkflowTemplate{ID: 41, Nodes: []db.WorkflowNode{{ID: 201, TemplateID: 51}, {ID: 202, TemplateID: 52}}, Edges: []db.WorkflowEdge{{ID: 301, SourceNodeID: 201, DestinationNodeID: 202, Condition: db.WorkflowEdgeOnSuccess, InputMode: db.WorkflowEdgeInputExplicit, InputMappings: []db.WorkflowInputMapping{{Var: "release", Key: "tag"}}}}},
		Nodes:              []db.WorkflowRunNode{{WorkflowNodeID: 201, Status: db.WorkflowRunNodeSucceeded}, {WorkflowNodeID: 202, Status: db.WorkflowRunNodePending}},
	}
	manager := &workflowManagerStub{run: run, tasks: []db.TaskWithTpl{{Task: db.Task{ID: 301, WorkflowRunID: &run.ID, WorkflowNodeID: &nodeID, Status: task_logger.TaskSuccessStatus, Artifacts: &document, Environment: `{"private":"hidden"}`}}}}
	controller := NewWorkflowController(&workflowServiceStub{run: run}, manager, &workflowDefinitionServiceStub{})
	recorder := httptest.NewRecorder()
	controller.GetWorkflowRun(recorder, workflowRunRequest(http.MethodGet, "/api/project/7/workflows/41/runs/91", workflow, run))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Nodes []struct {
			Task *struct {
				Artifacts *string `json:"artifacts"`
			} `json:"task"`
		} `json:"nodes"`
		Workflow struct {
			Edges []db.WorkflowEdge `json:"edges"`
		} `json:"workflow"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotNil(t, response.Nodes[0].Task)
	require.NotNil(t, response.Nodes[0].Task.Artifacts)
	assert.JSONEq(t, `{"values":{"tag":"v1.2","replicas":3}}`, *response.Nodes[0].Task.Artifacts)
	assert.NotContains(t, recorder.Body.String(), "private-structured-output")
	assert.NotContains(t, recorder.Body.String(), "private-extra-field")
	assert.Contains(t, recorder.Body.String(), `"input_mode":"explicit"`)
	assert.Contains(t, recorder.Body.String(), `"var":"release","key":"tag"`)
	assert.NotContains(t, recorder.Body.String(), "private")
	assert.NotContains(t, recorder.Body.String(), "hidden")
}
