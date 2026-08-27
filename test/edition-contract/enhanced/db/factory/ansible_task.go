package factory

import (
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/db/sql"
)

// NewAnsibleTaskRepository selects the Enhanced durable summary store.
func NewAnsibleTaskRepository(store db.Store) db.AnsibleTaskRepository {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return sql.NewAnsibleTask(nil)
	}
	return sql.NewAnsibleTask(connectionStore.GetConnection())
}
