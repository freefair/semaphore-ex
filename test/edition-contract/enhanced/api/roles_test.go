package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deniedProjectRoleCapabilityProvider struct{}

func (deniedProjectRoleCapabilityProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityProjectRoles,
			pro_interfaces.CapabilityStateUnavailable,
			pro_interfaces.CapabilityReasonProviderUnavailable,
			nil,
			nil,
		),
	}), nil
}

func (deniedProjectRoleCapabilityProvider) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, nil
}

func TestProjectRoleControllerCRUDCatalogIsolationAndStaleWrites(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Role API project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "Other role API project"})
	require.NoError(t, err)
	controller := NewRolesController(store, features.NewCapabilityProvider(store))

	catalogResponse := serveProjectRoleRequest(
		t, project, http.MethodGet, "/api/project/1/roles/permissions", nil, nil,
		controller.GetProjectPermissionCatalog,
	)
	assert.Equal(t, http.StatusOK, catalogResponse.Code)
	var catalog []pro_interfaces.PermissionDefinition
	require.NoError(t, json.Unmarshal(catalogResponse.Body.Bytes(), &catalog))
	assert.Len(t, catalog, 5)

	createResponse := serveProjectRoleRequest(
		t, project, http.MethodPost, "/api/project/1/roles",
		map[string]any{"name": "Resource viewer", "permissions": db.CanViewProjectResources}, nil,
		controller.AddProjectRole,
	)
	assert.Equal(t, http.StatusCreated, createResponse.Code)
	var created db.Role
	require.NoError(t, json.Unmarshal(createResponse.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, 1, created.Revision)
	assert.Equal(t, &project.ID, created.ProjectID)

	getResponse := serveProjectRoleRequest(
		t, project, http.MethodGet, "/api/project/1/roles/"+string(created.ID), nil,
		map[string]string{"role_id": string(created.ID)}, controller.GetProjectRole,
	)
	assert.Equal(t, http.StatusOK, getResponse.Code)

	updateResponse := serveProjectRoleRequest(
		t, project, http.MethodPut, "/api/project/1/roles/"+string(created.ID),
		map[string]any{
			"id": created.ID, "name": "Resource operator",
			"permissions": created.Permissions | db.CanManageProjectResources,
			"revision":    created.Revision,
		},
		map[string]string{"role_id": string(created.ID)}, controller.UpdateProjectRole,
	)
	assert.Equal(t, http.StatusOK, updateResponse.Code)
	var updated db.Role
	require.NoError(t, json.Unmarshal(updateResponse.Body.Bytes(), &updated))
	assert.Equal(t, 2, updated.Revision)

	staleResponse := serveProjectRoleRequest(
		t, project, http.MethodPut, "/api/project/1/roles/"+string(created.ID),
		map[string]any{
			"id": created.ID, "name": "Stale overwrite",
			"permissions": created.Permissions, "revision": created.Revision,
		},
		map[string]string{"role_id": string(created.ID)}, controller.UpdateProjectRole,
	)
	assert.Equal(t, http.StatusConflict, staleResponse.Code)

	crossProjectResponse := serveProjectRoleRequest(
		t, otherProject, http.MethodGet, "/api/project/2/roles/"+string(created.ID), nil,
		map[string]string{"role_id": string(created.ID)}, controller.GetProjectRole,
	)
	assert.Equal(t, http.StatusNotFound, crossProjectResponse.Code)

	deleteResponse := serveProjectRoleRequest(
		t, project, http.MethodDelete,
		"/api/project/1/roles/"+string(created.ID)+"?revision="+strconv.Itoa(updated.Revision), nil,
		map[string]string{"role_id": string(created.ID)}, controller.DeleteProjectRole,
	)
	assert.Equal(t, http.StatusNoContent, deleteResponse.Code)
}

func TestProjectRoleControllerFailsClosedWhenCapabilityIsUnavailable(t *testing.T) {
	controller := NewRolesController(nil, deniedProjectRoleCapabilityProvider{})
	response := serveProjectRoleRequest(
		t,
		db.Project{ID: 1},
		http.MethodGet,
		"/api/project/1/roles/permissions",
		nil,
		nil,
		controller.GetProjectPermissionCatalog,
	)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func serveProjectRoleRequest(
	t *testing.T,
	project db.Project,
	method string,
	path string,
	body any,
	vars map[string]string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	request = mux.SetURLVars(request, vars)
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
