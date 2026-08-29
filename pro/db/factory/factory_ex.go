package factory

import (
	"github.com/semaphoreui/semaphore/db"
)

func NewWorkflowTriggerStore(db.Store) db.WorkflowTriggerManager {
	return nil
}
