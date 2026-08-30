package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExecutorDisposableDaemonLifecycle(t *testing.T) {
	if os.Getenv("SEMAPHORE_TEST_DOCKER") != "1" {
		t.Skip("set SEMAPHORE_TEST_DOCKER=1 to use the disposable Docker daemon")
	}
	cfg, err := effectiveConfig(util.RunnerDockerConfig{
		Image: "nginx:alpine", HelperImage: "nginx:alpine", Network: "none", PullPolicy: "never",
		CleanupGraceSeconds: 1,
	})
	require.NoError(t, err)
	client, err := newMobyClient(cfg)
	require.NoError(t, err)
	realClient := client.(*mobyClient)
	policy := integrationDockerPolicy(t, realClient, "nginx:alpine")
	runnerBoot := fmt.Sprintf("it-%d", time.Now().UnixNano())

	t.Run("success and logs", func(t *testing.T) {
		logger := &recordingLogger{}
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 501, ProjectID: 50}, db.Template{}, logger)
		executor.policy = policy
		plan := integrationTaskPlan(t, "echo docker-integration-success")

		require.NoError(t, executor.runContainerPlan(context.Background(), plan))
		assert.True(t, logger.hasLog("docker-integration-success"))
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})

	t.Run("nonzero result", func(t *testing.T) {
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 502, ProjectID: 50}, db.Template{}, &recordingLogger{})
		executor.policy = policy
		plan := integrationTaskPlan(t, "echo docker-integration-failure; exit 23")

		err := executor.runContainerPlan(context.Background(), plan)
		require.ErrorContains(t, err, "run exited with code 23")
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})

	t.Run("bounded cancellation", func(t *testing.T) {
		logger := &recordingLogger{}
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 503, ProjectID: 50}, db.Template{}, logger)
		executor.policy = policy
		plan := integrationTaskPlan(t, "echo docker-integration-cancel-started; while :; do sleep 1; done")
		done := make(chan error, 1)
		go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
		require.Eventually(t, func() bool { return logger.hasLog("docker-integration-cancel-started") }, 10*time.Second, 50*time.Millisecond)

		executor.Kill()
		require.NoError(t, <-done)
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})
}

func TestDockerReconciliationDisposableDaemonCases(t *testing.T) {
	if os.Getenv("SEMAPHORE_TEST_DOCKER") != "1" {
		t.Skip("set SEMAPHORE_TEST_DOCKER=1 to use the disposable Docker daemon")
	}
	cfg, err := effectiveConfig(util.RunnerDockerConfig{Host: os.Getenv("DOCKER_HOST")})
	require.NoError(t, err)
	client, err := newMobyClient(cfg)
	require.NoError(t, err)
	realClient := client.(*mobyClient)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runnerID := int(time.Now().UnixNano()%1_000_000_000) + 1
	boot := fmt.Sprintf("reconcile-%d", time.Now().UnixNano())
	newTarget := func(taskID int, resource db.DockerReconciliationResource, suffix string) db.DockerReconciliationScanTarget {
		name := fmt.Sprintf("semaphore-task-%d-g1-%s", taskID, boot)
		if suffix != "" {
			name += suffix
		}
		return db.DockerReconciliationScanTarget{TargetBoot: boot, ProjectID: 1, TaskID: taskID, Generation: 1, Resource: resource, ContainerName: name}
	}
	running, exited, absent := newTarget(1, db.DockerReconciliationResourceTask, ""), newTarget(2, db.DockerReconciliationResourceTask, ""), newTarget(3, db.DockerReconciliationResourceTask, "")
	labels := func(target db.DockerReconciliationScanTarget) map[string]string {
		return map[string]string{"io.semaphore.managed": "v1", labelExecutor: "docker", "io.semaphore.runner-id": strconv.Itoa(runnerID), labelTaskID: strconv.Itoa(target.TaskID), labelProjectID: strconv.Itoa(target.ProjectID), "io.semaphore.assignment-generation": strconv.Itoa(target.Generation), labelRunnerBoot: target.TargetBoot, labelResource: string(target.Resource)}
	}
	created := make([]string, 0, 3)
	defer func() {
		for _, id := range created {
			_, _ = realClient.client.ContainerRemove(context.Background(), id, moby.ContainerRemoveOptions{Force: true})
		}
	}()
	createdVolume := "semaphore-task-1-g1-" + boot + "-bundle"
	defer func() {
		_, _ = realClient.client.VolumeRemove(context.Background(), createdVolume, moby.VolumeRemoveOptions{Force: true})
	}()
	create := func(target db.DockerReconciliationScanTarget, labels map[string]string) string {
		result, createErr := realClient.client.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: target.ContainerName, Config: &container.Config{Image: "nginx:alpine", Labels: labels}})
		require.NoError(t, createErr)
		created = append(created, result.ID)
		return result.ID
	}
	runningID := create(running, labels(running))
	_, err = realClient.client.ContainerStart(ctx, runningID, moby.ContainerStartOptions{})
	require.NoError(t, err)
	exitedID := create(exited, labels(exited))
	_, err = realClient.client.ContainerStart(ctx, exitedID, moby.ContainerStartOptions{})
	require.NoError(t, err)
	stopSeconds := 1
	_, err = realClient.client.ContainerStop(ctx, exitedID, moby.ContainerStopOptions{Timeout: &stopSeconds})
	require.NoError(t, err)
	// Docker enforces unique container names, so a true same-name duplicate is
	// impossible at the daemon edge; the reducer's duplicate branch is covered
	// deterministically by its unit test.
	_, duplicateErr := realClient.client.ContainerCreate(ctx, moby.ContainerCreateOptions{Name: running.ContainerName, Config: &container.Config{Image: "nginx:alpine", Labels: labels(running)}})
	require.Error(t, duplicateErr)
	malformed := newTarget(4, db.DockerReconciliationResourceTask, "")
	malformedLabels := labels(malformed)
	delete(malformedLabels, labelProjectID)
	create(malformed, malformedLabels)
	_, err = realClient.client.VolumeCreate(ctx, moby.VolumeCreateOptions{Name: createdVolume, Labels: labels(running)})
	require.NoError(t, err)
	resources, err := client.ListManagedResources(ctx, runnerID)
	require.NoError(t, err)
	session := db.DockerReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: runnerID, TargetBoot: "next", ScanTargets: []db.DockerReconciliationScanTarget{running, exited, absent}}
	observations, _, candidates, err := reduceDockerReconciliationScan(session, resources, time.Now().UTC())
	require.NoError(t, err)
	byTask := make(map[int]db.DockerReconciliationState)
	for _, observation := range observations {
		byTask[observation.TaskID] = observation.State
	}
	assert.Equal(t, db.DockerReconciliationRunning, byTask[running.TaskID])
	assert.Equal(t, db.DockerReconciliationExited, byTask[exited.TaskID])
	assert.Equal(t, db.DockerReconciliationAbsent, byTask[absent.TaskID])
	reasons := make([]db.DockerReconciliationCandidateReason, 0, len(candidates))
	for _, candidate := range candidates {
		reasons = append(reasons, candidate.Reason)
	}
	assert.Contains(t, reasons, db.DockerReconciliationCandidateMalformed)
	assert.Contains(t, reasons, db.DockerReconciliationCandidateExtra)
	pageProvider := &Provider{client: client, runnerID: runnerID}
	pageObservations, _, _, pageErr := pageProvider.ScanDockerReconciliation(ctx, session)
	require.NoError(t, pageErr)
	for _, observation := range pageObservations {
		if observation.TaskID == running.TaskID {
			assert.Equal(t, runningID, observation.ContainerID)
		}
	}
}

