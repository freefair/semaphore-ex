package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommunityWorkflowArtifactRetentionControllerIsUnavailable(t *testing.T) {
	controller := NewWorkflowArtifactRetentionController(nil)
	response := httptest.NewRecorder()
	controller.GetGlobalWorkflowArtifactRetention(response, httptest.NewRequest(http.MethodGet, "/api/workflow-artifact-retention", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
}
