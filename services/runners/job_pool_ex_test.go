package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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

type dockerPolicyConsumerStub struct{ policy db.DockerExecutionPolicy }

func (p *dockerPolicyConsumerStub) NewExecutor(db.Task, db.Template, db.Inventory, db.Repository, db.Environment, string) (tasks.Executor, error) {
	return nil, nil
}

func (p *dockerPolicyConsumerStub) ApplyDockerExecutionPolicy(policy db.DockerExecutionPolicy) error {
	if err := policy.Canonicalize(); err != nil {
		return err
	}
	p.policy = policy
	return nil
}

func (p *dockerPolicyConsumerStub) DockerExecutionPolicyAcknowledgement() db.DockerExecutionPolicyAck {
	return db.DockerExecutionPolicyAck{Revision: p.policy.Revision, Hash: p.policy.Hash}
}

func (*dockerPolicyConsumerStub) ApplyDockerRunnerIdentity(runnerID int) error {
	if runnerID <= 0 {
		return fmt.Errorf("runner id is required")
	}
	return nil
}

func (*dockerPolicyConsumerStub) ApplyDockerReconciliationSession(db.DockerReconciliationSession) error {
	return nil
}

func (*dockerPolicyConsumerStub) DockerReconciliationSession() db.DockerReconciliationSession {
	return db.DockerReconciliationSession{}
}

func (*dockerPolicyConsumerStub) ScanDockerReconciliation(_ context.Context, session db.DockerReconciliationSession) ([]db.DockerReconciliationObservation, db.DockerReconciliationScanComplete, []db.DockerReconciliationOrphanCandidate, error) {
	return nil, db.DockerReconciliationScanComplete{SessionID: session.SessionID, Fence: session.Fence}, []db.DockerReconciliationOrphanCandidate{{Resource: db.DockerReconciliationCandidateVolume, Identifier: "volume", Name: "volume", Reason: db.DockerReconciliationCandidateExtra, ObservedAt: time.Now().UTC()}}, nil
}

type denyingDockerPolicyProvider struct{ dockerPolicyConsumerStub }

func (p *denyingDockerPolicyProvider) NewExecutor(db.Task, db.Template, db.Inventory, db.Repository, db.Environment, string) (tasks.Executor, error) {
	return nil, db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleImageDenied}
}

func TestJobPoolAcknowledgesDockerPolicyBeforeDispatch(t *testing.T) {
	initConfig(t)
	util.Config.Runner.Executor.Type = util.ExecutorTypeDocker
	provider := &dockerPolicyConsumerStub{}
	pool := &JobPool{provider: provider}
	assert.False(t, pool.dockerDispatchReady())
	policy := db.DefaultDockerExecutionPolicy()
	require.NoError(t, pool.applyDockerPolicy(policy))
	pool.dockerReconciliationReady = true
	assert.True(t, pool.dockerDispatchReady())
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	pool.setCommonHeaders(request)
	assert.Equal(t, "0", request.Header.Get(RunnerDockerPolicyRevisionHeader))
	assert.Equal(t, policy.Hash, request.Header.Get(RunnerDockerPolicyHashHeader))
}

func TestJobPoolSubmitsDockerScanBeforeDispatch(t *testing.T) {
	var received RunnerProgress
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "scan-session", r.Header.Get(RunnerDockerSessionHeader))
		require.Equal(t, "scan-fence", r.Header.Get(RunnerDockerFenceHeader))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	util.Config = &util.ConfigType{WebHost: server.URL, Runner: &util.RunnerConfig{Token: "test-token", Executor: &util.ExecutorConfig{Type: util.ExecutorTypeDocker}, Connection: &util.RunnerConnectionConfig{}}}
	pool := &JobPool{provider: &dockerPolicyConsumerStub{}, runningJobs: make(map[int]*runningJob), startedAt: time.Now(), client: newHTTPClient(), dockerReconciliationSession: db.DockerReconciliationSession{SessionID: "scan-session", Fence: "scan-fence", RunnerID: 1, TargetBoot: "scan-target"}}
	assert.True(t, pool.sendProgress())
	require.NotNil(t, received.DockerReconciliationScanComplete)
	assert.Equal(t, "scan-session", received.DockerReconciliationScanComplete.SessionID)
	assert.Len(t, received.DockerReconciliationOrphanCandidates, 1)
}

