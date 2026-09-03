package api

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

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
