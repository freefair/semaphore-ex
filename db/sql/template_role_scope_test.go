package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateRoleOverrideSupportsDenyCASAndReadFiltering(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID,
		Name: "scoped-template", Playbook: "site.yml",
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "template-scope-user", Name: "Template scope user",
		Email: "template-scope@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: projectID, UserID: user.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)

	override, err := store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: projectID, TemplateID: template.ID, RoleSlug: string(db.ProjectGuest),
		AllowedPermissions: db.CanRunTemplate,
		DeniedPermissions:  db.CanReadTemplate,
		Revision:           1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, override.Revision)

	context, err := store.GetTemplatePermissionContext(projectID, template.ID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, context.Override)
	assert.Equal(t, db.CanViewProjectResources, context.ProjectPermissions)
	assert.Equal(t, db.CanRunTemplate, context.EffectivePermissions)

	legacy, err := store.GetTemplatePermission(projectID, template.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, legacy.Can(db.CanRunProjectTasks))
	assert.False(t, legacy.Can(db.CanViewProjectResources))

	listed, err := store.GetTemplatesWithPermissions(
		projectID, user.ID, db.TemplateFilter{}, db.RetrieveQueryParams{},
	)
	require.NoError(t, err)
	assert.Empty(t, listed, "explicit read deny removes the template from list results")

	override.AllowedPermissions = db.CanReadTemplate
	override.DeniedPermissions = db.CanRunTemplate
	updated, err := store.UpdateTemplateRole(override, override.Revision)
	require.NoError(t, err)
	assert.Equal(t, override.Revision+1, updated.Revision)
	_, err = store.UpdateTemplateRole(override, override.Revision)
	assert.ErrorIs(t, err, db.ErrTemplateRoleRevisionConflict)

	listed, err = store.GetTemplatesWithPermissions(
		projectID, user.ID, db.TemplateFilter{}, db.RetrieveQueryParams{},
	)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].Permissions)
	assert.True(t, listed[0].Permissions.Can(db.CanViewProjectResources))
	assert.False(t, listed[0].Permissions.Can(db.CanRunProjectTasks))

	assert.ErrorIs(t,
		store.DeleteTemplateRole(projectID, template.ID, updated.ID, 0),
		db.ErrTemplateRoleRevisionConflict,
	)
	require.NoError(t,
		store.DeleteTemplateRole(projectID, template.ID, updated.ID, updated.Revision),
	)
}
