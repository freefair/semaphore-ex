package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDebugFilterSourcePrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log:\n  debug_filter: from_config\n"), 0o600))

	tests := []struct {
		name        string
		flagValue   string
		flagSet     bool
		environment map[string]string
		expected    string
	}{
		{name: "flag", flagValue: "from_flag", flagSet: true, environment: map[string]string{"SEMAPHORE_DEBUG_FILTER": "from_env"}, expected: "from_flag"},
		{name: "environment", environment: map[string]string{"SEMAPHORE_DEBUG_FILTER": "from_env"}, expected: "from_env"},
		{name: "explicit empty environment", environment: map[string]string{"SEMAPHORE_DEBUG_FILTER": ""}, expected: ""},
		{name: "configuration file", environment: map[string]string{}, expected: "from_config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := debugFilterSource{
				flagValue:  tt.flagValue,
				flagSet:    tt.flagSet,
				configPath: path,
				lookupEnv: func(key string) (string, bool) {
					value, ok := tt.environment[key]
					return value, ok
				},
			}

			actual, err := source.Load()

			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestReloadDebugFilterReadsChangedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log:\n  debug_filter: runner\n"), 0o600))
	source := debugFilterSource{configPath: path, lookupEnv: func(string) (string, bool) { return "", false }}
	manager := debuglog.NewManager("node-a", "runner", time.Now().UTC())
	require.NoError(t, os.WriteFile(path, []byte("log:\n  debug_filter: task_*\n"), 0o600))

	diagnostics := reloadDebugFilter(manager, source, time.Now().UTC())

	assert.Empty(t, diagnostics.ReloadError)
	assert.False(t, manager.Enabled("runner"))
	assert.True(t, manager.Enabled("task_pool"))
}

func TestReloadDebugFilterKeepsLastKnownGoodConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log:\n  debug_filter: [\n"), 0o600))
	source := debugFilterSource{configPath: path, lookupEnv: func(string) (string, bool) { return "", false }}
	manager := debuglog.NewManager("node-a", "runner", time.Now().UTC())

	diagnostics := reloadDebugFilter(manager, source, time.Now().UTC())

	assert.NotEmpty(t, diagnostics.ReloadError)
	assert.True(t, manager.Enabled("runner"))
	assert.False(t, manager.Enabled("db"))
}
