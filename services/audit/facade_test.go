package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func (r failingEventRepository) CreateEventWithAuditWebhook(db.Event, db.AuditWebhookDelivery) (db.Event, error) {
	return db.Event{}, r.err
}

type auditWebhookStub struct {
	prepared pro_interfaces.AuditEvent
	notified bool
}

func (s *auditWebhookStub) PrepareDelivery(_ context.Context, event pro_interfaces.AuditEvent) (*db.AuditWebhookDelivery, error) {
	s.prepared = event
	envelope, err := pro_interfaces.NewAuditWebhookEnvelope(event)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return &db.AuditWebhookDelivery{EventID: event.EventID, Payload: string(payload)}, nil
}
func (s *auditWebhookStub) Notify() { s.notified = true }
func (*auditWebhookStub) Configuration(context.Context) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, nil
}
func (*auditWebhookStub) Configure(context.Context, pro_interfaces.AuditWebhookConfigInput) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, nil
}
func (*auditWebhookStub) TestDelivery(context.Context) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return pro_interfaces.AuditWebhookDeliveryDTO{}, nil
}
func (*auditWebhookStub) SetPaused(context.Context, bool) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, nil
}
func (*auditWebhookStub) DeliveryHistory(context.Context, db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return nil, nil
}
func (*auditWebhookStub) Start()       {}
func (*auditWebhookStub) Close() error { return nil }

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

func TestServiceFacadeCommitsWebhookOutboxWithStableMetadata(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	writer := &auditLogWriter{}
	webhook := &auditWebhookStub{}
	recorder := NewServiceFacade(store, writer, metrics.NewMetrics(), webhook)

	require.NoError(t, recorder.Record(context.Background(), validAuditEvent()))

	require.Regexp(t, `^[a-f0-9]{32}$`, webhook.prepared.EventID)
	assert.False(t, webhook.prepared.OccurredAt.IsZero())
	assert.True(t, webhook.notified)
	assert.Equal(t, webhook.prepared.EventID, writer.record.EventID)
	assert.Equal(t, webhook.prepared.OccurredAt, writer.record.OccurredAt)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	deliveries, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	assert.Equal(t, webhook.prepared.EventID, deliveries[0].EventID)
}

func TestServiceFacadeScopesProjectRunnerEventsAndPreservesGlobalEvents(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	actor, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "audit-actor", Name: "Audit Actor", Email: "audit-actor@example.invalid",
	}, Pwd: "synthetic-password"})
	require.NoError(t, err)
	member, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "audit-member", Name: "Audit Member", Email: "audit-member@example.invalid",
	}, Pwd: "synthetic-password"})
	require.NoError(t, err)
	unrelated, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "audit-unrelated", Name: "Audit Unrelated", Email: "audit-unrelated@example.invalid",
	}, Pwd: "synthetic-password"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "audit-scope"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    member.ID,
		Role:      db.ProjectGuest,
	})
	require.NoError(t, err)

	actorID := actor.ID
	projectID := project.ID
	writer := &auditLogWriter{}
	recorder := NewServiceFacade(store, writer, metrics.NewMetrics())
	require.NoError(t, recorder.Record(context.Background(), pro_interfaces.AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ActorID:       &actorID,
		ProjectID:     &projectID,
		Action:        pro_interfaces.AuditActionProjectRunnerCreate,
		TargetType:    pro_interfaces.AuditTargetProjectRunner,
		TargetID:      "project:" + strconv.Itoa(project.ID),
		Outcome:       pro_interfaces.AuditOutcomeDenied,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        string(pro_interfaces.CapabilityReasonInsufficientPermission),
	}))

	require.NotNil(t, writer.record.ProjectID)
	assert.Equal(t, project.ID, *writer.record.ProjectID)
	memberEvents, err := store.GetUserEvents(member.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, memberEvents, 1)
	require.NotNil(t, memberEvents[0].ProjectID)
	assert.Equal(t, project.ID, *memberEvents[0].ProjectID)
	require.NotNil(t, memberEvents[0].ObjectType)
	assert.Equal(t, db.EventProjectRunnerAudit, *memberEvents[0].ObjectType)
	unrelatedEvents, err := store.GetUserEvents(unrelated.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, unrelatedEvents)

	require.NoError(t, recorder.Record(context.Background(), validAuditEvent()))
	unrelatedEvents, err = store.GetUserEvents(unrelated.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, unrelatedEvents, 1)
	assert.Nil(t, unrelatedEvents[0].ProjectID)
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
