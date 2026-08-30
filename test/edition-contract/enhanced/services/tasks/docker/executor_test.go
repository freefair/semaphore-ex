package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveConfigAppliesDocumentedDefaultsAndValidatesInputs(t *testing.T) {
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	assert.Equal(t, "semaphoreui/job:latest", config.image)
	assert.Equal(t, "semaphoreui/helper:latest", config.helperImage)
	assert.Equal(t, "none", config.network)
	assert.Equal(t, PullIfNotPresent, config.pullPolicy)
	assert.Equal(t, 2*time.Second, config.pollInterval)
	assert.Equal(t, 30*time.Second, config.cleanupGrace)

	_, err = effectiveConfig(util.RunnerDockerConfig{Image: "https://registry.invalid/secret/image"})
	assert.ErrorIs(t, err, db.ErrExecutorImageInvalid)
	_, err = effectiveConfig(util.RunnerDockerConfig{PullPolicy: "sometimes"})
	assert.ErrorContains(t, err, "pull policy")
	_, err = effectiveConfig(util.RunnerDockerConfig{MemoryLimit: "many"})
	assert.ErrorContains(t, err, "memory limit")
}

func TestDockerExecutorRunsFixedStagesInsideTaskScopedResources(t *testing.T) {
	client := &fakeDockerClient{execExitCodes: []int{0, 0}}
	config, err := effectiveConfig(util.RunnerDockerConfig{
		Image:       "runner.example/job:stable",
		HelperImage: "runner.example/helper:stable",
		Network:     "runner-network",
		CPULimit:    1.5,
		MemoryLimit: "512m",
	})
	require.NoError(t, err)
	logger := &recordingLogger{}
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 42, ProjectID: 9}, db.Template{
		ExecutorImage: stringPointer("project.example/job:frozen"),
	}, logger)
	plan := &tasks.ContainerTaskPlan{
		App:    db.AppBash,
		Bundle: io.NopCloser(bytes.NewReader([]byte("bundle-secret"))),
	}

	require.NoError(t, executor.runContainerPlan(context.Background(), plan))

	assert.Equal(t, []imagePreparation{
		{image: "runner.example/helper:stable", policy: PullIfNotPresent},
		{image: "project.example/job:frozen", policy: PullIfNotPresent},
	}, client.images)
	require.Len(t, client.containers, 2)
	helper := client.containers[0]
	task := client.containers[1]
	assert.Equal(t, "runner.example/helper:stable", helper.Image)
	assert.Equal(t, "none", helper.Network)
	assert.False(t, helper.Privileged)
	assert.Empty(t, helper.BindMounts)
	assert.Empty(t, helper.Tmpfs)
	require.Len(t, helper.VolumeMounts, 1)
	assert.False(t, helper.VolumeMounts[0].ReadOnly)
	assert.Equal(t, "project.example/job:frozen", task.Image)
	assert.Equal(t, "65534:0", task.User)
	assert.Equal(t, "none", task.Network)
	assert.Equal(t, int64(1_000_000_000), task.NanoCPUs)
	assert.Equal(t, int64(512*1024*1024), task.Memory)
	assert.Equal(t, int64(256), task.PidsLimit)
	assert.True(t, task.ReadOnlyRootFS)
	assert.True(t, task.NoNewPrivileges)
	assert.True(t, task.DropAllCapabilities)
	assert.True(t, task.PrivateNamespaces)
	assert.Empty(t, task.Environment)
	assert.Empty(t, task.BindMounts)
	assert.Contains(t, task.Tmpfs, "/workspace")
	assert.Contains(t, task.Tmpfs, "/tmp")
	assert.Contains(t, task.Tmpfs, "/home/semaphore")
	require.Len(t, task.VolumeMounts, 1)
	assert.Equal(t, "/semaphore/bundle", task.VolumeMounts[0].Target)
	assert.True(t, task.VolumeMounts[0].ReadOnly)
	assert.NotContains(t, task.Name, "bundle-secret")
	assert.Equal(t, "42", task.Labels[labelTaskID])
	assert.Equal(t, "9", task.Labels[labelProjectID])
	assert.Equal(t, "boot-abc", task.Labels[labelRunnerBoot])

	assert.Equal(t, "/semaphore/bundle", client.copyDestination)
	assert.Equal(t, "bundle-secret", string(client.copiedArchive))
	assert.Equal(t, [][]string{
		{"/bin/sh", "/semaphore/bundle/run.sh", "bootstrap"},
		{"/bin/sh", "/semaphore/bundle/run.sh", "run"},
	}, client.execCommands)
	assert.Equal(t, []string{"helper-1", "task-2"}, client.removedContainers)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
	assert.Contains(t, logger.logs, "stage output")
	assert.Equal(t, db.RunnerExecutorMetadata{
		ExecutorType:   db.RunnerExecutorDocker,
		ContainerID:    "task-2",
		ContainerName:  "semaphore-task-42-g0-boot-abc",
		RequestedImage: "project.example/job:frozen",
		ResolvedImage:  "project.example/job:frozen",
		PolicyHash:     db.DefaultDockerExecutionPolicy().Hash,
		NanoCPUs:       1_000_000_000,
		MemoryBytes:    512 * 1024 * 1024,
		PidsLimit:      256,
	}, executor.ExecutorMetadata(), "cleanup must retain immutable task runtime identity")
}

