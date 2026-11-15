package projects

import (
	"net/http"
	"sync"
	"time"

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
// A change first claims the trigger in the database: the claim removes the
// principal, so no node can dispatch with the previous one while the change
// is written, and refuses a concurrent change, so the principal is always
// the user whose change came last. A successful change makes the acting user
// the principal; a failed one leaves none until the next successful change.

// recordScheduleCredentialPrincipal sets the principal of a new schedule.
func recordScheduleCredentialPrincipal(r *http.Request, scheduleID int) {
	recordNewTriggerPrincipal(r, db.CredentialPrincipalSchedule, func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetScheduleCredentialPrincipal(scheduleID, userID)
	})
}

// recordIntegrationCredentialPrincipal sets the principal of a new integration.
func recordIntegrationCredentialPrincipal(r *http.Request, integrationID int) {
	recordNewTriggerPrincipal(r, db.CredentialPrincipalIntegration, func(repo db.TaskCredentialPrincipalRepository, userID int) error {
		return repo.SetIntegrationCredentialPrincipal(integrationID, userID)
	})
}

// ScheduleCredentialPrincipalMiddleware wraps the write routes of one
// schedule (update, activation, deletion). It needs the schedule in the
// context.
func ScheduleCredentialPrincipalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schedule, ok := helpers.GetFromContext(r, "schedule").(db.Schedule)
		required := db.CanUpdateSchedules
		if r.Method == http.MethodDelete {
			required = db.CanDeleteSchedules
		}
		if isReadOnlyMethod(r.Method) || !ok || !mayChangeTrigger(r, required) {
			next.ServeHTTP(w, r)
			return
		}
		guardCredentialPrincipalChange(w, r, next, db.CredentialPrincipalSchedule, []int{schedule.ID})
	})
}

// IntegrationCredentialPrincipalMiddleware wraps the write routes of an
// integration and of its aliases, matchers and extracted values. A project
// alias can start every integration of the project, so its changes affect
// all of them.
func IntegrationCredentialPrincipalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required := db.CanUpdateIntegrations
		if r.Method == http.MethodDelete {
			required = db.CanDeleteIntegrations
		}
		if isReadOnlyMethod(r.Method) || !mayChangeTrigger(r, required) {
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
		guardCredentialPrincipalChange(w, r, next, db.CredentialPrincipalIntegration, ids)
	})
}

// mayChangeTrigger mirrors the route's permission check, so a request the
// route will refuse does not touch any principal.
func mayChangeTrigger(r *http.Request, required db.ProjectUserPermission) bool {
	user := helpers.UserFromContext(r)
	permissions, _ := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	return user != nil && (user.Admin || permissions.Can(required))
}

func guardCredentialPrincipalChange(w http.ResponseWriter, r *http.Request, next http.Handler, kind db.CredentialPrincipalKind, ids []int) {
	repo, ok := helpers.Store(r).(db.TaskCredentialPrincipalRepository)
	if !ok || len(ids) == 0 {
		next.ServeHTTP(w, r)
		return
	}
	claims := make(map[int]string, len(ids))
	release := func() {
		for id, token := range claims {
			if err := repo.AbortCredentialPrincipalChange(kind, id, token); err != nil {
				logPrincipalError(err, kind, id, "failed to release the credential principal claim")
			}
		}
	}
	for _, id := range ids {
		token, claimed, err := repo.BeginCredentialPrincipalChange(kind, id)
		if err != nil {
			release()
			logPrincipalError(err, kind, id, "failed to claim the trigger for a change")
			helpers.WriteError(w, err)
			return
		}
		if !claimed {
			release()
			helpers.WriteErrorStatus(w, "Another change to this "+string(kind)+" is in progress; try again", http.StatusConflict)
			return
		}
		claims[id] = token
	}
	stopRenewal := renewCredentialPrincipalClaims(repo, kind, claims)
	defer stopRenewal()
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		// A panicking handler leaves no principal behind.
		if recovered := recover(); recovered != nil {
			release()
			panic(recovered)
		}
	}()
	next.ServeHTTP(recorder, r)
	stopRenewal()
	user := helpers.UserFromContext(r)
	if recorder.status < 200 || recorder.status > 299 || user == nil {
		release()
		return
	}
	for id, token := range claims {
		if err := repo.FinishCredentialPrincipalChange(kind, id, token, user.ID); err != nil {
			logPrincipalError(err, kind, id, "failed to record the credential principal")
		}
	}
}

// renewCredentialPrincipalClaims keeps the claims of a running change
// alive, so they expire only when this node dies or stalls. The returned
// function stops the renewal and may be called more than once.
func renewCredentialPrincipalClaims(repo db.TaskCredentialPrincipalRepository, kind db.CredentialPrincipalKind, claims map[int]string) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(db.CredentialPrincipalClaimLifetime / 5)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				for id, token := range claims {
					if err := repo.RenewCredentialPrincipalChange(kind, id, token); err != nil {
						logPrincipalError(err, kind, id, "failed to renew the credential principal claim")
					}
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

func recordNewTriggerPrincipal(r *http.Request, kind db.CredentialPrincipalKind, set func(db.TaskCredentialPrincipalRepository, int) error) {
	repo, ok := helpers.Store(r).(db.TaskCredentialPrincipalRepository)
	user := helpers.UserFromContext(r)
	if !ok || user == nil {
		return
	}
	if err := set(repo, user.ID); err != nil {
		logPrincipalError(err, kind, 0, "failed to record the credential principal")
	}
}

func logPrincipalError(err error, kind db.CredentialPrincipalKind, id int, message string) {
	log.WithError(err).WithFields(log.Fields{"kind": kind, "id": id, "context": "credential_principal"}).Error(message)
}

func isReadOnlyMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// statusRecorder keeps the status the client receives: the first written
// header, or 200 for a body written without one.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(body)
}
