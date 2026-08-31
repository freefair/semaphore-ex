package api

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

type workflowAuditConfigurerStub struct {
	pro_interfaces.WorkflowService
	audit pro_interfaces.AuditServiceFacade
}

func (s *workflowAuditConfigurerStub) ConfigureWorkflowAudit(audit pro_interfaces.AuditServiceFacade) {
	s.audit = audit
}

type workflowAuditFacadeStub struct{}

func (workflowAuditFacadeStub) Record(context.Context, pro_interfaces.AuditEvent) error { return nil }

func TestConfigureWorkflowAuditUsesOptionalEnhancedSeam(t *testing.T) {
	service := &workflowAuditConfigurerStub{}
	audit := workflowAuditFacadeStub{}

	configureWorkflowAudit(service, audit)

	assert.Equal(t, audit, service.audit)
	configureWorkflowAudit(nil, audit)
}