func TestDockerExecutorCleansResourcesAfterStageFailure(t *testing.T) {
	client := &fakeDockerClient{execExitCodes: []int{0, 17}}
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 7, ProjectID: 2}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	err = executor.runContainerPlan(context.Background(), plan)
	require.Error(t, err)
	assert.ErrorContains(t, err, "run")
	assert.ErrorContains(t, err, "17")
	assert.Equal(t, []string{"helper-1", "task-2"}, client.removedContainers)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
}

func TestDockerExecutorStopsThenKillsAndCleansOnCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &fakeDockerClient{execStarted: started, execRelease: release, stopErr: errors.New("stop failed")}
	config, err := effectiveConfig(util.RunnerDockerConfig{CleanupGraceSeconds: 4})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 11, ProjectID: 3}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	done := make(chan error, 1)
	go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
	<-started
	executor.Kill()
	close(release)
	require.NoError(t, <-done)

	assert.Equal(t, []stopCall{{containerID: "task-2", grace: 4 * time.Second}}, client.stops)
	assert.Equal(t, []string{"task-2"}, client.killedContainers)
	assert.Equal(t, []string{"helper-1", "task-2"}, client.removedContainers)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
}

func TestDockerExecutorCancelsBlockedBundleCopyBeforeCreatingTaskContainer(t *testing.T) {
	copyStarted := make(chan struct{})
	copyRelease := make(chan struct{})
	client := &fakeDockerClient{copyStarted: copyStarted, copyRelease: copyRelease}
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 13, ProjectID: 3}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	done := make(chan error, 1)
	go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
	<-copyStarted
	executor.Kill()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		close(copyRelease)
		<-done
		t.Fatal("cancellation did not interrupt the blocked bundle copy")
	}

	assert.Len(t, client.containers, 1, "a canceled task must not create its task container")
	assert.Empty(t, client.execCommands, "a canceled task must not execute bootstrap")
	assert.Equal(t, []string{"helper-1"}, client.removedContainers)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
}

func TestDockerExecutorCancelsDuringTaskStartWithoutExecutingBootstrap(t *testing.T) {
	startStarted := make(chan struct{})
	startRelease := make(chan struct{})
	client := &fakeDockerClient{startTaskStarted: startStarted, startTaskRelease: startRelease}
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 14, ProjectID: 3}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	done := make(chan error, 1)
	go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
	<-startStarted
	executor.Kill()
	close(startRelease)

	require.NoError(t, <-done)
	assert.Empty(t, client.execCommands, "a cancellation during start must prevent bootstrap execution")
	assert.Equal(t, []string{"helper-1", "task-2"}, client.removedContainers)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
}

