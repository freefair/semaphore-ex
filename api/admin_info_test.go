package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type structuredLogDiagnosticsStub struct {
	diagnostics pro_interfaces.StructuredLogDiagnostics
	debug       pro_interfaces.DebugFilterDiagnostics
}

func (s structuredLogDiagnosticsStub) Diagnostics() pro_interfaces.StructuredLogDiagnostics {
	return s.diagnostics
}

func (s structuredLogDiagnosticsStub) DebugFilterDiagnostics() pro_interfaces.DebugFilterDiagnostics {
	return s.debug
}

func TestAdminInfoReturnsAuthorizedStructuredLogDiagnostics(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	flushed := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	request = helpers.SetContextValue(request, "log_writer", structuredLogDiagnosticsStub{
		diagnostics: pro_interfaces.StructuredLogDiagnostics{
			Enabled: true, State: pro_interfaces.StructuredLogHealthy, QueueDepth: 2, QueueCapacity: 16,
			LastSuccessfulFlush: &flushed, FlushInterval: "1s", RotationInterval: "24h",
			Destinations: []pro_interfaces.StructuredLogDestinationDiagnostics{{
				Category: "application", Enabled: true, Filename: "/var/log/semaphore/events.jsonl",
			}},
		},
	})
	recorder := httptest.NewRecorder()

	getAdminInfo(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		StructuredLogs pro_interfaces.StructuredLogDiagnostics `json:"structured_logs"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, pro_interfaces.StructuredLogHealthy, body.StructuredLogs.State)
	assert.Equal(t, 2, body.StructuredLogs.QueueDepth)
	assert.Equal(t, 16, body.StructuredLogs.QueueCapacity)
	assert.Equal(t, "/var/log/semaphore/events.jsonl", body.StructuredLogs.Destinations[0].Filename)
}

func TestAdminInfoReturnsDisabledStructuredLogStateWithoutWriter(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	recorder := httptest.NewRecorder()

	getAdminInfo(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/info", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		StructuredLogs pro_interfaces.StructuredLogDiagnostics `json:"structured_logs"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.False(t, body.StructuredLogs.Enabled)
	assert.Equal(t, pro_interfaces.StructuredLogDisabled, body.StructuredLogs.State)
	assert.Empty(t, body.StructuredLogs.Destinations)
}

func TestAdminInfoReturnsEffectiveAndRejectedDebugFilters(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	reloadedAt := time.Date(2026, 8, 27, 13, 15, 0, 0, time.UTC)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	request = helpers.SetContextValue(request, "log_writer", structuredLogDiagnosticsStub{
		debug: pro_interfaces.DebugFilterDiagnostics{
			Instance: "node-a", Default: "configured",
			Configured: []string{"runner", "task*middle"}, Effective: []string{"runner"},
			Rejected: []pro_interfaces.DebugFilterRejectedEntry{{
				Entry: "task*middle", Reason: "wildcard_must_be_terminal",
			}},
			ReloadedAt: reloadedAt,
		},
	})
	recorder := httptest.NewRecorder()

	getAdminInfo(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		DebugFilter pro_interfaces.DebugFilterDiagnostics `json:"debug_filter"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "node-a", body.DebugFilter.Instance)
	assert.Equal(t, []string{"runner"}, body.DebugFilter.Effective)
	assert.Equal(t, reloadedAt, body.DebugFilter.ReloadedAt)
	require.Len(t, body.DebugFilter.Rejected, 1)
	assert.Equal(t, "wildcard_must_be_terminal", body.DebugFilter.Rejected[0].Reason)
}

func TestAdminInfoReturnsExplicitDefaultDebugFilterWithoutWriter(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	recorder := httptest.NewRecorder()

	getAdminInfo(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/info", nil))

	var body struct {
		DebugFilter pro_interfaces.DebugFilterDiagnostics `json:"debug_filter"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "all", body.DebugFilter.Default)
	assert.Equal(t, []string{"*"}, body.DebugFilter.Effective)
	assert.Empty(t, body.DebugFilter.Configured)
}

func TestAdminInfoReturnsOnlyRedactedStructuredLogFailure(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	request = helpers.SetContextValue(request, "log_writer", structuredLogDiagnosticsStub{
		diagnostics: pro_interfaces.StructuredLogDiagnostics{
			Enabled: true, State: pro_interfaces.StructuredLogFailed,
			LastWriteError: "authorization=[REDACTED] write denied",
			Destinations:   []pro_interfaces.StructuredLogDestinationDiagnostics{},
		},
	})
	recorder := httptest.NewRecorder()

	getAdminInfo(recorder, request)

	assert.Contains(t, recorder.Body.String(), "[REDACTED]")
	assert.NotContains(t, recorder.Body.String(), "bearer-secret")
}

func TestAdminInfoStructuredLogDiagnosticsAreForbiddenToNonAdmins(t *testing.T) {
	restoreConfig := installAdminInfoTestConfig(t)
	defer restoreConfig()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 12, Admin: false})
	request = helpers.SetContextValue(request, "log_writer", structuredLogDiagnosticsStub{
		diagnostics: pro_interfaces.StructuredLogDiagnostics{
			Enabled: true, State: pro_interfaces.StructuredLogFailed, LastWriteError: "must-not-leak",
		},
		debug: pro_interfaces.DebugFilterDiagnostics{ReloadError: "debug-filter-must-not-leak"},
	})
	recorder := httptest.NewRecorder()

	adminMiddleware(http.HandlerFunc(getAdminInfo)).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "must-not-leak")
	assert.NotContains(t, recorder.Body.String(), "debug-filter-must-not-leak")
}

func installAdminInfoTestConfig(t *testing.T) func() {
	t.Helper()
	previous := util.Config
	util.Config = &util.ConfigType{Runners: &util.RunnersConfig{}}
	return func() { util.Config = previous }
}
