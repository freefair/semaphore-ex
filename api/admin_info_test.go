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
}

func (s structuredLogDiagnosticsStub) Diagnostics() pro_interfaces.StructuredLogDiagnostics {
	return s.diagnostics
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
	})
	recorder := httptest.NewRecorder()

	adminMiddleware(http.HandlerFunc(getAdminInfo)).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "must-not-leak")
}

func installAdminInfoTestConfig(t *testing.T) func() {
	t.Helper()
	previous := util.Config
	util.Config = &util.ConfigType{Runners: &util.RunnersConfig{}}
	return func() { util.Config = previous }
}
