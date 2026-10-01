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
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
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

func (deniedProjectRoleCapabilityProvider) GetRuntimeSecretsConfiguration(
	context.Context,
	pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilityConfiguration, error) {
	return pro_interfaces.CapabilityConfiguration{}, nil
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
	require.Len(t, catalog, 20)
	assert.Equal(t, pro_interfaces.PermissionViewWorkflow, catalog[5].ID)
	assert.Equal(t, pro_interfaces.PermissionAdministerWorkflow, catalog[9].ID)
	assert.Equal(t, pro_interfaces.PermissionManagePolicyGuardrails, catalog[13].ID)
	assert.Equal(t, pro_interfaces.PermissionRollbackPolicyGuardrails, catalog[14].ID)
	assert.Equal(t, pro_interfaces.PermissionReadTaskGroups, catalog[15].ID)
	assert.Equal(t, pro_interfaces.PermissionCreateTaskGroups, catalog[16].ID)
	assert.Equal(t, pro_interfaces.PermissionUpdateTaskGroups, catalog[17].ID)
	assert.Equal(t, pro_interfaces.PermissionDeleteTaskGroups, catalog[18].ID)
	assert.Equal(t, pro_interfaces.PermissionShareTaskGroups, catalog[19].ID)

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

func TestGlobalRoleControllerCRUDCatalogAssignmentsAndProvenance(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-role-api-admin", Name: "Global role API admin",
		Email: "global-role-api-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	target, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-role-api-target", Name: "Global role API target",
		Email: "global-role-api-target@example.test",
	})
	require.NoError(t, err)
	controller := NewRolesController(store, features.NewCapabilityProvider(store))

	catalogResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodGet, "/api/roles/permissions", nil, nil,
		controller.GetGlobalPermissionCatalog,
	)
	assert.Equal(t, http.StatusOK, catalogResponse.Code)
	var catalog []pro_interfaces.PermissionDefinition
	require.NoError(t, json.Unmarshal(catalogResponse.Body.Bytes(), &catalog))
	assert.Len(t, catalog, 9)
	assert.Equal(t, pro_interfaces.PermissionScopeGlobal, catalog[0].Scope)
	assert.Equal(t, pro_interfaces.PermissionManageGlobalPolicyGuardrails, catalog[7].ID)
	assert.Equal(t, pro_interfaces.PermissionRollbackGlobalPolicyGuardrails, catalog[8].ID)

	projectCatalogResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodGet, "/api/roles/project-permissions", nil, nil,
		controller.GetGlobalProjectPermissionCatalog,
	)
	assert.Equal(t, http.StatusOK, projectCatalogResponse.Code)
	var projectCatalog []pro_interfaces.PermissionDefinition
	require.NoError(t, json.Unmarshal(projectCatalogResponse.Body.Bytes(), &projectCatalog))
	require.NotEmpty(t, projectCatalog)
	assert.Equal(t, pro_interfaces.PermissionScopeProject, projectCatalog[0].Scope)

	createResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodPost, "/api/roles",
		map[string]any{
			"name":               "User auditor",
			"global_permissions": db.CanManageGlobalUsers | db.CanReadGlobalAudit,
		}, nil, controller.AddRole,
	)
	assert.Equal(t, http.StatusCreated, createResponse.Code)
	var created db.Role
	require.NoError(t, json.Unmarshal(createResponse.Body.Bytes(), &created))
	assert.NotEmpty(t, created.ID)
	assert.Nil(t, created.ProjectID)
	assert.Equal(t, 1, created.Revision)

	getResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodGet, "/api/roles/"+string(created.ID), nil,
		map[string]string{"role_id": string(created.ID)}, controller.GetGlobalRole,
	)
	assert.Equal(t, http.StatusOK, getResponse.Code)

	updateResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodPut, "/api/roles/"+string(created.ID),
		map[string]any{
			"id": created.ID, "name": "User manager and auditor",
			"global_permissions": created.GlobalPermissions, "revision": created.Revision,
		}, map[string]string{"role_id": string(created.ID)}, controller.UpdateRole,
	)
	assert.Equal(t, http.StatusOK, updateResponse.Code)
	var updated db.Role
	require.NoError(t, json.Unmarshal(updateResponse.Body.Bytes(), &updated))
	assert.Equal(t, 2, updated.Revision)

	staleUpdate := serveGlobalRoleRequest(
		t, store, admin, http.MethodPut, "/api/roles/"+string(created.ID),
		map[string]any{
			"id": created.ID, "name": "Stale overwrite",
			"global_permissions": created.GlobalPermissions, "revision": created.Revision,
		}, map[string]string{"role_id": string(created.ID)}, controller.UpdateRole,
	)
	assert.Equal(t, http.StatusConflict, staleUpdate.Code)

	assignmentResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodPost,
		"/api/users/"+strconv.Itoa(target.ID)+"/global-roles",
		map[string]any{"role_id": created.ID},
		map[string]string{"user_id": strconv.Itoa(target.ID)},
		controller.AddGlobalRoleAssignment,
	)
	assert.Equal(t, http.StatusCreated, assignmentResponse.Code)
	var assignment db.GlobalRoleAssignment
	require.NoError(t, json.Unmarshal(assignmentResponse.Body.Bytes(), &assignment))
	assert.Equal(t, target.ID, assignment.UserID)
	assert.Equal(t, created.ID, assignment.RoleID)

	listResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodGet,
		"/api/users/"+strconv.Itoa(target.ID)+"/global-roles", nil,
		map[string]string{"user_id": strconv.Itoa(target.ID)},
		controller.GetGlobalRoleAssignments,
	)
	assert.Equal(t, http.StatusOK, listResponse.Code)
	var assignments []db.GlobalRoleAssignment
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &assignments))
	assert.Len(t, assignments, 1)

	effectiveResponse := serveGlobalRoleRequest(
		t, store, admin, http.MethodGet,
		"/api/users/"+strconv.Itoa(target.ID)+"/global-permissions", nil,
		map[string]string{"user_id": strconv.Itoa(target.ID)},
		controller.GetEffectiveGlobalPermissions,
	)
	assert.Equal(t, http.StatusOK, effectiveResponse.Code)
	var effective pro_interfaces.EffectiveGlobalPermissions
	require.NoError(t, json.Unmarshal(effectiveResponse.Body.Bytes(), &effective))
	assert.True(t, effective.Permissions.Can(db.CanManageGlobalUsers))
	assert.Len(t, effective.Decisions, 9)
	assert.True(t, effective.Decisions[0].Allowed)
	assert.Equal(t, string(created.ID), effective.Decisions[0].Provenance.RoleID)
	assert.Empty(t, effective.Decisions[2].Provenance.RoleID)

	staleDelete := serveGlobalRoleRequest(
		t, store, admin, http.MethodDelete,
		"/api/users/"+strconv.Itoa(target.ID)+"/global-roles/"+strconv.Itoa(assignment.ID)+"?revision=2",
		nil, map[string]string{
			"user_id": strconv.Itoa(target.ID), "assignment_id": strconv.Itoa(assignment.ID),
		}, controller.DeleteGlobalRoleAssignment,
	)
	assert.Equal(t, http.StatusConflict, staleDelete.Code)

	deleteAssignment := serveGlobalRoleRequest(
		t, store, admin, http.MethodDelete,
		"/api/users/"+strconv.Itoa(target.ID)+"/global-roles/"+strconv.Itoa(assignment.ID)+"?revision=1",
		nil, map[string]string{
			"user_id": strconv.Itoa(target.ID), "assignment_id": strconv.Itoa(assignment.ID),
		}, controller.DeleteGlobalRoleAssignment,
	)
	assert.Equal(t, http.StatusNoContent, deleteAssignment.Code)

	deleteRole := serveGlobalRoleRequest(
		t, store, admin, http.MethodDelete,
		"/api/roles/"+string(created.ID)+"?revision="+strconv.Itoa(updated.Revision), nil,
		map[string]string{"role_id": string(created.ID)}, controller.DeleteRole,
	)
	assert.Equal(t, http.StatusNoContent, deleteRole.Code)
}

