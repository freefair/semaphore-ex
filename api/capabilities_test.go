package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capabilityFacadeStub struct {
	decision        pro_interfaces.CapabilityDecision
	resolveError    error
	resolutionCount int
	received        pro_interfaces.CapabilitySnapshot
	configured      pro_interfaces.CapabilityConfiguration
	configuredBy    pro_interfaces.CapabilityRequest
	listed          bool
	backgroundValue string
}

func (f *capabilityFacadeStub) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	f.resolutionCount++
	return pro_interfaces.NewCapabilitySnapshot(
		request,
		[]pro_interfaces.CapabilityDecision{f.decision},
	), f.resolveError
}

func (f *capabilityFacadeStub) Configure(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
	configuration pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	f.configured = configuration
	f.configuredBy = request
	return pro_interfaces.NewCapabilitySnapshot(
		request,
		[]pro_interfaces.CapabilityDecision{f.decision},
	), nil
}

func (f *capabilityFacadeStub) ListRecords(
	context.Context,
	pro_interfaces.CapabilitySnapshot,
) ([]pro_interfaces.CapabilityTestRecordDTO, error) {
	f.listed = true
	return []pro_interfaces.CapabilityTestRecordDTO{{ID: 1, Value: "kept", Source: "api"}}, nil
}

func (f *capabilityFacadeStub) CreateRecord(
	_ context.Context,
	snapshot pro_interfaces.CapabilitySnapshot,
	value string,
) (pro_interfaces.CapabilityTestRecordDTO, error) {
	f.received = snapshot
	return pro_interfaces.CapabilityTestRecordDTO{ID: 1, Value: value, Source: "api"}, nil
}

func (f *capabilityFacadeStub) RunBackgroundAction(
	_ context.Context,
	_ pro_interfaces.CapabilitySnapshot,
	value string,
) (pro_interfaces.CapabilityTestRecordDTO, error) {
	f.backgroundValue = value
	return pro_interfaces.CapabilityTestRecordDTO{ID: 2, Value: value, Source: "worker"}, nil
}

func TestCapabilityControllerConfiguresThroughFacade(t *testing.T) {
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		nil,
		nil,
	)}
	controller := NewCapabilityController(facade, nil)
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/capabilities/lifecycle-test",
		bytes.NewBufferString(`{"state":"active"}`),
	)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.Configure(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, pro_interfaces.CapabilityLifecycleTest, facade.configured.ID)
	assert.Equal(t, pro_interfaces.CapabilityStateActive, facade.configured.State)
	assert.Equal(t, 7, facade.configuredBy.UserID)
	assert.True(t, facade.configuredBy.IsAdmin)
}

func TestCapabilityControllerListsAndRunsBackgroundAction(t *testing.T) {
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessExecute,
		},
		nil,
	)}
	controller := NewCapabilityController(facade, nil)

	listHandler := controller.SnapshotMiddleware(
		controller.Require(pro_interfaces.CapabilityAccessRead)(http.HandlerFunc(controller.ListRecords)),
	)
	listRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	listRequest = helpers.SetContextValue(listRequest, "user", &db.User{ID: 7, Admin: true})
	listRecorder := httptest.NewRecorder()
	listHandler.ServeHTTP(listRecorder, listRequest)

	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assert.True(t, facade.listed)
	assert.Contains(t, listRecorder.Body.String(), `"value":"kept"`)

	backgroundHandler := controller.SnapshotMiddleware(
		controller.Require(pro_interfaces.CapabilityAccessExecute)(
			http.HandlerFunc(controller.RunBackgroundAction),
		),
	)
	backgroundRequest := httptest.NewRequest(
		http.MethodPost,
		"/",
		bytes.NewBufferString(`{"value":"background"}`),
	)
	backgroundRequest = helpers.SetContextValue(backgroundRequest, "user", &db.User{ID: 7, Admin: true})
	backgroundRecorder := httptest.NewRecorder()
	backgroundHandler.ServeHTTP(backgroundRecorder, backgroundRequest)

	assert.Equal(t, http.StatusCreated, backgroundRecorder.Code)
	assert.Equal(t, "background", facade.backgroundValue)
	assert.Contains(t, backgroundRecorder.Body.String(), `"source":"worker"`)
}

