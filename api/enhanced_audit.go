package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// EnhancedGlobalPermissionAuditMiddleware records bounded delegated global
// operations after explicit authorization has decided the request.
func EnhancedGlobalPermissionAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
			if !enhanced || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			status := captured.status
			if status == 0 {
				status = http.StatusOK
			}
			outcome, reason := enhancedAuditOutcome(status)
			event := routeAuditEvent(r, descriptor, outcome, reason)
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

// EnhancedGlobalCredentialEnabledAuditMiddleware classifies the explicit
// enable/disable transition without ever retaining or recording the body.
// The controller independently performs strict decoding and validation.
func EnhancedGlobalCredentialEnabledAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
			if !enhanced || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			action := descriptor.Action
			if enabled, ok := globalCredentialEnabledRequest(r); ok {
				if enabled {
					action = pro_interfaces.AuditActionGlobalCredentialEnable
				} else {
					action = pro_interfaces.AuditActionGlobalCredentialDisable
				}
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			status := captured.status
			if status == 0 {
				status = http.StatusOK
			}
			outcome, reason := enhancedAuditOutcome(status)
			descriptor.Action = action
			if err := audit.Record(r.Context(), routeAuditEvent(r, descriptor, outcome, reason)); err != nil {
				log.WithField("action", action).Error("Failed to store global credential audit event")
			}
		})
	}
}

// EnhancedGlobalCredentialDeleteAuditMiddleware records every deletion attempt
// with its outcome. Its descriptor contains only the stable credential ID.
func EnhancedGlobalCredentialDeleteAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
			if !enhanced || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			status := captured.status
			if status == 0 {
				status = http.StatusOK
			}
			outcome, reason := enhancedAuditOutcome(status)
			if err := audit.Record(r.Context(), routeAuditEvent(r, descriptor, outcome, reason)); err != nil {
				log.WithField("action", descriptor.Action).Error("Failed to store global credential deletion attempt")
			}
		})
	}
}

func globalCredentialEnabledRequest(r *http.Request) (bool, bool) {
	if r.Body == nil {
		return false, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, globalCredentialBodyLimit+1))
	if err != nil || len(body) > globalCredentialBodyLimit {
		return false, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Enabled == nil {
		return false, false
	}
	return *request.Enabled, true
}

func enhancedAuditOutcome(status int) (pro_interfaces.AuditOutcome, string) {
	switch {
	case status >= 200 && status < 300:
		return pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive)
	case status == http.StatusForbidden:
		return pro_interfaces.AuditOutcomeDenied, string(pro_interfaces.CapabilityReasonInsufficientPermission)
	default:
		return pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError
	}
}

func EnhancedProjectPermissionAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, enhanced := enhancedAuditForRoute(r)
			if !enhanced || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			if descriptor.TargetType == pro_interfaces.AuditTargetProjectRunner && !projectPermissionDenied(r) {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			if descriptor.TargetType == pro_interfaces.AuditTargetProjectRunner && captured.status != http.StatusForbidden {
				return
			}
			status := captured.status
			if status == 0 {
				status = http.StatusOK
			}
			outcome := pro_interfaces.AuditOutcomeFailure
			reason := pro_interfaces.AuditReasonOperationError
			switch {
			case status >= 200 && status < 300:
				outcome = pro_interfaces.AuditOutcomeAllowed
				reason = string(pro_interfaces.CapabilityReasonActive)
			case status == http.StatusForbidden:
				outcome = pro_interfaces.AuditOutcomeDenied
				reason = string(pro_interfaces.CapabilityReasonInsufficientPermission)
			}
			event := routeAuditEvent(r, descriptor, outcome, reason)
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store enhanced audit event")
			}
		})
	}
}

// EnhancedWorkflowDeniedAuditMiddleware records only denied workflow actions.
// It runs after entity middleware so the audit event can carry the live or
// immutable snapshot policy revision without turning hidden 404s into leaks.
func EnhancedWorkflowDeniedAuditMiddleware(audit pro_interfaces.AuditServiceFacade) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			descriptor, ok := workflowAuditDescriptor(r)
			if !ok || audit == nil {
				next.ServeHTTP(w, r)
				return
			}
			captured := &statusCapturingWriter{ResponseWriter: w}
			next.ServeHTTP(captured, r)
			if captured.status != http.StatusForbidden && captured.status != http.StatusNotFound {
				return
			}
			event := routeAuditEvent(r, descriptor, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonWorkflowPolicyDenied)
			event.WorkflowPolicyRevision = workflowAuditPolicyRevision(r)
			if err := audit.Record(r.Context(), event); err != nil {
				log.WithFields(event.SafeFields()).Error("Failed to store workflow audit event")
			}
		})
	}
}

