package server

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func NewWorkflowFileArtifactRetentionWorker(
	pro_interfaces.WorkflowFileArtifactRepository,
	pro_interfaces.AuditServiceFacade,
) pro_interfaces.WorkflowFileArtifactRetentionWorker {
	return nil
}
