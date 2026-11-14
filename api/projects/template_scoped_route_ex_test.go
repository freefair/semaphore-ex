package projects

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedTemplateGrantAllowsListedTemplateDetailRoute(t *testing.T) {
	f := newTaskControlFixture(t)
	role, err := f.store.CreateProjectRole(db.Role{ID: "scoped-detail-role", Slug: "scoped-detail-role", Name: "Scoped detail", ProjectID: &f.project.ID, Revision: 1})
	require.NoError(t, err)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "scoped-detail", Name: "Scoped detail", Email: "scoped-detail@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	grant, err := f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: f.template.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate})
	require.NoError(t, err)
	router := mux.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = helpers.SetContextValue(r, "store", f.store)
			r = helpers.SetContextValue(r, "user", &user)
			next.ServeHTTP(w, r)
		})
	})
	member := router.PathPrefix("/api/project/{project_id}").Subrouter()
	member.Use(ProjectMiddleware)
	member.HandleFunc("/templates", GetTemplates).Methods(http.MethodGet)
	project := router.PathPrefix("/api/project/{project_id}").Subrouter()
	project.Use(ProjectMiddleware, GetMustCanMiddleware(db.CanManageProjectResources))
	templates := project.PathPrefix("/templates").Subrouter()
	templates.Use(TemplatesMiddleware)
	templates.Path("/{template_id}").Handler(GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(http.HandlerFunc(GetTemplate))).Methods(http.MethodGet)
	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/project/%d/templates", f.project.ID), nil))
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var returned []db.TemplateWithPerms
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &returned))
	require.Len(t, returned, 1)
	assert.Equal(t, f.template.ID, returned[0].ID)
	url := fmt.Sprintf("/api/project/%d/templates/%d", f.project.ID, f.template.ID)
	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	require.NoError(t, f.store.DeleteTemplateRole(f.project.ID, f.template.ID, grant.ID, grant.Revision))
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, url, nil))
	assert.Equal(t, http.StatusNotFound, denied.Code)
}

func TestTemplateGrantsDoNotAuthorizeProjectResourceMutations(t *testing.T) {
	f := newTaskControlFixture(t)
	role, err := f.store.CreateProjectRole(db.Role{
		ID: "template-only-role", Slug: "template-only-role", Name: "Template only",
		ProjectID: &f.project.ID, Revision: 1,
	})
	require.NoError(t, err)
	user, err := f.store.CreateUserWithoutPassword(db.User{
		Username: "template-only", Name: "Template only", Email: "template-only@example.invalid",
	})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{
		ProjectID: f.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1,
	})
	require.NoError(t, err)
	editGrant, err := f.store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: f.project.ID, TemplateID: f.template.ID, RoleID: &role.ID,
		AllowedPermissions: db.CanEditTemplate, Revision: 1,
	})
	require.NoError(t, err)
	otherTemplate, err := f.store.CreateTemplate(db.Template{
		ProjectID: f.project.ID, RepositoryID: f.template.RepositoryID,
		Name: "other template", Playbook: "site.yml",
	})
	require.NoError(t, err)

	router := mux.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = helpers.SetContextValue(r, "store", f.store)
			r = helpers.SetContextValue(r, "user", &user)
			next.ServeHTTP(w, r)
		})
	})
	templates := router.PathPrefix("/api/project/{project_id}/templates").Subrouter()
	templates.Use(ProjectMiddleware, TemplatesMiddleware)
	templates.Path("/{template_id}").Handler(
		GetMustHaveTemplatePermissionMiddleware(db.CanEditTemplate)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		)),
	).Methods(http.MethodPut)
	templates.Path("/{template_id}").Handler(
		GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		)),
	).Methods(http.MethodGet)

	resourceMutations := []struct {
		resource   string
		action     string
		method     string
		permission db.ProjectUserPermission
	}{
		{"schedules", "create", http.MethodPost, db.CanCreateSchedules},
		{"schedules", "update", http.MethodPut, db.CanUpdateSchedules},
		{"schedules", "delete", http.MethodDelete, db.CanDeleteSchedules},
		{"keys", "create", http.MethodPost, db.CanCreateAccessKeys},
		{"keys", "update", http.MethodPut, db.CanUpdateAccessKeys},
		{"keys", "delete", http.MethodDelete, db.CanDeleteAccessKeys},
		{"inventory", "create", http.MethodPost, db.CanCreateInventories},
		{"inventory", "update", http.MethodPut, db.CanUpdateInventories},
		{"inventory", "delete", http.MethodDelete, db.CanDeleteInventories},
		{"environment", "create", http.MethodPost, db.CanCreateEnvironments},
		{"environment", "update", http.MethodPut, db.CanUpdateEnvironments},
		{"environment", "delete", http.MethodDelete, db.CanDeleteEnvironments},
		{"repositories", "create", http.MethodPost, db.CanCreateRepositories},
		{"repositories", "update", http.MethodPut, db.CanUpdateRepositories},
		{"repositories", "delete", http.MethodDelete, db.CanDeleteRepositories},
		{"views", "create", http.MethodPost, db.CanCreateViews},
		{"views", "update", http.MethodPut, db.CanUpdateViews},
		{"views", "delete", http.MethodDelete, db.CanDeleteViews},
		{"integrations", "create", http.MethodPost, db.CanCreateIntegrations},
		{"integrations", "update", http.MethodPut, db.CanUpdateIntegrations},
		{"integrations", "delete", http.MethodDelete, db.CanDeleteIntegrations},
	}
	for _, mutation := range resourceMutations {
		mutation := mutation
		path := "/{template_id}/" + mutation.resource + "/" + mutation.action
		templates.Path(path).Handler(
			GetMustHavePermissionMiddleware(mutation.permission)(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
			)),
		).Methods(mutation.method)
	}

	serve := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		return response
	}

	assert.Equal(t, http.StatusNoContent, serve(
		http.MethodPut, fmt.Sprintf("/api/project/%d/templates/%d", f.project.ID, f.template.ID),
	).Code)
	assert.Equal(t, http.StatusForbidden, serve(
		http.MethodPut, fmt.Sprintf("/api/project/%d/templates/%d", f.project.ID, otherTemplate.ID),
	).Code)
	for _, mutation := range resourceMutations {
		response := serve(
			mutation.method,
			fmt.Sprintf("/api/project/%d/templates/%d/%s/%s", f.project.ID, f.template.ID, mutation.resource, mutation.action),
		)
		assert.Equal(t, http.StatusForbidden, response.Code, "%s %s", mutation.resource, mutation.action)
	}

	require.NoError(t, f.store.DeleteTemplateRole(f.project.ID, f.template.ID, editGrant.ID, editGrant.Revision))
	_, err = f.store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: f.project.ID, TemplateID: f.template.ID, RoleID: &role.ID,
		AllowedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, serve(
		http.MethodGet, fmt.Sprintf("/api/project/%d/templates/%d", f.project.ID, f.template.ID),
	).Code)
	assert.Equal(t, http.StatusNotFound, serve(
		http.MethodGet, fmt.Sprintf("/api/project/%d/templates/%d", f.project.ID, otherTemplate.ID),
	).Code)
	for _, mutation := range resourceMutations {
		response := serve(
			mutation.method,
			fmt.Sprintf("/api/project/%d/templates/%d/%s/%s", f.project.ID, f.template.ID, mutation.resource, mutation.action),
		)
		assert.Equal(t, http.StatusForbidden, response.Code, "read-only %s %s", mutation.resource, mutation.action)
	}
}
