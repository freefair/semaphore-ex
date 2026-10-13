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
	workflowtasks "github.com/semaphoreui/semaphore/pro/services/tasks"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
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
	workflowExit         int32
	workflowWaitErr      error
	workflowPreflightErr error
	outputStages         []outputStage
	stageOutput          map[outputStage][]byte
	stageError           map[outputStage]error
}

func (f *fakeKubernetesClient) validateOutputExecAccess(context.Context) error {
	f.record("validate-output-exec")
	return f.workflowPreflightErr
}

func (f *fakeKubernetesClient) WaitForTaskCompletion(context.Context, ObjectIdentity, PodIdentity, map[string]string) (int32, error) {
	return f.workflowExit, f.workflowWaitErr
}

func (f *fakeKubernetesClient) execOutputStage(_ context.Context, _ ObjectIdentity, _ PodIdentity, _ map[string]string, stage outputStage, stdout io.Writer) error {
	f.outputStages = append(f.outputStages, stage)
	if err := f.stageError[stage]; err != nil {
		return err
	}
	if output := f.stageOutput[stage]; len(output) != 0 {
		_, err := stdout.Write(output)
		return err
	}
	return nil
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

func TestKubernetesWorkflowWithoutCollectorFailsClosedAfterAcknowledgingSidecar(t *testing.T) {
	client := &fakeKubernetesClient{workflowExit: 0}
	executor := newExecutorForPlan(client, config{}, db.KubernetesExecutionPolicy{}, 1, db.Task{}, db.Template{}, task_logger.NopLogger{})
	executor.job = ObjectIdentity{Name: "job", UID: types.UID("job-uid")}

	_, err := executor.collectWorkflowOutputs(context.Background(), PodIdentity{ObjectIdentity: ObjectIdentity{Name: "pod", UID: types.UID("pod-uid")}}, map[string]string{"io.semaphore.managed": "v1"})

	require.ErrorContains(t, err, "collector is unavailable")
	assert.Equal(t, []outputStage{acknowledgeOutputStage}, client.outputStages)
}

func TestKubernetesExecutorRejectsWorkflowOutputPermissionBeforeCreatingTaskObjects(t *testing.T) {
	configureKubernetesOutputsTest(t)
	local := newKubernetesOutputsLocalExecutor(t, db.AppBash, &kubernetesOutputsCapture{})
	client := &fakeKubernetesClient{workflowPreflightErr: db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied}}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppBash)
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, WorkflowOutputs: true, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.ErrorIs(t, err, db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied})
	assert.Equal(t, []string{"validate-output-exec"}, client.events)
	assert.Nil(t, client.createdJob)
	assert.Empty(t, client.createdBundle.Name)
}

func TestKubernetesExecutorCollectsProcessAndTerraformOutputsBeforeAcknowledgement(t *testing.T) {
	configureKubernetesOutputsTest(t)
	document := `{"values":{"file":"file-wins","terraform_public":"public-value"},"skipped":{"terraform_secret":"sensitive"}}`
	capture := &kubernetesOutputsCapture{document: &document}
	local := newKubernetesOutputsLocalExecutor(t, db.AppTerraform, capture)
	client := &fakeKubernetesClient{
		jobResult: JobResult{Succeeded: true, Lifecycle: "succeeded"},
		stageOutput: map[outputStage][]byte{
			collectOutputStage:   []byte(`{"values":{"file":"process-value","process":"kept"}}`),
			terraformOutputStage: []byte(`{"file":{"value":"terraform-value","sensitive":false},"terraform_public":{"value":"public-value","sensitive":false},"terraform_secret":{"value":"tf-secret-value","sensitive":true}}`),
		},
	}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppTerraform)
	plan := &tasks.ContainerTaskPlan{App: db.AppTerraform, WorkflowOutputs: true, Terraform: tasks.ContainerTerraformPlan{AutoApprove: true}, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []byte(`{"values":{"file":"process-value","process":"kept"}}`), capture.process)
	assert.Contains(t, string(capture.terraform), `"tf-secret-value"`)
	require.NotNil(t, executor.Outputs())
	assert.Equal(t, document, *executor.Outputs())
	assert.NotContains(t, *executor.Outputs(), "tf-secret-value")
	assert.Equal(t, []outputStage{collectOutputStage, terraformOutputStage, acknowledgeOutputStage}, client.outputStages)
}

