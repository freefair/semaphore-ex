package runners

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		ExecutorType:      db.RunnerExecutorDocker,
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
	assert.Equal(t, db.RunnerExecutorDocker, stored.ExecutorType)
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

func TestSecureRunnerRegistrationAndVersionRejectionContract(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "secure registration"})
	require.NoError(t, err)
	service := server.NewRunnerService(store)
	register := func(version string) *httptest.ResponseRecorder {
		_, token, createErr := service.CreateProjectRunner(db.Runner{
			Name: "secure runner", ProjectID: &project.ID,
			RegistrationPolicy: db.RunnerRegistrationSecure,
		})
		require.NoError(t, createErr)
		body, marshalErr := json.Marshal(runners.RunnerRegistration{
			RegistrationToken:       token,
			ExecutorType:            db.RunnerExecutorDocker,
			TransportTrust:          db.RunnerTransportSystemCA,
			RunnerVersion:           version,
			SecurityProtocolVersion: db.CurrentSecureRunnerProtocol,
			PublicKey:               "public-key",
		})
		require.NoError(t, marshalErr)
		request := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
		request = helpers.SetContextValue(request, "store", store)
		response := httptest.NewRecorder()
		RegisterRunner(response, request)
		return response
	}

	accepted := register(db.MinSecureRunnerVersion)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	assert.Contains(t, accepted.Body.String(), `"registration_policy":"secure"`)
	assert.NotContains(t, accepted.Body.String(), "public-key")

	rejected := register("2.19.99")
	require.Equal(t, http.StatusConflict, rejected.Code, rejected.Body.String())
	assert.Contains(t, rejected.Body.String(), "Upgrade the runner")
	assert.NotContains(t, rejected.Body.String(), "public-key")

	_, missingExecutorToken, err := service.CreateProjectRunner(db.Runner{
		Name: "secure missing executor", ProjectID: &project.ID,
		RegistrationPolicy: db.RunnerRegistrationSecure,
	})
	require.NoError(t, err)
	body, err := json.Marshal(runners.RunnerRegistration{
		RegistrationToken:       missingExecutorToken,
		TransportTrust:          db.RunnerTransportSystemCA,
		RunnerVersion:           db.MinSecureRunnerVersion,
		SecurityProtocolVersion: db.CurrentSecureRunnerProtocol,
		PublicKey:               "public-key",
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	request = helpers.SetContextValue(request, "store", store)
	missingExecutor := httptest.NewRecorder()
	RegisterRunner(missingExecutor, request)
	require.Equal(t, http.StatusConflict, missingExecutor.Code, missingExecutor.Body.String())
	assert.Contains(t, missingExecutor.Body.String(), "executor capability missing")
}

func TestSecureRunnerReconnectRejectsDowngradeAndStandardRunnerStaysCompatible(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "secure reconnect"})
	require.NoError(t, err)
	_, token, err := server.NewRunnerService(store).CreateProjectRunner(db.Runner{
		Name: "secure runner", ProjectID: &project.ID,
		RegistrationPolicy: db.RunnerRegistrationSecure,
	})
	require.NoError(t, err)
	secureRunner, err := store.RegisterRunner(server.HashRunnerRegistrationToken(token), db.RunnerSecurityReport{
		TransportTrust: db.RunnerTransportSystemCA, RunnerVersion: db.MinSecureRunnerVersion,
		ProtocolVersion: db.CurrentSecureRunnerProtocol, ExecutorType: db.RunnerExecutorDocker,
		PublicKey: "public-key",
	})
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	controller := NewRunnerController(store, &pool, nil, nil)
	poll := func(runner db.Runner, withSecureHeaders bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/internal/runners", nil)
		if withSecureHeaders {
			request.Header.Set(runners.RunnerVersionHeader, db.MinSecureRunnerVersion)
			request.Header.Set(runners.RunnerExecutorTypeHeader, string(db.RunnerExecutorDocker))
			request.Header.Set(runners.RunnerTransportTrustHeader, string(db.RunnerTransportSystemCA))
			request.Header.Set(runners.RunnerSecurityProtocolHeader, "1")
		}
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "runner", runner)
		response := httptest.NewRecorder()
		controller.GetRunner(response, request)
		return response
	}

	require.Equal(t, http.StatusOK, poll(secureRunner, true).Code)
	downgraded := poll(secureRunner, false)
	require.Equal(t, http.StatusConflict, downgraded.Code, downgraded.Body.String())
	assert.Contains(t, downgraded.Body.String(), "verified TLS server identity")
	persisted, err := store.GetRunner(project.ID, secureRunner.ID)
	require.NoError(t, err)
	assert.False(t, persisted.SecurityCompliant)

	standard, err := store.CreateRunner(db.Runner{
		Name: "standard runner", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, poll(standard, false).Code)
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

func TestRegisterRunner_InvalidTokenReturnsBadRequest(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	var logOutput bytes.Buffer
	logger := log.StandardLogger()
	previousOutput := logger.Out
	logger.SetOutput(&logOutput)
	t.Cleanup(func() { logger.SetOutput(previousOutput) })

	body, err := json.Marshal(map[string]any{
		"registration_token": server.RunnerRegistrationTokenPrefix + securityfixtures.TripwireValues[0],
		"name":               "test-runner",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	req = helpers.SetContextValue(req, "store", store)

	w := httptest.NewRecorder()
	RegisterRunner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var res map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "Invalid registration token", res["error"])
	securityfixtures.AssertTripwiresAbsent(t, w.Body.String(), logOutput.String())
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
		request.Header.Set(runners.RunnerExecutorTypeHeader, string(db.RunnerExecutorDocker))
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
	assert.Equal(t, db.RunnerExecutorDocker, stored.ExecutorType)

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

func newProgressRequest(t *testing.T, store db.Store, runner db.Runner, progress runners.RunnerProgress) *http.Request {
	t.Helper()

	body, err := json.Marshal(progress)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/api/internal/runners", bytes.NewReader(body))
	req = helpers.SetContextValue(req, "store", store)
	req = helpers.SetContextValue(req, "runner", runner)
	return req
}

func decodeProgressResponse(t *testing.T, w *httptest.ResponseRecorder) runners.RunnerProgressResponse {
	t.Helper()

	var res runners.RunnerProgressResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	return res
}

type taskExecutionEvidenceRecorderSpy struct {
	calls    int
	runnerID int
	evidence []db.TaskExecutionEvidence
}

func (s *taskExecutionEvidenceRecorderSpy) RecordTaskExecutionSnapshot(runnerID int, evidence []db.TaskExecutionEvidence) error {
	s.calls++
	s.runnerID = runnerID
	s.evidence = append([]db.TaskExecutionEvidence(nil), evidence...)
	return nil
}

func TestUpdateRunnerRecordsOnlyExplicitCompleteExecutionSnapshots(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	recorder := &taskExecutionEvidenceRecorderSpy{}
	controller := NewRunnerController(store, &pool, nil, nil, recorder)
	runner := db.Runner{ID: 7}

	explicit := newProgressRequest(t, store, runner, runners.RunnerProgress{
		KnownJobs: []runners.JobState{{
			ID: 41, Generation: 3, Status: task_logger.TaskRunningStatus,
		}},
	})
	explicitResponse := httptest.NewRecorder()
	controller.UpdateRunner(explicitResponse, explicit)
	require.Equal(t, http.StatusNoContent, explicitResponse.Code)
	assert.Equal(t, 1, recorder.calls)
	assert.Equal(t, runner.ID, recorder.runnerID)
	assert.Equal(t, []db.TaskExecutionEvidence{{
		TaskID: 41, Generation: 3, State: db.TaskExecutionEvidenceRunning, Status: task_logger.TaskRunningStatus,
	}}, recorder.evidence)

	legacy := newProgressRequest(t, store, runner, runners.RunnerProgress{})
	legacyResponse := httptest.NewRecorder()
	controller.UpdateRunner(legacyResponse, legacy)
	require.Equal(t, http.StatusNoContent, legacyResponse.Code)
	assert.Equal(t, 1, recorder.calls, "a missing KnownJobs field is unknown, not an empty snapshot")

	invalid := newProgressRequest(t, store, runner, runners.RunnerProgress{
		KnownJobs: []runners.JobState{{
			ID: 41, Generation: 0, Status: task_logger.TaskRunningStatus,
		}},
	})
	invalidResponse := httptest.NewRecorder()
	controller.UpdateRunner(invalidResponse, invalid)
	require.Equal(t, http.StatusBadRequest, invalidResponse.Code)
	assert.Equal(t, 1, recorder.calls)
}

func TestNormalizeReportedGenerationAllowsOnlyLegacyFirstAssignment(t *testing.T) {
	assert.Equal(t, 1, normalizeReportedGeneration(0, 1))
	assert.Equal(t, 0, normalizeReportedGeneration(0, 2))
	assert.Equal(t, 2, normalizeReportedGeneration(2, 2))
}

func TestUpdateRunner_StoppedTaskReportedAsTerminated(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	store := sql.InitConfigCreateTestStore() // also initializes util.Config

	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)

	runnerID := 1

	// The task was stopped on the server while the runner was offline.
	tr := tasks.NewTaskRunner(db.Task{
		ID:        5,
		ProjectID: 1,
		RunnerID:  &runnerID,
		Status:    task_logger.TaskStoppedStatus,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: runnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:     5,
			Status: task_logger.TaskRunningStatus,
			LogRecords: []runners.LogRecord{
				{Time: tz.Now(), Message: "late output"},
			},
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{5}, decodeProgressResponse(t, w).TerminatedJobs)

	// The late report must not overwrite the terminal status.
	assert.Equal(t, task_logger.TaskStoppedStatus, tr.Task.Status)
}

func TestUpdateRunner_UnknownTaskReportedAsTerminated(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	store := sql.InitConfigCreateTestStore()

	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)

	req := newProgressRequest(t, store, db.Runner{ID: 1}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:     999, // neither in the pool nor in the database
			Status: task_logger.TaskRunningStatus,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{999}, decodeProgressResponse(t, w).TerminatedJobs)
}

func TestUpdateRunner_ReassignedTaskReportedAsTerminated(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	store := sql.InitConfigCreateTestStore()

	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)

	oldRunnerID := 1
	newRunnerID := 2

	// Task was reassigned from runner 1 to runner 2 while runner 1 still had
	// the job in its local pool (e.g. after requeueTaskRunnerOffline).
	tr := tasks.NewTaskRunner(db.Task{
		ID:        8,
		ProjectID: 1,
		RunnerID:  &newRunnerID,
		Status:    task_logger.TaskStartingStatus,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: oldRunnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:     8,
			Status: task_logger.TaskRunningStatus,
			LogRecords: []runners.LogRecord{
				{Time: tz.Now(), Message: "stale runner output"},
			},
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []int{8}, decodeProgressResponse(t, w).TerminatedJobs)

	// The late report must not overwrite status or assignee.
	assert.Equal(t, task_logger.TaskStartingStatus, tr.Task.Status)
	assert.Equal(t, newRunnerID, *tr.Task.RunnerID)
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

func TestUpdateRunner_CancelingTaskAcceptsInflightNonTerminalProgress(t *testing.T) {
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
		Jobs: []runners.JobProgress{{
			ID: 10, Status: task_logger.TaskRunningStatus,
			Commit: &runners.CommitInfo{Hash: "cancel-commit", Message: "captured during cancellation"},
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStoppingStatus, tr.Task.Status)
	require.NotNil(t, tr.Task.CommitHash)
	assert.Equal(t, "cancel-commit", *tr.Task.CommitHash)
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

func TestUpdateRunner_RunningTaskAcceptedWithoutTermination(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	store := sql.InitConfigCreateTestStore()

	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	ctrl := NewRunnerController(nil, &pool, nil, nil)

	runnerID := 1

	tr := tasks.NewTaskRunner(db.Task{
		ID:        7,
		ProjectID: 1,
		RunnerID:  &runnerID,
		Status:    task_logger.TaskRunningStatus,
	}, &pool, "", nil)
	pool.StateStore().SetRunning(tr)

	req := newProgressRequest(t, store, db.Runner{ID: runnerID}, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:     7,
			Status: task_logger.TaskRunningStatus,
			LogRecords: []runners.LogRecord{
				{Time: tz.Now(), Message: "normal output"},
			},
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskRunningStatus, tr.Task.Status)
}

type runnerMetadataAPIFixture struct {
	store      *sql.SqlDb
	project    db.Project
	runner     db.Runner
	task       db.Task
	controller *RunnerController
}

func newRunnerMetadataAPIFixture(t *testing.T) runnerMetadataAPIFixture {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Docker metadata"})
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
		ProjectID: &project.ID, Name: "Docker runner", Token: db.GenerateRunnerToken(), Active: true,
		ExecutorType: db.RunnerExecutorDocker,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskStartingStatus,
	}, 0)
	require.NoError(t, err)
	assigned, ok, err := store.AssignTaskRunner(project.ID, task.ID, runner.ID, runner.Name, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok)

	pool := tasks.CreateTaskPool(store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)
	tracked := tasks.NewTaskRunner(assigned, &pool, "", nil)
	pool.StateStore().SetRunning(tracked)
	return runnerMetadataAPIFixture{
		store: store, project: project, runner: runner, task: assigned,
		controller: NewRunnerController(store, &pool, nil, nil),
	}
}

func TestUpdateRunnerPersistsBoundedDockerExecutorMetadata(t *testing.T) {
	fixture := newRunnerMetadataAPIFixture(t)
	metadata := db.RunnerExecutorMetadata{
		ExecutorType:  db.RunnerExecutorDocker,
		ContainerID:   "abc123",
		ContainerName: "semaphore-task-42-boot",
	}
	request := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.ID, Generation: fixture.task.AssignmentGeneration,
		Status: task_logger.TaskRunningStatus, ExecutorMetadata: &metadata,
	}}})
	response := httptest.NewRecorder()

	fixture.controller.UpdateRunner(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	attempts, err := fixture.store.GetTaskRunnerAttempts(fixture.project.ID, fixture.task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.RunnerExecutorDocker, attempts[0].ExecutorType)
	assert.Equal(t, metadata.ContainerID, attempts[0].ContainerID)
	assert.Equal(t, metadata.ContainerName, attempts[0].ContainerName)
}

