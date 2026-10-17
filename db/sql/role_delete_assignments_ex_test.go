package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForceDeleteProjectRoleRemovesManualAssignmentsAndGrants(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	role, err := store.CreateProjectRole(db.Role{ID: "force_manual", Name: "Force manual", Permissions: db.CanRunProjectTasks, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "force-manual", role)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: templateID, RoleID: &role.ID, Permissions: db.CanRunProjectTasks})
	require.NoError(t, err)

	impact, err := store.ForceDeleteProjectRole(projectID, role.ID, role.Revision)
	require.NoError(t, err)
	assert.Equal(t, db.RoleImpact{Members: 1, Projects: 1, TemplateGrants: 1}, impact)
	_, err = store.GetProjectRoleByID(projectID, role.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetProjectUser(projectID, user.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestForceDeleteProjectRoleRejectsDirectoryProvenance(t *testing.T) {
	store, projectID, _ := newTemplatePermissionTest(t)
	role, err := store.CreateProjectRole(db.Role{ID: "force_protected", Name: "Force protected", Permissions: db.CanRunProjectTasks, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "force-protected", role)
	_, err = store.exec("update project__user_role set ldap_group_managed_assignment_id=? where project_id=? and user_id=? and role_id=?", 77, projectID, user.ID, role.ID)
	require.NoError(t, err)

	_, err = store.ForceDeleteProjectRole(projectID, role.ID, role.Revision)
	assert.ErrorIs(t, err, db.ErrProjectRoleForceProtected)
	_, err = store.GetProjectRoleByID(projectID, role.ID)
	assert.NoError(t, err)
}

func TestForceDeleteProjectRoleReprojectsRemainingMembershipAndFencesCAS(t *testing.T) {
	store, projectID, _ := newTemplatePermissionTest(t)
	deleted, err := store.CreateProjectRole(db.Role{ID: "force_deleted", Name: "Deleted", Permissions: db.CanRunProjectTasks, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	remaining, err := store.CreateProjectRole(db.Role{ID: "force_remaining", Name: "Remaining", Permissions: db.CanViewProjectResources, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "force-remaining", deleted)
	membership, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, user.ID, []db.ProjectRoleID{deleted.ID, remaining.ID}, membership.Revision))
	membership, err = store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)

	_, err = store.ForceDeleteProjectRole(projectID, deleted.ID, deleted.Revision)
	require.NoError(t, err)
	got, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, got.RoleID)
	assert.Equal(t, remaining.ID, *got.RoleID)
	assert.Equal(t, db.ProjectNone, got.Role)
	assert.Equal(t, membership.Revision+1, got.Revision)
	roles, err := store.GetProjectUserRoles(projectID, user.ID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, remaining.ID, roles[0].ID)
}

func TestForceDeleteProjectRoleReprojectsBuiltinSurvivorForLegacyUpdate(t *testing.T) {
	store, projectID, _ := newTemplatePermissionTest(t)
	deleted, err := store.CreateProjectRole(db.Role{ID: "force_builtin_deleted", Name: "Deleted", Permissions: db.CanRunProjectTasks, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	manager, err := store.GetGlobalRoleBySlug("manager")
	require.NoError(t, err)
	owner, err := store.GetGlobalRoleBySlug("owner")
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "force-builtin", deleted)
	createTemplatePermissionTestUser(t, store, projectID, "force-builtin-owner", owner)
	membership, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, user.ID, []db.ProjectRoleID{deleted.ID, manager.ID}, membership.Revision))
	membership, err = store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)

	_, err = store.ForceDeleteProjectRole(projectID, deleted.ID, deleted.Revision)
	require.NoError(t, err)
	got, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectManager, got.Role)
	assert.Nil(t, got.RoleID)
	assert.Equal(t, membership.Revision+1, got.Revision)

	got.Role = db.ProjectGuest
	require.NoError(t, store.UpdateProjectUser(got))
	updated, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectGuest, updated.Role)
	assert.Nil(t, updated.RoleID)
}

func TestForceDeleteProjectRoleRetainsLastEffectiveAdministrator(t *testing.T) {
	store, projectID, _ := newTemplatePermissionTest(t)
	role, err := store.CreateProjectRole(db.Role{ID: "force_admin", Name: "Force admin", Permissions: db.CanManageProjectUsers, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	createTemplatePermissionTestUser(t, store, projectID, "force-admin", role)

	_, err = store.ForceDeleteProjectRole(projectID, role.ID, role.Revision)
	assert.ErrorIs(t, err, db.ErrLastProjectAdministrator)
	_, err = store.GetProjectRoleByID(projectID, role.ID)
	assert.NoError(t, err)
}

func TestDeleteProjectRoleRejectsSecondaryRoleAssignment(t *testing.T) {
	store, projectID, _ := newTemplatePermissionTest(t)
	role, err := store.CreateProjectRole(db.Role{ID: "secondary_delete_guard", Name: "Secondary", Permissions: db.CanRunProjectTasks, ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	guest, err := store.GetGlobalRoleBySlug("guest")
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "secondary-delete", role)
	membership, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, user.ID, []db.ProjectRoleID{guest.ID, role.ID}, membership.Revision))

	err = store.DeleteProjectRole(projectID, role.ID, role.Revision)
	assert.ErrorIs(t, err, db.ErrProjectRoleAssigned)
}
