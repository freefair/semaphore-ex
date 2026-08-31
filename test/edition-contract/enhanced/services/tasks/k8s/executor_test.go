package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
)

type fakeKubernetesClient struct {
	mu                   sync.Mutex
	events               []string
	logStreams           []io.ReadCloser
	logsOpened           chan struct{}
	logsOpenedOnce       sync.Once
	deleteJobErrors      []error
	deleteJobError       error
	createdJob           *batchv1.Job
	createdBundle        BundleSecret
	createdNetworkPolicy *networkingv1.NetworkPolicy
	jobResult            JobResult
}

func (f *fakeKubernetesClient) CreateNetworkPolicy(_ context.Context, policy *networkingv1.NetworkPolicy) (ObjectIdentity, error) {
	f.createdNetworkPolicy = policy.DeepCopy()
	f.record("create-network-policy")
	return ObjectIdentity{Name: policy.Name, UID: types.UID("network-policy-uid")}, nil
}

func (f *fakeKubernetesClient) record(event string) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
}

func (f *fakeKubernetesClient) DeleteNetworkPolicy(_ context.Context, _ ObjectIdentity, _ map[string]string) error {
	f.record("delete-network-policy")
	return nil
}

func (f *fakeKubernetesClient) CreateBundleSecret(_ context.Context, spec BundleSecret) (ObjectIdentity, error) {
	f.createdBundle = spec
	f.record("create-secret")
	return ObjectIdentity{Name: spec.Name, UID: types.UID("secret-uid")}, nil
}

func (f *fakeKubernetesClient) CreateJob(_ context.Context, job *batchv1.Job) (ObjectIdentity, error) {
	f.createdJob = job.DeepCopy()
	f.record("create-job")
	return ObjectIdentity{Name: job.Name, UID: types.UID("job-uid")}, nil
}

func (f *fakeKubernetesClient) WaitForTaskPod(_ context.Context, _ ObjectIdentity, _ map[string]string) (PodIdentity, error) {
	f.record("wait-pod")
	return PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}, RestartCount: 0}, nil
}

func (f *fakeKubernetesClient) OpenPodLogs(_ context.Context, _ PodIdentity, _ *time.Time, _ bool) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.logStreams)
	if index == 0 {
		return nil, io.EOF
	}
	stream := f.logStreams[0]
	f.logStreams = f.logStreams[1:]
	f.events = append(f.events, fmt.Sprintf("logs-%d", index))
	if f.logsOpened != nil && len(f.logStreams) == 0 {
		f.logsOpenedOnce.Do(func() { close(f.logsOpened) })
	}
	return stream, nil
}

func (f *fakeKubernetesClient) WaitForJob(ctx context.Context, _ ObjectIdentity, _ PodIdentity) (JobResult, error) {
	f.record("wait-job")
	logsOpened := f.logsOpened
	if logsOpened != nil {
		select {
		case <-logsOpened:
		case <-ctx.Done():
			return JobResult{}, ctx.Err()
		}
	}
	return f.jobResult, nil
}

func (f *fakeKubernetesClient) DeleteJobForeground(_ context.Context, _ ObjectIdentity, _ PodIdentity, _ time.Duration) error {
	f.record("delete-job")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteJobError != nil {
		return f.deleteJobError
	}
	if len(f.deleteJobErrors) == 0 {
		return nil
	}
	err := f.deleteJobErrors[0]
	f.deleteJobErrors = f.deleteJobErrors[1:]
	return err
}

func (f *fakeKubernetesClient) DeleteBundleSecret(_ context.Context, _ ObjectIdentity) error {
	f.record("delete-secret")
	return nil
}

type errorAfterReader struct {
	reader io.Reader
	err    error
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if err == io.EOF {
		return 0, r.err
	}
	return n, err
}

func (r *errorAfterReader) Close() error { return nil }

type captureLogger struct {
	task_logger.NopLogger
	mu      sync.Mutex
	records []string
}