func TestJobPoolRejectsQueuedDockerJobWithSupersededPolicySnapshot(t *testing.T) {
	initConfig(t)
	util.Config.Runner.Executor.Type = util.ExecutorTypeDocker
	provider := &dockerPolicyConsumerStub{}
	pool := &JobPool{provider: provider}
	first := db.DefaultDockerExecutionPolicy()
	require.NoError(t, pool.applyDockerPolicy(first))
	ack, ready := pool.currentDockerPolicyAck()
	require.True(t, ready)
	queued := &job{dockerPolicyAck: &ack}
	assert.True(t, pool.canDispatchQueuedJob(queued))

	updated := first
	updated.Revision = 1
	require.NoError(t, updated.Canonicalize())
	require.NoError(t, pool.applyDockerPolicy(updated))
	assert.False(t, pool.canDispatchQueuedJob(queued), "a pre-update executor snapshot must never begin after a policy revision")
}

func TestJobPoolQueuesOneTerminalReportForDockerPolicyDenial(t *testing.T) {
	policy := db.DefaultDockerExecutionPolicy()
	var mu sync.Mutex
	var reports []RunnerProgress
	terminalReported := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut {
			var progress RunnerProgress
			require.NoError(t, json.NewDecoder(r.Body).Decode(&progress))
			reports = append(reports, progress)
			terminalReported = true
			w.WriteHeader(http.StatusOK)
			return
		}
		state := RunnerState{RunnerID: 1, DockerPolicy: &policy, DockerReconciliationSession: &db.DockerReconciliationSession{SessionID: "test-session", Fence: "test-fence", RunnerID: 1, TargetBoot: "test-target", Ready: true}}
		if !terminalReported {
			state.NewJobs = []JobData{{Task: db.Task{ID: 81, ProjectID: 9, AssignmentGeneration: 3}}}
		}
		require.NoError(t, json.NewEncoder(w).Encode(state))
	}))
	defer server.Close()
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	util.Config = &util.ConfigType{
		WebHost: server.URL,
		Runner:  &util.RunnerConfig{Token: "test-token", Executor: &util.ExecutorConfig{Type: util.ExecutorTypeDocker}, Connection: &util.RunnerConnectionConfig{}},
	}
	pool := &JobPool{
		runningJobs: make(map[int]*runningJob), queue: make([]*job, 0), startedAt: time.Now(), client: newHTTPClient(),
		provider: &denyingDockerPolicyProvider{},
	}
	pool.checkNewJobs()
	pool.checkNewJobs()
	assert.Equal(t, 1, pool.queueLen(), "a duplicate poll must not enqueue another rejection")
	rejected, ok := pool.dequeue()
	require.True(t, ok, "a policy denial must be reported instead of silently leaving the task starting")
	reporter := &runningJob{job: rejected.job, taskID: rejected.taskID, generation: rejected.generation, status: task_logger.TaskStartingStatus}
	rejected.job.SetLogger(reporter)
	reporter.SetStatus(task_logger.TaskRunningStatus)
	require.ErrorContains(t, rejected.job.Run("", nil, ""), db.DockerPolicyRuleImageDenied)
	reporter.SetStatus(task_logger.TaskFailStatus)
	status, logs, _, _ := reporter.getProgress()
	assert.Equal(t, task_logger.TaskFailStatus, status)
	require.Len(t, logs, 1)
	assert.Equal(t, db.DockerPolicyRuleImageDenied, logs[0].Message)
	_ = rejected.job.Run("", nil, "")
	_, logs, _, _ = reporter.getProgress()
	assert.Len(t, logs, 1, "the same delivery must not emit duplicate policy-denial logs")
	pool.addRunningJob(rejected.taskID, reporter)
	require.True(t, pool.sendProgress())
	assert.Zero(t, pool.runningJobsCount(), "a successful terminal report must leave no locally redispatchable job")
	mu.Lock()
	require.Len(t, reports, 1)
	require.Len(t, reports[0].Jobs, 1)
	assert.Equal(t, task_logger.TaskFailStatus, reports[0].Jobs[0].Status)
	mu.Unlock()
	pool.checkNewJobs()
	assert.Zero(t, pool.queueLen(), "the simulated server stops redelivery after terminal progress")
}

type confirmedStopExecutor struct {
	*tasks.LocalExecutor
	confirmation tasks.StopConfirmation
	quarantine   db.DockerReconciliationStopQuarantine
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("transport unavailable")
}

func (e *confirmedStopExecutor) ConfirmStop(context.Context) tasks.StopConfirmation {
	return e.confirmation
}

func (e *confirmedStopExecutor) DockerCancellationQuarantine() db.DockerReconciliationStopQuarantine {
	return e.quarantine
}

