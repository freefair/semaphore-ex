package runners

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestUnregisterRunner_AlreadyRemovedRunnerIsNoContent(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	runner, err := store.CreateRunner(db.Runner{Name: "r"})
	require.NoError(t, err)
	require.NoError(t, store.DeleteGlobalRunner(runner.ID))

	req := httptest.NewRequest(http.MethodDelete, "/api/internal/runners", nil)
	req = helpers.SetContextValue(req, "store", store)
	req = helpers.SetContextValue(req, "runner", runner)
	w := httptest.NewRecorder()
	UnregisterRunner(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// outputsCollectorFake stands in for the pro outputs collector on the server:
// it records what the handler asked it to validate and answers as told.
type outputsCollectorFake struct {
	err       error
	validated []string
}

func (c *outputsCollectorFake) Begin(string, int) (pro_interfaces.TaskOutputsCapture, error) {
	return nil, nil
}

func (c *outputsCollectorFake) Validate(document string) (*string, error) {
	c.validated = append(c.validated, document)
	if c.err != nil {
		return nil, c.err
	}
	return &document, nil
}

type nopLogWriteService struct{}

func (nopLogWriteService) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (nopLogWriteService) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (nopLogWriteService) WriteResult(any) error                             { return nil }

// remoteWorkflowTaskFixture is a workflow task assigned to a remote runner,
// persisted so terminal statuses and outputs can be written to the store.
type remoteWorkflowTaskFixture struct {
	store     db.Store
	pool      *tasks.TaskPool
	collector *outputsCollectorFake
	runner    db.Runner
	task      *tasks.TaskRunner
}

func newRemoteWorkflowTaskFixture(t *testing.T, decorateStore ...func(db.Store) db.Store) remoteWorkflowTaskFixture {
	t.Helper()

	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	var store db.Store = sql.InitConfigCreateTestStore()
	for _, decorate := range decorateStore {
		store = decorate(store)
	}

	// Finalizing a terminal status writes the task event through the log
	// write service, so the fixture needs one.
	pool := tasks.CreateTaskPool(
		store,
		tasks.NewMemoryTaskStateStore(),
		nil,
		nil,
		nil,
		nil,
		nopLogWriteService{},
		nil,
		nil,
	)
	collector := &outputsCollectorFake{}
	pool.SetTaskOutputsCollector(collector)
	go pool.Run()
	t.Cleanup(pool.Stop)

	proj, err := store.CreateProject(db.Project{})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &proj.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{
		ProjectID: proj.ID,
		SSHKeyID:  key.ID,
		Name:      "Test",
		GitURL:    "git@example.com:test/test",
		GitBranch: "master",
	})
	require.NoError(t, err)
	inv, err := store.CreateInventory(db.Inventory{ProjectID: proj.ID})
	require.NoError(t, err)
	tpl, err := store.CreateTemplate(db.Template{
		Name:         "Test",
		Playbook:     "test.yml",
		ProjectID:    proj.ID,
		RepositoryID: repo.ID,
		InventoryID:  &inv.ID,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{ProjectID: proj.ID, TemplateID: tpl.ID, Status: task_logger.TaskStartingStatus}, 0)
	require.NoError(t, err)

	// UpdateTask persists runner_id, which references the runner table.
	runner, err := store.CreateRunner(db.Runner{Name: "remote", Token: "test-token", Active: true})
	require.NoError(t, err)
	assigned, ok, err := store.AssignTaskRunner(proj.ID, task.ID, runner.ID, runner.Name, tz.Now())
	require.NoError(t, err)
	require.True(t, ok)
	task = assigned
	runID := 11
	task.WorkflowRunID = &runID

	tr := tasks.NewTaskRunner(task, &pool, "", nil)
	tr.Template = tpl
	require.True(t, tr.ApplyRunnerProgress(task_logger.TaskRunningStatus, runner.ID, task.AssignmentGeneration, nil, ""))
	pool.StateStore().SetRunning(tr)
	var terminalTransitionPublished atomic.Bool
	tr.AddStatusListener(func(status task_logger.TaskStatus) {
		if status.IsFinished() {
			terminalTransitionPublished.Store(true)
		}
	})
	t.Cleanup(func() {
		if !terminalTransitionPublished.Load() {
			return
		}
		require.Eventually(t, func() bool {
			persisted, err := store.GetTaskByID(tr.Task.ID)
			return err == nil && persisted.End != nil
		}, time.Second, 10*time.Millisecond)
		require.Eventually(t, func() bool {
			state := pool.StateStore()
			if !state.TryFinalize(tr.Task.ID) {
				return false
			}
			state.DeleteFinalize(tr.Task.ID)
			return true
		}, time.Second, 10*time.Millisecond)
	})

	return remoteWorkflowTaskFixture{
		store:     store,
		pool:      &pool,
		collector: collector,
		runner:    runner,
		task:      tr,
	}
}

// terminalReportRaceStore advances the database row immediately before a
// terminal report's fenced CAS, modelling a concurrent HA progress report
// from another API node.
type terminalReportRaceStore struct {
	db.Store
	injected        bool
	transitioned    bool
	advanceStatuses []task_logger.TaskStatus
	advances        int
}

func (s *terminalReportRaceStore) UpdateTaskRunner(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
) (bool, error) {
	statuses := s.advanceStatuses
	if len(statuses) == 0 {
		statuses = []task_logger.TaskStatus{task_logger.TaskRunningStatus}
	}
	if task.Status.IsFinished() && s.advances < len(statuses) {
		s.injected = true
		concurrent, err := s.Store.GetTaskByID(task.ID)
		if err != nil {
			return false, err
		}
		concurrent.Status = statuses[s.advances]
		s.advances++
		transitioned, err := s.Store.UpdateTaskRunner(
			concurrent,
			expectedStatus,
			expectedRunnerID,
			expectedGeneration,
			db.RunnerAttemptActive,
			"",
			transitionedAt,
		)
		s.transitioned = transitioned
		if err != nil || !transitioned {
			return false, err
		}
	}
	return s.Store.UpdateTaskRunner(
		task, expectedStatus, expectedRunnerID, expectedGeneration, outcome, attemptReason, transitionedAt,
	)
}

type countingTaskRunnerStore struct {
	db.Store
	updates atomic.Int32
}

func (s *countingTaskRunnerStore) UpdateTaskRunner(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
) (bool, error) {
	// Count only report-phase CAS writes. Finalization persists End through
	// UpdateTask after the HTTP handler has returned.
	if task.End == nil {
		s.updates.Add(1)
	}
	return s.Store.UpdateTaskRunner(task, expectedStatus, expectedRunnerID, expectedGeneration, outcome, attemptReason, transitionedAt)
}

func setRemoteWorkflowFixtureStatus(t *testing.T, fixture remoteWorkflowTaskFixture, status task_logger.TaskStatus) {
	t.Helper()
	updatedTask := fixture.task.Task
	expectedStatus := updatedTask.Status
	updatedTask.Status = status
	updated, err := fixture.store.UpdateTaskRunner(
		updatedTask,
		expectedStatus,
		fixture.runner.ID,
		updatedTask.AssignmentGeneration,
		db.RunnerAttemptActive,
		"",
		tz.Now(),
	)
	require.NoError(t, err)
	require.True(t, updated)
	fixture.task.Task = updatedTask
}

func TestUpdateRunner_OutputsIgnoredWhileRunning(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"1.4.2"}}`

	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:         fixture.task.Task.ID,
			Generation: fixture.task.Task.AssignmentGeneration,
			Status:     task_logger.TaskRunningStatus,
			Outputs:    &document,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, fixture.collector.validated)
	assert.Nil(t, fixture.task.Task.Artifacts)
	assert.Equal(t, task_logger.TaskRunningStatus, fixture.task.Task.Status)
}

// A workflow run may be progressed by any HA node as soon as the terminal
// status is in the database, so the outputs must be stored before it.
func TestUpdateRunner_SuccessStoresOutputsBeforeStatus(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"1.4.2"}}`

	var storedAtSuccess *string
	fixture.task.AddStatusListener(func(status task_logger.TaskStatus) {
		if status != task_logger.TaskSuccessStatus {
			return
		}
		persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
		require.NoError(t, err)
		storedAtSuccess = persisted.Artifacts
	})

	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:         fixture.task.Task.ID,
			Generation: fixture.task.Task.AssignmentGeneration,
			Status:     task_logger.TaskSuccessStatus,
			Outputs:    &document,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, []string{document}, fixture.collector.validated)
	assert.Equal(t, task_logger.TaskSuccessStatus, fixture.task.Task.Status)
	require.NotNil(t, storedAtSuccess)
	assert.Equal(t, document, *storedAtSuccess)
	require.NotNil(t, fixture.task.Task.Artifacts)
	assert.Equal(t, document, *fixture.task.Task.Artifacts)
}

