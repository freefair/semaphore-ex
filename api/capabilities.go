package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

type capabilitySnapshotContextKey struct{}

// CapabilityController handles capability transport concerns through the facade boundary.
type CapabilityController struct {
	facade pro_interfaces.CapabilityServiceFacade
	audit  pro_interfaces.AuditServiceFacade
}

// NewCapabilityController creates the capability API controller.
func NewCapabilityController(
	facade pro_interfaces.CapabilityServiceFacade,
	audit pro_interfaces.AuditServiceFacade,
) *CapabilityController {
	return &CapabilityController{facade: facade, audit: audit}
}

// SnapshotMiddleware resolves exactly one immutable snapshot for the request.
func (c *CapabilityController) SnapshotMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, ok := capabilityRequestFromHTTP(r)
		if !ok {
			helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
			return
		}
		snapshot, err := c.facade.Resolve(r.Context(), request)
		if err != nil {
			event := capabilityAuditEvent(r, pro_interfaces.AuditActionCapabilityResolve,
				pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonProviderError)
			c.recordAudit(r, event)
			log.WithFields(event.SafeFields()).Error("Failed to resolve capability snapshot")
			helpers.WriteErrorStatus(w, "CAPABILITY_PROVIDER_ERROR", http.StatusServiceUnavailable)
			return
		}
		ctx := context.WithValue(r.Context(), capabilitySnapshotContextKey{}, snapshot)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Require rejects requests that lack the requested lifecycle-test access.
func (c *CapabilityController) Require(access pro_interfaces.CapabilityAccess) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			snapshot, ok := capabilitySnapshotFromHTTP(r)
			if !ok {
				helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
				return
			}
			if err := snapshot.Require(pro_interfaces.CapabilityLifecycleTest, access); err != nil {
				var denied pro_interfaces.CapabilityDeniedError
				reason := pro_interfaces.AuditReasonOperationError
				if errors.As(err, &denied) {
					reason = string(denied.Decision.Reason())
				}
				c.recordAudit(r, capabilityAuditEvent(r, auditActionForAccess(access),
					pro_interfaces.AuditOutcomeDenied, reason))
				writeCapabilityError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Configure updates the non-secret lifecycle-test configuration.
func (c *CapabilityController) Configure(w http.ResponseWriter, r *http.Request) {
	c.configure(w, r, pro_interfaces.CapabilityLifecycleTest)
}

func (c *CapabilityController) ConfigureRuntimeSecrets(w http.ResponseWriter, r *http.Request) {
	c.configure(w, r, pro_interfaces.CapabilityRuntimeSecrets)
}

func (c *CapabilityController) configure(
	w http.ResponseWriter,
	r *http.Request,
	id pro_interfaces.CapabilityID,
) {
	var body struct {
		State     pro_interfaces.CapabilityState `json:"state"`
		ExpiresAt *time.Time                     `json:"expires_at"`
	}
	if !helpers.Bind(w, r, &body) {
		c.recordAudit(r, capabilityAuditEvent(r, pro_interfaces.AuditActionCapabilityConfigure,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput))
		return
	}
	request, ok := capabilityRequestFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}
	snapshot, err := c.facade.Configure(r.Context(), request, pro_interfaces.CapabilityConfiguration{
		ID:        id,
		State:     body.State,
		ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		c.recordCapabilityError(r, pro_interfaces.AuditActionCapabilityConfigure, err)
		writeCapabilityError(w, err)
		return
	}
	decision := snapshot.Decision(id)
	c.recordAudit(r, capabilityAuditEvent(r, pro_interfaces.AuditActionCapabilityConfigure,
		pro_interfaces.AuditOutcomeAllowed, string(decision.Reason())))
	helpers.WriteJSON(w, http.StatusOK, snapshot)
}

// ListRecords returns retained lifecycle-test data.
func (c *CapabilityController) ListRecords(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := capabilitySnapshotFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}
	records, err := c.facade.ListRecords(r.Context(), snapshot)
	if err != nil {
		c.recordCapabilityError(r, pro_interfaces.AuditActionCapabilityRead, err)
		writeCapabilityError(w, err)
		return
	}
	c.recordAudit(r, capabilityAuditEvent(r, pro_interfaces.AuditActionCapabilityRead,
		pro_interfaces.AuditOutcomeAllowed, string(snapshot.Decision(pro_interfaces.CapabilityLifecycleTest).Reason())))
	helpers.WriteJSON(w, http.StatusOK, records)
}

// CreateRecord persists a lifecycle-test record through the request path.
func (c *CapabilityController) CreateRecord(w http.ResponseWriter, r *http.Request) {
	c.createRecord(w, r, pro_interfaces.AuditActionCapabilityWrite, pro_interfaces.AuditSourceAPI, c.facade.CreateRecord)
}

