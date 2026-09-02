package task_logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlockedTaskStatusIsTerminalAndValid(t *testing.T) {
	assert.True(t, TaskBlockedStatus.IsValid())
	assert.True(t, TaskBlockedStatus.IsFinished())
	assert.True(t, TaskBlockedStatus.IsNotifiable())
	assert.Equal(t, 100, TaskStatusProgressRank(TaskBlockedStatus))
	assert.Contains(t, TaskBlockedStatus.Format(), "BLOCKED")
	assert.NotContains(t, UnfinishedTaskStatuses(), TaskBlockedStatus)
}
