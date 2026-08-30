package sql

import (
	"errors"
	"sync"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalRoleAssignmentsMergePermissionsUseCASAndProtectLastAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	first, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-role-first", Name: "Global role first", Email: "global-role-first@example.test",
	})
	require.NoError(t, err)
	second, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-role-second", Name: "Global role second", Email: "global-role-second@example.test",
	})
	require.NoError(t, err)

	manager, err := store.CreateGlobalRole(db.Role{
		ID: "global_manager", Slug: "global_manager", Name: "Global manager",
		GlobalPermissions: db.CanManageGlobalRoles | db.CanManageGlobalUsers,
		Revision:          1,
	})
	require.NoError(t, err)
	auditor, err := store.CreateGlobalRole(db.Role{
		ID: "global_auditor", Slug: "global_auditor", Name: "Global auditor",
		GlobalPermissions: db.CanReadGlobalAudit,
		Revision:          1,
	})
	require.NoError(t, err)

	firstManager, err := store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: first.ID, RoleID: manager.ID, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: first.ID, RoleID: auditor.ID, Revision: 1,
	})
	require.NoError(t, err)

	permissions, err := store.GetEffectiveGlobalPermissions(first.ID)
	require.NoError(t, err)
	assert.True(t, permissions.Can(db.CanManageGlobalRoles))
	assert.True(t, permissions.Can(db.CanManageGlobalUsers))
	assert.True(t, permissions.Can(db.CanReadGlobalAudit))

	err = store.DeleteGlobalRoleAssignment(first.ID, firstManager.ID, firstManager.Revision)
	assert.ErrorIs(t, err, db.ErrLastGlobalAdministrator)

	secondManager, err := store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: second.ID, RoleID: manager.ID, Revision: 1,
	})
	require.NoError(t, err)
	require.NoError(t, store.DeleteGlobalRoleAssignment(first.ID, firstManager.ID, firstManager.Revision))
	err = store.DeleteGlobalRoleAssignment(second.ID, secondManager.ID, 0)
	assert.ErrorIs(t, err, db.ErrGlobalRoleAssignmentConflict)

	manager.Name = "Global manager updated"
	updated, err := store.UpdateGlobalRole(manager, manager.Revision)
	require.NoError(t, err)
	assert.Equal(t, manager.Revision+1, updated.Revision)
	_, err = store.UpdateGlobalRole(manager, manager.Revision)
	assert.ErrorIs(t, err, db.ErrGlobalRoleRevisionConflict)

	err = store.DeleteGlobalRole(manager.ID, updated.Revision)
	assert.ErrorIs(t, err, db.ErrGlobalRoleAssigned)
	assert.False(t, errors.Is(err, db.ErrLastGlobalAdministrator))
}

func TestBuiltInAdministratorHasAllGlobalPermissions(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "global-super-admin", Name: "Global super admin",
		Email: "global-super-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	permissions, err := store.GetEffectiveGlobalPermissions(admin.ID)
	require.NoError(t, err)
	assert.True(t, permissions.Can(db.CanManageGlobalUsers|db.CanManageGlobalRoles|
		db.CanManageGlobalSystem|db.CanReadGlobalAudit))
}

func TestUserMutationsProtectLastEffectiveGlobalAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	first, err := store.CreateUserWithoutPassword(db.User{
		Username: "first-built-in-admin", Name: "First built-in admin",
		Email: "first-built-in-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	second, err := store.CreateUserWithoutPassword(db.User{
		Username: "second-built-in-admin", Name: "Second built-in admin",
		Email: "second-built-in-admin@example.test", Admin: true,
	})
	require.NoError(t, err)

	first.Admin = false
	require.NoError(t, store.UpdateUser(db.UserWithPwd{User: first}))
	second.Admin = false
	err = store.UpdateUser(db.UserWithPwd{User: second})
	assert.ErrorIs(t, err, db.ErrLastGlobalAdministrator)

	err = store.DeleteUser(second.ID)
	assert.ErrorIs(t, err, db.ErrLastGlobalAdministrator)

	manager, err := store.CreateGlobalRole(db.Role{
		ID: "delegated_admin", Slug: "delegated_admin", Name: "Delegated admin",
		GlobalPermissions: db.CanManageGlobalRoles, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: first.ID, RoleID: manager.ID, Revision: 1,
	})
	require.NoError(t, err)
	require.NoError(t, store.UpdateUser(db.UserWithPwd{User: second}))
}

