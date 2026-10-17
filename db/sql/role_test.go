package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProjectRole(t *testing.T) (*SqlDb, db.Project, db.Role) {
	t.Helper()
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "role-owner"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_owner_ops", Slug: "ops", Name: "Ops",
		Permissions: db.CanRunProjectTasks, ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)
	return store, project, role
}

func TestGetProjectOrGlobalRoleBySlug_IgnoresCase(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "proj"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "Deployer", Name: "D", ProjectID: &project.ID})
	require.NoError(t, err)

	role, err := store.GetProjectOrGlobalRoleBySlug(project.ID, "deployer")

	require.NoError(t, err)
	assert.Equal(t, "Deployer", role.Slug)
}

func TestGetProjectOrGlobalRoleBySlug_ProjectRoleWins(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "proj"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	projectRole, err := store.CreateProjectRole(db.Role{ID: "project_ci", Slug: "project-ci", Name: "CI", ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)
	otherRole, err := store.CreateProjectRole(db.Role{ID: "other_ci", Slug: "other-ci", Name: "CI", ProjectID: &other.ID, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateProjectRole(db.Role{ID: "duplicate_ci", Slug: projectRole.Slug, Name: "Duplicate", ProjectID: &other.ID, Revision: 1})
	assert.Error(t, err, "legacy slugs remain globally unique")

	role, err := store.GetProjectRoleByID(project.ID, projectRole.ID)

	require.NoError(t, err)
	assert.Equal(t, projectRole.ID, role.ID)
	_, err = store.GetProjectRoleByID(project.ID, otherRole.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestGetProjectRole_IgnoresGlobalAndOtherProjects(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "proj"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "global", Name: "G"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "foreign", Name: "F", ProjectID: &other.ID})
	require.NoError(t, err)

	_, err = store.GetProjectRole(project.ID, "global")
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetProjectRole(project.ID, "foreign")
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetProjectOrGlobalRoleBySlug(project.ID, "foreign")
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestGetGlobalRoles_IncludesBuiltins(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "proj"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "auditor", Name: "Auditor"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "local", Name: "Local", ProjectID: &project.ID})
	require.NoError(t, err)

	globals, err := store.GetGlobalRoles()
	require.NoError(t, err)
	projectRoles, err := store.GetProjectRoles(project.ID)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"auditor", "guest", "manager", "owner", "task_runner"}, slugsOf(globals))
	assert.Equal(t, []string{"local"}, slugsOf(projectRoles))
}

func TestCreateRole_SlugScopes(t *testing.T) {
	store := InitConfigCreateTestStore()
	p1, err := store.CreateProject(db.Project{Name: "p1"})
	require.NoError(t, err)
	p2, err := store.CreateProject(db.Project{Name: "p2"})
	require.NoError(t, err)

	_, err = store.CreateRole(db.Role{ID: "p1_dev", Slug: "dev", Name: "Dev", ProjectID: &p1.ID})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{ID: "p2_dev", Slug: "dev", Name: "Dev", ProjectID: &p2.ID})
	assert.Error(t, err, "role slugs are globally unique")

	_, err = store.CreateRole(db.Role{Slug: "DEV", Name: "Dev", ProjectID: &p1.ID})
	assert.ErrorContains(t, err, "Role slug is taken")
	_, err = store.CreateRole(db.Role{Slug: "dev", Name: "Dev"})
	assert.Error(t, err, "a global slug used by a project")

	_, err = store.CreateRole(db.Role{Slug: "auditor", Name: "A"})
	require.NoError(t, err)
	_, err = store.CreateRole(db.Role{Slug: "Auditor", Name: "A", ProjectID: &p1.ID})
	assert.ErrorContains(t, err, "Role slug is taken", "a project slug used by a global role")
	_, err = store.CreateRole(db.Role{Slug: "AUDITOR", Name: "A"})
	assert.ErrorContains(t, err, "Role slug is taken", "two global roles")
}

