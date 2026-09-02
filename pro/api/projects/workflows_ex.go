package projects

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"net/http"
)

func (c *workflowController) ValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) GetWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}

func (c *workflowController) GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) DiffWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) RestoreWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) PreviewWorkflow(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) RetryWorkflowRunReconciliation(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *workflowController) GetWorkflowApprovalInbox(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []struct{}{})
}
