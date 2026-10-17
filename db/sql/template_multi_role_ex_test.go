package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplatePermissionContextUnionsCurrentRolesAndRevokesRemovedDenies(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	t.Cleanup(store.Close)
	guest, err := store.GetGlobalRoleBySlug("guest")
	require.NoError(t, err)
	runner, err := store.CreateProjectRole(db.Role{
		ID: "role_template_multi_runner", Name: "Runner", ProjectID: &projectID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	user := createTemplatePermissionTestUser(t, store, projectID, "template-multi-role", guest)
	membership, err := store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, user.ID, []db.ProjectRoleID{guest.ID, runner.ID}, membership.Revision))

	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: templateID, RoleID: &runner.ID,
		AllowedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: templateID, RoleSlug: string(db.ProjectGuest),
		DeniedPermissions: db.CanRunTemplate, Revision: 1,
	})
	require.NoError(t, err)

	context, err := store.GetTemplatePermissionContext(projectID, templateID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CanReadTemplate, context.EffectivePermissions,
		"a deny from any current role must override a base grant from another role")

	membership, err = store.GetProjectUser(projectID, user.ID)
	require.NoError(t, err)
	require.NoError(t, store.SetProjectUserRoleRefs(projectID, user.ID, []db.ProjectRoleID{runner.ID}, membership.Revision))
	context, err = store.GetTemplatePermissionContext(projectID, templateID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CanReadTemplate|db.CanRunTemplate, context.EffectivePermissions,
		"revoking the denying role must remove its override without losing another current role's base grant")
	listed, err := store.GetTemplatesWithPermissions(projectID, user.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, listed, 1, "list visibility must use the same multi-role context")
}

func TestTemplatePermissionContextLegacyPositiveMasksDoNotWidenUpdateProject(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	t.Cleanup(store.Close)
	first, err := store.CreateProjectRole(db.Role{ID: "role_template_legacy_first", Name: "First", ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	second, err := store.CreateProjectRole(db.Role{ID: "role_template_legacy_second", Name: "Second", ProjectID: &projectID, Revision: 1})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "template-legacy-roles", Name: "Template legacy roles", Email: "template-legacy-roles@example.test"})
	require.NoError(t, err)
	createMembershipWithRoles(t, store, projectID, user.ID, []db.Role{first, second})
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: templateID, RoleID: &first.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: templateID, RoleID: &second.ID, Permissions: db.CanUpdateProject, Revision: 1})
	require.NoError(t, err)

	context, err := store.GetTemplatePermissionContext(projectID, templateID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CanRunTemplate, context.EffectivePermissions)
}

func TestCreateTemplateRoleAcceptsBuiltinRoleReference(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	t.Cleanup(store.Close)
	manager, err := store.GetGlobalRoleBySlug("manager")
	require.NoError(t, err)
	created, err := store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: templateID, RoleID: &manager.ID,
		Permissions: db.CanRunProjectTasks, Revision: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "manager", created.RoleSlug)
}

func TestTemplatePermissionContextDoesNotApplyBuiltinLegacySlugGrantToShadowingCustomRole(t *testing.T) {
	store, projectID, templateID := newTemplatePermissionTest(t)
	t.Cleanup(store.Close)
	_, err := store.Sql().Exec("insert into role (role_id, slug, name, permissions, project_id, revision) values (?, ?, ?, ?, ?, ?)", "role_legacy_manager", "manager", "legacy manager", 0, projectID, 1)
	require.NoError(t, err)
	roleID := db.ProjectRoleID("role_legacy_manager")
	user, err := store.CreateUserWithoutPassword(db.User{Username: "legacy-manager-shadow", Name: "legacy manager shadow", Email: "legacy-manager-shadow@example.test"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, RoleID: &roleID, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: templateID, RoleSlug: string(db.ProjectManager), AllowedPermissions: db.CanRunTemplate, Revision: 1})
	require.NoError(t, err)
	context, err := store.GetTemplatePermissionContext(projectID, templateID, user.ID)
	require.NoError(t, err)
	assert.False(t, context.EffectivePermissions.Can(db.CanRunTemplate))
}
