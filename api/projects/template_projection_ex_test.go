package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateResponsesExposeRepositoryMonorepoForTemplateReaders(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, monorepoTemplate := createTemplatePermissionFixture(t, store)

	monorepo, err := store.GetRepository(project.ID, monorepoTemplate.RepositoryID)
	require.NoError(t, err)
	monorepo.Monorepo = true
	require.NoError(t, store.UpdateRepository(monorepo))

	plainRepository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID,
		Name:      "plain repository",
		GitURL:    "https://example.test/plain.git",
		GitBranch: "main",
		SSHKeyID:  monorepo.SSHKeyID,
	})
	require.NoError(t, err)
	plainTemplate, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: plainRepository.ID,
		Name: "Plain template", Playbook: "site.yml",
	})
	require.NoError(t, err)

	reader, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-projection-reader", Name: "Template projection reader",
		Email: "template-projection-reader@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: reader.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)
	for _, templateID := range []int{monorepoTemplate.ID, plainTemplate.ID} {
		_, err = store.CreateTemplateRole(db.TemplateRolePerm{
			ProjectID: project.ID, TemplateID: templateID, RoleSlug: string(db.ProjectGuest),
			AllowedPermissions: db.CanReadTemplate, Revision: 1,
		})
		require.NoError(t, err)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/project/1/templates", nil)
	listRequest = helpers.SetContextValue(listRequest, "store", store)
	listRequest = helpers.SetContextValue(listRequest, "project", project)
	listRequest = helpers.SetContextValue(listRequest, "user", &reader)
	listResponse := httptest.NewRecorder()
	GetTemplates(listResponse, listRequest)
	require.Equal(t, http.StatusOK, listResponse.Code)
	assertTemplateRepositoryMonorepoFlags(t, listResponse.Body.Bytes(), map[int]bool{
		monorepoTemplate.ID: true,
		plainTemplate.ID:    false,
	})

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/project/1/templates/1", nil)
	detailRequest = helpers.SetContextValue(detailRequest, "store", store)
	detailRequest = helpers.SetContextValue(detailRequest, "project", project)
	detailRequest = helpers.SetContextValue(detailRequest, "template", monorepoTemplate)
	detailRequest = helpers.SetContextValue(detailRequest, "user", &reader)
	detailRequest = helpers.SetContextValue(detailRequest, "permissions", db.ProjectGuest.GetPermissions())
	detailResponse := httptest.NewRecorder()
	GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(http.HandlerFunc(GetTemplate)).ServeHTTP(detailResponse, detailRequest)
	require.Equal(t, http.StatusOK, detailResponse.Code)
	assertTemplateRepositoryMonorepoFlags(t, detailResponse.Body.Bytes(), map[int]bool{
		monorepoTemplate.ID: true,
	})
}

func TestTemplateResponseDefaultsMissingLegacyRepositoryToFalse(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := createTemplatePermissionFixture(t, store)

	response, err := projectTemplateResponseFor(store, project.ID, db.TemplateWithPerms{
		Template: db.Template{ProjectID: project.ID, RepositoryID: 999999},
	})
	require.NoError(t, err)
	assert.False(t, response.RepositoryMonorepo)
}

func assertTemplateRepositoryMonorepoFlags(t *testing.T, body []byte, expected map[int]bool) {
	t.Helper()

	var responses []map[string]json.RawMessage
	if len(expected) == 1 {
		var response map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &response))
		responses = []map[string]json.RawMessage{response}
	} else {
		require.NoError(t, json.Unmarshal(body, &responses))
	}
	require.Len(t, responses, len(expected))
	for _, response := range responses {
		var id int
		require.NoError(t, json.Unmarshal(response["id"], &id))
		monorepo, present := response["repository_monorepo"]
		require.True(t, present, "template %d must explicitly serialize repository_monorepo", id)
		var actual bool
		require.NoError(t, json.Unmarshal(monorepo, &actual))
		assert.Equal(t, expected[id], actual, "template %d", id)
	}
}
