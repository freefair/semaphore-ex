package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// unavailablePolicyGuardrailController preserves the Enhanced route shape
// while ensuring Community never exposes policy governance data.
type unavailablePolicyGuardrailController struct{}

var _ pro_interfaces.PolicyGuardrailController = (*unavailablePolicyGuardrailController)(nil)

func NewPolicyGuardrailController(_ pro_interfaces.PolicyGuardrailGovernanceServiceFacade) pro_interfaces.PolicyGuardrailController {
	return &unavailablePolicyGuardrailController{}
}

func (*unavailablePolicyGuardrailController) Get(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) SaveDraft(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Validate(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) TestFixture(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Diff(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Publish(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Impact(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Revisions(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Evaluations(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailablePolicyGuardrailController) Rollback(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
