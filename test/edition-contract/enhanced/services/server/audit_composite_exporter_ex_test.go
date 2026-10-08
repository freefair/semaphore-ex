package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditCompositeExporterHECContinuesWhenSyslogUnavailable(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()

	refusedListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	refusedAddress := refusedListener.Addr().String()
	require.NoError(t, refusedListener.Close())

	delivered := make(chan hecEvent, 1)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		var event hecEvent
		require.NoError(t, decoder.Decode(&event))
		delivered <- event
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer receiver.Close()

	appMetrics := metrics.NewMetrics()
	exporter := NewAuditExporter(store, &util.AuditConfig{
		Syslog:    &util.AuditSyslogConfig{ID: "syslog-unavailable", Address: refusedAddress},
		SplunkHEC: &util.AuditSplunkHECConfig{ID: "hec-available", URL: receiver.URL, Token: "token", CAFile: writeAuditCertificate(t, receiver.Certificate()), Timeout: time.Second.String()},
	}, auditExportLeaserTest{}, appMetrics)
	require.NoError(t, exporter.Start())
	defer exporter.Stop()

	event, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "event-after-start", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"})
	require.NoError(t, err)

	select {
	case received := <-delivered:
		assert.Equal(t, event.EventID, received.Event.EventID)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "timed out waiting for HEC delivery")
	}

	require.Eventually(t, func() bool {
		cursor, cursorErr := store.InitializeAuditExportState(context.Background(), "hec-available")
		return cursorErr == nil && cursor == event.Seq
	}, 3*time.Second, 20*time.Millisecond)
	errorCount := regexp.MustCompile(`semaphore_audit_export_errors_total\{destination="syslog-unavailable"\} [1-9][0-9]*`)
	require.Eventually(t, func() bool {
		return errorCount.MatchString(scrapeAuditExportMetrics(appMetrics))
	}, 3*time.Second, 20*time.Millisecond)

	syslogCursor, err := store.InitializeAuditExportState(context.Background(), "syslog-unavailable")
	require.NoError(t, err)
	assert.Zero(t, syslogCursor)
}