func workflowAuditPolicyRevision(r *http.Request) int {
	if run, ok := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun); ok && run.DefinitionSnapshot.AccessPolicyRevision > 0 {
		return run.DefinitionSnapshot.AccessPolicyRevision
	}
	if workflow, ok := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate); ok && workflow.AccessPolicyRevision > 0 {
		return workflow.AccessPolicyRevision
	}
	return 1
}

func workflowAuditDescriptor(r *http.Request) (enhancedAuditDescriptor, bool) {
	projectID, ok := positiveMuxID(r, "project_id")
	if !ok {
		return enhancedAuditDescriptor{}, false
	}
	path, method := r.URL.Path, r.Method
	if strings.HasSuffix(path, "/workflow-approvals") && (method == http.MethodGet || method == http.MethodHead) {
		return enhancedAuditDescriptor{Action: pro_interfaces.AuditActionWorkflowApprovalInbox, TargetType: pro_interfaces.AuditTargetWorkflowApprovalInbox, TargetID: fmt.Sprintf("project:%d", projectID), ProjectID: &projectID}, true
	}
	if strings.Contains(path, "/tasks/") {
		if task, found := helpers.GetFromContext(r, "task").(db.Task); found && task.WorkflowRunID != nil {
			return enhancedAuditDescriptor{Action: pro_interfaces.AuditActionWorkflowRunLogsRead, TargetType: pro_interfaces.AuditTargetWorkflowRun, TargetID: fmt.Sprintf("run:%d", *task.WorkflowRunID), ProjectID: &projectID}, true
		}
	}
	if !strings.Contains(path, "/workflows") {
		return enhancedAuditDescriptor{}, false
	}
	if workflowID, has := positiveMuxID(r, "workflow_id"); has {
		target := fmt.Sprintf("workflow:%d", workflowID)
		if runID, runHas := positiveMuxID(r, "run_id"); runHas {
			target = fmt.Sprintf("run:%d", runID)
			action := pro_interfaces.AuditActionWorkflowRunRead
			if strings.HasSuffix(path, "/artifacts") {
				action = pro_interfaces.AuditActionWorkflowRunLogsRead
			}
			if strings.HasSuffix(path, "/stop") {
				action = pro_interfaces.AuditActionWorkflowStop
			}
			return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetWorkflowRun, TargetID: target, ProjectID: &projectID}, true
		}
		action := pro_interfaces.AuditActionWorkflowRead
		if strings.HasSuffix(path, "/run") {
			action = pro_interfaces.AuditActionWorkflowStart
		} else if method == http.MethodPut {
			action = pro_interfaces.AuditActionWorkflowUpdate
		} else if method == http.MethodDelete {
			action = pro_interfaces.AuditActionWorkflowDelete
		}
		return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetWorkflow, TargetID: target, ProjectID: &projectID}, true
	}
	action := pro_interfaces.AuditActionWorkflowList
	if method == http.MethodPost {
		action = pro_interfaces.AuditActionWorkflowCreate
	}
	return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetWorkflow, TargetID: fmt.Sprintf("project:%d", projectID), ProjectID: &projectID}, true
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
	ProjectID  *int
}