func TestUpdateRole_ChangesNameAndPermissionsOnly(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "proj"})
	require.NoError(t, err)
	role, err := store.CreateRole(db.Role{Slug: "ops", Name: "Ops", Permissions: db.CanRunProjectTasks, ProjectID: &project.ID})
	require.NoError(t, err)

	require.NoError(t, store.UpdateRole(db.Role{ID: role.ID, Slug: "other", Name: "Changed", Permissions: db.CanUpdateProject, Revision: role.Revision}))

	got, err := store.GetProjectRole(project.ID, "ops")
	require.NoError(t, err)
	assert.Equal(t, "Changed", got.Name)
	assert.Equal(t, db.CanUpdateProject, got.Permissions)
}

func TestUpdateRole_GlobalScopeDoesNotTouchProjectRole(t *testing.T) {
	store, project, role := setupProjectRole(t)
	role.Name = "Changed"

	_, err := store.UpdateGlobalRole(role, role.Revision)
	assert.ErrorIs(t, err, db.ErrNotFound)
	stored, err := store.GetProjectRoleByID(project.ID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, "Ops", stored.Name)
	assert.Equal(t, db.CanRunProjectTasks, stored.Permissions)
}

func TestUpdateRole_OtherProjectIsNotFound(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "owner"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{
		ID: "owner_deployer", Slug: "deployer", Name: "Deployer",
		Permissions: db.CanRunProjectTasks, ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)

	forged := role
	forged.ProjectID = &otherProject.ID
	forged.Name = "Forged update"
	_, err = store.UpdateProjectRole(otherProject.ID, forged, role.Revision)
	assert.ErrorIs(t, err, db.ErrNotFound)
	stored, err := store.GetProjectRoleByID(project.ID, role.ID)
	require.NoError(t, err)
	assert.Equal(t, "Deployer", stored.Name)
	assert.Equal(t, role.Revision, stored.Revision)
}

func TestUpdateRole_SameScopeIsUpdated(t *testing.T) {
	store, project, role := setupProjectRole(t)
	role.Name = "Changed"

	updated, err := store.UpdateProjectRole(project.ID, role, role.Revision)
	require.NoError(t, err)
	assert.Equal(t, role.Revision+1, updated.Revision)
	assert.Equal(t, "Changed", updated.Name)
}

func TestUpdateRole_IdenticalValuesSucceed(t *testing.T) {
	store, project, role := setupProjectRole(t)
	_, err := store.UpdateProjectRole(project.ID, role, role.Revision)
	require.NoError(t, err)

	global, err := store.CreateGlobalRole(db.Role{ID: "role_global_viewer", Slug: "viewer", Name: "Viewer", Revision: 1})
	require.NoError(t, err)
	_, err = store.UpdateGlobalRole(global, global.Revision)
	require.NoError(t, err)
}

func TestUpdateRole_MissingIsNotFound(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "missing"})
	require.NoError(t, err)
	missingProjectRole := db.Role{ID: "missing_project_role", Slug: "missing", Name: "Missing", ProjectID: &project.ID, Revision: 1}
	_, err = store.UpdateProjectRole(project.ID, missingProjectRole, missingProjectRole.Revision)
	assert.ErrorIs(t, err, db.ErrNotFound)
	missingGlobalRole := db.Role{ID: "missing_global_role", Slug: "missing-global", Name: "Missing", Revision: 1}
	_, err = store.UpdateGlobalRole(missingGlobalRole, missingGlobalRole.Revision)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestBuiltinRolesCannotChange(t *testing.T) {
	store := InitConfigCreateTestStore()
	owner, err := store.GetGlobalRoleBySlug("owner")
	require.NoError(t, err)

	assert.ErrorIs(t, store.UpdateRole(db.Role{ID: owner.ID, Name: "Changed", Permissions: 0, Revision: owner.Revision}), db.ErrProjectRoleRevisionConflict)
	assert.ErrorIs(t, store.DeleteRole(owner.ID), db.ErrNotFound)

	got, err := store.GetGlobalRoleBySlug("owner")
	require.NoError(t, err)
	assert.Equal(t, owner, got)
}

