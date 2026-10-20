package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowIntegrationMigrationPersistsPrincipalAndRollsBack(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := createTestIntegration(t, store)
	workflowID := createTestWorkflow(t, store, project.ID, "workflow migration")
	actor, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-migration-actor", Name: "Workflow migration actor", Email: "workflow-migration-actor@example.test"})
	require.NoError(t, err)
	created, err := store.CreateIntegration(db.Integration{Name: "workflow hook", ProjectID: project.ID, WorkflowID: &workflowID, WorkflowUserID: &actor.ID})
	require.NoError(t, err)

	loaded, err := store.GetIntegration(project.ID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.WorkflowUserID)
	assert.Equal(t, actor.ID, *loaded.WorkflowUserID)

	require.NoError(t, db.Rollback(store, "2.20.14"))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__integration"), "workflow_user_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run"), "integration_id")
}