func enhancedAuditForRoute(r *http.Request) (enhancedAuditDescriptor, bool) {
	method := r.Method
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/global-credentials"):
		return globalCredentialAuditDescriptor(r)
	case strings.HasSuffix(path, "/granted-credentials") && (method == http.MethodGet || method == http.MethodHead):
		if projectID, ok := positiveMuxID(r, "project_id"); ok {
			return enhancedAuditDescriptor{
				Action: pro_interfaces.AuditActionGrantedCredentialRead, TargetType: pro_interfaces.AuditTargetGlobalCredentialGrant,
				TargetID: fmt.Sprintf("project:%d", projectID), ProjectID: &projectID,
			}, true
		}
		return enhancedAuditDescriptor{}, false
	case strings.Contains(path, "/notification-governance"):
		return notificationGovernanceAuditDescriptor(r), true
	case strings.HasSuffix(path, "/audit-webhook/deliveries") && (method == http.MethodGet || method == http.MethodHead):
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookRead), true
	case strings.HasSuffix(path, "/audit-webhook/test") && method == http.MethodPost:
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookTest), true
	case strings.HasSuffix(path, "/audit-webhook/pause") && method == http.MethodPost:
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookPause), true
	case strings.HasSuffix(path, "/audit-webhook/resume") && method == http.MethodPost:
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookResume), true
	case strings.HasSuffix(path, "/audit-webhook") && (method == http.MethodGet || method == http.MethodHead):
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookRead), true
	case strings.HasSuffix(path, "/audit-webhook") && method == http.MethodPut:
		return webhookAuditDescriptor(pro_interfaces.AuditActionWebhookConfigure), true
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
		return globalRoleAuditForRoute(r)
	}
	projectTarget := fmt.Sprintf("project:%d", projectID)
	if templateID, ok := positiveMuxID(r, "template_id"); ok && strings.Contains(path, "/perms") {
		if permID, hasPerm := positiveMuxID(r, "perm_id"); hasPerm {
			targetID := fmt.Sprintf("template-role:%d", permID)
			switch method {
			case http.MethodPut:
				return templateRoleAuditDescriptor(
					pro_interfaces.AuditActionTemplateRoleUpdate, targetID, projectID,
				), true
			case http.MethodDelete:
				return templateRoleAuditDescriptor(
					pro_interfaces.AuditActionTemplateRoleDelete, targetID, projectID,
				), true
			}
		}
		if method == http.MethodPost {
			return templateRoleAuditDescriptor(
				pro_interfaces.AuditActionTemplateRoleCreate,
				fmt.Sprintf("template:%d", templateID), projectID,
			), true
		}
	}
	if strings.HasSuffix(path, "/roles") && method == http.MethodPost {
		return projectRoleAuditDescriptor(
			pro_interfaces.AuditActionProjectRoleCreate, projectTarget, projectID), true
	}
	if roleID := strings.TrimSpace(mux.Vars(r)["role_id"]); roleID != "" {
		roleTarget := "role:" + roleID
		switch method {
		case http.MethodPut, http.MethodPost:
			return projectRoleAuditDescriptor(
				pro_interfaces.AuditActionProjectRoleUpdate, roleTarget, projectID), true
		case http.MethodDelete:
			return projectRoleAuditDescriptor(
				pro_interfaces.AuditActionProjectRoleDelete, roleTarget, projectID), true
		}
	}
	if strings.HasSuffix(path, "/users") && method == http.MethodPost {
		return projectMembershipAuditDescriptor(
			pro_interfaces.AuditActionProjectMemberAdd, projectTarget, projectID), true
	}
	if userID, ok := positiveMuxID(r, "user_id"); ok {
		memberTarget := fmt.Sprintf("member:%d", userID)
		switch method {
		case http.MethodPut:
			return projectMembershipAuditDescriptor(
				pro_interfaces.AuditActionProjectRoleAssign, memberTarget, projectID), true
		case http.MethodDelete:
			return projectMembershipAuditDescriptor(
				pro_interfaces.AuditActionProjectMemberRemove, memberTarget, projectID), true
		}
	}
	if strings.HasSuffix(path, "/runners") {
		switch method {
		case http.MethodGet, http.MethodHead:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerList, projectTarget, projectID), true
		case http.MethodPost:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerCreate, projectTarget, projectID), true
		}
	}
	runnerID, runnerOK := positiveMuxID(r, "runner_id")
	if !runnerOK {
		return enhancedAuditDescriptor{}, false
	}
	runnerTarget := fmt.Sprintf("runner:%d", runnerID)
	if strings.HasSuffix(path, "/registration-token") && method == http.MethodPost {
		return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerIssue, runnerTarget, projectID), true
	}
	if strings.HasSuffix(path, "/active") && method == http.MethodPost {
		return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerActive, runnerTarget, projectID), true
	}
	if strings.HasSuffix(path, "/cache") && method == http.MethodDelete {
		return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerCache, runnerTarget, projectID), true
	}
	if strings.HasSuffix(path, fmt.Sprintf("/runners/%d", runnerID)) {
		switch method {
		case http.MethodGet, http.MethodHead:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerRead, runnerTarget, projectID), true
		case http.MethodPut, http.MethodPost:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerUpdate, runnerTarget, projectID), true
		case http.MethodDelete:
			return projectRunnerAuditDescriptor(pro_interfaces.AuditActionProjectRunnerDelete, runnerTarget, projectID), true
		}
	}
	return enhancedAuditDescriptor{}, false
}

