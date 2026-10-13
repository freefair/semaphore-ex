package tasks

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAddTaskFromWiresCollectorIntoActiveLocalExecutor(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	fixture.pool.register = make(chan *TaskRunner, 1)
	collector := &outputsCollectorFake{}
	fixture.pool.SetTaskOutputsCollector(collector)
	created, err := fixture.pool.AddTaskFrom(context.Background(), audit.TriggerAPI, db.Task{TemplateID: fixture.template.ID}, nil, "", fixture.template.ProjectID, false)
	require.NoError(t, err)
	registered := <-fixture.pool.register
	require.Equal(t, created.ID, registered.Task.ID)
	executor, ok := registered.currentJob().(*LocalExecutor)
	require.True(t, ok)
	require.NotNil(t, executor.OutputsCollector)
	assert.Same(t, collector, executor.OutputsCollector)
}