func TestDeleteRole(t *testing.T) {
	store := InitConfigCreateTestStore()
	role, err := store.CreateRole(db.Role{Slug: "custom", Name: "Custom"})
	require.NoError(t, err)

	require.NoError(t, store.DeleteRole(role.ID))

	_, err = store.GetGlobalRoleBySlug("custom")
	assert.ErrorIs(t, err, db.ErrNotFound)
	assert.ErrorIs(t, store.DeleteRole(role.ID), db.ErrNotFound)
}

func TestDeleteRole_GlobalScopeDoesNotTouchProjectRole(t *testing.T) {
	store, project, role := setupProjectRole(t)

	assert.ErrorIs(t, store.DeleteGlobalRole(role.ID, role.Revision), db.ErrNotFound)
	_, err := store.GetProjectRoleByID(project.ID, role.ID)
	assert.NoError(t, err)
}

func TestDeleteRole_OtherProjectIsNotFound(t *testing.T) {
	store, project, role := setupProjectRole(t)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)

	assert.ErrorIs(t, store.DeleteProjectRole(other.ID, role.ID, role.Revision), db.ErrNotFound)
	_, err = store.GetProjectRoleByID(project.ID, role.ID)
	assert.NoError(t, err)
}

func TestDeleteRole_SameScopeIsDeleted(t *testing.T) {
	store, project, role := setupProjectRole(t)
	require.NoError(t, store.DeleteProjectRole(project.ID, role.ID, role.Revision))
	_, err := store.GetProjectRoleByID(project.ID, role.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	global, err := store.CreateGlobalRole(db.Role{ID: "role_global_viewer", Slug: "viewer", Name: "Viewer", Revision: 1})
	require.NoError(t, err)
	require.NoError(t, store.DeleteGlobalRole(global.ID, global.Revision))
}

func TestDeleteRole_MissingIsNotFound(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "missing"})
	require.NoError(t, err)
	assert.ErrorIs(t, store.DeleteProjectRole(project.ID, "missing_project_role", 1), db.ErrNotFound)
	assert.ErrorIs(t, store.DeleteGlobalRole("missing_global_role", 1), db.ErrNotFound)
}

func TestProjectRoleAssignmentsUseStableIDsAndBlockDeletion(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_assigned_deployer", Name: "Deployer", ProjectID: &projectID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	guest, err := store.GetGlobalRoleBySlug("guest")
	require.NoError(t, err)
	createTemplatePermissionTestUser(t, store, projectID, "only", role)
	both := createTemplatePermissionTestUser(t, store, projectID, "both", role)
	membership, err := store.GetProjectUser(projectID, both.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, both.ID, []db.ProjectRoleID{role.ID, guest.ID}, membership.Revision))
	grant, err := store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: templateID, RoleID: &role.ID, Permissions: db.CanRunProjectTasks})
	require.NoError(t, err)
	check := func(assigned db.RoleAssignments) {
		t.Helper()
		require.Len(t, assigned.Members, 2)
		assert.Equal(t, "only", assigned.Members[0].Username)
		assert.Equal(t, []string{"role_assigned_deployer"}, slugsOf(assigned.Members[0].Roles))
		assert.Equal(t, "both", assigned.Members[1].Username)
		assert.ElementsMatch(t, []string{"role_assigned_deployer", "guest"}, slugsOf(assigned.Members[1].Roles))
		require.Len(t, assigned.Grants, 1)
		assert.Equal(t, grant.ID, assigned.Grants[0].ID)
		assert.Equal(t, db.RoleImpact{Members: 2, Projects: 1, TemplateGrants: 1}, assigned.Impact())
	}

	assigned, err := store.GetRoleAssignments(role.ID)
	require.NoError(t, err)
	check(assigned)
	assert.ErrorIs(t, store.DeleteProjectRole(projectID, role.ID, role.Revision), db.ErrProjectRoleAssigned)
	_, err = store.GetProjectRoleByID(projectID, role.ID)
	require.NoError(t, err, "a failed delete preserves the stable role")
}
