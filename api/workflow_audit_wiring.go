package api

import "github.com/semaphoreui/semaphore/pro_interfaces"

// configureWorkflowAudit keeps audit attachment optional so Community's
// workflow service remains source-compatible with the shared router.
func configureWorkflowAudit(service pro_interfaces.WorkflowService, audit pro_interfaces.AuditServiceFacade) {
	if configurable, ok := service.(pro_interfaces.WorkflowAuditConfigurer); ok {
		configurable.ConfigureWorkflowAudit(audit)
	}
}
