// Package sql adapts the public Community surface for the workspace fixture.
package sql

import (
	community "github.com/semaphoreui/semaphore/community-pro/db/sql"
	coresql "github.com/semaphoreui/semaphore/db/sql"
)

type TerraformStoreImpl = community.TerraformStoreImpl
type WorkflowStoreImpl struct {
	community.WorkflowStoreImpl
	connection *coresql.SqlDbConnection
}

func NewWorkflowStore(connection *coresql.SqlDbConnection) *WorkflowStoreImpl {
	return &WorkflowStoreImpl{connection: connection}
}
