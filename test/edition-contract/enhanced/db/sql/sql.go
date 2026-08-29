// Package sql adapts the public Community surface for the workspace fixture.
package sql

import (
	community "github.com/semaphoreui/semaphore/community-pro/db/sql"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
)

type AnsibleTaskStoreImpl = community.AnsibleTaskStoreImpl
type TerraformStoreImpl = community.TerraformStoreImpl
type WorkflowStoreImpl struct {
	community.WorkflowStoreImpl
	connection        *coresql.SqlDbConnection
	workflowTaskStore workflowRunTaskStore
}

var _ db.WorkflowTriggerManager = (*WorkflowStoreImpl)(nil)

type workflowRunTaskStore interface {
	GetWorkflowRunTasks(projectID int, runID int, params db.RetrieveQueryParams) ([]db.TaskWithTpl, error)
}

func NewWorkflowStore(connection *coresql.SqlDbConnection, taskStores ...workflowRunTaskStore) *WorkflowStoreImpl {
	store := &WorkflowStoreImpl{connection: connection}
	if len(taskStores) > 0 {
		store.workflowTaskStore = taskStores[0]
	}
	return store
}

var NewAnsibleTask = community.NewAnsibleTask
