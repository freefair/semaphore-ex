package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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

func TestDockerExecutorEmitsBoundedImageAndResourceTelemetry(t *testing.T) {
	client := &fakeDockerClient{execExitCodes: []int{0, 0}, resourceUsage: DockerResourceUsage{CPUUsageNanoseconds: 9, MemoryBytes: 10, PIDs: 1}}
	cfg, err := effectiveConfig(util.RunnerDockerConfig{Image: "runner.example/job:stable", HelperImage: "runner.example/helper:stable"})
	require.NoError(t, err)
	var events []db.DockerTelemetryEvent
	executor := newDockerExecutorForPlan(client, cfg, "boot", db.Task{ID: 88, ProjectID: 9}, db.Template{}, &recordingLogger{})
	executor.recordTelemetry = func(event db.DockerTelemetryEvent) { events = append(events, event) }
	plan := &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader([]byte("bundle")))}
	require.NoError(t, executor.runContainerPlan(context.Background(), plan))
	imageRoles := make(map[db.DockerTelemetryRole]db.DockerTelemetryPullSource)
	for _, event := range events {
		if event.Kind == db.DockerTelemetryImagePull {
			imageRoles[event.Role] = event.PullSource
		}
	}
	assert.Equal(t, db.DockerTelemetryPullLocal, imageRoles[db.DockerTelemetryRoleHelper])
	assert.Equal(t, db.DockerTelemetryPullLocal, imageRoles[db.DockerTelemetryRoleTask])
	assert.Contains(t, events, db.DockerTelemetryEvent{Kind: db.DockerTelemetryResourceUsage, Role: db.DockerTelemetryRoleHelper, CPUUsageNanoseconds: 9, MemoryBytes: 10, PIDs: 1})
	assert.Contains(t, events, db.DockerTelemetryEvent{Kind: db.DockerTelemetryResourceUsage, Role: db.DockerTelemetryRoleTask, CPUUsageNanoseconds: 9, MemoryBytes: 10, PIDs: 1})
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
	managedResources          []ManagedResource
	volumeReferenced          bool
	resourceUsage             DockerResourceUsage
	resourceUsageErr          error
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

func TestDockerTelemetryQueueRetainsUnacknowledgedTailAndResetsPerSession(t *testing.T) {
	provider := &Provider{runnerID: 8, policy: db.DefaultDockerExecutionPolicy()}
	first := db.DockerReconciliationSession{SessionID: "session-one", Fence: "fence-one", RunnerID: 8, TargetBoot: "boot-one"}
	require.NoError(t, provider.ApplyDockerReconciliationSession(first))
	provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryPolicyDenial, PolicyRule: db.DockerPolicyRuleImageDenied})
	provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryCleanupFailure, CleanupResource: db.DockerTelemetryCleanupTask})
	pending := provider.PendingDockerTelemetry()
	require.Len(t, pending.Events, 2)
	assert.Equal(t, int64(1), pending.Events[0].Sequence)
	provider.AcknowledgeDockerTelemetry(db.DockerTelemetryAck{HighestSequence: 1})
	pending = provider.PendingDockerTelemetry()
	require.Len(t, pending.Events, 1, "unacknowledged tail must survive a partial response")
	assert.Equal(t, int64(2), pending.Events[0].Sequence)
	second := first
	second.SessionID, second.Fence, second.TargetBoot, second.TelemetryHighestSequence = "session-two", "fence-two", "boot-two", 7
	require.NoError(t, provider.ApplyDockerReconciliationSession(second))
	assert.Empty(t, provider.PendingDockerTelemetry().Events, "a new authenticated session must not reuse old sequence values")
	provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryOrphan, OrphanState: db.DockerTelemetryOrphanDetected, Count: 1})
	pending = provider.PendingDockerTelemetry()
	require.Len(t, pending.Events, 1)
	assert.Equal(t, int64(8), pending.Events[0].Sequence)
}