func (l *captureLogger) Log(message string) {
	l.mu.Lock()
	l.records = append(l.records, message)
	l.mu.Unlock()
}

func (l *captureLogger) LogWithTime(_ time.Time, message string) { l.Log(message) }

func TestKubernetesExecutorRunsJobStreamsAtLeastOnceAndCleansUIDBoundObjects(t *testing.T) {
	firstTime := "2026-08-31T10:00:00.000000000Z"
	secondTime := "2026-08-31T10:00:01.000000000Z"
	client := &fakeKubernetesClient{
		logStreams: []io.ReadCloser{
			&errorAfterReader{reader: strings.NewReader(firstTime + " first\n"), err: errors.New("transient disconnect")},
			io.NopCloser(strings.NewReader(firstTime + " first\n" + secondTime + " second\n")),
		},
		logsOpened: make(chan struct{}),
		jobResult:  JobResult{Succeeded: true, Lifecycle: "succeeded"},
	}
	logger := &captureLogger{}
	cfg := config{
		clusterAlias:          "qa",
		namespace:             "semaphore-jobs",
		serviceAccount:        "semaphore-task",
		image:                 testImage,
		helperImage:           testImage,
		pollInterval:          time.Millisecond,
		cleanupGrace:          time.Second,
		activeDeadlineSeconds: 60,
	}
	executor := newExecutorForPlan(client, cfg, testKubernetesPolicy(t, cfg), 19, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, db.Template{App: db.AppBash}, logger)
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	err := executor.runPlan(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{"first", "first", "second"}, logger.records)
	require.NotNil(t, client.createdJob)
	assert.Equal(t, testImage, client.createdJob.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, []byte("bundle"), client.createdBundle.Data)
	assert.True(t, client.createdBundle.Immutable)
	assert.Less(t, indexOf(client.events, "create-secret"), indexOf(client.events, "create-job"))
	assert.Less(t, indexOf(client.events, "create-network-policy"), indexOf(client.events, "create-job"))
	assert.NotNil(t, client.createdNetworkPolicy)
	assert.Less(t, indexOf(client.events, "delete-job"), indexOf(client.events, "delete-network-policy"))
	assert.Less(t, indexOf(client.events, "delete-network-policy"), indexOf(client.events, "delete-secret"))
	metadata := executor.ExecutorMetadata()
	assert.Equal(t, db.RunnerExecutorK8s, metadata.ExecutorType)
	assert.Equal(t, "qa", metadata.K8sClusterAlias)
	assert.Equal(t, "semaphore-jobs", metadata.K8sNamespace)
	assert.Equal(t, "semaphore-task-41-3", metadata.K8sJobName)
	assert.Equal(t, "job-uid", metadata.K8sJobUID)
	assert.Equal(t, "semaphore-task-41-3-pod", metadata.K8sPodName)
	assert.Equal(t, "pod-uid", metadata.K8sPodUID)
	assert.Equal(t, "succeeded", metadata.K8sLifecycle)
}

func TestKubernetesLogStreamPreservesIdenticalTimestampedLines(t *testing.T) {
	timestamp := "2026-08-31T10:00:00.000000000Z"
	client := &fakeKubernetesClient{logStreams: []io.ReadCloser{io.NopCloser(strings.NewReader(
		timestamp + " same\n" + timestamp + " same\n",
	))}}
	logger := &captureLogger{}
	executor := newExecutorForPlan(client, config{}, db.KubernetesExecutionPolicy{}, 19, db.Task{}, db.Template{}, logger)

	require.NoError(t, executor.streamLogsOnce(context.Background(), PodIdentity{}, newLogTracker(), false))
	assert.Equal(t, []string{"same", "same"}, logger.records)
}

