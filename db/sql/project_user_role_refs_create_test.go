package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProjectUserWithRoleRefsIsAtomicAndProjectsLegacyShape(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Atomic role member project"})
	require.NoError(t, err)
	user := createProjectRoleTestUser(t, store, "atomic-role-member")
	global, err := store.CreateGlobalRole(db.Role{ID: "atomic-global", Slug: "atomic-global", Name: "Atomic global", Revision: 1})
	require.NoError(t, err)
	local, err := store.CreateProjectRole(db.Role{ID: "atomic-local", Name: "Atomic local", ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)

	_, err = store.CreateProjectUserWithRoleRefs(
		db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Revision: 1},
		[]db.ProjectRoleID{global.ID, "missing-role"},
	)
	require.Error(t, err)
	_, err = store.GetProjectUser(project.ID, user.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	membership, err := store.CreateProjectUserWithRoleRefs(
		db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Revision: 1},
		[]db.ProjectRoleID{global.ID},
	)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectUserRole(global.Slug), membership.Role)
	assert.Nil(t, membership.RoleID)
	assert.Equal(t, 1, membership.Revision)

	require.NoError(t, store.SetProjectUserRoleRefs(project.ID, user.ID, []db.ProjectRoleID{local.ID}, membership.Revision))
	membership, err = store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectNone, membership.Role)
	require.NotNil(t, membership.RoleID)
	assert.Equal(t, local.ID, *membership.RoleID)

	require.NoError(t, store.SetProjectUserRoleRefs(project.ID, user.ID, []db.ProjectRoleID{local.ID, db.ProjectRoleID(db.BuiltinProjectRoleReferenceManager)}, membership.Revision))
	membership, err = store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectManager, membership.Role)
	assert.Nil(t, membership.RoleID)
}
