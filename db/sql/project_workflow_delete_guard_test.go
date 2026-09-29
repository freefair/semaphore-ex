package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteProjectRejectsActiveWorkflowRuns(t *testing.T) {
	for _, status := range []db.WorkflowRunStatus{
		db.WorkflowRunPending,
		db.WorkflowRunQueued,
		db.WorkflowRunRunning,
		db.WorkflowRunApproval,
		db.WorkflowRunStopping,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			project, err := store.CreateProject(db.Project{Name: "workflow guard"})
			require.NoError(t, err)
			workflowID, err := store.insert("id", "insert into project__workflow_template(project_id, name) values (?, ?)", project.ID, "deploy")
			require.NoError(t, err)
			_, err = store.exec("insert into project__workflow_run(project_id, workflow_template_id, status) values (?, ?, ?)", project.ID, workflowID, status)
			require.NoError(t, err)

			err = store.DeleteProject(project.ID)

			assert.ErrorIs(t, err, db.ErrInvalidOperation)
			_, err = store.GetProject(project.ID)
			require.NoError(t, err)
		})
	}
}

func TestDeleteProjectRemovesTerminalWorkflowRuns(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "terminal workflow"})
	require.NoError(t, err)
	workflowID, err := store.insert("id", "insert into project__workflow_template(project_id, name) values (?, ?)", project.ID, "deploy")
	require.NoError(t, err)
	_, err = store.exec("insert into project__workflow_run(project_id, workflow_template_id, status) values (?, ?, ?)", project.ID, workflowID, db.WorkflowRunSucceeded)
	require.NoError(t, err)

	require.NoError(t, store.DeleteProject(project.ID))
	_, err = store.GetProject(project.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
}