func TestDockerTelemetryQueueBoundsOutageAndCoalescesQueueFullDrops(t *testing.T) {
	provider := &Provider{runnerID: 8, policy: db.DefaultDockerExecutionPolicy()}
	require.NoError(t, provider.ApplyDockerReconciliationSession(db.DockerReconciliationSession{SessionID: "queue-session", Fence: "queue-fence", RunnerID: 8, TargetBoot: "queue-boot"}))
	for index := 0; index < 150; index++ {
		provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryPolicyDenial, PolicyRule: db.DockerPolicyRuleImageDenied})
	}
	first := provider.PendingDockerTelemetry()
	require.Len(t, first.Events, 100)
	assert.Equal(t, int64(1), first.Events[0].Sequence)
	assert.Equal(t, int64(100), first.Events[99].Sequence)
	assert.Equal(t, first, provider.PendingDockerTelemetry(), "an outage retry must preserve the exact pending prefix")
	provider.AcknowledgeDockerTelemetry(db.DockerTelemetryAck{HighestSequence: 1})
	recovered := provider.PendingDockerTelemetry()
	require.Len(t, recovered.Events, 100)
	drop := recovered.Events[99]
	assert.Equal(t, db.DockerTelemetryDrop, drop.Kind)
	assert.Equal(t, db.DockerTelemetryDropQueueFull, drop.DropReason)
	assert.Equal(t, int64(50), drop.Count)
	assert.Equal(t, int64(101), drop.Sequence)
	provider.AcknowledgeDockerTelemetry(db.DockerTelemetryAck{HighestSequence: 101})
	provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryCleanupFailure, CleanupResource: db.DockerTelemetryCleanupTask})
	tail := provider.PendingDockerTelemetry()
	require.Len(t, tail.Events, 1)
	assert.Equal(t, db.DockerTelemetryCleanupFailure, tail.Events[0].Kind)
}

func TestDockerTelemetryQueueSaturatesDropsAndSupportsConcurrentAppend(t *testing.T) {
	provider := &Provider{runnerID: 8, policy: db.DefaultDockerExecutionPolicy()}
	require.NoError(t, provider.ApplyDockerReconciliationSession(db.DockerReconciliationSession{SessionID: "saturated-session", Fence: "saturated-fence", RunnerID: 8, TargetBoot: "saturated-boot"}))
	provider.telemetry = make([]db.DockerTelemetryEvent, 100)
	for index := range provider.telemetry {
		provider.telemetry[index] = db.DockerTelemetryEvent{Sequence: int64(index + 1), Kind: db.DockerTelemetryPolicyDenial, PolicyRule: db.DockerPolicyRuleImageDenied}
	}
	provider.telemetryNextSequence = 100
	provider.telemetryDropped = maxDockerTelemetryDroppedCount - 1
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			provider.recordDockerTelemetry(db.DockerTelemetryEvent{Kind: db.DockerTelemetryCleanupFailure, CleanupResource: db.DockerTelemetryCleanupTask})
		}()
	}
	group.Wait()
	provider.AcknowledgeDockerTelemetry(db.DockerTelemetryAck{HighestSequence: 100})
	pending := provider.PendingDockerTelemetry()
	require.Len(t, pending.Events, 1)
	assert.Equal(t, db.DockerTelemetryDrop, pending.Events[0].Kind)
	assert.Equal(t, maxDockerTelemetryDroppedCount, pending.Events[0].Count)
	assert.Equal(t, int64(101), pending.Events[0].Sequence)
}

