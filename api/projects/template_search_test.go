package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateSearchAPIUsesLiteralSearchForBaseAndViewLists(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, template := createTemplatePermissionFixture(t, store)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-search-admin", Name: "Template Search Admin",
		Email: "template-search-admin@example.test", Admin: true,
	})
	require.NoError(t, err)

	view, err := store.CreateView(db.View{ProjectID: project.ID, Title: "Search view"})
	require.NoError(t, err)
	template.Name = "exact-%_\\\x1f-literal"
	template.ViewID = &view.ID
	require.NoError(t, store.UpdateTemplate(template))

	for _, testCase := range []struct {
		name    string
		handler http.HandlerFunc
		view    bool
	}{
		{name: "base", handler: GetTemplates},
		{name: "view", handler: GetViewTemplates, view: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/project/1/templates?search=%25_%5C%1F", nil)
			request = helpers.SetContextValue(request, "store", store)
			request = helpers.SetContextValue(request, "project", project)
			request = helpers.SetContextValue(request, "user", &admin)
			if testCase.view {
				request = helpers.SetContextValue(request, "view", view)
			}
			response := httptest.NewRecorder()
			testCase.handler(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			var items []db.TemplateWithPerms
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &items))
			require.Len(t, items, 1)
			assert.Equal(t, template.ID, items[0].ID)
		})
	}
}

func TestTemplateSearchAPIRejectsLongAndInvalidPagingInput(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := createTemplatePermissionFixture(t, store)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-search-validation-admin", Name: "Template Search Validation Admin",
		Email: "template-search-validation-admin@example.test", Admin: true,
	})
	require.NoError(t, err)

	for _, path := range []string{
		"/api/project/1/templates?search=" + strings.Repeat("x", db.MaxTemplateSearchLength+1),
		"/api/project/1/templates?search=first&search=second",
		"/api/project/1/templates?count=not-a-number",
		"/api/project/1/templates?count=0",
		"/api/project/1/templates?count=201",
		"/api/project/1/templates?offset=0",
		"/api/project/1/templates?offset=1",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "project", project)
		request = helpers.SetContextValue(request, "user", &admin)
		response := httptest.NewRecorder()
		GetTemplates(response, request)
		assert.Equal(t, http.StatusBadRequest, response.Code, path)
	}
}

func TestTemplateSearchAPIExcludesHiddenMatchesBeforePagination(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, visible := createTemplatePermissionFixture(t, store)
	visible.Name = "a-visible-needle"
	require.NoError(t, store.UpdateTemplate(visible))
	hidden, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: visible.RepositoryID,
		Name: "b-hidden-needle", Playbook: "site.yml",
	})
	require.NoError(t, err)
	guest, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-search-api-guest", Name: "Template Search API Guest",
		Email: "template-search-api-guest@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: guest.ID, Role: db.ProjectGuest})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: hidden.ID, RoleSlug: string(db.ProjectGuest),
		DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)

	for _, testCase := range []struct {
		path    string
		wantIDs []int
	}{
		{path: "/api/project/1/templates?search=needle&count=1", wantIDs: []int{visible.ID}},
		{path: "/api/project/1/templates?search=needle&count=1&offset=1", wantIDs: []int{}},
		{path: "/api/project/1/templates?search=&count=1", wantIDs: []int{visible.ID}},
	} {
		request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "project", project)
		request = helpers.SetContextValue(request, "user", &guest)
		response := httptest.NewRecorder()
		GetTemplates(response, request)
		require.Equal(t, http.StatusOK, response.Code, testCase.path)
		var items []db.TemplateWithPerms
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &items))
		ids := make([]int, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		assert.Equal(t, testCase.wantIDs, ids, testCase.path)
	}
}
