package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	coreprojects "github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowRunLookupStub struct {
	db.WorkflowManager
	run db.WorkflowRun
}

func (s workflowRunLookupStub) GetWorkflowRunByID(projectID, runID int) (db.WorkflowRun, error) {
	if s.run.ProjectID != projectID || s.run.ID != runID {
		return db.WorkflowRun{}, db.ErrNotFound
	}
	return s.run, nil
}

func TestWorkflowAPIAccessMatrixHidesBeforeForbiddenAndBoundsApprovers(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "workflow access matrix"})
	require.NoError(t, err)
	actor, err := store.CreateUserWithoutPassword(db.User{
		Username: "workflow-matrix-runner", Name: "Workflow Matrix Runner", Email: "workflow-matrix-runner@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectTaskRunner})
	require.NoError(t, err)
	visible := db.WorkflowTemplate{ID: 41, ProjectID: project.ID, Name: "visible"}
	hidden := db.WorkflowTemplate{
		ID: 42, ProjectID: project.ID, Name: "hidden",
		AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}},
	}
	request := func(method, target string, workflow db.WorkflowTemplate) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		r = helpers.SetContextValue(r, "project", project)
		r = helpers.SetContextValue(r, "user", &actor)
		r = helpers.SetContextValue(r, "store", store)
		return helpers.SetContextValue(r, "workflow", workflow)
	}
	allow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	list := httptest.NewRecorder()
	coreprojects.WorkflowProjectPermissionMiddleware(pro_interfaces.PermissionViewWorkflow)(allow).ServeHTTP(list, request(http.MethodGet, "/api/project/1/workflows", visible))
	assert.Equal(t, http.StatusNoContent, list.Code)

	for _, test := range []struct {
		name       string
		permission pro_interfaces.PermissionID
		status     int
	}{
		{name: "detail", permission: pro_interfaces.PermissionViewWorkflow, status: http.StatusNoContent},
		{name: "edit", permission: pro_interfaces.PermissionEditWorkflow, status: http.StatusForbidden},
		{name: "start", permission: pro_interfaces.PermissionStartWorkflow, status: http.StatusNoContent},
		{name: "stop", permission: pro_interfaces.PermissionStopWorkflow, status: http.StatusNoContent},
		{name: "run", permission: pro_interfaces.PermissionViewWorkflow, status: http.StatusNoContent},
		{name: "artifact", permission: pro_interfaces.PermissionViewWorkflow, status: http.StatusNoContent},
	} {
		t.Run("visible-"+test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			coreprojects.WorkflowAccessMiddleware(test.permission)(allow).ServeHTTP(response, request(http.MethodGet, "/api/project/1/workflows/41", visible))
			assert.Equal(t, test.status, response.Code)
		})
		t.Run("hidden-"+test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			coreprojects.WorkflowAccessMiddleware(test.permission)(allow).ServeHTTP(response, request(http.MethodGet, "/api/project/1/workflows/42", hidden))
			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}

	run := db.WorkflowRun{ID: 91, ProjectID: project.ID, WorkflowTemplateID: hidden.ID, DefinitionSnapshot: hidden}
	pending := db.WorkflowApproval{ID: 12, ProjectID: project.ID, WorkflowRunID: run.ID, Status: db.WorkflowApprovalPending}
	manager := &workflowManagerStub{run: run}
	eligibleController := NewWorkflowController(&workflowServiceStub{inbox: []db.WorkflowApproval{pending}}, manager, &workflowDefinitionServiceStub{})
	eligibleRequest := helpers.SetContextValue(request(http.MethodGet, "/api/project/1/workflows/42/runs/91", hidden), "workflow_run", run)
	eligibleRun := httptest.NewRecorder()
	eligibleController.GetWorkflowRun(eligibleRun, eligibleRequest)
	assert.Equal(t, http.StatusOK, eligibleRun.Code, eligibleRun.Body.String())
	eligibleArtifacts := httptest.NewRecorder()
	eligibleController.GetWorkflowRunArtifacts(eligibleArtifacts, eligibleRequest)
	assert.Equal(t, http.StatusOK, eligibleArtifacts.Code, eligibleArtifacts.Body.String())

	hiddenController := NewWorkflowController(&workflowServiceStub{}, manager, &workflowDefinitionServiceStub{})
	hiddenRun := httptest.NewRecorder()
	hiddenController.GetWorkflowRun(hiddenRun, eligibleRequest)
	assert.Equal(t, http.StatusNotFound, hiddenRun.Code)

	taskRunID := run.ID
	taskRequest := helpers.SetContextValue(request(http.MethodGet, "/api/project/1/tasks/301/output", hidden), "task", db.Task{
		ID: 301, ProjectID: project.ID, WorkflowRunID: &taskRunID,
	})
	taskResponse := httptest.NewRecorder()
	coreprojects.NewTaskController(store, nil, workflowRunLookupStub{run: run}).WorkflowTaskAccessMiddleware(allow).ServeHTTP(taskResponse, taskRequest)
	assert.Equal(t, http.StatusNotFound, taskResponse.Code, "workflow task output must not bypass hidden run access")

	inbox := httptest.NewRecorder()
	eligibleController.GetWorkflowApprovalInbox(inbox, request(http.MethodGet, "/api/project/1/workflow-approvals", hidden))
	assert.Equal(t, http.StatusOK, inbox.Code, inbox.Body.String())
	assert.Contains(t, inbox.Body.String(), `"id":12`)

	approvalRequest := workflowRawRequest(http.MethodPost, "/api/project/1/workflows/42/runs/91/approvals/1", []byte(`{"status":"approved","source":"user"}`), &hidden)
	approvalRequest = helpers.SetContextValue(approvalRequest, "project", project)
	approvalRequest = helpers.SetContextValue(approvalRequest, "user", &actor)
	approvalRequest = helpers.SetContextValue(approvalRequest, "store", store)
	approvalRequest = helpers.SetContextValue(approvalRequest, "workflow_run", run)
	approvalRequest = mux.SetURLVars(approvalRequest, map[string]string{"node_id": "1"})
	approvalResponse := httptest.NewRecorder()
	deniedController := NewWorkflowController(&workflowServiceStub{approvalErr: pro_interfaces.ErrWorkflowPermissionDenied}, manager, &workflowDefinitionServiceStub{})
	deniedController.ResolveWorkflowApproval(approvalResponse, approvalRequest)
	assert.Equal(t, http.StatusForbidden, approvalResponse.Code)
}