func globalCredentialAuditDescriptor(r *http.Request) (enhancedAuditDescriptor, bool) {
	credentialID, hasCredentialID := positiveMuxID(r, "credential_id")
	if !hasCredentialID {
		if strings.HasSuffix(r.URL.Path, "/global-credentials") {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				return globalAuditDescriptor(
					pro_interfaces.AuditActionGlobalCredentialRead,
					pro_interfaces.AuditTargetGlobalCredential,
					"credentials",
				), true
			}
			if r.Method == http.MethodPost {
				return globalAuditDescriptor(
					pro_interfaces.AuditActionGlobalCredentialCreate,
					pro_interfaces.AuditTargetGlobalCredential,
					"credentials",
				), true
			}
		}
		return enhancedAuditDescriptor{}, false
	}
	credentialTarget := fmt.Sprintf("credential:%d", credentialID)
	if grantID, hasGrantID := positiveMuxID(r, "grant_id"); hasGrantID {
		grantTarget := fmt.Sprintf("credential-grant:%d", grantID)
		switch {
		case strings.HasSuffix(r.URL.Path, "/revoke") && r.Method == http.MethodPost:
			return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialGrantRevoke, pro_interfaces.AuditTargetGlobalCredentialGrant, grantTarget), true
		case strings.HasSuffix(r.URL.Path, "/restore") && r.Method == http.MethodPost:
			return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialGrantRestore, pro_interfaces.AuditTargetGlobalCredentialGrant, grantTarget), true
		case r.Method == http.MethodPut:
			return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialGrant, pro_interfaces.AuditTargetGlobalCredentialGrant, grantTarget), true
		case r.Method == http.MethodDelete:
			return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialGrantDelete, pro_interfaces.AuditTargetGlobalCredentialGrant, grantTarget), true
		}
		return enhancedAuditDescriptor{}, false
	}
	if strings.HasSuffix(r.URL.Path, "/grants") && r.Method == http.MethodPost {
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialGrant, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	}
	switch {
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialRead, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	case strings.HasSuffix(r.URL.Path, "/enabled") && r.Method == http.MethodPost:
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialUpdate, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	case strings.HasSuffix(r.URL.Path, "/rotate") && r.Method == http.MethodPost:
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialRotate, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	case r.Method == http.MethodPut:
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialUpdate, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	case r.Method == http.MethodDelete:
		return globalAuditDescriptor(pro_interfaces.AuditActionGlobalCredentialDeleteAttempt, pro_interfaces.AuditTargetGlobalCredential, credentialTarget), true
	}
	return enhancedAuditDescriptor{}, false
}

func notificationGovernanceAuditDescriptor(r *http.Request) enhancedAuditDescriptor {
	action := pro_interfaces.AuditActionCapabilityConfigure
	switch {
	case strings.HasSuffix(r.URL.Path, "/routing/preview"), strings.HasSuffix(r.URL.Path, "/deliveries"), strings.HasSuffix(r.URL.Path, "/events"):
		action = pro_interfaces.AuditActionCapabilityRead
	case strings.HasSuffix(r.URL.Path, "/test"), strings.HasSuffix(r.URL.Path, "/retry"):
		action = pro_interfaces.AuditActionCapabilityExecute
	}
	projectID, scoped := positiveMuxID(r, "project_id")
	if !scoped {
		return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetNotification, TargetID: "global"}
	}
	return enhancedAuditDescriptor{Action: action, TargetType: pro_interfaces.AuditTargetNotification, TargetID: fmt.Sprintf("project:%d", projectID), ProjectID: &projectID}
}

