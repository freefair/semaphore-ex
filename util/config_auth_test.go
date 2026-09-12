package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigType_MaxSessionLife(t *testing.T) {
	tests := []struct {
		name     string
		config   *ConfigType
		expected time.Duration
	}{
		{"nil config", nil, 0},
		{"auth section not configured", &ConfigType{}, 0},
		{"zero hours means unlimited", &ConfigType{Auth: &AuthConfig{MaxSessionLifeHours: 0}}, 0},
		{"negative hours means unlimited", &ConfigType{Auth: &AuthConfig{MaxSessionLifeHours: -5}}, 0},
		{"configured hours", &ConfigType{Auth: &AuthConfig{MaxSessionLifeHours: 12}}, 12 * time.Hour},
		{"largest representable hours", &ConfigType{Auth: &AuthConfig{MaxSessionLifeHours: int(maxSessionLifeHours)}}, time.Duration(maxSessionLifeHours) * time.Hour},
		{"overflowing direct config clamps to largest whole hour", &ConfigType{Auth: &AuthConfig{MaxSessionLifeHours: int(maxSessionLifeHours + 1)}}, time.Duration(maxSessionLifeHours) * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.MaxSessionLife())
		})
	}
}

func TestAuthConfig_LoadFromEnvironment(t *testing.T) {
	require.NoError(t, os.Setenv("SEMAPHORE_AUTH_MAX_SESSION_LIFE_HOURS", "36"))
	defer func() { _ = os.Unsetenv("SEMAPHORE_AUTH_MAX_SESSION_LIFE_HOURS") }()

	cfg := &ConfigType{}
	_, err := loadEnvironmentToObject(cfg)
	require.NoError(t, err)

	require.NotNil(t, cfg.Auth)
	assert.Equal(t, 36, cfg.Auth.MaxSessionLifeHours)
	assert.Equal(t, 36*time.Hour, cfg.MaxSessionLife())
}

func TestAuthConfig_ValidateRejectsNegative(t *testing.T) {
	assert.Error(t, validateAuthConfig(&AuthConfig{MaxSessionLifeHours: -1}))
	assert.NoError(t, validateAuthConfig(&AuthConfig{MaxSessionLifeHours: 0}))
	assert.NoError(t, validateAuthConfig(&AuthConfig{MaxSessionLifeHours: 720}))
	assert.NoError(t, validateAuthConfig(&AuthConfig{MaxSessionLifeHours: int(maxSessionLifeHours)}))
	assert.ErrorContains(t, validateAuthConfig(&AuthConfig{MaxSessionLifeHours: int(maxSessionLifeHours + 1)}), "must not exceed")
}

func TestAuthConfig_RejectsOverflowFromEnvironmentAndJSON(t *testing.T) {
	overflowingHours := int(maxSessionLifeHours + 1)

	t.Run("environment", func(t *testing.T) {
		t.Setenv("SEMAPHORE_AUTH_MAX_SESSION_LIFE_HOURS", strconv.Itoa(overflowingHours))
		config := &ConfigType{}
		_, err := loadEnvironmentToObject(config)
		require.NoError(t, err)
		require.NotNil(t, config.Auth)
		assert.ErrorContains(t, validateAuthConfig(config.Auth), "must not exceed")
	})

	t.Run("json", func(t *testing.T) {
		previous := Config
		t.Cleanup(func() { Config = previous })
		Config = &ConfigType{}
		decodeConfig(strings.NewReader(`{"auth":{"max_session_life_hours":`+strconv.Itoa(overflowingHours)+`}}`), "config.json")
		require.NotNil(t, Config.Auth)
		assert.ErrorContains(t, validateAuthConfig(Config.Auth), "must not exceed")
	})
}

func TestConfigInitRejectsOverflowingSessionLifetime(t *testing.T) {
	previousConfig := Config
	t.Cleanup(func() { Config = previousConfig })
	overflowingHours := strconv.Itoa(int(maxSessionLifeHours + 1))
	expectedError := fmt.Sprintf("auth.max_session_life_hours must not exceed %d hours", maxSessionLifeHours)

	t.Run("environment", func(t *testing.T) {
		t.Setenv("SEMAPHORE_AUTH_MAX_SESSION_LIFE_HOURS", overflowingHours)
		assert.PanicsWithError(t, expectedError, func() { ConfigInit("", true) })
	})

	t.Run("json", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.json")
		require.NoError(t, os.WriteFile(
			configPath,
			[]byte(`{"auth":{"max_session_life_hours":`+overflowingHours+`}}`),
			0600,
		))
		assert.PanicsWithError(t, expectedError, func() { ConfigInit(configPath, false) })
	})
}