func integrationDockerPolicy(t *testing.T, client *mobyClient, image string) db.DockerExecutionPolicy {
	t.Helper()
	inspected, err := client.client.ImageInspect(context.Background(), image)
	require.NoError(t, err)
	require.NotEmpty(t, inspected.RepoDigests)
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{inspected.RepoDigests[0]}
	require.NoError(t, policy.Canonicalize())
	return policy
}

func integrationTaskPlan(t *testing.T, runCommand string) *tasks.ContainerTaskPlan {
	t.Helper()
	var bundle bytes.Buffer
	archive := tar.NewWriter(&bundle)
	write := func(name string, mode int64, typeFlag byte, contents string) {
		t.Helper()
		header := &tar.Header{
			Name: name, Mode: mode, Typeflag: typeFlag, Size: int64(len(contents)),
			Uid: 65534, Gid: 0,
		}
		require.NoError(t, archive.WriteHeader(header))
		if contents != "" {
			_, err := io.WriteString(archive, contents)
			require.NoError(t, err)
		}
	}
	write("credentials", 0o700, tar.TypeDir, "")
	write("credentials/environment.sh", 0o600, tar.TypeReg, "export QA_VALUE='ready'\n")
	write("repository", 0o555, tar.TypeDir, "")
	write("repository/task.sh", 0o444, tar.TypeReg, "#!/bin/sh\n"+runCommand+"\n")
	write("run.sh", 0o500, tar.TypeReg, `#!/bin/sh
set -eu
case "${1-}" in
  bootstrap)
    cp -R /semaphore/bundle/repository/. /workspace/
    ;;
  run)
    . /semaphore/bundle/credentials/environment.sh
    /bin/sh /workspace/task.sh
    ;;
  *) exit 64 ;;
esac
`)
	require.NoError(t, archive.Close())
	return &tasks.ContainerTaskPlan{App: db.AppBash, Bundle: io.NopCloser(bytes.NewReader(bundle.Bytes()))}
}

func assertDockerResourcesAbsent(t *testing.T, client *mobyClient, runnerBoot string) {
	t.Helper()
	filter := make(moby.Filters).Add("label", labelRunnerBoot+"="+runnerBoot)
	containers, err := client.client.ContainerList(context.Background(), moby.ContainerListOptions{All: true, Filters: filter})
	require.NoError(t, err)
	volumes, err := client.client.VolumeList(context.Background(), moby.VolumeListOptions{Filters: filter})
	require.NoError(t, err)
	assert.Empty(t, containers.Items)
	assert.Empty(t, volumes.Items)
}

func (l *recordingLogger) hasLog(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Contains(strings.Join(l.logs, "\n"), fragment)
}
