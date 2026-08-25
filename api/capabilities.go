package api

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
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
}

// NewCapabilityController creates the capability API controller.
func NewCapabilityController(facade pro_interfaces.CapabilityServiceFacade) *CapabilityController {
	return &CapabilityController{facade: facade}
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
			log.WithFields(log.Fields{
				"context": "capability_resolution",
				"user_id": request.UserID,
			}).WithError(err).Error("Failed to resolve capability snapshot")
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
				writeCapabilityError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Configure updates the non-secret lifecycle-test configuration.
func (c *CapabilityController) Configure(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State     pro_interfaces.CapabilityState `json:"state"`
		ExpiresAt *time.Time                     `json:"expires_at"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	request, ok := capabilityRequestFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}
	snapshot, err := c.facade.Configure(r.Context(), request, pro_interfaces.CapabilityConfiguration{
		ID:        pro_interfaces.CapabilityLifecycleTest,
		State:     body.State,
		ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
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
		writeCapabilityError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, records)
}

// CreateRecord persists a lifecycle-test record through the request path.
func (c *CapabilityController) CreateRecord(w http.ResponseWriter, r *http.Request) {
	c.createRecord(w, r, c.facade.CreateRecord)
}

// RunBackgroundAction exercises the separately guarded background entry point.
func (c *CapabilityController) RunBackgroundAction(w http.ResponseWriter, r *http.Request) {
	c.createRecord(w, r, c.facade.RunBackgroundAction)
}

func (c *CapabilityController) createRecord(
	w http.ResponseWriter,
	r *http.Request,
	create func(context.Context, pro_interfaces.CapabilitySnapshot, string) (pro_interfaces.CapabilityTestRecordDTO, error),
) {
	var body struct {
		Value string `json:"value"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	snapshot, ok := capabilitySnapshotFromHTTP(r)
	if !ok {
		helpers.WriteErrorStatus(w, "CAPABILITY_CONTEXT_ERROR", http.StatusInternalServerError)
		return
	}
	record, err := create(r.Context(), snapshot, body.Value)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, record)
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
		helpers.WriteErrorStatus(w, validationError.Error(), http.StatusBadRequest)
		return
	}
	log.WithError(err).Error("Capability operation failed")
	debug.PrintStack()
	helpers.WriteErrorStatus(w, "CAPABILITY_OPERATION_ERROR", http.StatusInternalServerError)
}
