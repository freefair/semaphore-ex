// Package factory adapts the public Community surface for the workspace fixture.
package factory

import (
	community "github.com/semaphoreui/semaphore/community-pro/db/factory"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

var (
	NewAnsibleTaskRepository = community.NewAnsibleTaskRepository
	NewTerraformStore        = community.NewTerraformStore
)

func NewWorkflowStore(store db.Store) db.WorkflowManager {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return sql.NewWorkflowStore(nil)
	}
	taskStore, _ := store.(interface {
		GetWorkflowRunTasks(projectID int, runID int, params db.RetrieveQueryParams) ([]db.TaskWithTpl, error)
	})
	return sql.NewWorkflowStore(connectionStore.GetConnection(), taskStore)
}

func NewDeploymentWindowStore(store db.Store) pro_interfaces.DeploymentWindowGovernanceRepository {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	return sql.NewDeploymentWindowStore(connectionStore.GetConnection())
}

func NewPolicyGuardrailStore(store db.Store) pro_interfaces.PolicyGuardrailRepository {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok || connectionStore.GetConnection() == nil {
		return nil
	}
	return sql.NewPolicyGuardrailStore(connectionStore.GetConnection())
}

func NewWorkflowTriggerStore(store db.Store) db.WorkflowTriggerManager {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	return sql.NewWorkflowStore(connectionStore.GetConnection())
}
