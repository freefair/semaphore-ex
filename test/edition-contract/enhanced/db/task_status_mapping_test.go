package db

import (
	"testing"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
)

func TestWorkflowBlockedTaskMapsToBlockedNode(t *testing.T) {
	assert.Equal(t, coreDB.WorkflowRunNodeBlocked, WorkflowRunNodeStatusFromTaskStatus(task_logger.TaskBlockedStatus))
}
