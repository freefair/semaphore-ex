package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type projectRolePermissionStore struct {
	db.Store
	projectUser db.ProjectUser
	project     db.Project
	role        db.Role
}

func (s projectRolePermissionStore) GetProjectUser(projectID, userID int) (db.ProjectUser, error) {
	if projectID != s.projectUser.ProjectID || userID != s.projectUser.UserID {
		return db.ProjectUser{}, db.ErrNotFound
	}
	return s.projectUser, nil
}

func (s projectRolePermissionStore) GetProject(projectID int) (db.Project, error) {
	if projectID != s.project.ID {
		return db.Project{}, db.ErrNotFound
	}
	return s.project, nil
}

func (s projectRolePermissionStore) GetProjectRoleByID(projectID int, roleID db.ProjectRoleID) (db.Role, error) {
	if s.role.ProjectID == nil || projectID != *s.role.ProjectID || roleID != s.role.ID {
		return db.Role{}, db.ErrNotFound
	}
	return s.role, nil
}

func TestProjectMiddlewareResolvesCustomRoleByScopedImmutableID(t *testing.T) {
	projectID := 12
	roleID := db.ProjectRoleID("role_resource_viewer")
	store := projectRolePermissionStore{
		projectUser: db.ProjectUser{ProjectID: projectID, UserID: 34, RoleID: &roleID},
		project:     db.Project{ID: projectID, Name: "Scoped role project"},
		role: db.Role{
			ID: roleID, ProjectID: &projectID, Name: "Resource viewer",
			Permissions: db.CanViewProjectResources, Revision: 1,
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/project/12/repositories", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": "12"})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 34})
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()
	called := false

	ProjectMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, db.CanViewProjectResources, helpers.GetFromContext(r, "permissions"))
		assert.Equal(t, db.ProjectUserRole(roleID), helpers.GetFromContext(r, "projectUserRole"))
		assert.Equal(t, "Resource viewer", helpers.GetFromContext(r, "projectUserRoleName"))
	})).ServeHTTP(recorder, request)

	require.True(t, called)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestGetUserRoleIncludesCustomRoleDisplayName(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/project/12/role", nil)
	request = helpers.SetContextValue(request, "projectUserRole", db.ProjectUserRole("role_resource_viewer"))
	request = helpers.SetContextValue(request, "projectUserRoleName", "Resource viewer")
	request = helpers.SetContextValue(request, "permissions", db.CanViewProjectResources)
	response := httptest.NewRecorder()

	GetUserRole(response, request)

	assert.JSONEq(t, `{
		"role":"role_resource_viewer",
		"role_name":"Resource viewer",
		"permissions":16
	}`, response.Body.String())
}

func TestMustHavePermissionMiddlewareEnforcesReadAndWrite(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method+" denied", func(t *testing.T) {
			request := httptest.NewRequest(method, "/api/project/1/repositories", nil)
			request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
			request = helpers.SetContextValue(request, "permissions", db.ProjectUserPermission(0))
			recorder := httptest.NewRecorder()
			called := false
			GetMustHavePermissionMiddleware(db.CanViewProjectResources)(http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) { called = true },
			)).ServeHTTP(recorder, request)
			assert.False(t, called)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/api/project/1/repositories", nil)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	request = helpers.SetContextValue(request, "permissions", db.CanViewProjectResources)
	recorder := httptest.NewRecorder()
	called := false
	GetMustHavePermissionMiddleware(db.CanViewProjectResources)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { called = true },
	)).ServeHTTP(recorder, request)
	assert.True(t, called)
}

func TestRepositoryPermissionStackAllowsViewerListsAndDeniesMutations(t *testing.T) {
	projectID := 12
	roleID := db.ProjectRoleID("role_resource_viewer")
	store := projectRolePermissionStore{
		projectUser: db.ProjectUser{ProjectID: projectID, UserID: 34, RoleID: &roleID},
		project:     db.Project{ID: projectID, Name: "Scoped role project"},
		role: db.Role{
			ID: roleID, ProjectID: &projectID, Name: "Resource viewer",
			Permissions: db.CanViewProjectResources, Revision: 1,
		},
	}
	router := mux.NewRouter()
	project := router.PathPrefix("/api/project/{project_id}").Subrouter()
	project.Use(ProjectMiddleware, GetMustCanMiddleware(db.CanManageProjectResources))
	repositories := project.PathPrefix("/repositories").Subrouter()
	repositories.Use(GetMustHavePermissionMiddleware(db.CanViewProjectResources))
	repositories.Path("").HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)
	repositories.Path("").HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}).Methods(http.MethodPost)

	serve := func(method string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/project/12/repositories", nil)
		request = helpers.SetContextValue(request, "user", &db.User{ID: 34})
		request = helpers.SetContextValue(request, "store", store)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	assert.Equal(t, http.StatusOK, serve(http.MethodGet).Code)
	assert.Equal(t, http.StatusForbidden, serve(http.MethodPost).Code)

	store.role.Permissions = 0
	request := httptest.NewRequest(http.MethodGet, "/api/project/12/repositories", nil)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 34})
	request = helpers.SetContextValue(request, "store", store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
}