func globalRoleAuditForRoute(r *http.Request) (enhancedAuditDescriptor, bool) {
	method := r.Method
	path := r.URL.Path
	if strings.HasSuffix(path, "/audit/events") && (method == http.MethodGet || method == http.MethodHead) {
		return globalAuditDescriptor(
			pro_interfaces.AuditActionGlobalAuditRead,
			pro_interfaces.AuditTargetGlobalAudit,
			"events",
		), true
	}
	if strings.HasSuffix(path, "/subscription") {
		switch method {
		case http.MethodGet, http.MethodHead:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalSystemRead,
				pro_interfaces.AuditTargetGlobalSystem,
				"subscription",
			), true
		case http.MethodPost, http.MethodDelete:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalSystemWrite,
				pro_interfaces.AuditTargetGlobalSystem,
				"subscription",
			), true
		}
	}
	if strings.HasSuffix(path, "/subscription/refresh") && method == http.MethodPost {
		return globalAuditDescriptor(
			pro_interfaces.AuditActionGlobalSystemWrite,
			pro_interfaces.AuditTargetGlobalSystem,
			"subscription",
		), true
	}
	if strings.HasSuffix(path, "/options") {
		switch method {
		case http.MethodGet, http.MethodHead:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalSystemRead,
				pro_interfaces.AuditTargetGlobalSystem,
				"options",
			), true
		case http.MethodPost:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalSystemWrite,
				pro_interfaces.AuditTargetGlobalSystem,
				"options",
			), true
		}
	}
	if strings.HasSuffix(path, "/cache") && method == http.MethodDelete {
		return globalAuditDescriptor(
			pro_interfaces.AuditActionGlobalSystemWrite,
			pro_interfaces.AuditTargetGlobalSystem,
			"cache",
		), true
	}
	if strings.HasSuffix(path, "/users") {
		switch method {
		case http.MethodGet, http.MethodHead:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserRead,
				pro_interfaces.AuditTargetGlobalUser,
				"users",
			), true
		case http.MethodPost:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserCreate,
				pro_interfaces.AuditTargetGlobalUser,
				"users",
			), true
		}
	}
	if userID, ok := positiveMuxID(r, "user_id"); ok && strings.Contains(path, "/global-permissions") &&
		(method == http.MethodGet || method == http.MethodHead) {
		return globalRoleAuditDescriptor(
			pro_interfaces.AuditActionGlobalRoleRead,
			pro_interfaces.AuditTargetGlobalRoleAssignment,
			fmt.Sprintf("user:%d", userID),
		), true
	}
	if userID, ok := positiveMuxID(r, "user_id"); ok && !strings.Contains(path, "/global-roles") &&
		!strings.Contains(path, "/global-permissions") {
		targetID := fmt.Sprintf("user:%d", userID)
		if strings.HasSuffix(path, "/password") && method == http.MethodPost {
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserPassword,
				pro_interfaces.AuditTargetGlobalUser,
				targetID,
			), true
		}
		switch method {
		case http.MethodGet, http.MethodHead:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserRead,
				pro_interfaces.AuditTargetGlobalUser,
				targetID,
			), true
		case http.MethodPut:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserUpdate,
				pro_interfaces.AuditTargetGlobalUser,
				targetID,
			), true
		case http.MethodDelete:
			return globalAuditDescriptor(
				pro_interfaces.AuditActionGlobalUserDelete,
				pro_interfaces.AuditTargetGlobalUser,
				targetID,
			), true
		}
	}
	if strings.HasSuffix(path, "/roles") && method == http.MethodPost {
		return globalRoleAuditDescriptor(
			pro_interfaces.AuditActionGlobalRoleCreate,
			pro_interfaces.AuditTargetGlobalRole,
			"roles",
		), true
	}
	if strings.HasSuffix(path, "/roles") || strings.HasSuffix(path, "/roles/permissions") {
		if method == http.MethodGet || method == http.MethodHead {
			return globalRoleAuditDescriptor(
				pro_interfaces.AuditActionGlobalRoleRead,
				pro_interfaces.AuditTargetGlobalRole,
				"roles",
			), true
		}
	}
	if roleID := strings.TrimSpace(mux.Vars(r)["role_id"]); roleID != "" {
		targetID := "role:" + roleID
		switch method {
		case http.MethodGet, http.MethodHead:
			return globalRoleAuditDescriptor(
				pro_interfaces.AuditActionGlobalRoleRead,
				pro_interfaces.AuditTargetGlobalRole,
				targetID,
			), true
		case http.MethodPut, http.MethodPost:
			return globalRoleAuditDescriptor(
				pro_interfaces.AuditActionGlobalRoleUpdate,
				pro_interfaces.AuditTargetGlobalRole,
				targetID,
			), true
		case http.MethodDelete:
			return globalRoleAuditDescriptor(
				pro_interfaces.AuditActionGlobalRoleDelete,
				pro_interfaces.AuditTargetGlobalRole,
				targetID,
			), true
		}
	}
	if !strings.Contains(path, "/global-roles") {
		return enhancedAuditDescriptor{}, false
	}
	if assignmentID, ok := positiveMuxID(r, "assignment_id"); ok && method == http.MethodDelete {
		return globalRoleAuditDescriptor(
			pro_interfaces.AuditActionGlobalRoleUnassign,
			pro_interfaces.AuditTargetGlobalRoleAssignment,
			fmt.Sprintf("assignment:%d", assignmentID),
		), true
	}
	if userID, ok := positiveMuxID(r, "user_id"); ok && (method == http.MethodGet || method == http.MethodHead) {
		return globalRoleAuditDescriptor(
			pro_interfaces.AuditActionGlobalRoleRead,
			pro_interfaces.AuditTargetGlobalRoleAssignment,
			fmt.Sprintf("user:%d", userID),
		), true
	}
	if userID, ok := positiveMuxID(r, "user_id"); ok && method == http.MethodPost {
		return globalRoleAuditDescriptor(
			pro_interfaces.AuditActionGlobalRoleAssign,
			pro_interfaces.AuditTargetGlobalRoleAssignment,
			fmt.Sprintf("user:%d", userID),
		), true
	}
	return enhancedAuditDescriptor{}, false
}

