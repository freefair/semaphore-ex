package tasks

import (
	"github.com/semaphoreui/semaphore/db"
)

// SetExecutorImageCapabilityResolver injects the replaceable-edition entitlement decision.
func (p *TaskPool) SetExecutorImageCapabilityResolver(resolver func(*db.User) bool) {
	p.executorImageAvailable = resolver
}
