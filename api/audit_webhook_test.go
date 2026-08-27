package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
func (s *auditWebhookServiceStub) SetPaused(_ context.Context, paused bool) (pro_interfaces.AuditWebhookConfigDTO, error) {
	s.pausedWith = &paused
	s.config.Paused = paused
	return s.config, s.err
}
func (s *auditWebhookServiceStub) DeliveryHistory(context.Context, db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return s.history, s.err
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
