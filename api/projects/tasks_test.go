package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTasksPageParams(t *testing.T) {
	tests := []struct {
		name             string
		query            string
		expectedPageSize int
		expectedCount    int // params.Count == pageSize + 1
		expectedBeforeID int
	}{
		{"defaults", "", maxTasksPageSize, maxTasksPageSize + 1, 0},
		{"count and before", "count=20&before=100", 20, 21, 100},
		{"legacy limit", "limit=50", 50, 51, 0},
		{"count overrides limit", "count=10&limit=50", 10, 11, 0},
		{"page size capped at max", "count=10000", maxTasksPageSize, maxTasksPageSize + 1, 0},
		{"negative count ignored", "count=-5", maxTasksPageSize, maxTasksPageSize + 1, 0},
		{"zero count ignored", "count=0", maxTasksPageSize, maxTasksPageSize + 1, 0},
		{"invalid count ignored", "count=abc", maxTasksPageSize, maxTasksPageSize + 1, 0},
		{"negative before ignored", "count=20&before=-1", 20, 21, 0},
		{"invalid before ignored", "count=20&before=xyz", 20, 21, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, err := url.ParseQuery(tt.query)
			assert.NoError(t, err)

			params, pageSize := parseTasksPageParams(query, db.RetrieveQueryParams{})

			assert.Equal(t, tt.expectedPageSize, pageSize)
			assert.Equal(t, tt.expectedCount, params.Count)
			assert.Equal(t, tt.expectedBeforeID, params.BeforeID)
		})
	}
}

func TestParseTasksPageParams_PreservesBase(t *testing.T) {
	base := db.RetrieveQueryParams{SortBy: "id", SortInverted: true}

	params, pageSize := parseTasksPageParams(url.Values{}, base)

	assert.Equal(t, "id", params.SortBy)
	assert.True(t, params.SortInverted)
	assert.Equal(t, maxTasksPageSize, pageSize)
	assert.Equal(t, maxTasksPageSize+1, params.Count)
}

func TestGetTaskRunnerAttemptsIsScopedToContextTask(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "attempt history"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.com/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "template", Playbook: "site.yml",
	})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "runner", Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskStartingStatus,
	}, 0)
	require.NoError(t, err)
	assigned, ok, err := store.AssignTaskRunner(project.ID, task.ID, runner.ID, runner.Name, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok)

	request := httptest.NewRequest(http.MethodGet, "/api/project/1/tasks/1/runner-attempts", nil)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "task", assigned)
	response := httptest.NewRecorder()

	NewTaskController(store, nil).GetTaskRunnerAttempts(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var attempts []db.RunnerAttempt
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &attempts))
	require.Len(t, attempts, 1)
	assert.Equal(t, assigned.ID, attempts[0].TaskID)
	assert.Equal(t, 1, attempts[0].Generation)
	assert.Equal(t, runner.ID, attempts[0].RunnerID)
	assert.Equal(t, db.RunnerAttemptActive, attempts[0].Outcome)
}
