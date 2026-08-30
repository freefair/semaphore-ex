package runners

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRunningJob_GetProgressIncludesBoundedExecutorMetadata(t *testing.T) {
	rj := newTestRunningJob(1)
	rj.job = &metadataExecutor{LocalExecutor: tasks.LocalExecutor{}}

	_, _, _, metadata := rj.getProgress()

	require.NotNil(t, metadata)
	assert.Equal(t, db.RunnerExecutorDocker, metadata.ExecutorType)
	assert.Equal(t, "abc123", metadata.ContainerID)
	assert.Equal(t, "semaphore-task-1-boot", metadata.ContainerName)
}

type metadataExecutor struct {
	tasks.LocalExecutor
}

func (*metadataExecutor) ExecutorMetadata() db.RunnerExecutorMetadata {
	return db.RunnerExecutorMetadata{
		ExecutorType:  db.RunnerExecutorDocker,
		ContainerID:   "abc123",
		ContainerName: "semaphore-task-1-boot",
	}
}
