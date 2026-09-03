package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditWebhookServiceStub struct {
	config         pro_interfaces.AuditWebhookConfigDTO
	configuredWith pro_interfaces.AuditWebhookConfigInput
	delivery       pro_interfaces.AuditWebhookDeliveryDTO
	history        []pro_interfaces.AuditWebhookDeliveryDTO
	attempts       []pro_interfaces.AuditWebhookDeliveryAttemptDTO
	historyParams  db.RetrieveQueryParams
	attemptParams  db.RetrieveQueryParams
	attemptID      int
	testKey        pro_interfaces.AuditWebhookSigningKey
	revisions      []int
	secret         pro_interfaces.AuditWebhookSigningSecretDTO
	signingStatus  pro_interfaces.AuditWebhookSigningStatusDTO
	pausedWith     *bool
	err            error
}

func (*auditWebhookServiceStub) PrepareDelivery(context.Context, pro_interfaces.AuditEvent) (*db.AuditWebhookDelivery, error) {
	return nil, nil
}
func (*auditWebhookServiceStub) Notify() {}
func (s *auditWebhookServiceStub) Configuration(context.Context) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return s.config, s.err
}
func (s *auditWebhookServiceStub) Configure(_ context.Context, input pro_interfaces.AuditWebhookConfigInput) (pro_interfaces.AuditWebhookConfigDTO, error) {
	s.configuredWith = input
	return s.config, s.err
}
func (s *auditWebhookServiceStub) TestDelivery(context.Context) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return s.delivery, s.err
}
func (s *auditWebhookServiceStub) TestDeliveryWithSigningKey(_ context.Context, key pro_interfaces.AuditWebhookSigningKey) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	s.testKey = key
	return s.delivery, s.err
}
func (s *auditWebhookServiceStub) SetPaused(_ context.Context, paused bool) (pro_interfaces.AuditWebhookConfigDTO, error) {
	s.pausedWith = &paused
	s.config.Paused = paused
	return s.config, s.err
}
func (s *auditWebhookServiceStub) DeliveryHistory(_ context.Context, params db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryDTO, error) {
	s.historyParams = params
	return s.history, s.err
}
func (s *auditWebhookServiceStub) DeliveryAttemptHistory(_ context.Context, id int, params db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryAttemptDTO, error) {
	s.attemptID = id
	s.attemptParams = params
	return s.attempts, s.err
}
func (s *auditWebhookServiceStub) CreateSigningSecret(_ context.Context, revision int) (pro_interfaces.AuditWebhookSigningSecretDTO, error) {
	s.revisions = append(s.revisions, revision)
	return s.secret, s.err
}
func (s *auditWebhookServiceStub) StageSigningSecret(_ context.Context, revision int) (pro_interfaces.AuditWebhookSigningSecretDTO, error) {
	s.revisions = append(s.revisions, revision)
	return s.secret, s.err
}
func (s *auditWebhookServiceStub) PromoteSigningSecret(_ context.Context, revision int) (pro_interfaces.AuditWebhookSigningStatusDTO, error) {
	s.revisions = append(s.revisions, revision)
	return s.signingStatus, s.err
}
func (s *auditWebhookServiceStub) RevokeNextSigningSecret(_ context.Context, revision int) (pro_interfaces.AuditWebhookSigningStatusDTO, error) {
	s.revisions = append(s.revisions, revision)
	return s.signingStatus, s.err
}
func (*auditWebhookServiceStub) Start()       {}
func (*auditWebhookServiceStub) Close() error { return nil }

func TestAuditWebhookConfigurationIsSecretWriteOnly(t *testing.T) {
	secret := "api-write-only-secret"
	service := &auditWebhookServiceStub{config: pro_interfaces.AuditWebhookConfigDTO{
		Endpoint: "https://audit.example.test/events", CredentialConfigured: true,
	}}
	audit := &auditRecorderStub{}
	controller := NewAuditWebhookController(service, audit)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/audit-webhook", bytes.NewBufferString(
		`{"endpoint":"https://audit.example.test/events","credential":"`+secret+`"}`,
	))

	controller.Configure(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, service.configuredWith.Credential)
	assert.Equal(t, secret, *service.configuredWith.Credential)
	assert.NotContains(t, recorder.Body.String(), secret)
	assert.NotContains(t, recorder.Body.String(), "credential\"")
	assert.Contains(t, recorder.Body.String(), `"credential_configured":true`)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionWebhookConfigure, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditTargetWebhook, audit.events[0].TargetType)
}

func TestAuditWebhookConfigurationRejectsUnknownAndMultipleJSONValues(t *testing.T) {
	for _, body := range []string{
		`{"endpoint":"https://audit.example.test","token":"must-not-be-accepted"}`,
		`{"endpoint":"https://audit.example.test"}{}`,
	} {
		service := &auditWebhookServiceStub{}
		controller := NewAuditWebhookController(service, &auditRecorderStub{})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/audit-webhook", bytes.NewBufferString(body))

		controller.Configure(recorder, request)

		assert.Equal(t, http.StatusBadRequest, recorder.Code)
		assert.Empty(t, service.configuredWith.Endpoint)
	}
}

