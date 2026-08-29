package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectRoleAssignmentIsScopedRevisionFencedAndKeepsAnAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Role assignment project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "Other role assignment project"})
	require.NoError(t, err)
	firstUser := createProjectRoleTestUser(t, store, "first-role-admin")
	secondUser := createProjectRoleTestUser(t, store, "second-role-admin")
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: firstUser.ID, Role: db.ProjectOwner,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: secondUser.ID, Role: db.ProjectOwner,
	})
	require.NoError(t, err)

	firstMembership, err := store.GetProjectUser(project.ID, firstUser.ID)
	require.NoError(t, err)
	missingRevision := firstMembership
	missingRevision.Role = db.ProjectGuest
	missingRevision.Revision = 0
	err = store.UpdateProjectUser(missingRevision)
	assert.ErrorIs(t, err, db.ErrProjectMembershipRevisionConflict)

	firstMembership.Role = db.ProjectGuest
	require.NoError(t, store.UpdateProjectUser(firstMembership))
	updatedFirst, err := store.GetProjectUser(project.ID, firstUser.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectGuest, updatedFirst.Role)
	assert.Equal(t, firstMembership.Revision+1, updatedFirst.Revision)

	firstMembership.Role = db.ProjectManager
	err = store.UpdateProjectUser(firstMembership)
	assert.ErrorIs(t, err, db.ErrProjectMembershipRevisionConflict)

	secondMembership, err := store.GetProjectUser(project.ID, secondUser.ID)
	require.NoError(t, err)
	secondMembership.Role = db.ProjectGuest
	err = store.UpdateProjectUser(secondMembership)
	assert.ErrorIs(t, err, db.ErrLastProjectAdministrator)

	foreignRole, err := store.CreateProjectRole(db.Role{
		ID:          "role_foreign_admin",
		Name:        "Foreign administrator",
		Permissions: db.CanManageProjectUsers,
		ProjectID:   &otherProject.ID,
		Revision:    1,
	})
	require.NoError(t, err)
	updatedFirst, err = store.GetProjectUser(project.ID, firstUser.ID)
	require.NoError(t, err)
	updatedFirst.Role = db.ProjectNone
	updatedFirst.RoleID = &foreignRole.ID
	err = store.UpdateProjectUser(updatedFirst)
	assert.ErrorIs(t, err, db.ErrNotFound)

	persistedFirst, err := store.GetProjectUser(project.ID, firstUser.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectGuest, persistedFirst.Role)
	assert.Nil(t, persistedFirst.RoleID)
}

func TestDeleteProjectUserKeepsLastCustomAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Custom administrator project"})
	require.NoError(t, err)
	user := createProjectRoleTestUser(t, store, "custom-role-admin")
	role, err := store.CreateProjectRole(db.Role{
		ID:          "role_custom_admin",
		Name:        "Custom administrator",
		Permissions: db.CanManageProjectUsers,
		ProjectID:   &project.ID,
		Revision:    1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    user.ID,
		RoleID:    &role.ID,
	})
	require.NoError(t, err)

	err = store.DeleteProjectUser(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrLastProjectAdministrator)
}

func createProjectRoleTestUser(t *testing.T, store *SqlDb, username string) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: username,
		Name:     username,
		Email:    username + "@example.test",
	})
	require.NoError(t, err)
	return user
}
