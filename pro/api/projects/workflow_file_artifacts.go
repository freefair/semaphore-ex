package projects

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowFileArtifactController struct{}

var _ pro_interfaces.WorkflowFileArtifactController = (*workflowFileArtifactController)(nil)

func NewWorkflowFileArtifactController(pro_interfaces.WorkflowFileArtifactServiceFacade) pro_interfaces.WorkflowFileArtifactController {
	return &workflowFileArtifactController{}
}

func (*workflowFileArtifactController) BeginWorkflowFileArtifact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowFileArtifactController) AppendWorkflowFileArtifact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowFileArtifactController) FinalizeWorkflowFileArtifact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowFileArtifactController) GetWorkflowFileArtifacts(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}

func (*workflowFileArtifactController) GetWorkflowFileArtifact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*workflowFileArtifactController) DownloadWorkflowFileArtifact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
