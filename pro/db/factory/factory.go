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

func NewDeploymentWindowStore(db.Store) pro_interfaces.DeploymentWindowPolicyRepository { return nil }

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
