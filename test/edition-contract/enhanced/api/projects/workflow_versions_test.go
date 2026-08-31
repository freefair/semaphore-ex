package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowVersionDefinitionServiceStub struct {
	workflowDefinitionServiceStub
	versions       []db.WorkflowVersion
	version        db.WorkflowVersion
	diff           pro_interfaces.WorkflowDefinitionDiff
	restored       db.WorkflowTemplate
	restoreMessage string
	listParams     db.RetrieveQueryParams
}

func (s *workflowVersionDefinitionServiceStub) ListVersions(
	_ int, _ int, params db.RetrieveQueryParams, _ ...*db.User,
) ([]db.WorkflowVersion, error) {
	s.listParams = params
	return s.versions, nil
}

func (s *workflowVersionDefinitionServiceStub) GetVersion(
	_ int, _ int, _ int, _ ...*db.User,
) (db.WorkflowVersion, error) {
	return s.version, nil
}

func (s *workflowVersionDefinitionServiceStub) DiffVersions(
	_ int, _ int, _ int, _ int, _ ...*db.User,
) (pro_interfaces.WorkflowDefinitionDiff, error) {
	return s.diff, nil
}

func (s *workflowVersionDefinitionServiceStub) RestoreVersion(
	_ int, _ int, _ int, message string, _ ...*db.User,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	s.restoreMessage = message
	return s.restored, db.WorkflowValidationResult{Valid: true}, nil
}

func TestWorkflowVersionControllerListDetailDiffAndRestore(t *testing.T) {
	version := db.WorkflowVersion{
		ID: 11, ProjectID: 7, WorkflowTemplateID: 41, VersionNumber: 2,
		Message: "Rename", DefinitionSnapshot: db.WorkflowTemplate{Name: "private snapshot"},
	}
	service := &workflowVersionDefinitionServiceStub{
		versions: []db.WorkflowVersion{version}, version: version,
		diff: pro_interfaces.WorkflowDefinitionDiff{Changes: []pro_interfaces.WorkflowDefinitionDiffChange{{
			Section: "metadata", Before: json.RawMessage(`{"name":"Deploy"}`), After: json.RawMessage(`{"name":"Release"}`),
		}}},
		restored: db.WorkflowTemplate{ID: 41, ProjectID: 7, Revision: 3, Name: "Deploy"},
	}
	controller := NewWorkflowController(nil, nil, service)

	list := httptest.NewRecorder()
	controller.GetWorkflowVersions(list, workflowVersionRequest(http.MethodGet, "/api/project/7/workflows/41/versions?count=999&before=12", nil, ""))
	require.Equal(t, http.StatusOK, list.Code)
	assert.Contains(t, list.Body.String(), `"version_number":2`)
	assert.NotContains(t, list.Body.String(), "private snapshot", "timeline summaries must not serialize full definitions")
	assert.Equal(t, maxWorkflowVersionPageSize, service.listParams.Count)
	assert.Equal(t, 12, service.listParams.BeforeID)

	detail := httptest.NewRecorder()
	controller.GetWorkflowVersion(detail, workflowVersionRequest(http.MethodGet, "/api/project/7/workflows/41/versions/2", map[string]string{"version_number": "2"}, ""))
	require.Equal(t, http.StatusOK, detail.Code)
	assert.Contains(t, detail.Body.String(), `"definition":{"id":0`)

	diff := httptest.NewRecorder()
	controller.DiffWorkflowVersions(diff, workflowVersionRequest(http.MethodGet, "/api/project/7/workflows/41/versions/diff?from=1&to=2", nil, ""))
	require.Equal(t, http.StatusOK, diff.Code)
	assert.Contains(t, diff.Body.String(), `"section":"metadata"`)

	restore := httptest.NewRecorder()
	controller.RestoreWorkflowVersion(restore, workflowVersionRequest(
		http.MethodPost, "/api/project/7/workflows/41/versions/1/restore",
		map[string]string{"version_number": "1"}, `{"message":"Restore initial"}`,
	))
	require.Equal(t, http.StatusOK, restore.Code)
	assert.Equal(t, "Restore initial", service.restoreMessage)
	assert.Contains(t, restore.Body.String(), `"revision":3`)
}

func TestWorkflowVersionControllerRejectsInvalidDiffAndRestoreBodies(t *testing.T) {
	controller := NewWorkflowController(nil, nil, &workflowVersionDefinitionServiceStub{})

	invalidDiff := httptest.NewRecorder()
	controller.DiffWorkflowVersions(invalidDiff, workflowVersionRequest(
		http.MethodGet, "/api/project/7/workflows/41/versions/diff?from=zero&to=2", nil, "",
	))
	assert.Equal(t, http.StatusBadRequest, invalidDiff.Code)

	unknownRestoreField := httptest.NewRecorder()
	controller.RestoreWorkflowVersion(unknownRestoreField, workflowVersionRequest(
		http.MethodPost, "/api/project/7/workflows/41/versions/1/restore",
		map[string]string{"version_number": "1"}, `{"message":"Restore","unknown":true}`,
	))
	assert.Equal(t, http.StatusBadRequest, unknownRestoreField.Code)
}

func workflowVersionRequest(method string, target string, variables map[string]string, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = mux.SetURLVars(request, variables)
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "workflow", db.WorkflowTemplate{ID: 41, ProjectID: 7})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 9, Admin: true})
	return request
}
