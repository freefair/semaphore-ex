package k8s

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesTerraformCommandForwardsSIGTERMToPlanningChild(t *testing.T) {
	if os.Getenv("SEMAPHORE_RUN_K8S_SIGNAL_INTEGRATION") != "1" {
		t.Skip("set SEMAPHORE_RUN_K8S_SIGNAL_INTEGRATION=1 to run the local Docker signal regression")
	}

	bundleDir := t.TempDir()
	writeKubernetesSignalFixture(t, bundleDir)

	plan := &tasks.ContainerTaskPlan{
		App:       db.AppTerraform,
		Terraform: tasks.ContainerTerraformPlan{AutoApprove: true},
	}
	command, err := kubernetesTaskCommand(plan)
	require.NoError(t, err)
	require.Len(t, command, 3)

	containerName := fmt.Sprintf("semaphore-k8s-signal-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = dockerSignalCommand(context.Background(), "rm", "-f", containerName)
	})

	args := []string{
		"run", "-d",
		"--name", containerName,
		"--network", "none",
		"--entrypoint", "/bin/sh",
		"--volume", bundleDir + ":/semaphore/bundle",
		"alpine:latest",
	}
	args = append(args, command[1:]...)
	_, err = dockerSignalCommand(context.Background(), args...)
	require.NoError(t, err)

	planReady := filepath.Join(bundleDir, "plan-child-ready")
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(planReady)
		return statErr == nil
	}, 15*time.Second, 50*time.Millisecond, "Terraform planning child did not become ready")

	_, err = dockerSignalCommand(context.Background(), "kill", "--signal=TERM", containerName)
	require.NoError(t, err)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer waitCancel()
	waitOutput, err := dockerSignalCommand(waitCtx, "wait", containerName)
	require.NoError(t, err)
	assert.Equal(t, "143", strings.TrimSpace(waitOutput))

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer inspectCancel()
	running, err := dockerSignalCommand(inspectCtx, "inspect", "--format", "{{.State.Running}}", containerName)
	require.NoError(t, err)
	assert.Equal(t, "false", strings.TrimSpace(running))
	exitCode, err := dockerSignalCommand(inspectCtx, "inspect", "--format", "{{.State.ExitCode}}", containerName)
	require.NoError(t, err)
	assert.Equal(t, "143", strings.TrimSpace(exitCode))

	assert.FileExists(t, filepath.Join(bundleDir, "plan-child-cleanup"))
	assert.FileExists(t, filepath.Join(bundleDir, "plan-child-terminated"))
	assert.NoFileExists(t, filepath.Join(bundleDir, "apply-started"))
}

func writeKubernetesSignalFixture(t *testing.T, bundleDir string) {
	t.Helper()
	script := `#!/bin/sh
set -eu

case "$1" in
  bootstrap)
    exit 0
    ;;
  plan)
    trap 'printf cleanup > /semaphore/bundle/plan-child-cleanup; printf terminated > /semaphore/bundle/plan-child-terminated; exit 143' TERM
    printf ready > /semaphore/bundle/plan-child-ready
    while :; do
      sleep 1
    done
    ;;
  apply)
    printf started > /semaphore/bundle/apply-started
    exit 0
    ;;
  *)
    exit 64
    ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "run.sh"), []byte(script), 0o755))
}

func dockerSignalCommand(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
