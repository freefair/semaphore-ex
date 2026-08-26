package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
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
			descriptor, enhanced := enhancedAuditForRoute(r)
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
			event := routeAuditEvent(r, descriptor, pro_interfaces.AuditOutcomeDenied, reason)
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

func EnhancedAdminAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
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
			event := routeAuditEvent(r, descriptor, pro_interfaces.AuditOutcomeDenied,
				string(pro_interfaces.CapabilityReasonInsufficientPermission))
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

func EnhancedProjectPermissionAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
			if !enhanced || descriptor.TargetType != pro_interfaces.AuditTargetProjectRunner || audit == nil ||
				!projectPermissionDenied(r) {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			if captured.status != http.StatusForbidden {
				return
			}
			event := routeAuditEvent(r, descriptor, pro_interfaces.AuditOutcomeDenied,
				string(pro_interfaces.CapabilityReasonInsufficientPermission))
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

func projectPermissionDenied(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	user, userOK := helpers.GetFromContext(r, "user").(*db.User)
	permissions, permissionsOK := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	return userOK && permissionsOK && !user.Admin &&
		permissions&db.CanManageProjectResources != db.CanManageProjectResources
}

type enhancedAuditDescriptor struct {
	Action     pro_interfaces.AuditAction
	TargetType pro_interfaces.AuditTargetType
	TargetID   string
}

func enhancedAuditForRoute(r *http.Request) (enhancedAuditDescriptor, bool) {
	method := r.Method
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/capabilities/lifecycle-test") && method == http.MethodPut:
		return capabilityAuditDescriptor(pro_interfaces.AuditActionCapabilityConfigure), true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/records") && method == http.MethodGet:
		return capabilityAuditDescriptor(pro_interfaces.AuditActionCapabilityRead), true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/records") && method == http.MethodPost:
		return capabilityAuditDescriptor(pro_interfaces.AuditActionCapabilityWrite), true
	case strings.HasSuffix(path, "/capabilities/lifecycle-test/background-actions") && method == http.MethodPost:
		return capabilityAuditDescriptor(pro_interfaces.AuditActionCapabilityExecute), true
	}
	projectID, projectOK := positiveMuxID(r, "project_id")
	if !projectOK {
		return enhancedAuditDescriptor{}, false
	}
	projectTarget := fmt.Sprintf("project:%d", projectID)
	if strings.HasSuffix(path, "/runners") {
		switch method {
		case http.MethodGet, http.MethodHead:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerList, projectTarget), true
		case http.MethodPost:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerCreate, projectTarget), true
		}
	}
	runnerID, runnerOK := positiveMuxID(r, "runner_id")
	if !runnerOK {
		return enhancedAuditDescriptor{}, false
	}
	runnerTarget := fmt.Sprintf("runner:%d", runnerID)
	if strings.HasSuffix(path, "/registration-token") && method == http.MethodPost {
		return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerIssue, runnerTarget), true
	}
	if strings.HasSuffix(path, fmt.Sprintf("/runners/%d", runnerID)) && (method == http.MethodGet || method == http.MethodHead) {
		return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerRead, runnerTarget), true
	}
	return enhancedAuditDescriptor{}, false
}

func capabilityAuditDescriptor(action pro_interfaces.AuditAction) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetCapability,
		TargetID: string(pro_interfaces.CapabilityLifecycleTest),
	}
}

func projectRunnerAuditDescriptor(action pro_interfaces.AuditAction, targetID string) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetProjectRunner, TargetID: targetID}
}

func positiveMuxID(r *http.Request, name string) (int, bool) {
	value, err := strconv.Atoi(mux.Vars(r)[name])
	return value, err == nil && value > 0
}

func routeAuditEvent(
	r *http.Request,
	descriptor enhancedAuditDescriptor,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) pro_interfaces.AuditEvent {
	event := capabilityAuditEvent(r, descriptor.Action, outcome, reason)
	event.TargetType = descriptor.TargetType
	event.TargetID = descriptor.TargetID
	return event
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
