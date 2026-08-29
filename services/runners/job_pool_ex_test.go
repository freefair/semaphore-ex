package runners

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJobPool_CommonHeadersReportHealthMetadata(t *testing.T) {
	initConfig(t)
	previousVersion := util.Ver
	util.Ver = "2.20.4"
	t.Cleanup(func() { util.Ver = previousVersion })
	pool := NewJobPool(nil)
	pool.addRunningJob(1, &runningJob{job: &tasks.LocalExecutor{Task: db.Task{ID: 1}}})
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	pool.setCommonHeaders(request)

	assert.Contains(t, request.Header.Get(RunnerVersionHeader), "2.20.4")
	assert.NotEmpty(t, request.Header.Get(RunnerPlatformHeader))
	assert.Equal(t, "1", request.Header.Get(RunnerCurrentLoadHeader))
	assert.NotEmpty(t, request.Header.Get("X-Runner-Started-At"))
}

func TestJobPool_SendProgressIncludesAssignmentGeneration(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	received := make(chan RunnerProgress, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var progress RunnerProgress
		require.NoError(t, json.NewDecoder(r.Body).Decode(&progress))
		received <- progress
		_ = json.NewEncoder(w).Encode(RunnerProgressResponse{})
	}))
	t.Cleanup(srv.Close)
	util.Config = &util.ConfigType{
		WebHost: srv.URL,
		Runner: &util.RunnerConfig{
			Token: "test-token", Executor: &util.ExecutorConfig{}, Connection: &util.RunnerConnectionConfig{},
		},
	}
	pool := NewJobPool(nil)
	pool.addRunningJob(23, &runningJob{
		job: &tasks.LocalExecutor{Task: db.Task{ID: 23}}, generation: 7,
		status: task_logger.TaskRunningStatus,
	})

	require.True(t, pool.sendProgress())
	progress := <-received
	require.Len(t, progress.Jobs, 1)
	assert.Equal(t, 23, progress.Jobs[0].ID)
	assert.Equal(t, 7, progress.Jobs[0].Generation)
	require.Len(t, progress.KnownJobs, 1)
	assert.Equal(t, 23, progress.KnownJobs[0].ID)
	assert.Equal(t, 7, progress.KnownJobs[0].Generation)
}

func TestJobProgressWireKeepsLegacyKeysWhileAddingGeneration(t *testing.T) {
	payload, err := json.Marshal(RunnerProgress{Jobs: []JobProgress{{
		ID: 23, Generation: 7, Status: task_logger.TaskRunningStatus,
		LogRecords: []LogRecord{},
	}}})
	require.NoError(t, err)
	var decoded map[string][]map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Len(t, decoded["Jobs"], 1)
	job := decoded["Jobs"][0]
	assert.Equal(t, float64(23), job["ID"])
	assert.Equal(t, float64(7), job["Generation"])
	assert.Equal(t, string(task_logger.TaskRunningStatus), job["Status"])
	_, hasLegacyLogsKey := job["LogRecords"]
	assert.True(t, hasLegacyLogsKey)
}

func TestJobPoolSendProgressMarksAnEmptyKnownJobsSnapshotAsComplete(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })

	received := make(chan map[string]json.RawMessage, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var progress map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&progress))
		received <- progress
		_ = json.NewEncoder(w).Encode(RunnerProgressResponse{})
	}))
	t.Cleanup(srv.Close)
	util.Config = &util.ConfigType{
		WebHost: srv.URL,
		Runner: &util.RunnerConfig{
			Token: "test-token", Executor: &util.ExecutorConfig{}, Connection: &util.RunnerConnectionConfig{},
		},
	}

	pool := NewJobPool(nil)
	require.True(t, pool.sendProgress())
	payload := <-received
	assert.JSONEq(t, `[]`, string(payload["KnownJobs"]))
}

func TestJobPool_DuplicatePollDoesNotQueueAssignmentTwice(t *testing.T) {
	prevCfg := util.Config
	t.Cleanup(func() { util.Config = prevCfg })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(RunnerState{
			NewJobs:    []JobData{{Task: db.Task{ID: 31, AssignmentGeneration: 1}}},
			AccessKeys: map[int]db.AccessKey{},
		})
	}))
	t.Cleanup(srv.Close)
	util.Config = &util.ConfigType{
		WebHost: srv.URL,
		Runner: &util.RunnerConfig{
			Token: "test-token", Executor: &util.ExecutorConfig{}, Connection: &util.RunnerConnectionConfig{},
		},
	}
	pool := NewJobPool(nil)
	existing := newTestJob(31)
	existing.generation = 1
	pool.enqueue(existing)

	pool.checkNewJobs()
	pool.checkNewJobs()

	assert.Equal(t, 1, pool.queueLen())
	queued, ok := pool.dequeue()
	require.True(t, ok)
	assert.Equal(t, 31, queued.taskID)
	assert.Equal(t, 1, queued.generation)
}
