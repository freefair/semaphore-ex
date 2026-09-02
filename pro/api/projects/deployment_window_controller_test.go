package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestCommunityDeploymentWindowControllerIsStablyUnavailable(t *testing.T) {
	controller := NewDeploymentWindowController(nil, nil)
	for _, handler := range []func(http.ResponseWriter, *http.Request){
		controller.GetPolicy, controller.SavePolicy, controller.ResetPolicy,
		controller.Preview, controller.CurrentStatus, controller.DecisionHistory,
	} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
	_, ok := controller.(pro_interfaces.DeploymentWindowController)
	assert.True(t, ok)
}
