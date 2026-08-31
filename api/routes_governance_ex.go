package api

import (
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
)

// registerEnhancedGovernanceRoutes preserves the authenticated route and middleware order.
func registerEnhancedGovernanceRoutes(
	authenticatedAPI *mux.Router,
	auditFacade pro_interfaces.AuditServiceFacade,
	notificationGovernanceController *NotificationGovernanceController,
	delegatedProjectRolesSnapshot func(http.Handler) http.Handler,
	globalSystemPermission func(http.Handler) http.Handler,
) {
	globalNotificationManage := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanManageGlobalSystem)(handler),
		))
	}
	globalNotificationRead := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanReadGlobalAudit)(handler),
		))
	}
	projectNotificationManage := func(handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			projects.GetMustHavePermissionMiddleware(db.CanManageProjectResources)(handler),
		))
	}
	projectNotificationRead := func(handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			projects.GetMustHavePermissionMiddleware(db.CanViewProjectResources)(handler),
		))
	}
	authenticatedAPI.Path("/subscription").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalSystemPermission(http.HandlerFunc(subscriptionController.Activate))))).Methods("POST")
	authenticatedAPI.Path("/subscription/refresh").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalSystemPermission(http.HandlerFunc(subscriptionController.Refresh))))).Methods("POST")
	authenticatedAPI.Path("/subscription").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalSystemPermission(http.HandlerFunc(subscriptionController.GetSubscription))))).Methods("GET")
	authenticatedAPI.Path("/subscription").Handler(
		delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalSystemPermission(http.HandlerFunc(subscriptionController.Delete))))).Methods("DELETE")

	// Notification governance is deliberately independent from the legacy
	// project alert endpoint. Global and project scopes have separate route
	// middleware, so numeric IDs cannot cross a tenant boundary.
	authenticatedAPI.Path("/notification-governance/destinations").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.ListGlobalDestinations))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/notification-governance/destinations").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.CreateGlobalDestination))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.GetGlobalDestination))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.UpdateGlobalDestination))).Methods("PUT")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.DeleteGlobalDestination))).Methods("DELETE")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}/pause").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.PauseGlobalDestination))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}/resume").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.ResumeGlobalDestination))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/destinations/{destination_id}/test").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.TestGlobalDestination))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/rules").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.ListGlobalRules))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/notification-governance/rules").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.CreateGlobalRule))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/rules/{rule_id}").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.UpdateGlobalRule))).Methods("PUT")
	authenticatedAPI.Path("/notification-governance/rules/{rule_id}").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.DeleteGlobalRule))).Methods("DELETE")
	authenticatedAPI.Path("/notification-governance/routing/preview").Handler(globalNotificationRead(http.HandlerFunc(notificationGovernanceController.PreviewGlobalRouting))).Methods("POST")
	authenticatedAPI.Path("/notification-governance/deliveries").Handler(globalNotificationRead(http.HandlerFunc(notificationGovernanceController.GlobalHistory))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/notification-governance/events").Handler(globalNotificationRead(http.HandlerFunc(notificationGovernanceController.GlobalEventHistory))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/notification-governance/deliveries/{delivery_id}/retry").Handler(globalNotificationManage(http.HandlerFunc(notificationGovernanceController.RetryGlobalDelivery))).Methods("POST")

	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.ListProjectDestinations))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.CreateProjectDestination))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.GetProjectDestination))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.UpdateProjectDestination))).Methods("PUT")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.DeleteProjectDestination))).Methods("DELETE")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}/pause").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.PauseProjectDestination))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}/resume").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.ResumeProjectDestination))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/destinations/{destination_id}/test").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.TestProjectDestination))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/rules").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.ListProjectRules))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/rules").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.CreateProjectRule))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/rules/{rule_id}").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.UpdateProjectRule))).Methods("PUT")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/rules/{rule_id}").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.DeleteProjectRule))).Methods("DELETE")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/routing/preview").Handler(projectNotificationRead(http.HandlerFunc(notificationGovernanceController.PreviewProjectRouting))).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/deliveries").Handler(projectNotificationRead(http.HandlerFunc(notificationGovernanceController.ProjectHistory))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/events").Handler(projectNotificationRead(http.HandlerFunc(notificationGovernanceController.ProjectEventHistory))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/notification-governance/deliveries/{delivery_id}/retry").Handler(projectNotificationManage(http.HandlerFunc(notificationGovernanceController.RetryProjectDelivery))).Methods("POST")

}
