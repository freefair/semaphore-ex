package api

import (
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func EnhancedAnonymousAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action, enhanced := enhancedActionForRoute(r.Method, r.URL.Path)
			if !enhanced {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			if audit == nil {
				return
			}
			reason := ""
			switch captured.status {
			case http.StatusUnauthorized:
				reason = pro_interfaces.AuditReasonUnauthenticated
			case http.StatusForbidden:
				if origin, ok := requestOriginHost(r); ok && !isSameOriginHost(origin, r) {
					reason = pro_interfaces.AuditReasonCrossOrigin
				}
			}
			if reason == "" {
				return
			}
			event := capabilityAuditEvent(
				r,
				action,
				pro_interfaces.AuditOutcomeDenied,
				reason,
			)
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

func EnhancedAdminAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action, enhanced := enhancedActionForRoute(r.Method, r.URL.Path)
			if !enhanced || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			value, ok := helpers.GetOkFromContext(r, "user")
			user, valid := value.(*db.User)
			if !ok || !valid || user == nil || user.Admin {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			if captured.status != http.StatusForbidden {
				return
			}
			event := capabilityAuditEvent(r, action, pro_interfaces.AuditOutcomeDenied,
				string(pro_interfaces.CapabilityReasonInsufficientPermission))
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

func enhancedActionForRoute(method, path string) (pro_interfaces.AuditAction, bool) {
	switch {
	case strings.HasSuffix(path, "/capabilities/lifecycle-test") && method == http.MethodPut:
		return pro_interfaces.AuditActionCapabilityConfigure, true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/records") && method == http.MethodGet:
		return pro_interfaces.AuditActionCapabilityRead, true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/records") && method == http.MethodPost:
		return pro_interfaces.AuditActionCapabilityWrite, true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/background-actions") && method == http.MethodPost:
		return pro_interfaces.AuditActionCapabilityExecute, true
	default:
		return "", false
	}
}

func capabilityAuditEvent(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) pro_interfaces.AuditEvent {
	var actorID *int
	if value, ok := helpers.GetOkFromContext(r, "user"); ok {
		if user, valid := value.(*db.User); valid && user != nil {
			id := user.ID
			actorID = &id
		}
	}
	correlationID := helpers.CorrelationID(r.Context())
	if correlationID == "" {
		correlationID = "internal"
	}
	return pro_interfaces.AuditEvent{
		CorrelationID: correlationID,
		ActorID:       actorID,
		Action:        action,
		TargetType:    pro_interfaces.AuditTargetCapability,
		TargetID:      string(pro_interfaces.CapabilityLifecycleTest),
		Outcome:       outcome,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        reason,
	}
}
