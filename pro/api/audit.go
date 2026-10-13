package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

const auditProOnly = "Filters and export of the audit log are available in Semaphore PRO"

type AuditController struct{}

func NewAuditController(db.Store) *AuditController { return &AuditController{} }

func (*AuditController) GetEvents(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteErrorStatus(w, auditProOnly, http.StatusForbidden)
}

func (*AuditController) ExportEvents(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteErrorStatus(w, auditProOnly, http.StatusForbidden)
}
