package projects

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskSummaryRepositoryStub struct {
	summary     db.TaskSummary
	summaryErr  error
	hosts       db.TaskSummaryPage[db.TaskSummaryHost]
	stages      db.TaskSummaryPage[db.TaskSummaryStage]
	errors      db.TaskSummaryPage[db.TaskSummaryError]
	lastParams  db.RetrieveQueryParams
	repairCalls int
}

func (*taskSummaryRepositoryStub) CreateAnsibleTaskHost(db.AnsibleTaskHost) error   { return nil }
func (*taskSummaryRepositoryStub) CreateAnsibleTaskError(db.AnsibleTaskError) error { return nil }
func (*taskSummaryRepositoryStub) GetAnsibleTaskHosts(int, int) ([]db.AnsibleTaskHost, error) {
	return nil, nil
}
func (*taskSummaryRepositoryStub) GetAnsibleTaskErrors(int, int) ([]db.AnsibleTaskError, error) {
	return nil, nil
}
func (*taskSummaryRepositoryStub) IngestTaskSummaryEvent(int, int, db.TaskSummaryEvent, time.Time) error {
	return nil
}
func (*taskSummaryRepositoryStub) FinalizeTaskSummary(int, int, task_logger.TaskStatus, *time.Time, *time.Time) error {
	return nil
}
func (s *taskSummaryRepositoryStub) RepairTaskSummary(int, int, task_logger.TaskStatus) error {
	s.repairCalls++
	if errors.Is(s.summaryErr, db.ErrNotFound) {
		s.summaryErr = nil
		s.summary = db.TaskSummary{Version: 1, State: db.TaskSummaryEmpty}
	}
	return nil
}
func (s *taskSummaryRepositoryStub) GetTaskSummary(int, int) (db.TaskSummary, error) {
	return s.summary, s.summaryErr
}
func (s *taskSummaryRepositoryStub) GetTaskSummaryHosts(_ int, _ int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryHost], error) {
	s.lastParams = params
	return s.hosts, nil
}
func (s *taskSummaryRepositoryStub) GetTaskSummaryStages(_ int, _ int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryStage], error) {
	s.lastParams = params
	return s.stages, nil
}
func (s *taskSummaryRepositoryStub) GetTaskSummaryErrors(_ int, _ int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryError], error) {
	s.lastParams = params
	return s.errors, nil
}

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

func TestParseTaskSummaryPageParamsBoundsCursor(t *testing.T) {
	tests := []struct {
		query  string
		count  int
		before int
	}{
		{"", defaultTaskSummaryPageSize, 0},
		{"count=10&before=42", 10, 42},
		{"count=9999&before=-1", maxTaskSummaryPageSize, 0},
		{"count=invalid&before=invalid", defaultTaskSummaryPageSize, 0},
	}
	for _, test := range tests {
		query, err := url.ParseQuery(test.query)
		require.NoError(t, err)
		params := parseTaskSummaryPageParams(query)
		assert.Equal(t, test.count, params.Count)
		assert.Equal(t, test.before, params.BeforeID)
	}
}

func TestTaskSummaryAPIStatesAndRepairContract(t *testing.T) {
	states := []db.TaskSummaryState{
		db.TaskSummaryComplete,
		db.TaskSummaryPartial,
		db.TaskSummaryEmpty,
		db.TaskSummaryUnsupported,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			repository := &taskSummaryRepositoryStub{summary: db.TaskSummary{
				Version: 1, ProjectID: 7, TaskID: 11, State: state,
				TotalHosts: 3, OkHosts: 2, FailedHosts: 1,
			}}
			request := httptest.NewRequest(http.MethodGet, "/api/project/7/tasks/11/ansible/summary", nil)
			request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
			request = helpers.SetContextValue(request, "task", db.Task{
				ID: 11, ProjectID: 7, Status: task_logger.TaskSuccessStatus,
			})
			response := httptest.NewRecorder()

			NewTaskController(nil, repository).GetTaskSummary(response, request)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body db.TaskSummary
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, state, body.State)
			if state == db.TaskSummaryPartial {
				assert.Equal(t, 1, repository.repairCalls)
			} else {
				assert.Zero(t, repository.repairCalls)
			}
		})
	}

	repository := &taskSummaryRepositoryStub{summaryErr: db.ErrNotFound}
	request := httptest.NewRequest(http.MethodGet, "/api/project/7/tasks/12/ansible/summary", nil)
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "task", db.Task{
		ID: 12, ProjectID: 7, Status: task_logger.TaskSuccessStatus,
	})
	response := httptest.NewRecorder()
	NewTaskController(nil, repository).GetTaskSummary(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 1, repository.repairCalls)
	assert.Contains(t, response.Body.String(), `"state":"empty"`)
}

