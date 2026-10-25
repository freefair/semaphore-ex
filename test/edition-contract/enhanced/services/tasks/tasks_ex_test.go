package tasks

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
	core "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

type unusedRedis struct{ t *testing.T }

func (r unusedRedis) Ping(context.Context) error {
	r.t.Error("runtime task store must not use Redis")
	return nil
}
func (r unusedRedis) Close() error { r.t.Error("runtime task store must not close Redis"); return nil }
func (r unusedRedis) Describe() string {
	r.t.Error("runtime task store must not inspect Redis")
	return ""
}

func TestTaskStateFactoryKeepsExecutionHandlesLocal(t *testing.T) {
	first := NewTaskStateStore(unusedRedis{t})
	second := NewTaskStateStore(unusedRedis{t})
	require.NoError(t, first.Start(nil))
	task := &core.TaskRunner{Task: db.Task{ID: 71, ProjectID: 3}}
	first.Enqueue(task)
	assert.Equal(t, 1, first.QueueLen())
	assert.Zero(t, second.QueueLen())
	assert.True(t, first.ClaimAndDequeue(71))
	assert.Zero(t, first.QueueLen())
	first.SetRunning(task)
	assert.Same(t, task, first.RunningRange()[0])
	assert.Zero(t, second.RunningCount())
	first.DeleteRunning(71)
	assert.Zero(t, first.RunningCount())
}
