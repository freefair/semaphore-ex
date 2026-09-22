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

func TestTemplateCreateRejectsTaskGroupRunnerTagConflict(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "group runner conflict"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.test/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "runner-a", Tags: []string{"linux-a"},
		Active: true, Token: db.GenerateRunnerToken(),
	})
	require.NoError(t, err)
	group, err := store.CreateTaskGroup(db.TaskGroup{
		ProjectID: project.ID, Name: "runner-a-only", MaxParallelTasks: 1,
		RunnerIDs: db.TaskGroupBindings{runner.ID},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(
		`{"repository_id":`+strconv.Itoa(repository.ID)+`,"name":"conflicting","playbook":"site.yml","runner_tags":["linux-b"],"task_groups":[`+strconv.Itoa(group.ID)+`]}`))
	req = helpers.SetContextValue(req, "project", project)
	req = helpers.SetContextValue(req, "store", store)
	response := httptest.NewRecorder()

	addTemplate(response, req, nil)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "task group runner requirements have no common runner")
}
