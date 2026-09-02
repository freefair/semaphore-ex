package runners

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/taskredaction"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRunningJobRedactsGlobalCredentialBeforeProgressUpload(t *testing.T) {
	runner := newTestRunningJob(1)
	runner.redactor = taskredaction.NewFromTaskSecret(`{"deploy_token":"runner-secret-value"}`, []string{"deploy_token"})
	runner.Log("credential=runner-secret-value")

	_, records, _, _ := runner.getProgress()
	require.Len(t, records, 1)
	assert.NotContains(t, records[0].Message, "runner-secret-value")
	assert.Contains(t, records[0].Message, "[REDACTED]")
}

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
