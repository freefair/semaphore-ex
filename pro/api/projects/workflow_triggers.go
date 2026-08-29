package projects

import (
	"net/http"

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
func (c *workflowTriggerController) TestTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) GetTriggerHistory(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) InvokeAPITrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (c *workflowTriggerController) InvokeWebhookTrigger(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
