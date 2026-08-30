package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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
	runnerBoot := fmt.Sprintf("it-%d", time.Now().UnixNano())

	t.Run("success and logs", func(t *testing.T) {
		logger := &recordingLogger{}
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 501, ProjectID: 50}, db.Template{}, logger)
		plan := integrationTaskPlan(t, "echo docker-integration-success")

		require.NoError(t, executor.runContainerPlan(context.Background(), plan))
		assert.True(t, logger.hasLog("docker-integration-success"))
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})

	t.Run("nonzero result", func(t *testing.T) {
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 502, ProjectID: 50}, db.Template{}, &recordingLogger{})
		plan := integrationTaskPlan(t, "echo docker-integration-failure; exit 23")

		err := executor.runContainerPlan(context.Background(), plan)
		require.ErrorContains(t, err, "run exited with code 23")
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})

	t.Run("bounded cancellation", func(t *testing.T) {
		logger := &recordingLogger{}
		executor := newDockerExecutorForPlan(client, cfg, runnerBoot, db.Task{ID: 503, ProjectID: 50}, db.Template{}, logger)
		plan := integrationTaskPlan(t, "echo docker-integration-cancel-started; while :; do sleep 1; done")
		done := make(chan error, 1)
		go func() { done <- executor.runContainerPlan(context.Background(), plan) }()
		require.Eventually(t, func() bool { return logger.hasLog("docker-integration-cancel-started") }, 10*time.Second, 50*time.Millisecond)

		executor.Kill()
		require.NoError(t, <-done)
		assertDockerResourcesAbsent(t, realClient, runnerBoot)
	})
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
