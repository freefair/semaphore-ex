package api

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
	proApi "github.com/semaphoreui/semaphore/pro/api"
	proProjects "github.com/semaphoreui/semaphore/pro/api/projects"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	identityServices "github.com/semaphoreui/semaphore/services/identity"
	log "github.com/sirupsen/logrus"
)

type identityServiceBundle struct {
	capabilityProvider      pro_interfaces.CapabilityProvider
	totpService             pro_interfaces.TOTPService
	ldapService             pro_interfaces.LDAPService
	oidcGroupMappingService pro_interfaces.OIDCGroupMappingService
}

// newIdentityServiceBundle preserves the startup order for the capability,
// TOTP, LDAP, and OIDC identity services used by the route constructor.
func newIdentityServiceBundle(store db.Store) identityServiceBundle {
	capabilityProvider := proFeatures.NewCapabilityProvider(store)
	totpService := proFeatures.NewTOTPService(store, capabilityProvider)
	if err := totpService.Initialize(context.Background()); err != nil {
		log.WithError(err).Panic("failed to initialize TOTP lifecycle service")
	}
	ldapService := proFeatures.NewLDAPService(store, capabilityProvider, identityServices.NewLDAPClient())
	if err := ldapService.Initialize(context.Background()); err != nil {
		log.WithError(err).Panic("failed to initialize LDAP lifecycle service")
	}
	return identityServiceBundle{
		capabilityProvider: capabilityProvider, totpService: totpService, ldapService: ldapService,
		oidcGroupMappingService: proFeatures.NewOIDCGroupMappingService(store),
	}
}

type workflowFileArtifactBundle struct {
	service             pro_interfaces.WorkflowFileArtifactServiceFacade
	controller          pro_interfaces.WorkflowFileArtifactController
	retentionController pro_interfaces.WorkflowArtifactRetentionController
}

// newWorkflowFileArtifactBundle retains repository-to-service wiring before
// exposing the transport and retention controllers to the shared route host.
func newWorkflowFileArtifactBundle(store db.Store, workflowStore db.WorkflowManager) workflowFileArtifactBundle {
	identityStore, _ := store.(pro_interfaces.WorkflowFileArtifactIdentityStore)
	repository := proFactory.NewWorkflowFileArtifactStore(store)
	service := proServer.NewWorkflowFileArtifactService(repository, workflowStore, identityStore)
	controller := proProjects.NewWorkflowFileArtifactController(service)
	retentionService := proServer.NewWorkflowArtifactRetentionGovernanceService(repository)
	return workflowFileArtifactBundle{
		service:             service,
		controller:          controller,
		retentionController: proApi.NewWorkflowArtifactRetentionController(retentionService),
	}
}

func configureCrossProjectTemplateAudit(controller pro_interfaces.CrossProjectTemplateController, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := controller.(pro_interfaces.CrossProjectTemplateAuditConfigurer); ok {
		configurable.ConfigureCrossProjectTemplateAudit(audit)
	}
}

func configureExecutionPreflightAudit(controller any, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := controller.(pro_interfaces.ExecutionPreflightAuditConfigurer); ok {
		configurable.ConfigureExecutionPreflightAudit(audit)
	}
}

func configureWorkflowFileArtifactAudit(target any, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := target.(pro_interfaces.WorkflowFileArtifactAuditConfigurer); ok {
		configurable.ConfigureWorkflowFileArtifactAudit(audit)
	}
}

func configureWorkflowArtifactRetentionAudit(target any, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := target.(pro_interfaces.WorkflowArtifactRetentionAuditConfigurer); ok {
		configurable.ConfigureWorkflowArtifactRetentionAudit(audit)
	}
}

func configureDeploymentWindowAudit(target any, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := target.(pro_interfaces.DeploymentWindowAuditConfigurer); ok {
		configurable.ConfigureDeploymentWindowAudit(audit)
	}
}

func configurePolicyGuardrailAudit(controller pro_interfaces.PolicyGuardrailController, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := controller.(pro_interfaces.PolicyGuardrailAuditConfigurer); ok {
		configurable.ConfigurePolicyGuardrailAudit(audit)
	}
}
