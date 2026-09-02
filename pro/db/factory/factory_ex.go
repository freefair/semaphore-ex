package factory

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func NewDeploymentWindowStore(db.Store) pro_interfaces.DeploymentWindowPolicyRepository { return nil }

func NewWorkflowTriggerStore(db.Store) db.WorkflowTriggerManager {
	return nil
}