func TestDockerRemediationReinspectsImmutableIdentityAndManagedLabels(t *testing.T) {
	key := db.DockerReconciliationKey{DockerReconciliationOwner: db.DockerReconciliationOwner{RunnerID: 8, RunnerBoot: "prior-boot"}, ProjectID: 3, TaskID: 5, Generation: 2, Resource: db.DockerReconciliationResourceTask}
	command := db.DockerReconciliationRemediationCommand{CommandID: strings.Repeat("c", 64), SessionID: "current", RunnerID: 8, Action: db.DockerReconciliationRemediationRetryStopAndCleanup, Target: db.DockerReconciliationRemediationTargetQuarantine, DaemonID: "immutable-daemon-id", Quarantine: &key}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s", command.Target, command.DaemonID, key.RunnerBoot, key.ProjectID, key.TaskID, key.Generation, key.Resource)))
	command.Fingerprint = fmt.Sprintf("%x", sum[:])
	labels := map[string]string{"io.semaphore.managed": "v1", labelExecutor: "docker", "io.semaphore.runner-id": "8", labelRunnerBoot: "prior-boot", labelProjectID: "3", labelTaskID: "5", "io.semaphore.assignment-generation": "2", labelResource: "task"}
	client := &fakeDockerClient{inspectStates: map[string]ContainerState{"immutable-daemon-id": {Exists: true, ID: "immutable-daemon-id", Labels: labels}}}
	provider := &Provider{client: client, runnerID: 8, session: db.DockerReconciliationSession{SessionID: "current", RunnerID: 8, TargetBoot: "boot"}}
	result := provider.RemediateDockerReconciliation(context.Background(), command)
	assert.Equal(t, db.DockerReconciliationRemediationSucceeded, result.Status)
	assert.Equal(t, []string{"immutable-daemon-id"}, client.removedContainers)

	client = &fakeDockerClient{inspectStates: map[string]ContainerState{"immutable-daemon-id": {Exists: true, ID: "different-daemon-id", Labels: labels}}}
	provider = &Provider{client: client, runnerID: 8, session: db.DockerReconciliationSession{SessionID: "current", RunnerID: 8, TargetBoot: "boot"}}
	result = provider.RemediateDockerReconciliation(context.Background(), command)
	assert.Equal(t, db.DockerReconciliationEvidenceIdentityMismatch, result.Evidence)
	assert.Empty(t, client.removedContainers)
}

func TestDockerVolumeRemediationRejectsDeleteRecreateSameName(t *testing.T) {
	labels := map[string]string{"io.semaphore.managed": "v1", labelExecutor: "docker", "io.semaphore.runner-id": "8"}
	oldIdentity := volumeCreationIdentity("bundle", "2026-08-30T11:00:00.100000001Z", "local", "local")
	newIdentity := volumeCreationIdentity("bundle", "2026-08-30T11:00:01.100000001Z", "local", "local")
	require.NotEmpty(t, oldIdentity)
	require.NotEqual(t, oldIdentity, newIdentity)
	command := db.DockerReconciliationRemediationCommand{CommandID: strings.Repeat("d", 64), SessionID: "current", RunnerID: 8, Action: db.DockerReconciliationRemediationRetryStopAndCleanup, Target: db.DockerReconciliationRemediationTargetCandidate, Fingerprint: strings.Repeat("a", 64), DaemonID: "bundle", CandidateResource: db.DockerReconciliationCandidateVolume, CandidateIdentity: oldIdentity}
	client := &fakeDockerClient{managedResources: []ManagedResource{{Kind: ManagedVolume, ID: "bundle", Name: "bundle", Labels: labels, CreationIdentity: oldIdentity}}}
	provider := &Provider{client: client, runnerID: 8, session: db.DockerReconciliationSession{SessionID: "current", RunnerID: 8, TargetBoot: "boot"}}
	result := provider.RemediateDockerReconciliation(context.Background(), command)
	assert.Equal(t, db.DockerReconciliationRemediationSucceeded, result.Status)
	assert.Equal(t, []string{"bundle"}, client.removedVolumes)

	client = &fakeDockerClient{managedResources: []ManagedResource{{Kind: ManagedVolume, ID: "bundle", Name: "bundle", Labels: labels, CreationIdentity: newIdentity}}}
	provider = &Provider{client: client, runnerID: 8, session: db.DockerReconciliationSession{SessionID: "current", RunnerID: 8, TargetBoot: "boot"}}
	result = provider.RemediateDockerReconciliation(context.Background(), command)
	assert.Equal(t, db.DockerReconciliationRemediationErrored, result.Status)
	assert.Equal(t, db.DockerReconciliationEvidenceIdentityMismatch, result.Evidence)
	assert.Empty(t, client.removedVolumes)
}

func (c *fakeDockerClient) ListManagedResources(context.Context, int) ([]ManagedResource, error) {
	c.listCalls++
	return c.managedResources, nil
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

func (c *fakeDockerClient) SampleContainerResources(context.Context, string) (DockerResourceUsage, error) {
	return c.resourceUsage, c.resourceUsageErr
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

func (c *fakeDockerClient) VolumeReferenced(context.Context, string) (bool, error) {
	return c.volumeReferenced, nil
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