func TestKubernetesExecutorPassesLargeSensitiveTerraformOutputToActualCollector(t *testing.T) {
	configureKubernetesOutputsTest(t)
	workflowID := 1
	local := &tasks.LocalExecutor{
		Task:             db.Task{ID: 41, WorkflowRunID: &workflowID},
		Template:         db.Template{ProjectID: 7, App: db.AppTerraform},
		Logger:           task_logger.NopLogger{},
		OutputsCollector: workflowtasks.NewTaskOutputsCollector(),
	}
	local.SetStatus(task_logger.TaskRunningStatus)
	_, err := local.BeginWorkflowOutputs(nil)
	require.NoError(t, err)
	largeSensitiveValue := strings.Repeat("sensitive-marker-", (maxContainerOutputsBytes/len("sensitive-marker-"))+1)
	terraform := fmt.Sprintf(`{"large_secret":{"value":%q,"sensitive":true},"safe":{"value":"kept","sensitive":false}}`, largeSensitiveValue)
	client := &fakeKubernetesClient{
		jobResult: JobResult{Succeeded: true, Lifecycle: "succeeded"},
		stageOutput: map[outputStage][]byte{
			collectOutputStage:   []byte(`{}`),
			terraformOutputStage: []byte(terraform),
		},
	}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppTerraform)
	plan := &tasks.ContainerTaskPlan{App: db.AppTerraform, WorkflowOutputs: true, Terraform: tasks.ContainerTerraformPlan{AutoApprove: true}, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err = executor.runPlan(context.Background(), plan)

	require.NoError(t, err)
	require.NotNil(t, executor.Outputs())
	assert.JSONEq(t, `{"values":{"safe":"kept"},"skipped":{"large_secret":"sensitive"}}`, *executor.Outputs())
	assert.NotContains(t, *executor.Outputs(), "sensitive-marker")
	assert.Greater(t, len(client.stageOutput[terraformOutputStage]), maxContainerOutputsBytes)
	assert.Equal(t, []outputStage{collectOutputStage, terraformOutputStage, acknowledgeOutputStage}, client.outputStages)
}

func TestKubernetesExecutorSkipsFailedTerraformOutputRetrievalWithoutFailingTask(t *testing.T) {
	configureKubernetesOutputsTest(t)
	logger := &captureLogger{}
	local := newKubernetesOutputsLocalExecutor(t, db.AppTerraform, &kubernetesOutputsCapture{document: stringPointer(`{"values":{"process":"kept"}}`)})
	client := &fakeKubernetesClient{
		jobResult:   JobResult{Succeeded: true, Lifecycle: "succeeded"},
		stageOutput: map[outputStage][]byte{collectOutputStage: []byte(`{"process":"kept"}`)},
		stageError:  map[outputStage]error{terraformOutputStage: errors.New("raw secret marker")},
	}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppTerraform)
	executor.SetLogger(logger)
	plan := &tasks.ContainerTaskPlan{App: db.AppTerraform, WorkflowOutputs: true, Terraform: tasks.ContainerTerraformPlan{AutoApprove: true}, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.NoError(t, err)
	assert.Equal(t, []string{"Terraform outputs were not captured"}, logger.records)
	assert.Equal(t, []outputStage{collectOutputStage, terraformOutputStage, acknowledgeOutputStage}, client.outputStages)
	assert.NotContains(t, strings.Join(logger.records, "\n"), "secret")
}

