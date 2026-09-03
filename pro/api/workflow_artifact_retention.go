package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowArtifactRetentionController struct{}

var _ pro_interfaces.WorkflowArtifactRetentionController = (*workflowArtifactRetentionController)(nil)

func NewWorkflowArtifactRetentionController(pro_interfaces.WorkflowArtifactRetentionGovernanceServiceFacade) pro_interfaces.WorkflowArtifactRetentionController {
	return &workflowArtifactRetentionController{}
}

func (*workflowArtifactRetentionController) GetGlobalWorkflowArtifactRetention(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowArtifactRetentionController) PublishGlobalWorkflowArtifactRetention(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowArtifactRetentionController) GetProjectWorkflowArtifactRetention(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowArtifactRetentionController) PublishProjectWorkflowArtifactRetention(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
