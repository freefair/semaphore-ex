package projects

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	log "github.com/sirupsen/logrus"
)

// The credential principal of a schedule or integration answers for the
// global credentials its tasks consume without a starting user. Every change
// that affects when such a task runs or what it receives moves the
// responsibility to the acting user, so a user without consume permission
// cannot reuse someone else's.
//
// The principal is removed before the change and written after it succeeds.
// No node can dispatch with the previous principal while the change is being
// written, and a failed change leaves the trigger without principal: its
// unattended credential use stays blocked until a successful change.

// recordScheduleCredentialPrincipal sets the principal of a new schedule.
func recordScheduleCredentialPrincipal(r *http.Request, scheduleID int) {
	recordCredentialPrincipals(r, "schedule", func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetScheduleCredentialPrincipal(scheduleID, userID)
	})
}

// recordIntegrationCredentialPrincipal sets the principal of a new integration.
func recordIntegrationCredentialPrincipal(r *http.Request, integrationID int) {
	recordCredentialPrincipals(r, "integration", func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetIntegrationCredentialPrincipal(integrationID, userID)
	})
}

// ScheduleCredentialPrincipalMiddleware wraps the write routes of one
// schedule (update, activation). It needs the schedule in the context.
func ScheduleCredentialPrincipalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schedule, ok := helpers.GetFromContext(r, "schedule").(db.Schedule)
		if isReadOnlyMethod(r.Method) || !ok {
			next.ServeHTTP(w, r)
			return
		}
		guardCredentialPrincipalChange(w, r, next, "schedule",
			func(repo db.TaskCredentialPrincipalRepository) error {
				return repo.ClearScheduleCredentialPrincipal(schedule.ID)
			},
			func(repo db.TaskCredentialPrincipalRepository, userID int) error {
				return repo.SetScheduleCredentialPrincipal(schedule.ID, userID)
			})
	})
}

// IntegrationCredentialPrincipalMiddleware wraps the write routes of an
// integration and of its aliases, matchers and extracted values. A project
// alias can start every integration of the project, so its changes affect
// all of them.
func IntegrationCredentialPrincipalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReadOnlyMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		var ids []int
		if integration, ok := helpers.GetFromContext(r, "integration").(db.Integration); ok {
			ids = []int{integration.ID}
		} else if project, ok := helpers.GetFromContext(r, "project").(db.Project); ok {
			integrations, err := helpers.Store(r).GetIntegrations(project.ID, db.RetrieveQueryParams{}, false)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}
			for _, integration := range integrations {
				ids = append(ids, integration.ID)
			}
		}
		guardCredentialPrincipalChange(w, r, next, "integration",
			func(repo db.TaskCredentialPrincipalRepository) error {
				for _, id := range ids {
					if err := repo.ClearIntegrationCredentialPrincipal(id); err != nil {
						return err
					}
				}
				return nil
			},
			func(repo db.TaskCredentialPrincipalRepository, userID int) error {
				for _, id := range ids {
					if err := repo.SetIntegrationCredentialPrincipal(id, userID); err != nil {
						return err
					}
				}
				return nil
			})
	})
}

func guardCredentialPrincipalChange(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	kind string,
	clear func(db.TaskCredentialPrincipalRepository) error,
	set func(db.TaskCredentialPrincipalRepository, int) error,
) {
	repo, ok := helpers.Store(r).(db.TaskCredentialPrincipalRepository)
	if !ok {
		next.ServeHTTP(w, r)
		return
	}
	if err := clear(repo); err != nil {
		log.WithError(err).WithFields(log.Fields{"kind": kind, "context": "credential_principal"}).
			Error("failed to clear the credential principal before a change")
		helpers.WriteError(w, err)
		return
	}
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	next.ServeHTTP(recorder, r)
	if recorder.status < 200 || recorder.status > 299 {
		return
	}
	recordCredentialPrincipals(r, kind, set)
}

func recordCredentialPrincipals(r *http.Request, kind string, set func(db.TaskCredentialPrincipalRepository, int) error) {
	repo, ok := helpers.Store(r).(db.TaskCredentialPrincipalRepository)
	user := helpers.UserFromContext(r)
	if !ok || user == nil {
		return
	}
	if err := set(repo, user.ID); err != nil {
		log.WithError(err).WithFields(log.Fields{"kind": kind, "context": "credential_principal"}).
			Error("failed to record the credential principal")
	}
}

func isReadOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
