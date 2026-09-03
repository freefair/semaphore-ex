// Package sql adapts the public Community surface for the workspace fixture.
package sql

import (
	community "github.com/semaphoreui/semaphore/community-pro/db/sql"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
)

type TerraformStoreImpl = community.TerraformStoreImpl
type WorkflowStoreImpl struct {
	community.WorkflowStoreImpl
	connection               *coresql.SqlDbConnection
	notificationRouter       *coresql.NotificationTransactionRouter
	workflowTaskStore        workflowRunTaskStore
	deploymentWindowRequired bool
	policyGuardrailRequired  bool
}

// ConfigureDeploymentWindowAdmission marks this Enhanced store as the final
// workflow-run persistence boundary. Once enabled it rejects unbound runs.
func (d *WorkflowStoreImpl) ConfigureDeploymentWindowAdmission() {
	if d != nil {
		d.deploymentWindowRequired = true
	}
}

// ConfigurePolicyGuardrailAdmission marks this Enhanced store as the final
// workflow-run persistence boundary. Once enabled it rejects unbound runs.
func (d *WorkflowStoreImpl) ConfigurePolicyGuardrailAdmission() {
	if d != nil {
		d.policyGuardrailRequired = true
	}
}

var _ db.WorkflowTriggerManager = (*WorkflowStoreImpl)(nil)

type workflowRunTaskStore interface {
	GetWorkflowRunTasks(projectID int, runID int, params db.RetrieveQueryParams) ([]db.TaskWithTpl, error)
}

func NewWorkflowStore(connection *coresql.SqlDbConnection, taskStores ...workflowRunTaskStore) *WorkflowStoreImpl {
	store := &WorkflowStoreImpl{connection: connection, notificationRouter: coresql.NewNotificationTransactionRouter(connection)}
	if len(taskStores) > 0 {
		store.workflowTaskStore = taskStores[0]
	}
	return store
}
