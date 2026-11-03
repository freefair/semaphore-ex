package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamWorkflowMigrationsPreserveEXMasksAndRollback(t *testing.T) {
	before := "2.20.16"
	store := InitConfigCreateTestStoreAt(&before)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "migration-ex-permissions"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "migration-ex-role", Slug: "migration-ex-role", Name: "Migration role", ProjectID: &project.ID, Permissions: db.CanViewWorkflows | db.CanStopWorkflows, Revision: 1})
	require.NoError(t, err)
	target := "2.20.18"
	require.NoError(t, db.Migrate(store, &target))
	migrated, err := store.GetProjectRoleByID(project.ID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, role.Permissions|db.CanReadProject|db.CanApproveWorkflows, migrated.Permissions)
	workflow := insertWorkflow(t, store, project.ID, "migration-ex-workflow")
	_, err = store.CreateWorkflowRole(db.WorkflowRolePerm{ProjectID: project.ID, WorkflowID: workflow, RoleID: db.ProjectRoleID("builtin:guest"), Permissions: db.CanReadProject})
	require.NoError(t, err)
	require.NoError(t, db.Rollback(store, before))
	restored, err := store.GetProjectRoleByID(project.ID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, role.Permissions, restored.Permissions)
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_role")
}
