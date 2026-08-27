package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadDebugFilterConfigReadsYAMLAndJSONWithoutMutatingConfig(t *testing.T) {
	previous := Config
	Config = &ConfigType{WebHost: "unchanged"}
	defer func() { Config = previous }()
	for name, content := range map[string]string{
		"config.yaml": "web_host: https://secret.invalid\nlog:\n  debug_filter: runner,task_*\n",
		"config.json": `{"web_host":"https://secret.invalid","log":{"debug_filter":"db"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			value, err := ReadDebugFilterConfig(path)
			require.NoError(t, err)
			assert.NotEmpty(t, value)
			assert.Equal(t, "unchanged", Config.WebHost)
		})
	}
}

func TestReadDebugFilterConfigReportsInvalidReloadSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("log: ["), 0o600))

	_, err := ReadDebugFilterConfig(path)

	assert.ErrorContains(t, err, "decode debug filter configuration")
}
