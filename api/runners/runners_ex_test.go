package runners

import (
	"bytes"
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
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