func TestKubernetesExecutorDoesNotCollectOutputsWhenTaskFails(t *testing.T) {
	configureKubernetesOutputsTest(t)
	capture := &kubernetesOutputsCapture{document: stringPointer(`{"values":{"unexpected":"value"}}`)}
	local := newKubernetesOutputsLocalExecutor(t, db.AppBash, capture)
	client := &fakeKubernetesClient{workflowExit: 1, jobResult: JobResult{Succeeded: false, Lifecycle: "failed", Reason: "NonZeroExit"}}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppBash)
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, WorkflowOutputs: true, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.ErrorContains(t, err, "Kubernetes Job failed: NonZeroExit")
	assert.Nil(t, executor.Outputs())
	assert.Empty(t, capture.process)
	assert.Empty(t, capture.terraform)
	assert.Equal(t, []outputStage{acknowledgeOutputStage}, client.outputStages)
	assert.Contains(t, client.events, "wait-job")
}

func TestKubernetesExecutorRejectsMalformedOutputsBeforeAcknowledgementAndCleansUp(t *testing.T) {
	configureKubernetesOutputsTest(t)
	capture := &kubernetesOutputsCapture{err: errors.New("outputs document is malformed")}
	local := newKubernetesOutputsLocalExecutor(t, db.AppBash, capture)
	client := &fakeKubernetesClient{
		jobResult:   JobResult{Succeeded: true, Lifecycle: "succeeded"},
		stageOutput: map[outputStage][]byte{collectOutputStage: []byte(`not-json`)},
	}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppBash)
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, WorkflowOutputs: true, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.ErrorContains(t, err, "validating Kubernetes workflow outputs")
	assert.Nil(t, executor.Outputs())
	assert.Equal(t, []byte("not-json"), capture.process)
	assert.Equal(t, []outputStage{collectOutputStage}, client.outputStages)
	assert.Contains(t, client.events, "delete-job")
	assert.Contains(t, client.events, "delete-secret")
	assert.NotContains(t, client.events, "wait-job")
}

func TestKubernetesExecutorCancellationDoesNotPublishOutputs(t *testing.T) {
	configureKubernetesOutputsTest(t)
	capture := &kubernetesOutputsCapture{document: stringPointer(`{"values":{"unexpected":"value"}}`)}
	local := newKubernetesOutputsLocalExecutor(t, db.AppBash, capture)
	client := &fakeKubernetesClient{workflowWaitErr: context.Canceled}
	executor := newKubernetesOutputsExecutor(t, client, local, db.AppBash)
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, WorkflowOutputs: true, Bundle: io.NopCloser(strings.NewReader("bundle"))}

	err := executor.runPlan(context.Background(), plan)

	require.NoError(t, err)
	assert.Nil(t, executor.Outputs())
	assert.Empty(t, client.outputStages)
	assert.Empty(t, capture.process)
	assert.Empty(t, capture.terraform)
	assert.NotEqual(t, "succeeded", executor.ExecutorMetadata().K8sLifecycle)
}

func TestKubernetesTerraformTaskCommandDoesNotOverwriteCapturedOutput(t *testing.T) {
	plan := &tasks.ContainerTaskPlan{App: db.AppTerraform, WorkflowOutputs: true, Terraform: tasks.ContainerTerraformPlan{AutoApprove: true}}

	command, err := kubernetesTaskCommand(plan)

	require.NoError(t, err)
	assert.NotContains(t, command[2], "terraform-outputs >")
	assert.NotContains(t, command[2], "terraform-outputs.json")
	assert.Contains(t, command[2], "run_stage plan")
	assert.Contains(t, command[2], "/semaphore/bundle/run.sh apply")
	assert.Contains(t, command[2], "forward_stage_signal")
	assert.Contains(t, command[2], "exec /bin/sh /semaphore/bundle/run.sh apply")
}

type kubernetesOutputsCollector struct {
	capture *kubernetesOutputsCapture
}

func (c *kubernetesOutputsCollector) Begin(string, int) (pro_interfaces.TaskOutputsCapture, error) {
	return c.capture, nil
}

func (c *kubernetesOutputsCollector) Validate(document string) (*string, error) {
	return &document, nil
}

type kubernetesOutputsCapture struct {
	document  *string
	err       error
	process   []byte
	terraform []byte
}

func (*kubernetesOutputsCapture) Env() []string { return nil }

func (*kubernetesOutputsCapture) AnsibleCallbackDir() (string, error) { return "", nil }