func TestUpdateRunner_TerminalReportSurvivesConcurrentProgressBeforeTerminalCAS(t *testing.T) {
	raceStore := &terminalReportRaceStore{}
	fixture := newRemoteWorkflowTaskFixture(t, func(store db.Store) db.Store {
		raceStore.Store = store
		return raceStore
	})
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)

	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"1.4.2"}}`
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:         fixture.task.Task.ID,
			Generation: fixture.task.Task.AssignmentGeneration,
			Status:     task_logger.TaskSuccessStatus,
			Outputs:    &document,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.True(t, raceStore.injected)
	require.True(t, raceStore.transitioned)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskSuccessStatus, fixture.task.Task.Status)
	assert.Equal(t, &document, fixture.task.Task.Artifacts)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, persisted.Status)
	assert.Equal(t, &document, persisted.Artifacts)
}

func TestUpdateRunner_UncontendedTerminalReportUsesOneCAS(t *testing.T) {
	countingStore := &countingTaskRunnerStore{}
	fixture := newRemoteWorkflowTaskFixture(t, func(store db.Store) db.Store {
		countingStore.Store = store
		return countingStore
	})
	countingStore.updates.Store(0)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"one-terminal-cas"}}`
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, int32(1), countingStore.updates.Load())
	assert.Equal(t, task_logger.TaskSuccessStatus, fixture.task.Task.Status)
	assert.Equal(t, &document, fixture.task.Task.Artifacts)
	require.Eventually(t, func() bool {
		persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
		return err == nil && persisted.End != nil
	}, time.Second, 10*time.Millisecond)
}

