package haresilience

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartServiceKeepsStoppedDependenciesDown(t *testing.T) {
	directory := t.TempDir()
	state := filepath.Join(directory, "owner-state")
	require.NoError(t, os.WriteFile(state, []byte("stopped"), 0600))
	// Model Compose's dependency traversal: only --no-deps preserves the fault.
	docker := `#!/bin/sh
set -eu
case " $* " in
 *" --no-deps "*) ;;
 *) printf running > "$OWNER_STATE" ;;
esac
case " $* " in
 *" up "*" runner "*|*" up "*" runner") printf started ;;
 *) exit 1 ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(directory, "docker"), []byte(docker), 0700))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := &commandRunner{repositoryDir: directory, composeFile: "compose.yaml", projectName: "fixture", environment: map[string]string{"OWNER_STATE": state}}
	require.NoError(t, runner.startService(context.Background(), "runner"))
	value, err := os.ReadFile(state)
	require.NoError(t, err)
	assert.Equal(t, "stopped", string(value))
}

func TestCommandOutputSeparatesWarningsAndPreservesFailureDiagnostics(t *testing.T) {
	runner := &commandRunner{repositoryDir: t.TempDir()}
	output, err := runner.run(context.Background(), "sh", "-c", "printf 'warning\\n' >&2")
	require.NoError(t, err)
	assert.Empty(t, output, "stderr must not look like a running service or a SQL result")
	output, err = runner.run(context.Background(), "sh", "-c", "printf 'partial\\n'; printf 'failure\\n' >&2; exit 1")
	require.Error(t, err)
	assert.Equal(t, "partial\n", output)
	assert.ErrorContains(t, err, "partial")
	assert.ErrorContains(t, err, "failure")
}
