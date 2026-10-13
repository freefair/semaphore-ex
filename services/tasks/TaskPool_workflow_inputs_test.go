package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainLogs reads the log records queued for the pool's log writer.
func drainLogs(p *TaskPool) (lines []string) {
	for {
		select {
		case rec := <-p.logger:
			lines = append(lines, rec.output)
		default:
			return
		}
	}
}

func TestTaskPool_AddFailedTaskRejectsUnfencedWorkflowCreation(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	runID, nodeID := 4, 9
	before, err := fixture.store.GetTemplateTasks(fixture.task.ProjectID, fixture.template.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	task, err := fixture.pool.AddFailedTask(db.Task{
		ProjectID: fixture.task.ProjectID, TemplateID: fixture.template.ID,
		WorkflowRunID: &runID, WorkflowNodeID: &nodeID,
	}, nil, "", []string{"missing input"})
	require.ErrorContains(t, err, "unfenced")
	assert.Zero(t, task.ID)
	after, err := fixture.store.GetTemplateTasks(fixture.task.ProjectID, fixture.template.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Len(t, after, len(before))
	assert.Empty(t, fixture.pool.state.QueueRange())
	assert.Empty(t, drainLogs(&fixture.pool))
}

func TestTaskPool_AddFailedTask_UnknownTemplate(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)

	_, err := fixture.pool.AddFailedTask(db.Task{ProjectID: fixture.task.ProjectID, TemplateID: 99999}, nil, "", nil)

	assert.Error(t, err)
}

func TestTaskPool_LogTask(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)

	t.Run("task registered in the pool", func(t *testing.T) {
		tr := NewTaskRunner(fixture.task, &fixture.pool, "", nil)
		fixture.pool.StateStore().SetRunning(tr)
		t.Cleanup(func() { fixture.pool.StateStore().DeleteRunning(tr.Task.ID) })

		fixture.pool.LogTask(fixture.task, []string{"first", "second"})

		assert.Equal(t, []string{"first", "second"}, drainLogs(&fixture.pool))
	})

	t.Run("task not registered yet still gets its lines", func(t *testing.T) {
		fixture.pool.LogTask(fixture.task, []string{"early"})

		assert.Equal(t, []string{"early"}, drainLogs(&fixture.pool))
	})

	t.Run("no lines, no records", func(t *testing.T) {
		fixture.pool.LogTask(fixture.task, nil)

		assert.Empty(t, drainLogs(&fixture.pool))
	})
}