func TestJobPoolApplyTerminatedDockerJobRequiresConfirmedStop(t *testing.T) {
	initConfig(t)
	for _, tt := range []struct {
		name         string
		confirmation tasks.StopConfirmation
		status       task_logger.TaskStatus
		remaining    int
	}{
		{name: "confirmed", confirmation: tasks.StopConfirmed, status: task_logger.TaskStoppedStatus, remaining: 0},
		{name: "daemon still running", confirmation: tasks.StopPending, status: task_logger.TaskStoppingStatus, remaining: 1},
		{name: "daemon error quarantines", confirmation: tasks.StopQuarantined, status: task_logger.TaskStoppingStatus, remaining: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewJobPool(nil)
			executor := &confirmedStopExecutor{LocalExecutor: &tasks.LocalExecutor{Task: db.Task{ID: 7}}, confirmation: tt.confirmation, quarantine: db.DockerReconciliationStopQuarantine{ProjectID: 1, TaskID: 7, Generation: 1, Resource: db.DockerReconciliationResourceTask, ContainerName: "task-7", Reason: "daemon stop state could not be confirmed"}}
			job := &runningJob{job: executor, status: task_logger.TaskRunningStatus}
			executor.Logger = job
			pool.addRunningJob(7, job)
			pool.applyTerminatedJobs([]int{7})
			assert.Equal(t, tt.status, job.getStatus())
			assert.Equal(t, tt.remaining, pool.runningJobsCount())
			if tt.confirmation == tasks.StopQuarantined {
				_, logs, _, _ := job.getProgress()
				require.NotEmpty(t, logs)
				assert.Contains(t, logs[len(logs)-1].Message, "quarantined")
				require.Len(t, pool.dockerQuarantines, 1)
				pool.applyTerminatedJobs([]int{7})
				assert.Len(t, pool.dockerQuarantines, 1)
			}
		})
	}
}

func TestJobPoolBoundsAndAcknowledgesDockerQuarantineProgressPrefix(t *testing.T) {
	initConfig(t)
	util.Config.Runner.Token = "token"
	util.Config.Runner.Executor.Type = util.ExecutorTypeDocker
	pool := &JobPool{runningJobs: make(map[int]*runningJob), client: newHTTPClient()}
	for index := 0; index < 101; index++ {
		pool.dockerQuarantines = append(pool.dockerQuarantines, db.DockerReconciliationStopQuarantine{ProjectID: 1, TaskID: index + 1, Generation: 1, Resource: db.DockerReconciliationResourceTask, ContainerName: fmt.Sprintf("task-%d", index), Reason: "daemon stop state could not be confirmed"})
	}
	var received RunnerProgress
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		pool.dockerPolicyMu.Lock()
		pool.dockerQuarantines = append(pool.dockerQuarantines, db.DockerReconciliationStopQuarantine{ProjectID: 1, TaskID: 999, Generation: 1, Resource: db.DockerReconciliationResourceTask, ContainerName: "concurrent", Reason: "daemon stop state could not be confirmed"})
		pool.dockerPolicyMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	util.Config.WebHost = server.URL
	require.True(t, pool.sendProgress())
	assert.Len(t, received.DockerReconciliationQuarantines, 100)
	assert.Len(t, pool.dockerQuarantines, 2)
	assert.Equal(t, 101, pool.dockerQuarantines[0].TaskID)
	assert.Equal(t, 999, pool.dockerQuarantines[1].TaskID)
	pool.finishStoppedJob(&runningJob{job: &confirmedStopExecutor{LocalExecutor: &tasks.LocalExecutor{Task: db.Task{ID: 101}}, confirmation: tasks.StopQuarantined, quarantine: pool.dockerQuarantines[0]}, status: task_logger.TaskStoppingStatus})
	assert.Len(t, pool.dockerQuarantines, 2, "dedup keeps an already queued quarantine singular")
}

func TestJobPoolRetainsDockerQuarantinesAfterRejectedOrFailedProgress(t *testing.T) {
	initConfig(t)
	util.Config.Runner.Token = "token"
	util.Config.Runner.Executor.Type = util.ExecutorTypeDocker
	quarantine := db.DockerReconciliationStopQuarantine{ProjectID: 1, TaskID: 1, Generation: 1, Resource: db.DockerReconciliationResourceTask, ContainerName: "task-1", Reason: "daemon stop state could not be confirmed"}
	for _, tc := range []struct {
		name      string
		host      func(t *testing.T) string
		transport bool
	}{
		{name: "rejected", host: func(t *testing.T) string {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) }))
			t.Cleanup(server.Close)
			return server.URL
		}},
		{name: "transport error", host: func(*testing.T) string { return "http://runner.invalid" }, transport: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &JobPool{runningJobs: make(map[int]*runningJob), client: newHTTPClient(), dockerQuarantines: []db.DockerReconciliationStopQuarantine{quarantine}}
			if tc.transport {
				pool.client = &http.Client{Transport: failingRoundTripper{}}
			}
			util.Config.WebHost = tc.host(t)
			assert.False(t, pool.sendProgress())
			assert.Equal(t, []db.DockerReconciliationStopQuarantine{quarantine}, pool.dockerQuarantines)
		})
	}
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
