package projects

import (
	"net/http"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// unavailableDeploymentWindowController preserves the Community route shape
// without exposing an Enhanced-only governance surface.
type unavailableDeploymentWindowController struct{}

var _ pro_interfaces.DeploymentWindowController = (*unavailableDeploymentWindowController)(nil)

func NewDeploymentWindowController(
	_ pro_interfaces.DeploymentWindowGovernanceServiceFacade,
	_ db.WorkflowManager,
) pro_interfaces.DeploymentWindowController {
	return &unavailableDeploymentWindowController{}
}

func (*unavailableDeploymentWindowController) GetPolicy(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableDeploymentWindowController) SavePolicy(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableDeploymentWindowController) ResetPolicy(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableDeploymentWindowController) Preview(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableDeploymentWindowController) CurrentStatus(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableDeploymentWindowController) DecisionHistory(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
