package runners

import (
	"bytes"
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegisterRunnerConsumesProjectTokenOnceAndPreservesBinding(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	origin, err := store.CreateProject(db.Project{Name: "origin"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	created, registrationToken, err := server.NewRunnerService(store).CreateProjectRunner(db.Runner{
		Name: "bound runner", ProjectID: &origin.ID,
	})
	require.NoError(t, err)

	body, err := json.Marshal(runners.RunnerRegistration{
		RegistrationToken: registrationToken,
		ProjectID:         &other.ID,
		Enabled:           false,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	request = helpers.SetContextValue(request, "store", store)
	response := httptest.NewRecorder()

	RegisterRunner(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), registrationToken)
	stored, err := store.GetRunner(origin.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ProjectID)
	assert.Equal(t, origin.ID, *stored.ProjectID)
	assert.True(t, stored.Active)
	assert.True(t, stored.IsRegistered())
	assert.Nil(t, stored.RegistrationTokenHash)
	assert.Nil(t, stored.RegistrationTokenExpiresAt)
	persistedJSON, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(persistedJSON), registrationToken)

	replay := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	replay = helpers.SetContextValue(replay, "store", store)
	replayResponse := httptest.NewRecorder()
	RegisterRunner(replayResponse, replay)
	assert.Equal(t, http.StatusBadRequest, replayResponse.Code)
}

func TestRegisterRunnerRejectsExpiredProjectToken(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "expired"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{Name: "expired", ProjectID: &project.ID})
	require.NoError(t, err)
	registrationToken := server.RunnerRegistrationTokenPrefix + strings.Repeat("x", 43)
	err = store.ResetRunnerRegistration(
		runner.ID,
		server.HashRunnerRegistrationToken(registrationToken),
		time.Now().Add(-time.Minute),
	)
	require.NoError(t, err)
	body, err := json.Marshal(runners.RunnerRegistration{RegistrationToken: registrationToken})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	request = helpers.SetContextValue(request, "store", store)
	response := httptest.NewRecorder()

	RegisterRunner(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.False(t, stored.IsRegistered())
}

func TestGetRunnerAcknowledgesCacheClearOnceAcrossRepeatedPolls(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "cache clear"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "cache runner", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.ClearRunnerCache(runner))
	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil, nil, nil, nil, nil, nil, nil,
	)
	controller := NewRunnerController(store, &pool, nil, nil)

	poll := func() runners.RunnerState {
		fresh, getErr := store.GetRunner(project.ID, runner.ID)
		require.NoError(t, getErr)
		request := httptest.NewRequest(http.MethodGet, "/api/internal/runners", nil)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "runner", fresh)
		response := httptest.NewRecorder()
		controller.GetRunner(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var state runners.RunnerState
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
		return state
	}

	first := poll()
	assert.True(t, first.ClearCache)
	require.NotNil(t, first.CacheCleanProjectID)
	assert.Equal(t, project.ID, *first.CacheCleanProjectID)

	second := poll()
	assert.False(t, second.ClearCache)
	assert.Nil(t, second.CacheCleanProjectID)
}

func TestGetRunnerPersistsBoundedHealthReportAndRestart(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "runner health"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "health runner", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	controller := NewRunnerController(store, &pool, nil, nil)

	poll := func(started time.Time, version, platform, load string) *httptest.ResponseRecorder {
		fresh, getErr := store.GetRunner(project.ID, runner.ID)
		require.NoError(t, getErr)
		request := httptest.NewRequest(http.MethodGet, "/api/internal/runners", nil)
		request.Header.Set("X-Runner-Started-At", started.Format(time.RFC3339))
		request.Header.Set(runners.RunnerVersionHeader, version)
		request.Header.Set(runners.RunnerPlatformHeader, platform)
		request.Header.Set(runners.RunnerCurrentLoadHeader, load)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "runner", fresh)
		response := httptest.NewRecorder()
		controller.GetRunner(response, request)
		return response
	}

	firstStart := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	require.Equal(t, http.StatusOK, poll(firstStart, "old", "linux/arm64", "4").Code)
	restartedAt := time.Now().Add(-10 * time.Second).UTC().Truncate(time.Second)
	require.Equal(t, http.StatusOK, poll(restartedAt, "new", "linux/amd64", "1").Code)
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.StartedAt)
	assert.Equal(t, restartedAt, *stored.StartedAt)
	assert.Equal(t, "new", stored.Version)
	assert.Equal(t, "linux/amd64", stored.Platform)
	assert.Equal(t, 1, stored.CurrentLoad)

	invalid := poll(restartedAt, "new", "linux/amd64", "-1")
	assert.Equal(t, http.StatusBadRequest, invalid.Code)
}

