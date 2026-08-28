// Package factory adapts the public Community surface for the workspace fixture.
package factory

import (
	community "github.com/semaphoreui/semaphore/community-pro/db/factory"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/db/sql"
)

var (
	NewTerraformStore = community.NewTerraformStore
)

func NewWorkflowStore(store db.Store) db.WorkflowManager {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return sql.NewWorkflowStore(nil)
	}
	return sql.NewWorkflowStore(connectionStore.GetConnection())
}