func TestDockerExecutorCleansTaskContainerCreatedBeforeCanceledCreateResponse(t *testing.T) {
	taskCreateStarted := make(chan struct{})
	client := &fakeDockerClient{taskCreateStarted: taskCreateStarted, taskCreateReturnsCanceled: true}
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 15, ProjectID: 3}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}

	done := make(chan error, 1)
	go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
	<-taskCreateStarted
	executor.Kill()

	require.NoError(t, <-done)
	assert.Empty(t, client.execCommands, "a task with a canceled create response must never execute bootstrap")
	assert.Equal(t,
		[]string{"helper-1", "semaphore-task-15-g0-boot-abc"},
		client.removedContainers,
		"cleanup must remove the task by its runner-owned name when Docker lost its create response",
	)
	assert.Equal(t, []string{"volume-1"}, client.removedVolumes)
}

func TestDockerExecutorHandlesTerraformConfirmationStages(t *testing.T) {
	client := &fakeDockerClient{execExitCodes: []int{0, 2, 0}}
	config, err := effectiveConfig(util.RunnerDockerConfig{})
	require.NoError(t, err)
	executor := newDockerExecutorForPlan(client, config, "boot-abc", db.Task{ID: 12, ProjectID: 3}, db.Template{}, &recordingLogger{})
	plan := &tasks.ContainerTaskPlan{
		App:       db.AppTerraform,
		Bundle:    io.NopCloser(bytes.NewReader([]byte("bundle"))),
		Terraform: tasks.ContainerTerraformPlan{AutoApprove: true},
	}

	require.NoError(t, executor.runContainerPlan(context.Background(), plan))
	assert.Equal(t, [][]string{
		{"/bin/sh", "/semaphore/bundle/run.sh", "bootstrap"},
		{"/bin/sh", "/semaphore/bundle/run.sh", "plan"},
		{"/bin/sh", "/semaphore/bundle/run.sh", "apply"},
	}, client.execCommands)
}

type fakeDockerClient struct {
	mu                        sync.Mutex
	images                    []imagePreparation
	containers                []ContainerSpec
	copyDestination           string
	copiedArchive             []byte
	copyStarted               chan struct{}
	copyRelease               chan struct{}
	execCommands              [][]string
	execExitCodes             []int
	execStarted               chan struct{}
	execRelease               chan struct{}
	startTaskStarted          chan struct{}
	startTaskRelease          chan struct{}
	taskCreateStarted         chan struct{}
	taskCreateReturnsCanceled bool
	stopErr                   error
	inspectState              *ContainerState
	inspectErr                error
	inspectStates             map[string]ContainerState
	inspectCalls              int
	listCalls                 int
	stops                     []stopCall
	killedContainers          []string
	removedContainers         []string
	removedVolumes            []string
}

func (c *fakeDockerClient) InspectContainer(_ context.Context, containerID string) (ContainerState, error) {
	c.inspectCalls++
	if c.inspectErr != nil {
		return ContainerState{}, c.inspectErr
	}
	if state, ok := c.inspectStates[containerID]; ok {
		return state, nil
	}
	if c.inspectState != nil {
		return *c.inspectState, nil
	}
	for _, killed := range c.killedContainers {
		if killed == containerID {
			return ContainerState{Exists: true, Running: false}, nil
		}
	}
	return ContainerState{Exists: false}, nil
}