func TestConcurrentGlobalAdministratorRevocationRetainsOneAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	store.Sql().Db.SetMaxOpenConns(1)
	first, err := store.CreateUserWithoutPassword(db.User{
		Username: "concurrent-global-admin-one", Name: "Concurrent global admin one",
		Email: "concurrent-global-admin-one@example.test",
	})
	require.NoError(t, err)
	second, err := store.CreateUserWithoutPassword(db.User{
		Username: "concurrent-global-admin-two", Name: "Concurrent global admin two",
		Email: "concurrent-global-admin-two@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "concurrent_global_admin", Slug: "concurrent_global_admin",
		Name: "Concurrent global admin", GlobalPermissions: db.CanManageGlobalRoles,
		Revision: 1,
	})
	require.NoError(t, err)
	firstAssignment, err := store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: first.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)
	secondAssignment, err := store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: second.ID, RoleID: role.ID, Revision: 1,
	})
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, assignment := range []db.GlobalRoleAssignment{firstAssignment, secondAssignment} {
		assignment := assignment
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- store.DeleteGlobalRoleAssignment(
				assignment.UserID, assignment.ID, assignment.Revision,
			)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var succeeded int
	var protected int
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, db.ErrLastGlobalAdministrator):
			protected++
		default:
			require.NoError(t, result)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, protected)
	firstPermissions, err := store.GetEffectiveGlobalPermissions(first.ID)
	require.NoError(t, err)
	secondPermissions, err := store.GetEffectiveGlobalPermissions(second.ID)
	require.NoError(t, err)
	assert.True(t,
		firstPermissions.Can(db.CanManageGlobalRoles) ||
			secondPermissions.Can(db.CanManageGlobalRoles),
	)
}

func TestDeleteGlobalRoleRejectsProjectMemberships(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "Global role membership project"})
	require.NoError(t, err)
	user := createProjectRoleTestUser(t, store, "global-role-project-member")
	role, err := store.CreateGlobalRole(db.Role{
		ID: "global_project_member", Slug: "global_project_member", Name: "Global project member",
		Permissions: db.CanManageProjectUsers, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectUserRole(role.Slug), Revision: 1,
	})
	require.NoError(t, err)

	err = store.DeleteGlobalRole(role.ID, role.Revision)
	assert.ErrorIs(t, err, db.ErrGlobalRoleAssigned)

	_, err = store.GetProjectOrGlobalRoleBySlug(project.ID, role.Slug)
	require.NoError(t, err)
	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectUserRole(role.Slug), membership.Role)
}

func TestUpdateGlobalRoleRetainsAnAdministratorInEveryAffectedProject(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	protectedProject, err := store.CreateProject(db.Project{Name: "Protected global role project"})
	require.NoError(t, err)
	safeProject, err := store.CreateProject(db.Project{Name: "Safe global role project"})
	require.NoError(t, err)
	firstProtectedUser := createProjectRoleTestUser(t, store, "first-protected-global-role-member")
	secondProtectedUser := createProjectRoleTestUser(t, store, "second-protected-global-role-member")
	safeRoleUser := createProjectRoleTestUser(t, store, "safe-global-role-member")
	safeOwner := createProjectRoleTestUser(t, store, "safe-project-owner")
	role, err := store.CreateGlobalRole(db.Role{
		ID: "global_project_administrator", Slug: "global_project_administrator", Name: "Global project administrator",
		Permissions: db.CanManageProjectUsers, Revision: 1,
	})
	require.NoError(t, err)
	for _, membership := range []db.ProjectUser{
		{ProjectID: protectedProject.ID, UserID: firstProtectedUser.ID, Role: db.ProjectUserRole(role.Slug), Revision: 1},
		{ProjectID: protectedProject.ID, UserID: secondProtectedUser.ID, Role: db.ProjectUserRole(role.Slug), Revision: 1},
		{ProjectID: safeProject.ID, UserID: safeRoleUser.ID, Role: db.ProjectUserRole(role.Slug), Revision: 1},
		{ProjectID: safeProject.ID, UserID: safeOwner.ID, Role: db.ProjectOwner, Revision: 1},
	} {
		_, err = store.CreateProjectUser(membership)
		require.NoError(t, err)
	}

	role.Permissions = db.CanViewProjectResources
	_, err = store.UpdateGlobalRole(role, role.Revision)
	assert.ErrorIs(t, err, db.ErrLastProjectAdministrator)

	persisted, err := store.GetGlobalRoleByID(role.ID)
	require.NoError(t, err)
	assert.True(t, persisted.Permissions.Can(db.CanManageProjectUsers))
}

func TestUpdateGlobalRoleAllowsProjectsWithIndependentAdministrators(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "Safe global role reduction project"})
	require.NoError(t, err)
	roleUser := createProjectRoleTestUser(t, store, "safe-global-role-reduction-member")
	owner := createProjectRoleTestUser(t, store, "safe-global-role-reduction-owner")
	role, err := store.CreateGlobalRole(db.Role{
		ID: "safe_global_project_administrator", Slug: "safe_global_project_administrator",
		Name: "Safe global project administrator", Permissions: db.CanManageProjectUsers, Revision: 1,
	})
	require.NoError(t, err)
	for _, membership := range []db.ProjectUser{
		{ProjectID: project.ID, UserID: roleUser.ID, Role: db.ProjectUserRole(role.Slug), Revision: 1},
		{ProjectID: project.ID, UserID: owner.ID, Role: db.ProjectOwner, Revision: 1},
	} {
		_, err = store.CreateProjectUser(membership)
		require.NoError(t, err)
	}

	role.Permissions = db.CanViewProjectResources
	updated, err := store.UpdateGlobalRole(role, role.Revision)
	require.NoError(t, err)
	assert.False(t, updated.Permissions.Can(db.CanManageProjectUsers))
}
