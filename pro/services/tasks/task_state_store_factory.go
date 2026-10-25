package tasks

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
)

// NewTaskStateStore preserves the upstream factory seam. The selected EX
// module also keeps process-local handles in memory and durable state in SQL.
func NewTaskStateStore(_ pro_interfaces.HARedis) tasks.TaskStateStore {
	return tasks.NewMemoryTaskStateStore()
}
