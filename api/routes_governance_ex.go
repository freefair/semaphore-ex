package api

import (
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
)

// registerEnhancedGovernanceRoutes preserves the authenticated route and middleware order.
func registerEnhancedGovernanceRoutes(
	authenticatedAPI *mux.Router,
	capabilityController *CapabilityController,
	auditFacade pro_interfaces.AuditServiceFacade,
	notificationGovernanceController *NotificationGovernanceController,
	globalCredentialController *GlobalCredentialController,
	workflowArtifactRetentionController pro_interfaces.WorkflowArtifactRetentionController,
	deploymentWindowController pro_interfaces.DeploymentWindowController,
	policyGuardrailController pro_interfaces.PolicyGuardrailController,
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
	globalWorkflowArtifactRetentionManage := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalSystemPermission(handler),
		))
	}
	projectWorkflowArtifactRetentionManage := func(handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			projects.GetMustHaveBaseProjectPermissionMiddleware(db.CanManageProjectResources)(handler),
		))
	}
	projectDeploymentWindowManage := func(access pro_interfaces.CapabilityAccess, handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			capabilityController.SnapshotMiddleware(capabilityController.RequireCapability(
				pro_interfaces.CapabilityDeploymentWindows, access,
			)(projects.GetMustHaveBaseProjectPermissionMiddleware(db.CanManageProjectResources)(handler))),
		))
	}
	projectDeploymentWindowStatus := func(handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(capabilityController.SnapshotMiddleware(
			capabilityController.RequireCapability(pro_interfaces.CapabilityDeploymentWindows, pro_interfaces.CapabilityAccessExecute)(handler),
		))
	}
	projectPolicyGuardrail := func(access pro_interfaces.CapabilityAccess, permission db.ProjectUserPermission, handler http.Handler) http.Handler {
		return projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			capabilityController.SnapshotMiddleware(capabilityController.RequireCapability(
				pro_interfaces.CapabilityPolicyGuardrails, access,
			)(projects.GetMustHaveBaseProjectPermissionMiddleware(permission)(handler))),
		))
	}
	globalPolicyGuardrail := func(access pro_interfaces.CapabilityAccess, permission db.GlobalPermission, handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			capabilityController.SnapshotMiddleware(capabilityController.RequireCapability(
				pro_interfaces.CapabilityPolicyGuardrails, access,
			)(globalPermissionMiddleware(permission)(handler))),
		))
	}
	globalCredentialAnyRead := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.UserFromContext(r)
			for _, permission := range []db.GlobalPermission{db.CanReadGlobalAudit, db.CanManageGlobalCredentialsMetadata, db.CanManageGlobalCredentialsRotate, db.CanManageGlobalCredentialsGrant} {
				allowed, err := hasGlobalPermission(r, user, permission)
				if err != nil {
					helpers.WriteError(w, err)
					return
				}
				if allowed {
					handler.ServeHTTP(w, r)
					return
				}
			}
			w.WriteHeader(http.StatusForbidden)
		})))
	}
	globalCredentialAudited := func(permission db.GlobalPermission, handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(permission)(handler),
		))
	}
	globalCredentialCreate := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanManageGlobalCredentialsMetadata)(
				globalPermissionMiddleware(db.CanManageGlobalCredentialsRotate)(handler),
			),
		))
	}
	globalCredentialMetadata := func(handler http.Handler) http.Handler {
		return globalCredentialAudited(db.CanManageGlobalCredentialsMetadata, handler)
	}
	globalCredentialRotate := func(handler http.Handler) http.Handler {
		return globalCredentialAudited(db.CanManageGlobalCredentialsRotate, handler)
	}
	globalCredentialGrant := func(handler http.Handler) http.Handler {
		return globalCredentialAudited(db.CanManageGlobalCredentialsGrant, handler)
	}
	globalCredentialUsage := func(handler http.Handler) http.Handler {
		return globalCredentialAudited(db.CanReadGlobalAudit, handler)
	}
	globalCredentialEnabled := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalCredentialEnabledAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanManageGlobalCredentialsMetadata)(handler),
		))
	}
	globalCredentialDelete := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalCredentialDeleteAuditMiddleware(auditFacade)(
			globalPermissionMiddleware(db.CanManageGlobalCredentialsMetadata)(handler),
		))
	}
	projectGrantedCredentialList := func(handler http.Handler) http.Handler {
		return delegatedProjectRolesSnapshot(projects.ProjectMiddleware(EnhancedProjectPermissionAuditMiddleware(auditFacade)(
			projects.GetMustHavePermissionMiddleware(db.CanListGrantedCredentials)(handler),
		)))
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
	authenticatedAPI.Path("/global-credentials").Handler(globalCredentialAnyRead(http.HandlerFunc(globalCredentialController.List))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials/grant-projects").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.ListGrantProjects))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials").Handler(globalCredentialCreate(http.HandlerFunc(globalCredentialController.Create))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}").Handler(globalCredentialMetadata(http.HandlerFunc(globalCredentialController.Get))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials/{credential_id}").Handler(globalCredentialMetadata(http.HandlerFunc(globalCredentialController.Update))).Methods("PUT")
	authenticatedAPI.Path("/global-credentials/{credential_id}/enabled").Handler(globalCredentialEnabled(http.HandlerFunc(globalCredentialController.SetEnabled))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}/rotate").Handler(globalCredentialRotate(http.HandlerFunc(globalCredentialController.Rotate))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}/usage").Handler(globalCredentialUsage(http.HandlerFunc(globalCredentialController.ListUsage))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials/{credential_id}/impact").Handler(globalCredentialAnyRead(http.HandlerFunc(globalCredentialController.GetImpact))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials/{credential_id}").Handler(globalCredentialDelete(http.HandlerFunc(globalCredentialController.Delete))).Methods("DELETE")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.ListGrants))).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.CreateGrant))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants/{grant_id}").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.UpdateGrant))).Methods("PUT")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants/{grant_id}/revoke").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.RevokeGrant))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants/{grant_id}/restore").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.RestoreGrant))).Methods("POST")
	authenticatedAPI.Path("/global-credentials/{credential_id}/grants/{grant_id}").Handler(globalCredentialGrant(http.HandlerFunc(globalCredentialController.DeleteGrant))).Methods("DELETE")
	authenticatedAPI.Path("/project/{project_id}/granted-credentials").Handler(projectGrantedCredentialList(http.HandlerFunc(globalCredentialController.ListGranted))).Methods("GET", "HEAD")

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

	authenticatedAPI.Path("/workflow-artifact-retention").Handler(
		globalWorkflowArtifactRetentionManage(http.HandlerFunc(workflowArtifactRetentionController.GetGlobalWorkflowArtifactRetention)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/workflow-artifact-retention").Handler(
		globalWorkflowArtifactRetentionManage(http.HandlerFunc(workflowArtifactRetentionController.PublishGlobalWorkflowArtifactRetention)),
	).Methods("PUT")
	authenticatedAPI.Path("/project/{project_id}/workflow-artifact-retention").Handler(
		projectWorkflowArtifactRetentionManage(http.HandlerFunc(workflowArtifactRetentionController.GetProjectWorkflowArtifactRetention)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/workflow-artifact-retention").Handler(
		projectWorkflowArtifactRetentionManage(http.HandlerFunc(workflowArtifactRetentionController.PublishProjectWorkflowArtifactRetention)),
	).Methods("PUT")

	authenticatedAPI.Path("/project/{project_id}/deployment-windows").Handler(
		projectDeploymentWindowManage(pro_interfaces.CapabilityAccessRead, http.HandlerFunc(deploymentWindowController.GetPolicy)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/deployment-windows").Handler(
		projectDeploymentWindowManage(pro_interfaces.CapabilityAccessWrite, http.HandlerFunc(deploymentWindowController.SavePolicy)),
	).Methods("PUT")
	authenticatedAPI.Path("/project/{project_id}/deployment-windows").Handler(
		projectDeploymentWindowManage(pro_interfaces.CapabilityAccessWrite, http.HandlerFunc(deploymentWindowController.ResetPolicy)),
	).Methods("DELETE")
	authenticatedAPI.Path("/project/{project_id}/deployment-windows/preview").Handler(
		projectDeploymentWindowManage(pro_interfaces.CapabilityAccessExecute, http.HandlerFunc(deploymentWindowController.Preview)),
	).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/deployment-windows/status").Handler(
		projectDeploymentWindowStatus(http.HandlerFunc(deploymentWindowController.CurrentStatus)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/deployment-windows/history").Handler(
		projectDeploymentWindowManage(pro_interfaces.CapabilityAccessRead, http.HandlerFunc(deploymentWindowController.DecisionHistory)),
	).Methods("GET", "HEAD")

	authenticatedAPI.Path("/project/{project_id}/policy-guardrails").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Get)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/draft").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.SaveDraft)),
	).Methods("PUT")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/validate").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Validate)),
	).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/test").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessExecute, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.TestFixture)),
	).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/diff").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Diff)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/publish").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Publish)),
	).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/impact").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessExecute, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Impact)),
	).Methods("POST")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/revisions").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Revisions)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/evaluations").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManagePolicyGuardrails, http.HandlerFunc(policyGuardrailController.Evaluations)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/project/{project_id}/policy-guardrails/rollback").Handler(
		projectPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanRollbackPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Rollback)),
	).Methods("POST")

	authenticatedAPI.Path("/policy-guardrails").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Get)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/policy-guardrails/draft").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.SaveDraft)),
	).Methods("PUT")
	authenticatedAPI.Path("/policy-guardrails/validate").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Validate)),
	).Methods("POST")
	authenticatedAPI.Path("/policy-guardrails/test").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessExecute, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.TestFixture)),
	).Methods("POST")
	authenticatedAPI.Path("/policy-guardrails/diff").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Diff)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/policy-guardrails/publish").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Publish)),
	).Methods("POST")
	authenticatedAPI.Path("/policy-guardrails/impact").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessExecute, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Impact)),
	).Methods("POST")
	authenticatedAPI.Path("/policy-guardrails/revisions").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Revisions)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/policy-guardrails/evaluations").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessRead, db.CanManageGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Evaluations)),
	).Methods("GET", "HEAD")
	authenticatedAPI.Path("/policy-guardrails/rollback").Handler(
		globalPolicyGuardrail(pro_interfaces.CapabilityAccessWrite, db.CanRollbackGlobalPolicyGuardrails, http.HandlerFunc(policyGuardrailController.Rollback)),
	).Methods("POST")

}