func TestAuditWebhookAdminWorkflowsAndPagination(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	delivery := pro_interfaces.AuditWebhookDeliveryDTO{
		ID: 1, EventID: "0123456789abcdef0123456789abcdef",
		Status: db.AuditWebhookDeliverySucceeded, Attempts: 1, CreatedAt: now, UpdatedAt: now,
	}
	service := &auditWebhookServiceStub{delivery: delivery, history: []pro_interfaces.AuditWebhookDeliveryDTO{delivery}}
	controller := NewAuditWebhookController(service, &auditRecorderStub{})

	testRecorder := httptest.NewRecorder()
	controller.TestDelivery(testRecorder, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/test", nil))
	assert.Equal(t, http.StatusCreated, testRecorder.Code)
	assert.Contains(t, testRecorder.Body.String(), delivery.EventID)

	pauseRecorder := httptest.NewRecorder()
	controller.Pause(pauseRecorder, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/pause", nil))
	require.NotNil(t, service.pausedWith)
	assert.True(t, *service.pausedWith)

	resumeRecorder := httptest.NewRecorder()
	controller.Resume(resumeRecorder, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/resume", nil))
	require.NotNil(t, service.pausedWith)
	assert.False(t, *service.pausedWith)

	historyRecorder := httptest.NewRecorder()
	controller.History(historyRecorder, httptest.NewRequest(http.MethodGet, "/api/audit-webhook/deliveries?count=25&offset=0", nil))
	assert.Equal(t, http.StatusOK, historyRecorder.Code)
	var history []pro_interfaces.AuditWebhookDeliveryDTO
	require.NoError(t, json.Unmarshal(historyRecorder.Body.Bytes(), &history))
	require.Len(t, history, 1)
	assert.Equal(t, delivery.EventID, history[0].EventID)

	invalidRecorder := httptest.NewRecorder()
	controller.History(invalidRecorder, httptest.NewRequest(http.MethodGet, "/api/audit-webhook/deliveries?count=101", nil))
	assert.Equal(t, http.StatusBadRequest, invalidRecorder.Code)
}

func TestAuditWebhookSigningSecretControllersSelectKeysAndKeepSecretsWriteOnly(t *testing.T) {
	secret := "swhsec_one_time_only"
	delivery := pro_interfaces.AuditWebhookDeliveryDTO{ID: 12, EventID: "0123456789abcdef", Status: db.AuditWebhookDeliverySucceeded}
	attempt := pro_interfaces.AuditWebhookDeliveryAttemptDTO{ID: 8, DeliveryID: delivery.ID, EventID: delivery.EventID, Attempt: 1, KeyID: "swhkid_attempt", Outcome: db.AuditWebhookDeliveryAttemptSucceeded}
	service := &auditWebhookServiceStub{
		config:   pro_interfaces.AuditWebhookConfigDTO{Endpoint: "https://audit.example.test/events", AuditWebhookSigningStatusDTO: pro_interfaces.AuditWebhookSigningStatusDTO{CurrentKeyID: "swhkid_current", Revision: 4}},
		delivery: delivery, history: []pro_interfaces.AuditWebhookDeliveryDTO{delivery}, attempts: []pro_interfaces.AuditWebhookDeliveryAttemptDTO{attempt},
		secret:        pro_interfaces.AuditWebhookSigningSecretDTO{Secret: secret, AuditWebhookSigningStatusDTO: pro_interfaces.AuditWebhookSigningStatusDTO{CurrentKeyID: "swhkid_current", Revision: 5}},
		signingStatus: pro_interfaces.AuditWebhookSigningStatusDTO{CurrentKeyID: "swhkid_current", Revision: 7},
	}
	controller := NewAuditWebhookController(service, nil)

	for _, request := range []struct {
		method string
		path   string
		handle http.HandlerFunc
		status int
	}{
		{http.MethodPost, "/api/audit-webhook/signing-secret?revision=4", controller.CreateSigningSecret, http.StatusCreated},
		{http.MethodPost, "/api/audit-webhook/signing-secret/stage?revision=5", controller.StageSigningSecret, http.StatusCreated},
		{http.MethodPost, "/api/audit-webhook/signing-secret/promote?revision=6", controller.PromoteSigningSecret, http.StatusOK},
		{http.MethodDelete, "/api/audit-webhook/signing-secret/next?revision=7", controller.RevokeNextSigningSecret, http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		request.handle(recorder, httptest.NewRequest(request.method, request.path, nil))
		assert.Equal(t, request.status, recorder.Code)
		if request.status == http.StatusCreated {
			assert.Contains(t, recorder.Body.String(), secret)
		}
	}
	assert.Equal(t, []int{4, 5, 6, 7}, service.revisions)

	current := httptest.NewRecorder()
	controller.TestDelivery(current, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/test?key=current", nil))
	assert.Equal(t, http.StatusCreated, current.Code)
	assert.Equal(t, pro_interfaces.AuditWebhookSigningKeyCurrent, service.testKey)
	next := httptest.NewRecorder()
	controller.TestDelivery(next, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/test?key=next", nil))
	assert.Equal(t, http.StatusCreated, next.Code)
	assert.Equal(t, pro_interfaces.AuditWebhookSigningKeyNext, service.testKey)

	status := httptest.NewRecorder()
	controller.GetConfiguration(status, httptest.NewRequest(http.MethodGet, "/api/audit-webhook", nil))
	history := httptest.NewRecorder()
	controller.History(history, httptest.NewRequest(http.MethodGet, "/api/audit-webhook/deliveries?count=1", nil))
	attemptHistory := httptest.NewRecorder()
	controller.AttemptHistory(attemptHistory, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/audit-webhook/deliveries/12/attempts?count=1", nil), map[string]string{"delivery_id": "12"}))
	assert.Equal(t, http.StatusOK, attemptHistory.Code)
	assert.Equal(t, 12, service.attemptID)
	assert.Equal(t, 1, service.attemptParams.Count)
	assert.Equal(t, 1, service.historyParams.Count)
	assert.NotContains(t, status.Body.String(), secret)
	assert.NotContains(t, history.Body.String(), secret)
	assert.NotContains(t, attemptHistory.Body.String(), secret)
	assert.NotContains(t, attemptHistory.Body.String(), "signature")
}

func TestAuditWebhookSigningSecretControllersRejectInvalidRevisionAndMapConflicts(t *testing.T) {
	service := &auditWebhookServiceStub{err: pro_interfaces.ErrAuditWebhookSigningStateConflict}
	controller := NewAuditWebhookController(service, nil)
	conflict := httptest.NewRecorder()
	controller.PromoteSigningSecret(conflict, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/signing-secret/promote?revision=3", nil))
	assert.Equal(t, http.StatusConflict, conflict.Code)

	service.err = pro_interfaces.ErrAuditWebhookInvalidSigningKey
	invalidOperation := httptest.NewRecorder()
	controller.RevokeNextSigningSecret(invalidOperation, httptest.NewRequest(http.MethodDelete, "/api/audit-webhook/signing-secret/next?revision=3", nil))
	assert.Equal(t, http.StatusBadRequest, invalidOperation.Code)
	invalidRevision := httptest.NewRecorder()
	controller.CreateSigningSecret(invalidRevision, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/signing-secret?revision=-1", nil))
	assert.Equal(t, http.StatusBadRequest, invalidRevision.Code)
	invalidKey := httptest.NewRecorder()
	controller.TestDelivery(invalidKey, httptest.NewRequest(http.MethodPost, "/api/audit-webhook/test?key=retired", nil))
	assert.Equal(t, http.StatusBadRequest, invalidKey.Code)
}

func TestAuditWebhookEndpointsRequireAdministratorPermission(t *testing.T) {
	service := &auditWebhookServiceStub{}
	controller := NewAuditWebhookController(service, nil)
	handler := adminMiddleware(http.HandlerFunc(controller.GetConfiguration))

	nonAdminRecorder := httptest.NewRecorder()
	nonAdminRequest := helpers.SetContextValue(
		httptest.NewRequest(http.MethodGet, "/api/audit-webhook", nil), "user", &db.User{ID: 7},
	)
	handler.ServeHTTP(nonAdminRecorder, nonAdminRequest)
	assert.Equal(t, http.StatusForbidden, nonAdminRecorder.Code)

	adminRecorder := httptest.NewRecorder()
	adminRequest := helpers.SetContextValue(
		httptest.NewRequest(http.MethodGet, "/api/audit-webhook", nil), "user", &db.User{ID: 8, Admin: true},
	)
	handler.ServeHTTP(adminRecorder, adminRequest)
	assert.Equal(t, http.StatusOK, adminRecorder.Code)
}

func TestAuditWebhookSigningRoutesRequireAdministratorPermission(t *testing.T) {
	controller := NewAuditWebhookController(&auditWebhookServiceStub{}, nil)
	for _, handler := range []http.HandlerFunc{
		controller.CreateSigningSecret,
		controller.StageSigningSecret,
		controller.PromoteSigningSecret,
		controller.RevokeNextSigningSecret,
		controller.AttemptHistory,
	} {
		wrapped := adminMiddleware(http.HandlerFunc(handler))
		denied := httptest.NewRecorder()
		request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/audit-webhook", nil), "user", &db.User{ID: 7})
		wrapped.ServeHTTP(denied, request)
		assert.Equal(t, http.StatusForbidden, denied.Code)
	}
}

func TestAuditWebhookUnavailableAndNotConfiguredContracts(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{pro_interfaces.ErrAuditWebhookUnavailable, http.StatusNotFound},
		{pro_interfaces.ErrAuditWebhookNotConfigured, http.StatusConflict},
		{pro_interfaces.ErrAuditWebhookInvalidInput, http.StatusBadRequest},
	}
	for _, test := range tests {
		service := &auditWebhookServiceStub{err: test.err}
		controller := NewAuditWebhookController(service, nil)
		recorder := httptest.NewRecorder()

		controller.GetConfiguration(recorder, httptest.NewRequest(http.MethodGet, "/api/audit-webhook", nil))

		assert.Equal(t, test.status, recorder.Code)
	}
}
