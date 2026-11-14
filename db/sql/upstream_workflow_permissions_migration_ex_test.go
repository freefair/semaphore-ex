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

func TestMigration22018EXRemovesOnlyApprovalFromExistingGuestWorkflowGrant(t *testing.T) {
	before := "2.20.17"
	store := InitConfigCreateTestStoreAt(&before)
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "guest workflow grant migration"})
	require.NoError(t, err)
	workflow := insertWorkflow(t, store, project.ID, "guest workflow grant")
	original := db.CanReadProject | db.CanRunWorkflows | db.CanApproveWorkflows
	_, err = store.exec(
		"insert into project__workflow_role (workflow_id, role_id, project_id, permissions) values (?, ?, ?, ?)",
		workflow, db.ProjectRoleID("builtin:guest"), project.ID, original,
	)
	require.NoError(t, err)

	target := "2.20.18-ex1.1"
	require.NoError(t, db.Migrate(store, &target))

	var migrated db.ProjectUserPermission
	require.NoError(t, store.Sql().SelectOne(&migrated,
		"select permissions from project__workflow_role where workflow_id=? and role_id=?", workflow, "builtin:guest"))
	assert.Equal(t, original&^db.CanApproveWorkflows, migrated)
}
