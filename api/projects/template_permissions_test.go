package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplatePermissionAPIEnforcesDenyAllowInheritanceAndBoundedProvenance(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-permission-user", Name: "Template permission user",
		Email: "template-permission-user@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID,
		RoleSlug: string(db.ProjectGuest), AllowedPermissions: db.CanRunTemplate,
		DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)

	readReached := false
	readHandler := GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			readReached = true
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	readResponse := serveTemplatePermissionHandler(
		store, project, template, user, http.MethodGet, readHandler,
	)
	assert.Equal(t, http.StatusForbidden, readResponse.Code)
	assert.False(t, readReached)

	runReached := false
	runHandler := GetMustHaveTemplatePermissionMiddleware(db.CanRunTemplate)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			runReached = true
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	runResponse := serveTemplatePermissionHandler(
		store, project, template, user, http.MethodPost, runHandler,
	)
	assert.Equal(t, http.StatusNoContent, runResponse.Code)
	assert.True(t, runReached)

	detailHandler := GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(
		http.HandlerFunc(GetTemplate),
	)
	detailResponse := serveTemplatePermissionHandler(
		store, project, template, user, http.MethodGet, detailHandler,
	)
	assert.Equal(t, http.StatusForbidden, detailResponse.Code)
	assert.NotContains(t, detailResponse.Body.String(), template.Name)

	controller := NewTemplateController(store, store)
	effectiveRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/project/1/templates/1/permissions/effective",
		nil,
	)
	effectiveRequest = helpers.SetContextValue(effectiveRequest, "store", store)
	effectiveRequest = helpers.SetContextValue(effectiveRequest, "project", project)
	effectiveRequest = helpers.SetContextValue(effectiveRequest, "template", template)
	effectiveRequest = helpers.SetContextValue(effectiveRequest, "user", &user)
	effectiveResponse := httptest.NewRecorder()
	controller.GetEffectiveTemplatePermissions(effectiveResponse, effectiveRequest)
	assert.Equal(t, http.StatusOK, effectiveResponse.Code)
	var effective pro_interfaces.EffectiveTemplatePermissions
	require.NoError(t, json.Unmarshal(effectiveResponse.Body.Bytes(), &effective))
	assert.Equal(t, db.CanRunTemplate, effective.Permissions)
	assert.Len(t, effective.Decisions, 4)
	assert.False(t, effective.Decisions[0].Allowed)
	assert.Equal(t, pro_interfaces.PermissionScopeTemplate, effective.Decisions[0].Provenance.Scope)
	assert.Equal(t, string(db.ProjectGuest), effective.Decisions[0].Provenance.RoleID)
	assert.True(t, effective.Decisions[1].Allowed)
	assert.Equal(t, pro_interfaces.PermissionScopeTemplate, effective.Decisions[1].Provenance.Scope)
	for _, decision := range effective.Decisions {
		assert.NotContains(t, decision.Provenance.RoleName, "unrelated")
	}
}

func TestTemplatePermissionAPIUsesCASAndRejectsCrossScopeRole(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	controller := NewTemplateController(store, store)

	createResponse := serveTemplateRoleMutation(
		t, store, project, template, http.MethodPost, "/perms",
		map[string]any{
			"role_slug":           string(db.ProjectManager),
			"allowed_permissions": db.CanReadTemplate | db.CanRunTemplate,
			"denied_permissions":  db.CanDeleteTemplate,
		}, nil, controller.AddTemplatePerm,
	)
	assert.Equal(t, http.StatusCreated, createResponse.Code)
	var created db.TemplateRolePerm
	require.NoError(t, json.Unmarshal(createResponse.Body.Bytes(), &created))
	assert.Equal(t, 1, created.Revision)

	updateResponse := serveTemplateRoleMutation(
		t, store, project, template, http.MethodPut,
		"/perms/"+strconv.Itoa(created.ID),
		map[string]any{
			"role_slug": created.RoleSlug, "revision": created.Revision,
			"allowed_permissions": db.CanReadTemplate,
			"denied_permissions":  db.CanRunTemplate | db.CanDeleteTemplate,
		}, map[string]string{"perm_id": strconv.Itoa(created.ID)}, controller.UpdateTemplatePerm,
	)
	assert.Equal(t, http.StatusOK, updateResponse.Code)
	var updated db.TemplateRolePerm
	require.NoError(t, json.Unmarshal(updateResponse.Body.Bytes(), &updated))
	assert.Equal(t, created.Revision+1, updated.Revision)

	staleUpdate := serveTemplateRoleMutation(
		t, store, project, template, http.MethodPut,
		"/perms/"+strconv.Itoa(created.ID),
		map[string]any{
			"role_slug": created.RoleSlug, "revision": created.Revision,
			"allowed_permissions": db.CanRunTemplate,
		}, map[string]string{"perm_id": strconv.Itoa(created.ID)}, controller.UpdateTemplatePerm,
	)
	assert.Equal(t, http.StatusConflict, staleUpdate.Code)

	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "cross_scope_global", Slug: "cross_scope_global", Name: "Cross scope global",
		GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)
	crossScope := serveTemplateRoleMutation(
		t, store, project, template, http.MethodPost, "/perms",
		map[string]any{
			"role_id": globalRole.ID, "allowed_permissions": db.CanReadTemplate,
		}, nil, controller.AddTemplatePerm,
	)
	assert.Equal(t, http.StatusNotFound, crossScope.Code)

	staleDelete := serveTemplateRoleMutation(
		t, store, project, template, http.MethodDelete,
		"/perms/"+strconv.Itoa(created.ID)+"?revision=1", nil,
		map[string]string{"perm_id": strconv.Itoa(created.ID)}, controller.DeleteTemplatePerm,
	)
	assert.Equal(t, http.StatusConflict, staleDelete.Code)

	deleteResponse := serveTemplateRoleMutation(
		t, store, project, template, http.MethodDelete,
		"/perms/"+strconv.Itoa(created.ID)+"?revision="+strconv.Itoa(updated.Revision), nil,
		map[string]string{"perm_id": strconv.Itoa(created.ID)}, controller.DeleteTemplatePerm,
	)
	assert.Equal(t, http.StatusNoContent, deleteResponse.Code)
}