func TestUpdateRunner_TerminalReportRetriesWhenSQLAlreadyAdvancedBeforeGuard(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)
	advanced := fixture.task.Task
	advanced.Status = task_logger.TaskRunningStatus
	updated, err := fixture.store.UpdateTaskRunner(
		advanced,
		task_logger.TaskStartingStatus,
		fixture.runner.ID,
		advanced.AssignmentGeneration,
		db.RunnerAttemptActive,
		"",
		tz.Now(),
	)
	require.NoError(t, err)
	require.True(t, updated)

	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"advanced-before-guard"}}`
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskSuccessStatus, fixture.task.Task.Status)
	assert.Equal(t, &document, fixture.task.Task.Artifacts)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, persisted.Status)
	assert.Equal(t, &document, persisted.Artifacts)
}

func TestUpdateRunner_TerminalReportReturnsRetryableErrorAfterTwoBenignCASConflicts(t *testing.T) {
	raceStore := &terminalReportRaceStore{advanceStatuses: []task_logger.TaskStatus{
		task_logger.TaskRunningStatus,
		task_logger.TaskStoppingStatus,
	}}
	fixture := newRemoteWorkflowTaskFixture(t, func(store db.Store) db.Store {
		raceStore.Store = store
		return raceStore
	})
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"retry-after-conflict"}}`
	progress := runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}}
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, newProgressRequest(t, fixture.store, fixture.runner, progress))

	require.Equal(t, 2, raceStore.advances)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, task_logger.TaskStoppingStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppingStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)

	accepted := httptest.NewRecorder()
	ctrl.UpdateRunner(accepted, newProgressRequest(t, fixture.store, fixture.runner, progress))

	require.Equal(t, http.StatusOK, accepted.Code)
	assert.Empty(t, decodeProgressResponse(t, accepted).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStoppedStatus, fixture.task.Task.Status)
	persisted, err = fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppedStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)
}

func TestUpdateRunner_TerminalReportUsesStopOutcomeAfterConcurrentProgressBeforeTerminalCAS(t *testing.T) {
	raceStore := &terminalReportRaceStore{advanceStatuses: []task_logger.TaskStatus{task_logger.TaskStoppingStatus}}
	fixture := newRemoteWorkflowTaskFixture(t, func(store db.Store) db.Store {
		raceStore.Store = store
		return raceStore
	})
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)

	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"must-not-persist"}}`
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.True(t, raceStore.injected)
	require.True(t, raceStore.transitioned)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStoppedStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
	assert.Equal(t, []string{document}, fixture.collector.validated)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppedStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)
}

func TestUpdateRunner_InvalidOutputsDoNotOverrideConcurrentStop(t *testing.T) {
	raceStore := &terminalReportRaceStore{advanceStatuses: []task_logger.TaskStatus{task_logger.TaskStoppingStatus}}
	fixture := newRemoteWorkflowTaskFixture(t, func(store db.Store) db.Store {
		raceStore.Store = store
		return raceStore
	})
	fixture.collector.err = errors.New("output validation failed")
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"image_tag":"invalid"}}`
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.True(t, raceStore.injected)
	require.True(t, raceStore.transitioned)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, decodeProgressResponse(t, w).TerminatedJobs)
	assert.Equal(t, task_logger.TaskStoppedStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
	assert.Equal(t, []string{document}, fixture.collector.validated)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppedStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)
}

