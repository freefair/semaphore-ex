package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22049AddsWorkflowRolePolicyStorageAndMigratesRoleBits(t *testing.T) {
	legacyVersion := "2.20.48"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "workflow policy migration"})
	require.NoError(t, err)
	legacyRole, err := store.CreateProjectRole(db.Role{
		ID: "role_legacyworkflow", Slug: "role_legacyworkflow", Name: "Legacy workflow",
		ProjectID: &project.ID, Permissions: db.CanRunProjectTasks | db.CanManageProjectResources |
			db.CanViewProjectResources, Revision: 1,
	})
	require.NoError(t, err)
	globalRole, err := store.CreateGlobalRole(db.Role{
		ID: "role_globallegacy", Slug: "role_globallegacy", Name: "Global legacy",
		Permissions: db.CanRunProjectTasks | db.CanManageProjectResources |
			db.CanViewProjectResources,
		Revision: 1,
	})
	require.NoError(t, err)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_template"), "access_policy")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_template"), "access_policy_revision")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_node"), "approval_role_policy")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_approval"), "role_policy_snapshot")
	assert.Contains(t, sqliteTableNames(t, store), "project__workflow_approval_contribution")
	contributionColumns := sqliteColumnNames(t, store, "project__workflow_approval_contribution")
	for _, column := range []string{
		"role_origin", "directory_mapping_id", "directory_mapping_revision", "directory_revision_fingerprint",
		"decision", "comment", "created", "policy_revision", "correlation_id",
	} {
		assert.Contains(t, contributionColumns, column)
	}
	assert.Contains(t, sqliteForeignKeyTargets(t, store, "project__workflow_approval_contribution"), "project__workflow_approval")
	var actorIndexCount int
	require.NoError(t, store.Sql().Db.QueryRow(
		"select count(1) from sqlite_master where type='index' and name=?",
		"project__workflow_approval_contribution__actor",
	).Scan(&actorIndexCount))
	assert.Equal(t, 1, actorIndexCount)

	migrated, err := store.GetProjectRoleByID(project.ID, legacyRole.ID)
	require.NoError(t, err)
	assert.True(t, migrated.Permissions.Can(db.CanViewWorkflows))
	assert.True(t, migrated.Permissions.Can(db.CanEditWorkflows))
	assert.True(t, migrated.Permissions.Can(db.CanStartWorkflows))
	assert.True(t, migrated.Permissions.Can(db.CanStopWorkflows))
	assert.True(t, migrated.Permissions.Can(db.CanAdministerWorkflows))

	unmappedGlobal, err := store.GetGlobalRoleByID(globalRole.ID)
	require.NoError(t, err)
	assert.Equal(t, globalRole.Permissions, unmappedGlobal.Permissions)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_template"), "access_policy")
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_approval_contribution")
}
