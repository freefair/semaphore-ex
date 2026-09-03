package projects

import (
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowTriggerController struct{}

var _ pro_interfaces.WorkflowTriggerController = (*workflowTriggerController)(nil)

func NewWorkflowTriggerController(pro_interfaces.WorkflowTriggerService) pro_interfaces.WorkflowTriggerController {
	return &workflowTriggerController{}
}

func (c *workflowTriggerController) GetTriggers(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) AddTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) GetTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) UpdateTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) DeleteTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) SetTriggerEnabled(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) RotateTriggerCredential(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) StageWebhookSigningKey(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) BootstrapWebhookSigningKey(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) PromoteWebhookSigningKey(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) RevokeWebhookSigningKey(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) TestTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) GetTriggerHistory(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) InvokeAPITrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) InvokeWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	// Community has no webhook implementation. Reject bearer-shaped input at the
	// public boundary so it can never appear to be an API-trigger fallback.
	if strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}
