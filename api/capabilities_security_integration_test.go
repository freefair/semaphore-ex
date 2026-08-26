package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	auditservice "github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type integrationAuditWriter struct{ record pro_interfaces.EventLogRecord }

func (w *integrationAuditWriter) WriteEventLog(record pro_interfaces.EventLogRecord) error {
	w.record = record
	return nil
}
func (*integrationAuditWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error { return nil }
func (*integrationAuditWriter) WriteResult(any) error                           { return nil }

func TestDeniedCapabilityActionPersistsRedactedAuditAndMetrics(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "audit-user", Name: "Audit User", Email: "audit@example.com",
	})
	require.NoError(t, err)
	writer := &integrationAuditWriter{}
	appMetrics := metrics.NewMetrics()
	auditFacade := auditservice.NewServiceFacade(store, writer, appMetrics)
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityReasonDisabledByAdmin,
		nil,
		nil,
	)}
	controller := NewCapabilityController(facade, auditFacade)
	handler := helpers.CorrelationMiddleware(controller.SnapshotMiddleware(
		controller.Require(pro_interfaces.CapabilityAccessWrite)(http.HandlerFunc(controller.CreateRecord)),
	))
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/lifecycle-test/records",
		bytes.NewBufferString(`{"value":"`+securityfixtures.TripwireValues[0]+`"}`))
	request = helpers.SetContextValue(request, "user", &user)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Description)
	filePayload, err := json.Marshal(writer.record)
	require.NoError(t, err)
	metricsResponse := httptest.NewRecorder()
	appMetrics.ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	securityfixtures.AssertTripwiresAbsent(t,
		response.Body.String(), *events[0].Description, string(filePayload), metricsResponse.Body.String())
	assert.Contains(t, *events[0].Description, `"outcome":"denied"`)
	assert.Contains(t, metricsResponse.Body.String(),
		`semaphore_enhanced_actions_total{action="capability_write",outcome="denied",source="api"} 1`)
}

func TestAnonymousMissingProjectDenialIsRetainedWithoutUserFeedExposure(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "unrelated-audit-user", Name: "Unrelated Audit User", Email: "unrelated-audit@example.invalid",
	})
	require.NoError(t, err)
	auditFacade := auditservice.NewServiceFacade(store, &integrationAuditWriter{}, metrics.NewMetrics())
	handler := EnhancedAnonymousAuditMiddleware(auditFacade)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/project/999999/runners", nil)
	request = mux.SetURLVars(request, map[string]string{"project_id": "999999"})
	request = helpers.SetContextValue(request, "store", store)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Nil(t, events[0].ProjectID)
	require.NotNil(t, events[0].ObjectType)
	assert.Equal(t, db.EventProjectRunnerAudit, *events[0].ObjectType)
	userEvents, err := store.GetUserEvents(user.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, userEvents)
}