func TestGetRunnerAcknowledgesEqualTimestampCacheClearOnce(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "equal timestamp cache clear"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "cache runner", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	equalTimestamp := time.Now().UTC().Truncate(time.Second)
	_, err = store.Sql().Exec(
		store.PrepareQuery("update runner set touched=?, cleaning_requested=? where id=?"),
		equalTimestamp, equalTimestamp, runner.ID,
	)
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil, nil, nil, nil, nil, nil, nil,
	)
	controller := NewRunnerController(store, &pool, nil, nil)

	poll := func() runners.RunnerState {
		fresh, getErr := store.GetRunner(project.ID, runner.ID)
		require.NoError(t, getErr)
		request := httptest.NewRequest(http.MethodGet, "/api/internal/runners", nil)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "runner", fresh)
		response := httptest.NewRecorder()
		controller.GetRunner(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var state runners.RunnerState
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
		return state
	}

	assert.True(t, poll().ClearCache)
	assert.False(t, poll().ClearCache)
}

func TestNormalizeReportedGenerationAllowsOnlyLegacyFirstAssignment(t *testing.T) {
	assert.Equal(t, 1, normalizeReportedGeneration(0, 1))
	assert.Equal(t, 0, normalizeReportedGeneration(0, 2))
	assert.Equal(t, 2, normalizeReportedGeneration(2, 2))
}

func TestUpdateRunner_StaleGenerationFromSameRunnerReportedAsTerminated(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })
	store := sql.InitConfigCreateTestStore()
	pool := tasks.CreateTaskPool(
		store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)
	runnerID := 1
	tr := tasks.NewTaskRunner(db.Task{
		ID: 9, ProjectID: 1, RunnerID: &runnerID,
		AssignmentGeneration: 2, Status: task_logger.TaskStartingStatus,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: runnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID: 9, Generation: 1, Status: task_logger.TaskSuccessStatus,
			LogRecords: []runners.LogRecord{{Time: tz.Now(), Message: "late attempt output"}},
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{9}, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStartingStatus, tr.Task.Status)
	assert.Equal(t, 2, tr.Task.AssignmentGeneration)
}

func TestUpdateRunner_CancelingTaskRejectsNonTerminalProgress(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })
	store := sql.InitConfigCreateTestStore()
	pool := tasks.CreateTaskPool(
		store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)
	runnerID := 1
	tr := tasks.NewTaskRunner(db.Task{
		ID: 10, ProjectID: 1, RunnerID: &runnerID,
		Status: task_logger.TaskStoppingStatus,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: runnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{ID: 10, Status: task_logger.TaskRunningStatus}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{10}, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStoppingStatus, tr.Task.Status)
}

func TestUpdateRunner_RejectedTaskRejectsNonTerminalProgress(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })
	store := sql.InitConfigCreateTestStore()
	pool := tasks.CreateTaskPool(
		store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)
	runnerID := 1
	tr := tasks.NewTaskRunner(db.Task{
		ID: 11, ProjectID: 1, RunnerID: &runnerID, Status: task_logger.TaskRejected,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: runnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{ID: 11, Status: task_logger.TaskRunningStatus}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{11}, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskRejected, tr.Task.Status)
}
