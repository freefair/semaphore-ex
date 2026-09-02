package factory

import (
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func NewTerraformStore(store db.Store) db.TerraformStore {
	return &sql.TerraformStoreImpl{}
}

// NewDeploymentWindowStore is unavailable in Community. The Enhanced module
// replaces this factory with the SQL-backed governance repository.
func NewDeploymentWindowStore(db.Store) pro_interfaces.DeploymentWindowGovernanceRepository {
	return nil
}

func NewAnsibleTaskRepository(store db.Store) db.AnsibleTaskRepository {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return sql.NewAnsibleTask(nil)
	}
	return sql.NewAnsibleTask(connectionStore.GetConnection())
}

func NewWorkflowStore(store db.Store) db.WorkflowManager {
	return &sql.WorkflowStoreImpl{}
}

func NewWorkflowTriggerStore(db.Store) db.WorkflowTriggerManager {
	return nil
}
