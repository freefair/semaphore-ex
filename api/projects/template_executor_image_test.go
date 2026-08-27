package projects

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executorImageLogWriter struct{}

func (executorImageLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (executorImageLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (executorImageLogWriter) WriteResult(any) error                             { return nil }

func executorImageAPIContext(
	t *testing.T,
	store *sql.SqlDb,
	method string,
	body string,
) (*http.Request, db.Project, db.User, db.Repository) {
	t.Helper()
	project, err := store.CreateProject(db.Project{Name: "executor image api"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "image-api", Name: "Image API", Email: "image-api@example.invalid",
	})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.com/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	request := httptest.NewRequest(method, "/api/project/1/templates", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "log_writer", executorImageLogWriter{})
	return request, project, user, repository
}

func TestTemplateExecutorImageAPIValidationAndCapability(t *testing.T) {
	for _, tt := range []struct {
		name       string
		image      string
		available  bool
		statusCode int
	}{
		{name: "valid", image: " registry.example.com/team/job:v1 ", available: true, statusCode: http.StatusCreated},
		{name: "invalid", image: "https://user:secret@registry.example.com/job:v1", available: true, statusCode: http.StatusBadRequest},
		{name: "disabled", image: "registry.example.com/team/job:v1", statusCode: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := sql.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			request, project, _, repository := executorImageAPIContext(t, store, http.MethodPost, "")
			payload, err := json.Marshal(db.Template{
				ProjectID: project.ID, RepositoryID: repository.ID,
				Name: "image", Playbook: "site.yml", ExecutorImage: &tt.image,
			})
			require.NoError(t, err)
			request.Body = io.NopCloser(bytes.NewReader(payload))
			request.ContentLength = int64(len(payload))
			response := httptest.NewRecorder()
			controller := NewTemplateController(store, store, func(*db.User) bool { return tt.available })

			controller.AddTemplate(response, request)

			assert.Equal(t, tt.statusCode, response.Code, response.Body.String())
			if tt.statusCode == http.StatusCreated {
				var created db.Template
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
				require.NotNil(t, created.ExecutorImage)
				assert.Equal(t, "registry.example.com/team/job:v1", *created.ExecutorImage)
			}
		})
	}
}

func TestDirectTaskStartRejectsIncompatibleExecutorBeforeEnqueue(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	request, project, user, repository := executorImageAPIContext(t, store, http.MethodPost, "")
	image := "registry.example.com/team/job:v1"
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID,
		Name: "image", Playbook: "site.yml", ExecutorImage: &image,
	})
	require.NoError(t, err)
	_, err = store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "local", IsDefault: true,
		Token: db.GenerateRunnerToken(), Active: true, ExecutorType: db.RunnerExecutorLocal,
	})
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	pool.SetExecutorImageCapabilityResolver(func(*db.User) bool { return true })
	request = helpers.SetContextValue(request, "task", db.Task{TemplateID: template.ID})
	request = helpers.SetContextValue(request, "task_pool", &pool)
	request = helpers.SetContextValue(request, "user", &user)
	response := httptest.NewRecorder()

	NewTaskController(store, nil).AddTask(response, request)

	assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	stored, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}