func TestUpdateRunnerRejectsExecutorMetadataForAnotherRunnerType(t *testing.T) {
	fixture := newRunnerMetadataAPIFixture(t)
	metadata := db.RunnerExecutorMetadata{
		ExecutorType:  db.RunnerExecutorK8s,
		ContainerName: "foreign-runtime",
	}
	request := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.ID, Generation: fixture.task.AssignmentGeneration,
		Status: task_logger.TaskRunningStatus, ExecutorMetadata: &metadata,
	}}})
	response := httptest.NewRecorder()

	fixture.controller.UpdateRunner(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "Invalid executor metadata")
	attempts, err := fixture.store.GetTaskRunnerAttempts(fixture.project.ID, fixture.task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.RunnerExecutorDocker, attempts[0].ExecutorType)
	assert.Empty(t, attempts[0].ContainerID)
	assert.Empty(t, attempts[0].ContainerName)
}

func TestRegisterRunner_NonSmrsTokenWithoutGlobalMatchReturnsBadRequest(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })
	util.Config = &util.ConfigType{
		RunnerRegistrationToken: "global-reg-token",
	}

	store := sql.InitConfigCreateTestStore()

	body, err := json.Marshal(map[string]any{
		"registration_token": "legacy-one-time-token-without-smrs-prefix",
		"name":               "test-runner",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/runners", bytes.NewReader(body))
	req = helpers.SetContextValue(req, "store", store)

	w := httptest.NewRecorder()
	RegisterRunner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
