package db

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func TestRunner_IsOnline(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	offlineTimeout := 2 * time.Minute

	touchedAgo := func(d time.Duration) *time.Time {
		v := now.Add(-d)
		return &v
	}

	tests := []struct {
		name     string
		runner   Runner
		expected bool
	}{
		{"fresh heartbeat", Runner{Touched: touchedAgo(30 * time.Second)}, true},
		{"heartbeat at threshold", Runner{Touched: touchedAgo(2 * time.Minute)}, true},
		{"stale heartbeat", Runner{Touched: touchedAgo(2*time.Minute + time.Second)}, false},
		{"never polled", Runner{}, false},
		{"webhook runner never polled", Runner{Webhook: "https://example.com/hook"}, true},
		{"webhook runner stale heartbeat", Runner{Webhook: "https://example.com/hook", Touched: touchedAgo(time.Hour)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.runner.IsOnline(now, offlineTimeout))
		})
	}
}

func TestRunner_FillStatus(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	offlineTimeout := 2 * time.Minute

	touchedAgo := func(d time.Duration) *time.Time {
		v := now.Add(-d)
		return &v
	}

	tests := []struct {
		name     string
		runner   Runner
		expected RunnerStatus
	}{
		{"fresh heartbeat", Runner{Touched: touchedAgo(30 * time.Second)}, RunnerStatusOnline},
		{"stale heartbeat", Runner{Touched: touchedAgo(2*time.Minute + time.Second)}, RunnerStatusOffline},
		{"never polled", Runner{}, RunnerStatusOffline},
		{"webhook runner", Runner{Webhook: "https://example.com/hook"}, RunnerStatusOnline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.runner
			r.FillStatus(now, offlineTimeout)
			assert.Equal(t, tt.expected, r.Status)
		})
	}
}
