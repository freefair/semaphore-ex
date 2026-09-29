package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type upstreamRevisionDefinitionServiceStub struct {
	workflowDefinitionServiceStub
	versions      []db.WorkflowVersion
	version       db.WorkflowVersion
	listErr       error
	listCalls     int
	getCalls      int
	getVersionArg int
}

type upstreamRevisionStoreStub struct {
	db.WorkflowManager
	graph     db.WorkflowTemplate
	revisions []db.WorkflowRevision
}

func (s upstreamRevisionStoreStub) GetWorkflowRevisions(_ int, _ int) ([]db.WorkflowRevision, error) {
	return s.revisions, nil
}

func (s upstreamRevisionStoreStub) GetWorkflowRevisionGraph(_ int, _ int) (db.WorkflowTemplate, error) {
	return s.graph, nil
}

func (s *upstreamRevisionDefinitionServiceStub) ListVersions(_ int, _ int, _ db.RetrieveQueryParams, _ ...*db.User) ([]db.WorkflowVersion, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.versions, nil
}

func (s *upstreamRevisionDefinitionServiceStub) GetVersion(_ int, _ int, versionNumber int, _ ...*db.User) (db.WorkflowVersion, error) {
	s.getCalls++
	s.getVersionArg = versionNumber
	return s.version, nil
}

func (s *upstreamRevisionDefinitionServiceStub) DiffVersions(_ int, _ int, _ int, _ int, _ ...*db.User) (pro_interfaces.WorkflowDefinitionDiff, error) {
	return pro_interfaces.WorkflowDefinitionDiff{}, db.ErrNotFound
}

func (s *upstreamRevisionDefinitionServiceStub) RestoreVersion(_ int, _ int, _ int, _ string, _ ...*db.User) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
}

func TestWorkflowRevisionDetailUsesAuthorizedDefinitionService(t *testing.T) {
	service := &upstreamRevisionDefinitionServiceStub{
		workflowDefinitionServiceStub: workflowDefinitionServiceStub{workflows: []db.WorkflowTemplate{{ID: 41, ProjectID: 7}}},
		versions:                      []db.WorkflowVersion{{ID: 71, VersionNumber: 3}},
		version: db.WorkflowVersion{
			ID: 71, ProjectID: 7, WorkflowTemplateID: 41, VersionNumber: 3,
			DefinitionSnapshotJSON: `{"owner_credential":"must-not-serialize"}`,
			DefinitionSnapshot:     db.WorkflowTemplate{ID: 41, ProjectID: 7, Name: "private definition"},
		},
	}
	controller := NewWorkflowController(nil, upstreamRevisionStoreStub{graph: db.WorkflowTemplate{ID: 41, ProjectID: 7, RevisionID: 71, Revision: 3}}, service)
	request := workflowRevisionRequest("/api/project/7/workflows/41/revisions/71", map[string]string{"revision_id": "71"})
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRevision(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Zero(t, service.listCalls)
	assert.Equal(t, 1, service.getCalls)
	assert.Equal(t, 3, service.getVersionArg)
	assert.Contains(t, recorder.Body.String(), `"revision_id":71`)
	assert.Contains(t, recorder.Body.String(), `"revision":3`)
	assert.Contains(t, recorder.Body.String(), `"name":"private definition"`)
	assert.NotContains(t, recorder.Body.String(), "must-not-serialize")
}

func TestWorkflowRevisionDetailHidesUnavailableWorkflow(t *testing.T) {
	service := &upstreamRevisionDefinitionServiceStub{workflowDefinitionServiceStub: workflowDefinitionServiceStub{workflows: nil}, listErr: db.ErrNotFound}
	controller := NewWorkflowController(nil, nil, service)
	request := workflowRevisionRequest("/api/project/7/workflows/41/revisions/71", map[string]string{"revision_id": "71"})
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRevision(recorder, request)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Zero(t, service.listCalls)
	assert.Zero(t, service.getCalls)
}

func TestWorkflowRevisionDetailRejectsMismatchedVersionRowID(t *testing.T) {
	service := &upstreamRevisionDefinitionServiceStub{
		workflowDefinitionServiceStub: workflowDefinitionServiceStub{workflows: []db.WorkflowTemplate{{ID: 41, ProjectID: 7}}},
		version:                       db.WorkflowVersion{ID: 72, ProjectID: 7, WorkflowTemplateID: 41, VersionNumber: 3},
	}
	controller := NewWorkflowController(nil, upstreamRevisionStoreStub{graph: db.WorkflowTemplate{ID: 41, ProjectID: 7, RevisionID: 71, Revision: 3}}, service)
	request := workflowRevisionRequest("/api/project/7/workflows/41/revisions/71", map[string]string{"revision_id": "71"})
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRevision(recorder, request)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestWorkflowRevisionListReturnsOnlyAuthorizedVersionIDs(t *testing.T) {
	service := &upstreamRevisionDefinitionServiceStub{
		versions: []db.WorkflowVersion{{ID: 71, VersionNumber: 3}},
	}
	store := upstreamRevisionStoreStub{revisions: []db.WorkflowRevision{
		{ID: 71, ProjectID: 7, WorkflowTemplateID: 41, Number: 3},
		{ID: 72, ProjectID: 7, WorkflowTemplateID: 41, Number: 4},
	}}
	controller := NewWorkflowController(nil, store, service)
	request := workflowRevisionRequest("/api/project/7/workflows/41/revisions", nil)
	recorder := httptest.NewRecorder()

	controller.GetWorkflowRevisions(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"id":71`)
	assert.NotContains(t, recorder.Body.String(), `"id":72`)
}

func workflowRevisionRequest(target string, variables map[string]string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request = mux.SetURLVars(request, variables)
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "workflow", db.WorkflowTemplate{ID: 41, ProjectID: 7})
	return helpers.SetContextValue(request, "user", &db.User{ID: 9})
}
