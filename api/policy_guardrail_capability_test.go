package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestPolicyGuardrailRouteCapabilityUnavailableIsHidden(t *testing.T) {
	facade := &capabilityFacadeStub{decision: pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityPolicyGuardrails,
		pro_interfaces.CapabilityStateUnavailable,
		pro_interfaces.CapabilityReasonProviderUnavailable,
		nil,
		nil,
	)}
	controller := NewCapabilityController(facade, nil)
	handler := controller.SnapshotMiddleware(controller.RequireCapability(
		pro_interfaces.CapabilityPolicyGuardrails,
		pro_interfaces.CapabilityAccessRead,
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	request := httptest.NewRequest(http.MethodGet, "/api/policy-guardrails", nil)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), string(pro_interfaces.CapabilityPolicyGuardrails))
}