func TestTaskSummaryAPIUsesBoundedPersistedPages(t *testing.T) {
	next := 8
	repository := &taskSummaryRepositoryStub{hosts: db.TaskSummaryPage[db.TaskSummaryHost]{
		Items:      []db.TaskSummaryHost{{ID: 9, Host: "web-01", Status: "success"}},
		NextCursor: &next,
	}}
	request := httptest.NewRequest(http.MethodGet,
		"/api/project/7/tasks/11/ansible/summary/hosts?count=9999&before=10", nil)
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "task", db.Task{ID: 11, ProjectID: 7})
	response := httptest.NewRecorder()

	NewTaskController(nil, repository).GetTaskSummaryHosts(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, maxTaskSummaryPageSize, repository.lastParams.Count)
	assert.Equal(t, 10, repository.lastParams.BeforeID)
	assert.Contains(t, response.Body.String(), `"next_cursor":8`)
	assert.Contains(t, response.Body.String(), `"host":"web-01"`)
}

func TestTaskSummaryRouteHidesForbiddenProjectAndReturnsMissingTask(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "summary access"})
	require.NoError(t, err)
	outsider, err := store.CreateUserWithoutPassword(db.User{
		Username: "summary-outsider", Name: "Summary Outsider", Email: "summary-outsider@example.com",
	})
	require.NoError(t, err)

	repository := &taskSummaryRepositoryStub{}
	controller := NewTaskController(store, repository)
	endpoint := controller.GetTaskMiddleware(http.HandlerFunc(controller.GetTaskSummary))

	missing := httptest.NewRequest(http.MethodGet, "/api/project/7/tasks/999/ansible/summary", nil)
	missing = mux.SetURLVars(missing, map[string]string{"task_id": "999"})
	missing = helpers.SetContextValue(missing, "project", project)
	missingResponse := httptest.NewRecorder()
	endpoint.ServeHTTP(missingResponse, missing)
	assert.Equal(t, http.StatusNotFound, missingResponse.Code)

	forbidden := httptest.NewRequest(http.MethodGet,
		"/api/project/"+strconv.Itoa(project.ID)+"/tasks/999/ansible/summary", nil)
	forbidden = mux.SetURLVars(forbidden, map[string]string{
		"project_id": strconv.Itoa(project.ID), "task_id": "999",
	})
	forbidden = helpers.SetContextValue(forbidden, "store", store)
	forbidden = helpers.SetContextValue(forbidden, "user", &outsider)
	forbiddenResponse := httptest.NewRecorder()
	ProjectMiddleware(endpoint).ServeHTTP(forbiddenResponse, forbidden)
	assert.Equal(t, http.StatusNotFound, forbiddenResponse.Code,
		"project membership is deliberately concealed as not found")
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

func TestGetTaskReturnsRedactedPlacementDecision(t *testing.T) {
	runnerID := 7
	task := db.Task{
		ID: 42, ProjectID: 3, TemplateID: 9,
		PlacementDecision: &db.RunnerPlacementDecision{
			RequestedTags:    []string{"gpu", "linux"},
			MatchMode:        db.RunnerTagMatchAll,
			SelectedRunnerID: &runnerID,
			SelectedName:     "project-gpu",
			SelectedScope:    db.RunnerPlacementProject,
			Reason:           "selected project runner #7 by scope, current load, and stable runner id",
			Evaluations: []db.RunnerPlacementEvaluation{{
				RunnerID: runnerID, RunnerName: "project-gpu", Scope: db.RunnerPlacementProject,
				Eligible: true, AcceptedCriteria: []string{"active", "registered"},
			}},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/project/3/tasks/42", nil)
	request = helpers.SetContextValue(request, "task", task)
	response := httptest.NewRecorder()

	NewTaskController(nil, nil).GetTask(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	placement, ok := body["placement_decision"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"gpu", "linux"}, placement["requested_tags"])
	assert.Equal(t, "all", placement["match_mode"])
	assert.Equal(t, float64(runnerID), placement["selected_runner_id"])
	assert.NotContains(t, response.Body.String(), "token")
	assert.NotContains(t, response.Body.String(), "webhook")
}
