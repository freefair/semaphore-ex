package runners

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pamDispatchFixture serves the poll from a node that holds no in-memory
// state for the task, as another HA node would.
func pamDispatchFixture(t *testing.T) (runnerMetadataAPIFixture, *RunnerController, *tasks.TaskPool) {
	t.Helper()
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	fixture := newRunnerMetadataAPIFixture(t)
	util.Config.HA = &util.HAConfig{Enabled: true}
	_, err := fixture.store.Sql().Exec(fixture.store.PrepareQuery(
		"update runner set executor_type=? where id=?"), db.RunnerExecutorLocal, fixture.runner.ID)
	require.NoError(t, err)
	fixture.runner.ExecutorType = db.RunnerExecutorLocal
	encryption := server.NewAccessKeyEncryptionService(fixture.store, nil, nil, nil)
	storage, err := fixture.store.CreateSecretStorage(db.SecretStorage{
		ProjectID: fixture.project.ID, Name: "pam", Type: db.SecretStorageTypeInfisical, ReadOnly: true,
		Params: db.MapStringAnyField{
			"url": "https://infisical.example.com", "client_id": "client-1", "session_duration": "1h0m0s",
			"pam_routes": []any{map[string]any{"host": "web-1", "account": "servers/web-1"}},
		},
	})
	require.NoError(t, err)
	key := db.AccessKey{
		Name: "pam secret", Type: db.AccessKeyString, String: "machine-secret", ProjectID: &fixture.project.ID,
		Owner: db.AccessKeySecretStorage, StorageID: &storage.ID,
	}
	require.NoError(t, encryption.SerializeSecret(&key))
	_, err = fixture.store.CreateAccessKey(key)
	require.NoError(t, err)
	pool := tasks.CreateTaskPool(fixture.store, tasks.NewMemoryTaskStateStore(), nil, nil, encryption, nil, &runnerAPILogWriter{}, nil, nil)
	return fixture, NewRunnerController(fixture.store, &pool, encryption, nil), &pool
}

func pollRunner(t *testing.T, fixture runnerMetadataAPIFixture, controller *RunnerController, header http.Header) runners.RunnerState {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/runners", nil)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	request = helpers.SetContextValue(request, "store", fixture.store)
	request = helpers.SetContextValue(request, "runner", fixture.runner)
	response := httptest.NewRecorder()
	controller.GetRunner(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var state runners.RunnerState
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
	return state
}

func TestGetRunnerDispatchesInfisicalPAMFromAnotherHANode(t *testing.T) {
	fixture, controller, _ := pamDispatchFixture(t)

	header := http.Header{}
	header.Set(runners.RunnerInfisicalPAMHeader, "1")
	state := pollRunner(t, fixture, controller, header)

	require.Len(t, state.NewJobs, 1)
	require.Len(t, state.NewJobs[0].InfisicalPAM, 1)
	config := state.NewJobs[0].InfisicalPAM[0]
	assert.Equal(t, []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}}, config.Routes)
	assert.Equal(t, "machine-secret", state.AccessKeys[config.CredentialKeyID].String)
}

func TestGetRunnerFailsInfisicalPAMTaskForRunnerWithoutCapability(t *testing.T) {
	fixture, controller, pool := pamDispatchFixture(t)
	tracked := tasks.NewTaskRunner(fixture.task, pool, "", nil)
	template, err := fixture.store.GetTemplate(fixture.project.ID, fixture.task.TemplateID)
	require.NoError(t, err)
	tracked.Template = template
	pool.StateStore().SetRunning(tracked)
	go pool.Run()
	t.Cleanup(pool.Stop)

	state := pollRunner(t, fixture, controller, http.Header{})

	assert.Empty(t, state.NewJobs, "a runner that ignores the routes must not receive the task")
	assert.Empty(t, state.AccessKeys)
	assert.Equal(t, task_logger.TaskFailStatus, tracked.Task.Status)
}

func TestRunnerSupportsInfisicalPAMHeader(t *testing.T) {
	header := http.Header{}
	assert.False(t, runners.RunnerSupportsInfisicalPAM(header))
	header.Set(runners.RunnerInfisicalPAMHeader, "0")
	assert.False(t, runners.RunnerSupportsInfisicalPAM(header))
	header.Set(runners.RunnerInfisicalPAMHeader, "1")
	assert.True(t, runners.RunnerSupportsInfisicalPAM(header))
}
