package tasks

import "github.com/semaphoreui/semaphore/db"

// TaskControlLifecycle is the narrow replaceable-edition boundary for durable
// HA ownership. Registration completes before a runner can observe an
// assignment; release is idempotent and happens on every pool-stop path.
type TaskControlLifecycle interface {
	RegisterTaskControl(task db.Task) error
	ReleaseTaskControl(task db.Task)
}
