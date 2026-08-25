package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditLogWriter struct {
	record pro_interfaces.EventLogRecord
	err    error
}

func (w *auditLogWriter) WriteEventLog(record pro_interfaces.EventLogRecord) error {
	w.record = record
	return w.err
}
func (*auditLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error { return nil }
func (*auditLogWriter) WriteResult(any) error                           { return nil }

type failingEventRepository struct{ err error }

func (r failingEventRepository) CreateEvent(db.Event) (db.Event, error) {
	return db.Event{}, r.err
}

func TestServiceFacadePersistsSafeEventToBothSinks(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	writer := &auditLogWriter{}
	appMetrics := metrics.NewMetrics()
	recorder := NewServiceFacade(store, writer, appMetrics)

	require.NoError(t, recorder.Record(context.Background(), validAuditEvent()))

	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Description)
	filePayload, err := json.Marshal(writer.record)
	require.NoError(t, err)
	metricsPayload := scrapeAuditMetrics(appMetrics)
	securityfixtures.AssertTripwiresAbsent(t, *events[0].Description, string(filePayload), metricsPayload)
	assert.Contains(t, metricsPayload,
		`semaphore_enhanced_actions_total{action="capability_write",outcome="denied",source="api"} 1`)
}

func TestServiceFacadeRedactsSinkFailuresAndMeasuresDrops(t *testing.T) {
	tripwireError := errors.New(securityfixtures.TripwireValues[0])
	writer := &auditLogWriter{err: tripwireError}
	appMetrics := metrics.NewMetrics()
	recorder := NewServiceFacade(failingEventRepository{err: tripwireError}, writer, appMetrics)

	err := recorder.Record(context.Background(), validAuditEvent())

	require.EqualError(t, err, "audit persistence failed")
	metricsPayload := scrapeAuditMetrics(appMetrics)
	securityfixtures.AssertTripwiresAbsent(t, err.Error(), metricsPayload)
	assert.Contains(t, metricsPayload,
		`semaphore_enhanced_dependency_healthy{dependency="audit_database"} 0`)
	assert.Contains(t, metricsPayload,
		`semaphore_enhanced_dependency_failures_total{dependency="audit_file"} 1`)
	assert.Contains(t, metricsPayload,
		`semaphore_enhanced_dropped_records_total{reason="write_failure",sink="database"} 1`)
}

func validAuditEvent() pro_interfaces.AuditEvent {
	return pro_interfaces.AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        pro_interfaces.AuditActionCapabilityWrite,
		TargetType:    pro_interfaces.AuditTargetCapability,
		TargetID:      string(pro_interfaces.CapabilityLifecycleTest),
		Outcome:       pro_interfaces.AuditOutcomeDenied,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        string(pro_interfaces.CapabilityReasonDisabledByAdmin),
	}
}

func scrapeAuditMetrics(appMetrics *metrics.Metrics) string {
	recorder := httptest.NewRecorder()
	appMetrics.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	return recorder.Body.String()
}
