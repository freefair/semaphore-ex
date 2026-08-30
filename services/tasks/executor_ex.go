package tasks

import (
	"github.com/semaphoreui/semaphore/db"
)

// ExecutorMetadataProvider is an optional, additive capability implemented by
// remote executors that have a bounded runtime identity worth reporting. The
// runner never serializes executor implementation objects or task inputs.
type ExecutorMetadataProvider interface {
	ExecutorMetadata() db.RunnerExecutorMetadata
}