func (c *kubernetesOutputsCapture) Collect([]byte) (*string, []string, error) {
	return c.document, nil, c.err
}

func (c *kubernetesOutputsCapture) CollectContent(process []byte, terraform []byte) (*string, []string, error) {
	c.process = append([]byte(nil), process...)
	c.terraform = append([]byte(nil), terraform...)
	return c.document, nil, c.err
}

func (*kubernetesOutputsCapture) Close() {}

func configureKubernetesOutputsTest(t *testing.T) {
	t.Helper()
	previous := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previous })
}

func newKubernetesOutputsLocalExecutor(t *testing.T, app db.TemplateApp, capture *kubernetesOutputsCapture) *tasks.LocalExecutor {
	t.Helper()
	workflowID := 1
	local := &tasks.LocalExecutor{
		Task:             db.Task{ID: 41, WorkflowRunID: &workflowID},
		Template:         db.Template{ProjectID: 7, App: app},
		Logger:           task_logger.NopLogger{},
		OutputsCollector: &kubernetesOutputsCollector{capture: capture},
	}
	local.SetStatus(task_logger.TaskRunningStatus)
	_, err := local.BeginWorkflowOutputs(nil)
	require.NoError(t, err)
	return local
}

func newKubernetesOutputsExecutor(t *testing.T, client *fakeKubernetesClient, local *tasks.LocalExecutor, app db.TemplateApp) *KubernetesExecutor {
	t.Helper()
	cfg := config{clusterAlias: "qa", namespace: "semaphore-jobs", serviceAccount: "semaphore-task", image: testImage, helperImage: testImage, pollInterval: time.Millisecond, cleanupGrace: time.Second, activeDeadlineSeconds: 60}
	executor := newExecutorForPlan(client, cfg, testKubernetesPolicy(t, cfg), 19, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, db.Template{App: app}, task_logger.NopLogger{})
	executor.local = local
	return executor
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

func TestKubernetesTelemetryQueueFencesAcknowledgementsAndAccountsDrops(t *testing.T) {
	provider := newProviderWithClient(config{clusterAlias: "qa", namespace: "semaphore-jobs"}, &fakeKubernetesClient{})
	require.NoError(t, provider.ApplyRunnerIdentity(19))
	first := db.KubernetesReconciliationSession{SessionID: "session-one", Fence: "fence-one", RunnerID: 19, ClusterAlias: "qa", Namespace: "semaphore-jobs"}
	require.NoError(t, provider.ApplyKubernetesReconciliationSession(first))
	for index := 0; index < 101; index++ {
		provider.recordKubernetesTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryDenial, PolicyRule: db.KubernetesPolicyRuleRBACDenied})
	}
	pending := provider.PendingKubernetesTelemetry()
	require.Len(t, pending.Events, 100)
	provider.AcknowledgeKubernetesTelemetry(db.KubernetesTelemetryAck{SessionID: "stale", HighestSequence: 100})
	assert.Equal(t, pending, provider.PendingKubernetesTelemetry(), "a wrong session acknowledgement cannot drop telemetry")
	provider.AcknowledgeKubernetesTelemetry(db.KubernetesTelemetryAck{SessionID: first.SessionID, HighestSequence: 100})
	recovered := provider.PendingKubernetesTelemetry()
	require.Len(t, recovered.Events, 1)
	assert.Equal(t, db.KubernetesTelemetryDrop, recovered.Events[0].Kind)
	assert.Equal(t, int64(1), recovered.Events[0].Count)
	second := first
	second.SessionID, second.Fence, second.TelemetryHighestSequence = "session-two", "fence-two", 7
	require.NoError(t, provider.ApplyKubernetesReconciliationSession(second))
	assert.Empty(t, provider.PendingKubernetesTelemetry().Events)
	provider.recordKubernetesTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryOrphan, Count: 1})
	assert.Equal(t, int64(8), provider.PendingKubernetesTelemetry().Events[0].Sequence)
}

func indexOf(values []string, expected string) int {
	for index, value := range values {
		if value == expected {
			return index
		}
	}
	return -1
}
