package db

import (
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunnerRegistrationMaterialIsExcludedFromSerializationAndBackups(t *testing.T) {
	registrationHash := strings.Repeat("a", 64)
	expiresAt := time.Now().Add(time.Hour)
	runner := Runner{
		Token:                      "runner-auth-material",
		RegistrationTokenHash:      &registrationHash,
		RegistrationTokenExpiresAt: &expiresAt,
	}

	serialized, err := json.Marshal(runner)
	assert.NoError(t, err)
	assert.NotContains(t, string(serialized), runner.Token)
	assert.NotContains(t, string(serialized), registrationHash)

	runnerType := reflect.TypeOf(Runner{})
	for _, fieldName := range []string{"Token", "RegistrationTokenHash", "RegistrationTokenExpiresAt"} {
		field, found := runnerType.FieldByName(fieldName)
		assert.True(t, found)
		assert.Equal(t, "-", field.Tag.Get("backup"), fieldName)
	}
}

func TestRunner_HealthDerivesHeartbeatAndUptime(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	timeout := 2 * time.Minute
	started := now.Add(-75 * time.Second)
	touched := now.Add(-timeout)
	runner := Runner{
		ID: 9, Name: "health", Version: "2.20.4", Platform: "linux/amd64",
		StartedAt: &started, Touched: &touched, CurrentLoad: 2, MaxParallelTasks: 4,
	}

	health := runner.Health(now, timeout)

	assert.Equal(t, RunnerHeartbeatOnline, health.HeartbeatState)
	assert.Equal(t, int64(75), *health.UptimeSeconds)
	assert.Equal(t, int64(120), *health.HeartbeatAgeSeconds)
	assert.Equal(t, int64(120), health.HeartbeatTimeoutSeconds)
	assert.Equal(t, 2, health.CurrentLoad)
	assert.Equal(t, 4, health.MaxParallelTasks)

	assert.Equal(t, RunnerHeartbeatOffline, runner.Health(now.Add(time.Nanosecond), timeout).HeartbeatState)
	assert.Equal(t, RunnerHeartbeatWebhook, (Runner{Webhook: "https://example.com/hook"}).Health(now, timeout).HeartbeatState)
}

func TestRunner_HealthClampsFutureReportTimes(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	health := (Runner{StartedAt: &future, Touched: &future}).Health(now, 2*time.Minute)

	assert.Equal(t, int64(0), *health.UptimeSeconds)
	assert.Equal(t, int64(0), *health.HeartbeatAgeSeconds)
}