// RunBackgroundAction exercises the separately guarded background entry point.
func (c *CapabilityController) RunBackgroundAction(w http.ResponseWriter, r *http.Request) {
	c.createRecord(w, r, pro_interfaces.AuditActionCapabilityExecute, pro_interfaces.AuditSourceWorker,
		c.facade.RunBackgroundAction)
}

func (c *CapabilityController) createRecord(
	w http.ResponseWriter,
	r *http.Request,
	action pro_interfaces.AuditAction,
	source pro_interfaces.AuditSource,
	create func(context.Context, pro_interfaces.CapabilitySnapshot, string) (pro_interfaces.CapabilityTestRecordDTO, error),
) {
	var body struct {
		Value string `json:"value"`
	}
	if !helpers.Bind(w, r, &body) {
		c.recordAudit(r, capabilityAuditEvent(r, action,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput))
		return
	}
	snapshot, ok := capabilitySnapshotFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}
	record, err := create(r.Context(), snapshot, body.Value)
	if err != nil {
		c.recordCapabilityError(r, action, err)
		writeCapabilityError(w, err)
		return
	}
	event := capabilityAuditEvent(r, action, pro_interfaces.AuditOutcomeAllowed,
		string(snapshot.Decision(pro_interfaces.CapabilityLifecycleTest).Reason()))
	event.Source = source
	c.recordAudit(r, event)
	helpers.WriteJSON(w, http.StatusCreated, record)
}

func (c *CapabilityController) recordCapabilityError(
	r *http.Request,
	action pro_interfaces.AuditAction,
	err error,
) {
	outcome := pro_interfaces.AuditOutcomeFailure
	reason := pro_interfaces.AuditReasonOperationError
	var denied pro_interfaces.CapabilityDeniedError
	var validationError *common_errors.ValidationError
	switch {
	case errors.As(err, &denied):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = string(denied.Decision.Reason())
	case errors.As(err, &validationError):
		reason = pro_interfaces.AuditReasonInvalidInput
	}
	c.recordAudit(r, capabilityAuditEvent(r, action, outcome, reason))
}

func (c *CapabilityController) recordAudit(r *http.Request, event pro_interfaces.AuditEvent) {
	if c.audit == nil {
		return
	}
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
	}
}

func auditActionForAccess(access pro_interfaces.CapabilityAccess) pro_interfaces.AuditAction {
	switch access {
	case pro_interfaces.CapabilityAccessRead:
		return pro_interfaces.AuditActionCapabilityRead
	case pro_interfaces.CapabilityAccessExecute:
		return pro_interfaces.AuditActionCapabilityExecute
	default:
		return pro_interfaces.AuditActionCapabilityWrite
	}
}

func capabilityRequestFromHTTP(r *http.Request) (pro_interfaces.CapabilityRequest, bool) {
	user, ok := helpers.GetOkFromContext(r, "user")
	if !ok {
		return pro_interfaces.CapabilityRequest{}, false
	}
	currentUser, ok := user.(*db.User)
	if !ok || currentUser == nil {
		return pro_interfaces.CapabilityRequest{}, false
	}
	return pro_interfaces.CapabilityRequest{
		UserID:  currentUser.ID,
		IsAdmin: currentUser.Admin,
		At:      tz.Now(),
	}, true
}

func capabilitySnapshotFromHTTP(r *http.Request) (pro_interfaces.CapabilitySnapshot, bool) {
	snapshot, ok := r.Context().Value(capabilitySnapshotContextKey{}).(pro_interfaces.CapabilitySnapshot)
	return snapshot, ok
}

func writeCapabilityError(w http.ResponseWriter, err error) {
	var denied pro_interfaces.CapabilityDeniedError
	if errors.As(err, &denied) {
		status := http.StatusForbidden
		if denied.Decision.State() == pro_interfaces.CapabilityStateUnavailable {
			status = http.StatusNotFound
		}
		helpers.WriteJSON(w, status, map[string]any{
			"error":           "CAPABILITY_DENIED",
			"capability":      denied.Decision.ID(),
			"state":           denied.Decision.State(),
			"reason":          denied.Decision.Reason(),
			"required_access": denied.Required,
		})
		return
	}
	var validationError *common_errors.ValidationError
	if errors.As(err, &validationError) {
		helpers.WriteErrorStatus(w, "CAPABILITY_INPUT_INVALID", http.StatusBadRequest)
		return
	}
	helpers.WriteErrorStatus(w, "CAPABILITY_OPERATION_ERROR", http.StatusInternalServerError)
}
