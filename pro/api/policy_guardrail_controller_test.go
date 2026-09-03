package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestCommunityPolicyGuardrailControllerIsStablyUnavailable(t *testing.T) {
	controller := NewPolicyGuardrailController(nil)
	for _, handler := range []func(http.ResponseWriter, *http.Request){
		controller.Get, controller.SaveDraft, controller.Validate, controller.TestFixture,
		controller.Diff, controller.Publish, controller.Impact, controller.Revisions,
		controller.Evaluations, controller.Rollback,
	} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
	_, ok := controller.(pro_interfaces.PolicyGuardrailController)
	assert.True(t, ok)
}