func TestKubernetesLogStreamReconnectsAfterCleanFollowEOF(t *testing.T) {
	firstTime := "2026-08-31T10:00:00.000000000Z"
	secondTime := "2026-08-31T10:00:01.000000000Z"
	client := &fakeKubernetesClient{logStreams: []io.ReadCloser{
		io.NopCloser(strings.NewReader(firstTime + " first\n")),
		io.NopCloser(strings.NewReader(secondTime + " second\n")),
	}}
	logger := &captureLogger{}
	executor := newExecutorForPlan(client, config{pollInterval: time.Millisecond}, db.KubernetesExecutionPolicy{}, 19, db.Task{}, db.Template{}, logger)

	executor.streamLogs(context.Background(), PodIdentity{}, newLogTracker())

	assert.Equal(t, []string{"first", "second"}, logger.records)
}

func TestKubernetesExecutorFailsSuccessfulJobWhenCleanupCannotBeConfirmed(t *testing.T) {
	client := &fakeKubernetesClient{
		logStreams:     []io.ReadCloser{io.NopCloser(strings.NewReader(""))},
		logsOpened:     make(chan struct{}),
		jobResult:      JobResult{Succeeded: true, Lifecycle: "succeeded"},
		deleteJobError: errors.New("pod still running"),
	}
	cfg := config{
		clusterAlias: "qa", namespace: "semaphore-jobs", serviceAccount: "semaphore-task",
		image: testImage, helperImage: testImage, pollInterval: time.Millisecond,
		cleanupGrace: time.Second, activeDeadlineSeconds: 60,
	}
	executor := newExecutorForPlan(client, cfg, testKubernetesPolicy(t, cfg), 19, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, db.Template{App: db.AppBash}, &captureLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	err := executor.runPlan(context.Background(), plan)

	require.ErrorContains(t, err, "cleaning Kubernetes task objects")
	assert.Equal(t, "failed", executor.ExecutorMetadata().K8sLifecycle)
	assert.Equal(t, "CleanupFailed", executor.ExecutorMetadata().K8sTerminalReason)
	assert.NotContains(t, client.events, "delete-secret")
}

func TestKubernetesExecutorDoesNotDeleteSecretBeforeUIDBoundJobDeletion(t *testing.T) {
	client := &fakeKubernetesClient{deleteJobErrors: []error{errors.New("pod still running"), nil}}
	executor := newExecutorForPlan(client, config{cleanupGrace: time.Second}, db.KubernetesExecutionPolicy{}, 19, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, db.Template{}, task_logger.NopLogger{})
	executor.secret = ObjectIdentity{Name: "bundle", UID: types.UID("secret-uid")}
	executor.job = ObjectIdentity{Name: "job", UID: types.UID("job-uid")}
	executor.pod = PodIdentity{ObjectIdentity: ObjectIdentity{Name: "pod", UID: types.UID("pod-uid")}}

	assert.Equal(t, tasks.StopPending, executor.ConfirmStop(context.Background()))
	assert.NotContains(t, client.events, "delete-secret")
	assert.Equal(t, tasks.StopConfirmed, executor.ConfirmStop(context.Background()))
	assert.Equal(t, []string{"delete-job", "delete-job", "delete-secret"}, client.events)
}

func TestKubernetesExecutorRejectsOversizedBundleBeforeCreatingObjects(t *testing.T) {
	client := &fakeKubernetesClient{}
	executor := newExecutorForPlan(client, config{}, db.KubernetesExecutionPolicy{}, 19, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, db.Template{}, task_logger.NopLogger{})
	plan := &tasks.ContainerTaskPlan{Bundle: io.NopCloser(io.LimitReader(strings.NewReader(strings.Repeat("x", maxBundleBytes+1)), maxBundleBytes+1))}

	err := executor.runPlan(context.Background(), plan)

	require.ErrorContains(t, err, "bundle exceeds")
	assert.Empty(t, client.events)
}

func indexOf(values []string, expected string) int {
	for index, value := range values {
		if value == expected {
			return index
		}
	}
	return -1
}
