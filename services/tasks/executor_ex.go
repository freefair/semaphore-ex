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

// DockerExecutionPolicyConsumer is implemented only by Docker-backed providers.
// The job pool uses it to atomically install the central server policy before it
// accepts Docker work; other executor implementations remain unaffected.
type DockerExecutionPolicyConsumer interface {
	ApplyDockerExecutionPolicy(db.DockerExecutionPolicy) error
	DockerExecutionPolicyAcknowledgement() db.DockerExecutionPolicyAck
}