func globalAuditDescriptor(
	action pro_interfaces.AuditAction,
	targetType pro_interfaces.AuditTargetType,
	targetID string,
) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{Action: action, TargetType: targetType, TargetID: targetID}
}

func globalRoleAuditDescriptor(
	action pro_interfaces.AuditAction,
	targetType pro_interfaces.AuditTargetType,
	targetID string,
) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{Action: action, TargetType: targetType, TargetID: targetID}
}

func templateRoleAuditDescriptor(
	action pro_interfaces.AuditAction,
	targetID string,
	projectID int,
) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetTemplateRole,
		TargetID: targetID, ProjectID: &projectID,
	}
}

func webhookAuditDescriptor(action pro_interfaces.AuditAction) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetWebhook, TargetID: "audit_webhook",
	}
}

func capabilityAuditDescriptor(action pro_interfaces.AuditAction) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetCapability,
		TargetID: string(pro_interfaces.CapabilityLifecycleTest),
	}
}

func projectRunnerAuditDescriptor(action pro_interfaces.AuditAction, targetID string, projectID int) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetProjectRunner,
		TargetID: targetID, ProjectID: &projectID,
	}
}

func projectRoleAuditDescriptor(action pro_interfaces.AuditAction, targetID string, projectID int) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetProjectRole,
		TargetID: targetID, ProjectID: &projectID,
	}
}

func projectMembershipAuditDescriptor(action pro_interfaces.AuditAction, targetID string, projectID int) enhancedAuditDescriptor {
	return enhancedAuditDescriptor{
		Action: action, TargetType: pro_interfaces.AuditTargetProjectMembership,
		TargetID: targetID, ProjectID: &projectID,
	}
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
	event.ProjectID = verifiedAuditProjectID(r, descriptor.ProjectID)
	return event
}

func verifiedAuditProjectID(r *http.Request, requestedProjectID *int) *int {
	if requestedProjectID == nil {
		return nil
	}
	if project, ok := helpers.GetFromContext(r, "project").(db.Project); ok && project.ID == *requestedProjectID {
		projectID := project.ID
		return &projectID
	}
	value, ok := helpers.GetOkFromContext(r, "store")
	store, valid := value.(db.Store)
	if !ok || !valid {
		return nil
	}
	project, err := store.GetProject(*requestedProjectID)
	if err != nil || project.ID != *requestedProjectID {
		return nil
	}
	projectID := project.ID
	return &projectID
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
		SourceIP:      pro_interfaces.NormalizeAuditSourceIP(r.RemoteAddr),
		UserAgent:     pro_interfaces.SanitizeAuditUserAgent(r.UserAgent()),
		Reason:        reason,
	}
}
