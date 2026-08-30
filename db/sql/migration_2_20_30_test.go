package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22030SeparatesGlobalAssignmentsAndTemplateOverrides(t *testing.T) {
	legacyVersion := "2.20.29"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-role-migration-user",
		Name:     "Global role migration user",
		Email:    "global-role-migration@example.test",
		Admin:    true,
	})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "legacy-global", Name: "Legacy global"})
	require.NoError(t, err)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "role"), "global_permissions")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template_role"), "role_id")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template_role"), "allowed_permissions")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template_role"), "denied_permissions")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template_role"), "revision")
	assert.NotContains(t, sqliteForeignKeyTargets(t, store, "project__template_role"), "role")
	assert.ElementsMatch(t, []string{"id", "user_id", "role_id", "revision"},
		sqliteColumnNames(t, store, "user__global_role"))

	stateRevision, err := store.Sql().SelectInt("select revision from global_role_state where id=1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), stateRevision)

	role, err := store.GetGlobalRoleBySlug("legacy-global")
	require.NoError(t, err)
	assert.Zero(t, role.GlobalPermissions)
	assert.Positive(t, user.ID)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "role"), "global_permissions")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__template_role"), "role_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__template_role"), "allowed_permissions")
	assert.Contains(t, sqliteForeignKeyTargets(t, store, "project__template_role"), "role")
}

func TestMigration22030RollbackRefusesToRemoveTheLastGlobalAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	builtInAdmin, err := store.CreateUserWithoutPassword(db.User{
		Username: "rollback-built-in-admin", Name: "Rollback built-in admin",
		Email: "rollback-built-in-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	delegatedAdmin, err := store.CreateUserWithoutPassword(db.User{
		Username: "rollback-delegated-admin", Name: "Rollback delegated admin",
		Email: "rollback-delegated-admin@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "rollback_delegated_admin", Slug: "rollback_delegated_admin", Name: "Rollback delegated admin",
		GlobalPermissions: db.CanManageGlobalRoles, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: delegatedAdmin.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)
	builtInAdmin.Admin = false
	require.NoError(t, store.UpdateUser(db.UserWithPwd{User: builtInAdmin}))

	err = db.Rollback(store, "2.20.29")
	assert.ErrorIs(t, err, db.ErrLastGlobalAdministrator)
	assert.Contains(t, sqliteTableNames(t, store), "user__global_role")
	_, err = store.GetGlobalRoleByID(role.ID)
	require.NoError(t, err)
}

func TestMigration22030RollbackRefusesToRegrantDeniedTemplatePermissions(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	projectID, repositoryID := newTemplateTestProject(t, store)
	_, err := store.CreateRole(db.Role{
		ID: "rollback_guest_role", Slug: string(db.ProjectGuest), Name: "Rollback guest",
		ProjectID: &projectID, Permissions: db.ProjectGuest.GetPermissions(), Revision: 1,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "Rollback denied template", Playbook: "site.yml",
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)

	err = db.Rollback(store, "2.20.29")
	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	assert.Contains(t, sqliteColumnNames(t, store, "project__template_role"), "denied_permissions")
}

func sqliteForeignKeyTargets(t *testing.T, store *SqlDb, table string) []string {
	t.Helper()
	rows, err := store.Sql().Db.Query("pragma foreign_key_list(" + table + ")")
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck

	var targets []string
	for rows.Next() {
		var id, sequence int
		var target, from, to, onUpdate, onDelete, match string
		require.NoError(t, rows.Scan(
			&id, &sequence, &target, &from, &to, &onUpdate, &onDelete, &match,
		))
		targets = append(targets, target)
	}
	require.NoError(t, rows.Err())
	return targets
}
