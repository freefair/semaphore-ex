package projects

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
)

type unavailableCrossProjectTemplateController struct{}

func NewCrossProjectTemplateController(_ pro_interfaces.CrossProjectTemplateService) pro_interfaces.CrossProjectTemplateController {
	return &unavailableCrossProjectTemplateController{}
}
func (*unavailableCrossProjectTemplateController) PublishTemplateVersion(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) ListTemplateVersions(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) CreateGrant(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) ListGrants(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) UpdateGrant(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) DeleteGrant(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) AcceptGrant(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) RevokeGrant(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) ListReferences(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*unavailableCrossProjectTemplateController) ConfigureCrossProjectTemplateAudit(pro_interfaces.AuditServiceFacade) {
}