func TestTemplatePermissionGuardsRequireBaseACLAuthorityAndExactActions(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-delete-only", Name: "Template delete only",
		Email: "template-delete-only@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		AllowedPermissions: db.CanDeleteTemplate, Revision: 1,
	})
	require.NoError(t, err)

	for _, permission := range []db.TemplatePermission{db.CanRunTemplate, db.CanEditTemplate} {
		reached := false
		handler := GetMustHaveTemplatePermissionMiddleware(permission)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			},
		))
		response := serveTemplatePermissionHandler(
			store, project, template, user, http.MethodPost, handler,
		)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.False(t, reached)
	}

	aclReached := false
	aclHandler := GetMustHaveBaseProjectPermissionMiddleware(db.CanManageProjectResources)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			aclReached = true
			w.WriteHeader(http.StatusNoContent)
		},
	))
	aclRequest := httptest.NewRequest(http.MethodPut, "/api/project/1/templates/1/perms/1", nil)
	aclRequest = helpers.SetContextValue(aclRequest, "store", store)
	aclRequest = helpers.SetContextValue(aclRequest, "project", project)
	aclRequest = helpers.SetContextValue(aclRequest, "template", template)
	aclRequest = helpers.SetContextValue(aclRequest, "user", &user)
	aclRequest = helpers.SetContextValue(aclRequest, "basePermissions", db.ProjectGuest.GetPermissions())
	aclResponse := httptest.NewRecorder()
	aclHandler.ServeHTTP(aclResponse, aclRequest)
	assert.Equal(t, http.StatusForbidden, aclResponse.Code)
	assert.False(t, aclReached)

	managerRequest := helpers.SetContextValue(aclRequest, "basePermissions", db.CanManageProjectResources)
	managerResponse := httptest.NewRecorder()
	aclHandler.ServeHTTP(managerResponse, managerRequest)
	assert.Equal(t, http.StatusNoContent, managerResponse.Code)
	assert.True(t, aclReached)
}

func TestCommunityGuestTemplateListRetainsLegacyVisibility(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := createTemplatePermissionFixture(t, store)
	guest, err := store.CreateUserWithoutPassword(db.User{
		Username: "community-template-guest", Name: "Community template guest",
		Email: "community-template-guest@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: guest.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "/api/project/1/templates", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &guest)
	response := httptest.NewRecorder()
	GetTemplates(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	var templates []db.TemplateWithPerms
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &templates))
	assert.Len(t, templates, 1)
}

func TestRunOnlyTemplatePermissionBypassesBroadResourceGuardForTaskStop(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-run-only", Name: "Template run only",
		Email: "template-run-only@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		AllowedPermissions: db.CanRunTemplate, Revision: 1,
	})
	require.NoError(t, err)

	reached := false
	handler := ProjectMiddleware(GetMustCanMiddleware(db.CanManageProjectResources)(
		TemplatesMiddleware(GetMustHaveTemplatePermissionMiddleware(db.CanRunTemplate)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			}),
		)),
	))
	request := httptest.NewRequest(http.MethodPost, "/api/project/1/templates/1/stop_all_tasks", nil)
	request = mux.SetURLVars(request, map[string]string{
		"project_id": strconv.Itoa(project.ID), "template_id": strconv.Itoa(template.ID),
	})
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &user)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, reached)
}

func createTemplatePermissionFixture(
	t *testing.T,
	store *coresql.SqlDb,
) (db.Project, db.Template) {
	t.Helper()
	for _, role := range []db.ProjectUserRole{db.ProjectGuest, db.ProjectManager} {
		_, err := store.CreateRole(db.Role{
			ID: db.ProjectRoleID(role), Slug: string(role), Name: string(role),
			Permissions: role.GetPermissions(), Revision: 1,
		})
		require.NoError(t, err)
	}
	project, err := store.CreateProject(db.Project{Name: "Template permission project"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, Name: "Template permission repository",
		GitURL: "https://example.test/repository.git", GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID,
		Name: "Protected template", Playbook: "site.yml",
	})
	require.NoError(t, err)
	return project, template
}

func serveTemplatePermissionHandler(
	store db.Store,
	project db.Project,
	template db.Template,
	user db.User,
	method string,
	handler http.Handler,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/project/1/templates/1", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "template", template)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "permissions", db.CanViewProjectResources)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func serveTemplateRoleMutation(
	t *testing.T,
	store db.Store,
	project db.Project,
	template db.Template,
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
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "template", template)
	request = mux.SetURLVars(request, vars)
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