func TestDockerExecutorConfirmStopRequiresDaemonEvidence(t *testing.T) {
	for _, tt := range []struct {
		name       string
		stopErr    error
		state      ContainerState
		inspectErr error
		expected   tasks.StopConfirmation
	}{
		{name: "non-running confirms", state: ContainerState{Exists: true, Running: false}, expected: tasks.StopConfirmed},
		{name: "running remains pending", state: ContainerState{Exists: true, Running: true}, expected: tasks.StopPending},
		{name: "daemon error quarantines", stopErr: errors.New("daemon unavailable"), inspectErr: errors.New("inspect unavailable"), expected: tasks.StopQuarantined},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeDockerClient{stopErr: tt.stopErr, inspectErr: tt.inspectErr, inspectState: &tt.state}
			executor := newDockerExecutorForPlan(client, config{cleanupGrace: time.Second}, "boot", db.Task{ID: 1, ProjectID: 1, AssignmentGeneration: 1}, db.Template{}, task_logger.NopLogger{})
			executor.setContainerID("task")
			assert.Equal(t, tt.expected, executor.ConfirmStop(context.Background()))
		})
	}
}

func (c *fakeDockerClient) ListManagedResources(context.Context, int) ([]ManagedResource, error) {
	c.listCalls++
	return nil, nil
}

type imagePreparation struct {
	image  string
	policy PullPolicy
}

type stopCall struct {
	containerID string
	grace       time.Duration
}

func (c *fakeDockerClient) ResolveImage(_ context.Context, image string, _ ImageRole, _ db.DockerExecutionPolicy) (ResolvedImage, error) {
	c.images = append(c.images, imagePreparation{image: image, policy: PullIfNotPresent})
	return ResolvedImage{RequestedReference: image, ResolvedReference: image, Digest: "@sha256:test", Source: "fake", SizeBytes: 1}, nil
}

func (c *fakeDockerClient) CreateVolume(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "volume-1", nil
}

func (c *fakeDockerClient) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	c.containers = append(c.containers, spec)
	if len(c.containers) == 1 {
		return "helper-1", nil
	}
	if c.taskCreateReturnsCanceled {
		close(c.taskCreateStarted)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "task-2", nil
}

func (c *fakeDockerClient) StartContainer(ctx context.Context, containerID string) error {
	if containerID == "task-2" && c.startTaskStarted != nil {
		close(c.startTaskStarted)
		select {
		case <-c.startTaskRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *fakeDockerClient) CopyArchive(ctx context.Context, _ string, destination string, archive io.Reader) error {
	c.copyDestination = destination
	var err error
	c.copiedArchive, err = io.ReadAll(archive)
	if c.copyStarted != nil {
		close(c.copyStarted)
		select {
		case <-c.copyRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (c *fakeDockerClient) Exec(_ context.Context, _ string, command []string, stdout io.Writer, _ io.Writer) (int, error) {
	c.mu.Lock()
	c.execCommands = append(c.execCommands, slices.Clone(command))
	index := len(c.execCommands) - 1
	started := c.execStarted
	release := c.execRelease
	c.mu.Unlock()
	_, _ = stdout.Write([]byte("stage output\n"))
	if index == 0 && started != nil {
		close(started)
		<-release
	}
	if index < len(c.execExitCodes) {
		return c.execExitCodes[index], nil
	}
	return 0, nil
}

func (c *fakeDockerClient) StopContainer(_ context.Context, containerID string, grace time.Duration) error {
	c.stops = append(c.stops, stopCall{containerID: containerID, grace: grace})
	return c.stopErr
}

func (c *fakeDockerClient) KillContainer(_ context.Context, containerID string) error {
	c.killedContainers = append(c.killedContainers, containerID)
	return nil
}

func (c *fakeDockerClient) RemoveContainer(_ context.Context, containerID string) error {
	c.removedContainers = append(c.removedContainers, containerID)
	return nil
}

func (c *fakeDockerClient) RemoveVolume(_ context.Context, volume string) error {
	c.removedVolumes = append(c.removedVolumes, volume)
	return nil
}

type recordingLogger struct {
	task_logger.NopLogger
	mu       sync.Mutex
	logs     []string
	statuses []task_logger.TaskStatus
}

func (l *recordingLogger) Log(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, message)
}

func (l *recordingLogger) SetStatus(status task_logger.TaskStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statuses = append(l.statuses, status)
}

func stringPointer(value string) *string { return &value }
