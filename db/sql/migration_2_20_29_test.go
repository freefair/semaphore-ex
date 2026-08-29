package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22029BackfillsProjectRoleIdentityAndAssignments(t *testing.T) {
	legacyVersion := "2.20.28"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "Legacy project roles"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "legacy-role-user",
		Name:     "Legacy Role User",
		Email:    "legacy-role-user@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{
		Slug:      "legacy-viewer",
		Name:      "Legacy Viewer",
		ProjectID: &project.ID,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      "legacy-viewer",
	})
	require.NoError(t, err)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "role"), "role_id")
	assert.Contains(t, sqliteColumnNames(t, store, "role"), "revision")
	assert.Contains(t, sqliteColumnNames(t, store, "project__user"), "role_id")
	assert.Contains(t, sqliteColumnNames(t, store, "project__user"), "revision")

	role, err := store.GetProjectRole(project.ID, "legacy-viewer")
	require.NoError(t, err)
	assert.Equal(t, db.ProjectRoleID("legacy-viewer"), role.ID)
	assert.Equal(t, 1, role.Revision)
	assert.True(t, role.Permissions&db.CanViewProjectResources != 0)

	assignment, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, assignment.RoleID)
	assert.Equal(t, role.ID, *assignment.RoleID)
	assert.Equal(t, 1, assignment.Revision)

	secondProject, err := store.CreateProject(db.Project{Name: "Second project"})
	require.NoError(t, err)
	_, err = store.exec(
		"insert into `role` (role_id, slug, name, permissions, project_id, revision) values (?, ?, ?, ?, ?, ?)",
		"role_second", "second-viewer", role.Name, db.CanViewProjectResources, secondProject.ID, 1,
	)
	require.NoError(t, err, "display names are unique per project, not globally")
	_, err = store.exec(
		"insert into `role` (role_id, slug, name, permissions, project_id, revision) values (?, ?, ?, ?, ?, ?)",
		"role_duplicate", "duplicate-viewer", role.Name, db.CanViewProjectResources, project.ID, 1,
	)
	assert.Error(t, err, "duplicate display names in one project must be rejected")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "role"), "role_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "role"), "revision")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__user"), "role_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__user"), "revision")

	legacyRole, err := store.GetProjectRole(project.ID, "legacy-viewer")
	require.NoError(t, err)
	assert.Zero(t, legacyRole.Permissions&db.CanViewProjectResources)
}
