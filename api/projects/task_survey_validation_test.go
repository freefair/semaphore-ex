package projects

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A user who may only run a template must not set variables the template's
// survey does not declare, because every answer becomes an extra var.
func TestAddTaskRejectsSurveyAnswersTheTemplateDoesNotAllow(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "survey validation"})
	require.NoError(t, err)
	repositoryKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repo", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: repositoryKey.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID:    project.ID,
		RepositoryID: repository.ID,
		Name:         "deploy",
		Playbook:     "site.yml",
		SurveyVars: []db.SurveyVar{
			{Name: "region", Title: "Region"},
			{Name: "token", Title: "Token", Type: db.SurveyVarSecret},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name        string
		environment string
		secret      string
	}{
		{"undeclared variable", `{"ansible_ssh_common_args":"-o ProxyCommand=sh"}`, ""},
		{"undeclared variable through the secret field", `{"region":"eu"}`, `{"ansible_become_pass":"x"}`},
		{"secret answer stored in clear", `{"token":"visible"}`, ""},
		{"environment that is not an object", `["region"]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := db.Task{TemplateID: template.ID, Environment: tt.environment, Secret: tt.secret}
			body := `{"template_id":` + strconv.Itoa(template.ID) + `}`
			req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
			req = helpers.SetContextValue(req, "project", project)
			req = helpers.SetContextValue(req, "user", &db.User{ID: 1})
			req = helpers.SetContextValue(req, "permissions", db.CanRunProjectTasks)
			req = helpers.SetContextValue(req, "store", store)
			req = helpers.SetContextValue(req, "task", task)
			w := httptest.NewRecorder()

			NewTaskController(store, nil).AddTask(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			tasks, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
			require.NoError(t, err)
			assert.Empty(t, tasks)
		})
	}
}