func TestGlobalRoleControllerFailsClosedWhenCapabilityIsUnavailable(t *testing.T) {
	controller := NewRolesController(nil, deniedProjectRoleCapabilityProvider{})
	response := serveGlobalRoleRequest(
		t, nil, db.User{ID: 1}, http.MethodGet, "/api/roles/permissions", nil, nil,
		controller.GetGlobalPermissionCatalog,
	)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = serveGlobalRoleRequest(
		t, nil, db.User{ID: 1}, http.MethodGet, "/api/roles/project-permissions", nil, nil,
		controller.GetGlobalProjectPermissionCatalog,
	)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestRoleControllerRecordsAuditOnlyForSuccessfulStoredMutations(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Audited role API project"})
	require.NoError(t, err)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "audited-role-admin", Name: "Audited role administrator",
		Email: "audited-role-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	controller := NewRolesController(store, features.NewCapabilityProvider(store))

	globalCreate, globalCreateAudit := serveGlobalRoleAuditRequest(
		t, store, admin, http.MethodPost, "/api/roles",
		map[string]any{
			"name":               "Global auditor",
			"permissions":        db.CanViewWorkflows,
			"global_permissions": db.CanManageGlobalUsers | db.CanReadGlobalAudit,
		}, nil, controller.AddRole,
	)
	assert.Equal(t, http.StatusCreated, globalCreate.Code)
	var globalRole db.Role
	require.NoError(t, json.Unmarshal(globalCreate.Body.Bytes(), &globalRole))
	globalCreated := onlyRoleAuditEvent(t, globalCreateAudit, audit.IAMRoleCreate)
	assert.Equal(t, &audit.Target{Type: audit.TargetRole, ID: string(globalRole.ID), Name: globalRole.Name}, globalCreated.Event.Target)
	assert.Equal(t, audit.RoleMetadata{Permissions: []string{
		"workflow.view", "global.users.manage", "global.audit.read",
	}}, globalCreated.Event.Metadata)

	globalUpdate, globalUpdateAudit := serveGlobalRoleAuditRequest(
		t, store, admin, http.MethodPut, "/api/roles/"+string(globalRole.ID),
		map[string]any{
			"id": globalRole.ID, "name": "Updated global auditor",
			"permissions":        db.CanViewWorkflows | db.CanStartWorkflows,
			"global_permissions": db.CanManageGlobalRoles,
			"revision":           globalRole.Revision,
		}, map[string]string{"role_id": string(globalRole.ID)}, controller.UpdateRole,
	)
	assert.Equal(t, http.StatusOK, globalUpdate.Code)
	var updatedGlobalRole db.Role
	require.NoError(t, json.Unmarshal(globalUpdate.Body.Bytes(), &updatedGlobalRole))
	globalUpdated := onlyRoleAuditEvent(t, globalUpdateAudit, audit.IAMRoleUpdate)
	assert.Equal(t, &audit.Target{Type: audit.TargetRole, ID: string(updatedGlobalRole.ID), Name: updatedGlobalRole.Name}, globalUpdated.Event.Target)
	assert.Equal(t, audit.RoleMetadata{Permissions: []string{
		"workflow.view", "workflow.start", "global.roles.manage",
	}}, globalUpdated.Event.Metadata)

	staleGlobal, staleGlobalAudit := serveGlobalRoleAuditRequest(
		t, store, admin, http.MethodPut, "/api/roles/"+string(globalRole.ID),
		map[string]any{"id": globalRole.ID, "name": "Stale", "revision": globalRole.Revision},
		map[string]string{"role_id": string(globalRole.ID)}, controller.UpdateRole,
	)
	assert.Equal(t, http.StatusConflict, staleGlobal.Code)
	assert.Empty(t, staleGlobalAudit.All())

	globalDelete, globalDeleteAudit := serveGlobalRoleAuditRequest(
		t, store, admin, http.MethodDelete,
		"/api/roles/"+string(updatedGlobalRole.ID)+"?revision="+strconv.Itoa(updatedGlobalRole.Revision),
		nil, map[string]string{"role_id": string(updatedGlobalRole.ID)}, controller.DeleteRole,
	)
	assert.Equal(t, http.StatusNoContent, globalDelete.Code)
	globalDeleted := onlyRoleAuditEvent(t, globalDeleteAudit, audit.IAMRoleDelete)
	assert.Equal(t, &audit.Target{Type: audit.TargetRole, ID: string(updatedGlobalRole.ID), Name: updatedGlobalRole.Name}, globalDeleted.Event.Target)

	projectCreate, projectCreateAudit := serveProjectRoleAuditRequest(
		t, project, http.MethodPost, "/api/project/"+strconv.Itoa(project.ID)+"/roles",
		map[string]any{"name": "Project viewer", "permissions": db.CanViewWorkflows}, nil,
		controller.AddProjectRole,
	)
	assert.Equal(t, http.StatusCreated, projectCreate.Code)
	var projectRole db.Role
	require.NoError(t, json.Unmarshal(projectCreate.Body.Bytes(), &projectRole))
	projectCreated := onlyRoleAuditEvent(t, projectCreateAudit, audit.IAMProjectRoleDefinitionCreate)
	assert.Equal(t, &audit.Target{Type: audit.TargetProjectRoleDefinition, ID: string(projectRole.ID), Name: projectRole.Name}, projectCreated.Event.Target)
	assert.Equal(t, project.ID, projectCreated.Event.ProjectID)
	assert.Equal(t, audit.RoleMetadata{Permissions: []string{"workflow.view"}}, projectCreated.Event.Metadata)

	projectUpdate, projectUpdateAudit := serveProjectRoleAuditRequest(
		t, project, http.MethodPut, "/api/project/"+strconv.Itoa(project.ID)+"/roles/"+string(projectRole.ID),
		map[string]any{
			"id": projectRole.ID, "name": "Project operator",
			"permissions": db.CanViewWorkflows | db.CanStartWorkflows, "revision": projectRole.Revision,
		}, map[string]string{"role_id": string(projectRole.ID)}, controller.UpdateProjectRole,
	)
	assert.Equal(t, http.StatusOK, projectUpdate.Code)
	var updatedProjectRole db.Role
	require.NoError(t, json.Unmarshal(projectUpdate.Body.Bytes(), &updatedProjectRole))
	projectUpdated := onlyRoleAuditEvent(t, projectUpdateAudit, audit.IAMProjectRoleDefinitionUpdate)
	assert.Equal(t, &audit.Target{Type: audit.TargetProjectRoleDefinition, ID: string(updatedProjectRole.ID), Name: updatedProjectRole.Name}, projectUpdated.Event.Target)
	assert.Equal(t, project.ID, projectUpdated.Event.ProjectID)
	assert.Equal(t, audit.RoleMetadata{Permissions: []string{"workflow.view", "workflow.start"}}, projectUpdated.Event.Metadata)

	missingProject, missingProjectAudit := serveProjectRoleAuditRequest(
		t, project, http.MethodDelete, "/api/project/"+strconv.Itoa(project.ID)+"/roles/missing?revision=1",
		nil, map[string]string{"role_id": "missing"}, controller.DeleteProjectRole,
	)
	assert.Equal(t, http.StatusNotFound, missingProject.Code)
	assert.Empty(t, missingProjectAudit.All())

	projectDelete, projectDeleteAudit := serveProjectRoleAuditRequest(
		t, project, http.MethodDelete,
		"/api/project/"+strconv.Itoa(project.ID)+"/roles/"+string(updatedProjectRole.ID)+"?revision="+strconv.Itoa(updatedProjectRole.Revision),
		nil, map[string]string{"role_id": string(updatedProjectRole.ID)}, controller.DeleteProjectRole,
	)
	assert.Equal(t, http.StatusNoContent, projectDelete.Code)
	projectDeleted := onlyRoleAuditEvent(t, projectDeleteAudit, audit.IAMProjectRoleDefinitionDelete)
	assert.Equal(t, &audit.Target{Type: audit.TargetProjectRoleDefinition, ID: string(updatedProjectRole.ID), Name: updatedProjectRole.Name}, projectDeleted.Event.Target)
	assert.Equal(t, project.ID, projectDeleted.Event.ProjectID)

	deniedController := NewRolesController(store, deniedProjectRoleCapabilityProvider{})
	denied, deniedAudit := serveProjectRoleAuditRequest(
		t, project, http.MethodPost, "/api/project/"+strconv.Itoa(project.ID)+"/roles",
		map[string]any{"name": "Denied", "permissions": db.CanViewWorkflows}, nil, deniedController.AddProjectRole,
	)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Empty(t, deniedAudit.All())
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

func serveProjectRoleAuditRequest(
	t *testing.T,
	project db.Project,
	method string,
	path string,
	body any,
	vars map[string]string,
	handler http.HandlerFunc,
) (*httptest.ResponseRecorder, *audittest.Recorder) {
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
	recorder := &audittest.Recorder{}
	request = helpers.SetContextValue(request, "audit", recorder)
	response := httptest.NewRecorder()
	handler(response, request)
	return response, recorder
}

func serveGlobalRoleRequest(
	t *testing.T,
	store db.Store,
	user db.User,
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
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &user)
	request = mux.SetURLVars(request, vars)
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

func serveGlobalRoleAuditRequest(
	t *testing.T,
	store db.Store,
	user db.User,
	method string,
	path string,
	body any,
	vars map[string]string,
	handler http.HandlerFunc,
) (*httptest.ResponseRecorder, *audittest.Recorder) {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &user)
	request = mux.SetURLVars(request, vars)
	recorder := &audittest.Recorder{}
	request = helpers.SetContextValue(request, "audit", recorder)
	response := httptest.NewRecorder()
	handler(response, request)
	return response, recorder
}

func onlyRoleAuditEvent(t *testing.T, recorder *audittest.Recorder, kind audit.Kind) audittest.Recorded {
	t.Helper()
	recorded, err := recorder.Only(kind)
	require.NoError(t, err)
	return recorded
}
