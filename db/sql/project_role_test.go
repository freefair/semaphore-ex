package sql

import (
	"errors"
	"sync"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectRolePersistenceIsScopedAndRevisionFenced(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	firstProject, err := store.CreateProject(db.Project{Name: "First role project"})
	require.NoError(t, err)
	secondProject, err := store.CreateProject(db.Project{Name: "Second role project"})
	require.NoError(t, err)

	first, err := store.CreateProjectRole(db.Role{
		ID:          "role_first_viewer",
		Name:        "Resource viewer",
		Permissions: db.CanViewProjectResources,
		ProjectID:   &firstProject.ID,
		Revision:    1,
	})
	require.NoError(t, err)
	second, err := store.CreateProjectRole(db.Role{
		ID:          "role_second_viewer",
		Name:        "Resource viewer",
		Permissions: db.CanViewProjectResources,
		ProjectID:   &secondProject.ID,
		Revision:    1,
	})
	require.NoError(t, err, "the same display name is valid in another project")
	assert.NotEqual(t, first.Slug, second.Slug)

	_, err = store.GetProjectRoleByID(secondProject.ID, first.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	first.Name = "Resource operator"
	first.Permissions |= db.CanManageProjectResources
	updated, err := store.UpdateProjectRole(firstProject.ID, first, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	assert.Equal(t, first.Name, updated.Name)

	first.Name = "Stale overwrite"
	_, err = store.UpdateProjectRole(firstProject.ID, first, 1)
	assert.ErrorIs(t, err, db.ErrProjectRoleRevisionConflict)
	persisted, err := store.GetProjectRoleByID(firstProject.ID, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "Resource operator", persisted.Name)

	_, err = store.UpdateProjectRole(secondProject.ID, first, updated.Revision)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestConcurrentProjectRoleEditsAllowExactlyOneRevision(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Concurrent role project"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_concurrent_viewer", Name: "Concurrent viewer",
		Permissions: db.CanViewProjectResources, ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"First concurrent edit", "Second concurrent edit"} {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			<-start
			candidate := role
			candidate.Name = name
			_, updateErr := store.UpdateProjectRole(project.ID, candidate, role.Revision)
			results <- updateErr
		}(name)
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for updateErr := range results {
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, db.ErrProjectRoleRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent update error: %v", updateErr)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	persisted, err := store.GetProjectRoleByID(project.ID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, role.Revision+1, persisted.Revision)
}

func TestDeleteProjectRoleRejectsAssignmentsAndStaleRevision(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Assigned role project"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "assigned-role-user",
		Name:     "Assigned Role User",
		Email:    "assigned-role-user@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID:          "role_assigned_viewer",
		Name:        "Assigned viewer",
		Permissions: db.CanViewProjectResources,
		ProjectID:   &project.ID,
		Revision:    1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      db.ProjectNone,
		RoleID:    &role.ID,
		Revision:  1,
	})
	require.NoError(t, err)

	err = store.DeleteProjectRole(project.ID, role.ID, role.Revision)
	assert.ErrorIs(t, err, db.ErrProjectRoleAssigned)

	require.NoError(t, store.DeleteProjectUser(project.ID, user.ID))
	err = store.DeleteProjectRole(project.ID, role.ID, role.Revision+1)
	assert.ErrorIs(t, err, db.ErrProjectRoleRevisionConflict)
	require.NoError(t, store.DeleteProjectRole(project.ID, role.ID, role.Revision))
	_, err = store.GetProjectRoleByID(project.ID, role.ID)
	assert.True(t, errors.Is(err, db.ErrNotFound))
}