func TestUpdateRunner_StaleAssignmentAfterCASRefreshIsTerminated(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)
	otherRunner, err := fixture.store.CreateRunner(db.Runner{Name: "replacement", Token: "replacement-token", Active: true})
	require.NoError(t, err)
	cleared := fixture.task.Task
	cleared.RunnerID = nil
	require.NoError(t, fixture.store.UpdateTask(cleared))
	reassigned, assigned, err := fixture.store.AssignTaskRunner(
		fixture.task.Task.ProjectID, fixture.task.Task.ID, otherRunner.ID, otherRunner.Name, tz.Now(),
	)
	require.NoError(t, err)
	require.True(t, assigned)
	require.NotEqual(t, fixture.task.Task.AssignmentGeneration, reassigned.AssignmentGeneration)

	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration, Status: task_logger.TaskSuccessStatus,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, decodeProgressResponse(t, w).TerminatedJobs, fixture.task.Task.ID)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	require.NotNil(t, persisted.RunnerID)
	assert.Equal(t, otherRunner.ID, *persisted.RunnerID)
	assert.Equal(t, reassigned.AssignmentGeneration, persisted.AssignmentGeneration)
	assert.Equal(t, task_logger.TaskStartingStatus, persisted.Status)
}

func TestUpdateRunner_StaleSQLCompletionAfterCASRefreshIsTerminated(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	setRemoteWorkflowFixtureStatus(t, fixture, task_logger.TaskStartingStatus)
	document := `{"values":{"image_tag":"already-persisted"}}`
	completed := fixture.task.Task
	completed.Status = task_logger.TaskSuccessStatus
	completed.Artifacts = &document
	updated, err := fixture.store.UpdateTaskRunner(
		completed,
		task_logger.TaskStartingStatus,
		fixture.runner.ID,
		completed.AssignmentGeneration,
		db.RunnerAttemptSucceeded,
		"",
		tz.Now(),
	)
	require.NoError(t, err)
	require.True(t, updated)

	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration, Status: task_logger.TaskSuccessStatus,
	}}})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, decodeProgressResponse(t, w).TerminatedJobs, fixture.task.Task.ID)
	assert.Empty(t, fixture.collector.validated)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, persisted.Status)
	assert.Equal(t, &document, persisted.Artifacts)
}

func TestUpdateRunner_InvalidOutputsFailTheTask(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	fixture.collector.err = errors.New("output name \"bad name\" is not valid")
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"bad name":1}}`

	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:         fixture.task.Task.ID,
			Generation: fixture.task.Task.AssignmentGeneration,
			Status:     task_logger.TaskSuccessStatus,
			Outputs:    &document,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, task_logger.TaskFailStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskFailStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)
}

func TestUpdateRunner_SuccessWithoutOutputs(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)

	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
		Jobs: []runners.JobProgress{{
			ID:         fixture.task.Task.ID,
			Generation: fixture.task.Task.AssignmentGeneration,
			Status:     task_logger.TaskSuccessStatus,
		}},
	})
	w := httptest.NewRecorder()

	ctrl.UpdateRunner(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, fixture.collector.validated)
	assert.Equal(t, task_logger.TaskSuccessStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
}

func TestUpdateRunner_StaleGenerationCannotPersistOutputsOrTerminalStatus(t *testing.T) {
	fixture := newRemoteWorkflowTaskFixture(t)
	ctrl := NewRunnerController(nil, fixture.pool, nil, nil)
	document := `{"values":{"stale":"must-not-store"}}`
	require.Positive(t, fixture.task.Task.AssignmentGeneration)
	req := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{Jobs: []runners.JobProgress{{
		ID: fixture.task.Task.ID, Generation: fixture.task.Task.AssignmentGeneration + 1,
		Status: task_logger.TaskSuccessStatus, Outputs: &document,
	}}})
	w := httptest.NewRecorder()
	ctrl.UpdateRunner(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, decodeProgressResponse(t, w).TerminatedJobs, fixture.task.Task.ID)
	assert.Equal(t, task_logger.TaskRunningStatus, fixture.task.Task.Status)
	assert.Nil(t, fixture.task.Task.Artifacts)
	assert.Empty(t, fixture.collector.validated)
	persisted, err := fixture.store.GetTaskByID(fixture.task.Task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskRunningStatus, persisted.Status)
	assert.Nil(t, persisted.Artifacts)
}
