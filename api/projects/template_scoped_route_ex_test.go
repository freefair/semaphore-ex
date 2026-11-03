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
