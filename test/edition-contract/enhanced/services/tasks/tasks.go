package tasks

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
)

// NewTaskStateStore keeps execution-local handles in memory. Durable task state
// and HA assignment fencing remain in SQL; Redis only coordinates live signals.
func NewTaskStateStore(_ pro_interfaces.HARedis) tasks.TaskStateStore {
	return tasks.NewMemoryTaskStateStore()
}
