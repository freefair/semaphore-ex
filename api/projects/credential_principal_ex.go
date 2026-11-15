package projects

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	log "github.com/sirupsen/logrus"
)

// recordScheduleCredentialPrincipal makes the acting user answer for the
// global credentials the schedule's tasks consume. Any change by another user
// moves the responsibility, so a user without consume permission cannot
// switch on unattended use set up by someone else.
func recordScheduleCredentialPrincipal(r *http.Request, scheduleID int) {
	recordCredentialPrincipal(r, "schedule", scheduleID, func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetScheduleCredentialPrincipal(scheduleID, userID)
	})
}

func recordIntegrationCredentialPrincipal(r *http.Request, integrationID int) {
	recordCredentialPrincipal(r, "integration", integrationID, func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetIntegrationCredentialPrincipal(integrationID, userID)
	})
}

// recordCredentialPrincipal is best effort after a committed change: a failure
// leaves the previous principal, whose permissions the resolver still checks
// at every dispatch, and is logged for the operator.
func recordCredentialPrincipal(r *http.Request, kind string, triggerID int, set func(db.TaskCredentialPrincipalRepository, int) error) {
	repo, ok := helpers.Store(r).(db.TaskCredentialPrincipalRepository)
	user := helpers.UserFromContext(r)
	if !ok || user == nil {
		return
	}
	if err := set(repo, user.ID); err != nil {
		log.WithError(err).WithFields(log.Fields{"kind": kind, "id": triggerID, "context": "credential_principal"}).
			Error("failed to record the credential principal")
	}
}
