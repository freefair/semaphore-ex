package factory

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// NewDeploymentWindowStore is unavailable in Community. The Enhanced module
// replaces this factory with the SQL-backed governance repository.
func NewDeploymentWindowStore(db.Store) pro_interfaces.DeploymentWindowGovernanceRepository {
	return nil
}

// NewPolicyGuardrailStore is unavailable in Community. The Enhanced module
// replaces this factory with the SQL-backed policy repository.
func NewPolicyGuardrailStore(db.Store) pro_interfaces.PolicyGuardrailRepository {
	return nil
}

func NewWorkflowTriggerStore(db.Store) db.WorkflowTriggerManager {
	return nil
}

// NewWorkflowFileArtifactStore is unavailable in Community.
func NewWorkflowFileArtifactStore(db.Store) pro_interfaces.WorkflowFileArtifactRepository {
	return nil
}