func TestCapabilityMiddlewareResolvesOneSnapshotForRequest(t *testing.T) {
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessWrite},
		nil,
	)}
	controller := NewCapabilityController(facade, nil)
	handler := controller.SnapshotMiddleware(
		controller.Require(pro_interfaces.CapabilityAccessWrite)(http.HandlerFunc(controller.CreateRecord)),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/capabilities/lifecycle-test/records",
		bytes.NewBufferString(`{"value":"created"}`),
	)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	assert.Equal(t, 1, facade.resolutionCount)
	assert.Equal(t, 7, facade.received.Request().UserID)
}

func TestCapabilityMiddlewareMapsLifecycleDenials(t *testing.T) {
	tests := []struct {
		name   string
		state  pro_interfaces.CapabilityState
		reason pro_interfaces.CapabilityReasonCode
		status int
	}{
		{"unavailable", pro_interfaces.CapabilityStateUnavailable, pro_interfaces.CapabilityReasonProviderUnavailable, http.StatusNotFound},
		{"disabled", pro_interfaces.CapabilityStateDisabled, pro_interfaces.CapabilityReasonDisabledByAdmin, http.StatusForbidden},
		{"expired", pro_interfaces.CapabilityStateExpired, pro_interfaces.CapabilityReasonEntitlementExpired, http.StatusForbidden},
		{"read only", pro_interfaces.CapabilityStateReadOnly, pro_interfaces.CapabilityReasonReadOnly, http.StatusForbidden},
		{"insufficient permission", pro_interfaces.CapabilityStateInsufficientPermission, pro_interfaces.CapabilityReasonInsufficientPermission, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityLifecycleTest,
				tt.state,
				tt.reason,
				nil,
				nil,
			)}
			controller := NewCapabilityController(facade, nil)
			handler := controller.SnapshotMiddleware(
				controller.Require(pro_interfaces.CapabilityAccessWrite)(http.HandlerFunc(controller.CreateRecord)),
			)
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"value":"blocked"}`))
			request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assert.Equal(t, tt.status, recorder.Code)
			var response map[string]string
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, "CAPABILITY_DENIED", response["error"])
			assert.Equal(t, string(tt.state), response["state"])
			assert.Equal(t, string(tt.reason), response["reason"])
			assert.Equal(t, string(pro_interfaces.CapabilityAccessWrite), response["required_access"])
		})
	}
}

func TestDelegatedProjectRolesMiddlewareFailsClosedAndKeepsBreakGlass(t *testing.T) {
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityProjectRoles,
		pro_interfaces.CapabilityStateUnavailable,
		pro_interfaces.CapabilityReasonProviderUnavailable,
		nil,
		nil,
	)}
	controller := NewCapabilityController(facade, nil)
	handler := controller.DelegatedProjectRolesSnapshotMiddleware(
		controller.RequireDelegatedProjectRolesForRequest(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		)),
	)

	delegatedRequest := httptest.NewRequest(http.MethodPost, "/api/project/1/templates/1", nil)
	delegatedRequest = helpers.SetContextValue(delegatedRequest, "user", &db.User{ID: 7})
	delegatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(delegatedResponse, delegatedRequest)
	assert.Equal(t, http.StatusNotFound, delegatedResponse.Code)

	adminRequest := httptest.NewRequest(http.MethodPost, "/api/project/1/templates/1", nil)
	adminRequest = helpers.SetContextValue(adminRequest, "user", &db.User{ID: 8, Admin: true})
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	assert.Equal(t, http.StatusNoContent, adminResponse.Code)
	assert.Equal(t, 1, facade.resolutionCount)
}

func TestCapabilityMiddlewareHidesResolutionError(t *testing.T) {
	var logOutput bytes.Buffer
	logger := log.StandardLogger()
	previousOutput := logger.Out
	logger.SetOutput(&logOutput)
	defer logger.SetOutput(previousOutput)
	facade := &capabilityFacadeStub{resolveError: errors.New(securityfixtures.TripwireValues[0])}
	controller := NewCapabilityController(facade, nil)
	handler := controller.SnapshotMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.JSONEq(t, `{"error":"CAPABILITY_PROVIDER_ERROR"}`, recorder.Body.String())
	securityfixtures.AssertTripwiresAbsent(t, recorder.Body.String(), logOutput.String())
}

func TestCapabilityValidationErrorDoesNotEchoInput(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeCapabilityError(recorder, common_errors.NewValidationError(securityfixtures.TripwireValues[0]))

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.JSONEq(t, `{"error":"CAPABILITY_INPUT_INVALID"}`, recorder.Body.String())
	securityfixtures.AssertTripwiresAbsent(t, recorder.Body.String())
}
