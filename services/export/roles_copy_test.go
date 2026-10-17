package export

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyKeepsRolesMembershipsAndGrants(t *testing.T) {
	src := sql.InitConfigCreateTestStore()

	user, err := src.CreateUser(db.UserWithPwd{Pwd: "3412341234123", User: db.User{Username: "u", Name: "u", Email: "u@example.com"}})
	require.NoError(t, err)
	proj, err := src.CreateProject(db.Project{Name: "P"})
	require.NoError(t, err)
	key, err := src.CreateAccessKey(db.AccessKey{ProjectID: &proj.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := src.CreateRepository(db.Repository{ProjectID: proj.ID, SSHKeyID: key.ID, Name: "Repo", GitURL: "git@example.com:t/t", GitBranch: "master"})
	require.NoError(t, err)
	inv, err := src.CreateInventory(db.Inventory{ProjectID: proj.ID})
	require.NoError(t, err)
	tpl, err := src.CreateTemplate(db.Template{Name: "Tpl", Playbook: "t.yml", ProjectID: proj.ID, RepositoryID: repo.ID, InventoryID: &inv.ID})
	require.NoError(t, err)

	local, err := src.CreateProjectRole(db.Role{ID: "role_export_local", Name: "Local", ProjectID: &proj.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	global, err := src.CreateGlobalRole(db.Role{ID: "role_export_deployer", Name: "Deployer", Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	legacy, err := src.CreateGlobalRole(db.Role{ID: "role_export_legacy", Name: "Legacy", Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	manager, err := src.GetGlobalRoleBySlug("manager")
	require.NoError(t, err)

	membership, err := src.CreateProjectUser(db.ProjectUser{ProjectID: proj.ID, UserID: user.ID, Role: db.ProjectManager, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, src.SetProjectUserRoleRefs(proj.ID, user.ID, []db.ProjectRoleID{manager.ID, local.ID, global.ID}, membership.Revision))
	for _, id := range []db.ProjectRoleID{local.ID, manager.ID} {
		roleID := id
		_, err = src.CreateTemplateRole(db.TemplateRolePerm{ProjectID: proj.ID, TemplateID: tpl.ID, RoleID: &roleID, Permissions: db.CanRunProjectTasks})
		require.NoError(t, err)
	}
	// This is the pre-immutable-ID representation for a global grant.
	_, err = src.CreateTemplateRole(db.TemplateRolePerm{ProjectID: proj.ID, TemplateID: tpl.ID, RoleSlug: legacy.Slug, Permissions: db.CanRunProjectTasks})
	require.NoError(t, err)

	dst := sql.InitConfigCreateTestStore()
	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.Load(src))
	require.NoError(t, chain.Restore(dst, 10))

	projects, err := dst.GetAllProjects()
	require.NoError(t, err)
	require.Len(t, projects, 1)
	users, err := dst.GetProjectUsers(projects[0].ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, users, 1)
	var roleIDs []db.ProjectRoleID
	for _, r := range users[0].Roles {
		roleIDs = append(roleIDs, r.ID)
	}
	destinationRoles, err := dst.GetProjectRoles(projects[0].ID)
	require.NoError(t, err)
	require.Len(t, destinationRoles, 1)
	destinationLocal := destinationRoles[0]
	destinationGlobals, err := dst.GetGlobalRoles()
	require.NoError(t, err)
	var destinationGlobal db.Role
	var kept db.Role
	for _, candidate := range destinationGlobals {
		switch candidate.Name {
		case global.Name:
			destinationGlobal = candidate
		case legacy.Name:
			kept = candidate
		}
	}
	require.NotZero(t, destinationGlobal.ID)
	require.NotZero(t, kept.ID)
	assert.NotEqual(t, local.ID, destinationLocal.ID)
	assert.NotEqual(t, global.ID, destinationGlobal.ID)
	assert.ElementsMatch(t, []db.ProjectRoleID{manager.ID, destinationLocal.ID, destinationGlobal.ID}, roleIDs)

	assert.Equal(t, "Legacy", kept.Name)
	assert.False(t, kept.Builtin)
	assert.NotEqual(t, legacy.ID, kept.ID)

	templates, err := dst.GetTemplates(projects[0].ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	grants, err := dst.GetTemplateRoles(projects[0].ID, templates[0].ID)
	require.NoError(t, err)
	var grantRoleIDs []db.ProjectRoleID
	for _, g := range grants {
		require.NotNil(t, g.RoleID)
		grantRoleIDs = append(grantRoleIDs, *g.RoleID)
	}
	assert.ElementsMatch(t, []db.ProjectRoleID{destinationLocal.ID, kept.ID, manager.ID}, grantRoleIDs)
}

func TestCopyIntoSameDatabaseRegeneratesProjectRoleIDs(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "Same database source"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "same-db-source-role", Name: "Same database source role", ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)
	copied, err := store.CreateProject(db.Project{Name: "Same database destination"})
	require.NoError(t, err)
	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.mapKeys(Project, GlobalScope, NewKeyFromInt(*role.ProjectID), copied.GetDbKey()))
	exporter := &RoleExporter{}
	require.NoError(t, exporter.restoreValue(EntityObject[db.Role]{value: role, scope: GlobalScope}, store, chain))
	mappedID, err := chain.getNewKey(Role, GlobalScope, role.GetDbKey())
	require.NoError(t, err)
	copiedRoles, err := store.GetProjectRoles(copied.ID)
	require.NoError(t, err)
	require.Len(t, copiedRoles, 1)
	assert.NotEqual(t, role.ID, copiedRoles[0].ID)
	assert.Equal(t, string(copiedRoles[0].ID), string(mappedID))
}

func TestRestoreLegacySlugOnlyTemplateGrantsUsesExportedRoleIdentities(t *testing.T) {
	source := sql.InitConfigCreateTestStore()
	t.Cleanup(source.Close)
	project, err := source.CreateProject(db.Project{Name: "Legacy grant source"})
	require.NoError(t, err)
	key, err := source.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := source.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "Repository", GitURL: "git@example.test:legacy/grant", GitBranch: "main"})
	require.NoError(t, err)
	template, err := source.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "Template", Playbook: "site.yml"})
	require.NoError(t, err)
	global, err := source.CreateGlobalRole(db.Role{ID: "legacy-import-global", Slug: "legacy-import-global", Name: "Legacy import global", Revision: 1})
	require.NoError(t, err)
	manager, err := source.GetGlobalRoleBySlug(string(db.ProjectManager))
	require.NoError(t, err)
	for _, roleID := range []db.ProjectRoleID{global.ID, manager.ID} {
		id := roleID
		_, err = source.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleID: &id, Permissions: db.CanRunProjectTasks, Revision: 1})
		require.NoError(t, err)
	}

	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.Load(source))
	legacyTemplateRoles := chain.getTypeExporter(TemplateRole).(*TemplateRoleExporter)
	for i := range legacyTemplateRoles.values {
		legacyTemplateRoles.values[i].value.RoleID = nil
	}

	destination := sql.InitConfigCreateTestStore()
	t.Cleanup(destination.Close)
	require.NoError(t, chain.Restore(destination, 10))
	projects, err := destination.GetAllProjects()
	require.NoError(t, err)
	require.Len(t, projects, 1)
	templates, err := destination.GetTemplates(projects[0].ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	grants, err := destination.GetTemplateRoles(projects[0].ID, templates[0].ID)
	require.NoError(t, err)
	require.Len(t, grants, 2)

	var restoredIDs []db.ProjectRoleID
	for _, grant := range grants {
		require.NotNil(t, grant.RoleID)
		restoredIDs = append(restoredIDs, *grant.RoleID)
	}
	destinationGlobals, err := destination.GetGlobalRoles()
	require.NoError(t, err)
	var restoredGlobal db.Role
	for _, candidate := range destinationGlobals {
		if candidate.Name == global.Name {
			restoredGlobal = candidate
			break
		}
	}
	require.NotZero(t, restoredGlobal.ID)
	assert.NotEqual(t, global.ID, restoredGlobal.ID)
	assert.ElementsMatch(t, []db.ProjectRoleID{manager.ID, restoredGlobal.ID}, restoredIDs)
}

func TestRestoreLegacySlugOnlyTemplateGrantsKeepsCaseDistinctRoleAliases(t *testing.T) {
	source := sql.InitConfigCreateTestStore()
	t.Cleanup(source.Close)
	project, err := source.CreateProject(db.Project{Name: "Case-sensitive legacy roles"})
	require.NoError(t, err)
	key, err := source.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := source.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "Repository", GitURL: "git@example.test:legacy/case", GitBranch: "main"})
	require.NoError(t, err)
	template, err := source.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "Template", Playbook: "site.yml"})
	require.NoError(t, err)
	for _, role := range []db.Role{
		{ID: "legacy-case-upper", Slug: "Deploy", Name: "Upper deploy", Revision: 1},
		{ID: "legacy-case-lower", Slug: "deploy", Name: "Lower deploy", Revision: 1},
	} {
		_, err = source.Sql().Exec(source.PrepareQuery(`insert into role (role_id, slug, name, permissions, global_permissions, project_id, revision)
			values (?, ?, ?, ?, ?, ?, ?)`), role.ID, role.Slug, role.Name, role.Permissions, role.GlobalPermissions, role.ProjectID, role.Revision)
		require.NoError(t, err)
		id := role.ID
		_, err = source.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleID: &id, Permissions: db.CanRunProjectTasks, Revision: 1})
		require.NoError(t, err)
	}

	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.Load(source))
	legacyTemplateRoles := chain.getTypeExporter(TemplateRole).(*TemplateRoleExporter)
	for i := range legacyTemplateRoles.values {
		legacyTemplateRoles.values[i].value.RoleID = nil
	}

	destination := sql.InitConfigCreateTestStore()
	t.Cleanup(destination.Close)
	require.NoError(t, chain.Restore(destination, 10))
	projects, err := destination.GetAllProjects()
	require.NoError(t, err)
	require.Len(t, projects, 1)
	templates, err := destination.GetTemplates(projects[0].ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	grants, err := destination.GetTemplateRoles(projects[0].ID, templates[0].ID)
	require.NoError(t, err)
	require.Len(t, grants, 2)

	roles, err := destination.GetGlobalRoles()
	require.NoError(t, err)
	restoredByName := make(map[string]db.ProjectRoleID)
	for _, role := range roles {
		if role.Name == "Upper deploy" || role.Name == "Lower deploy" {
			restoredByName[role.Name] = role.ID
		}
	}
	require.Len(t, restoredByName, 2)
	var restoredGrantIDs []db.ProjectRoleID
	for _, grant := range grants {
		require.NotNil(t, grant.RoleID)
		restoredGrantIDs = append(restoredGrantIDs, *grant.RoleID)
	}
	assert.ElementsMatch(t, []db.ProjectRoleID{restoredByName["Upper deploy"], restoredByName["Lower deploy"]}, restoredGrantIDs)
}

func TestRestoreLegacySlugOnlyTemplateGrantRejectsEmptySlugWithoutAliasLookup(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	mapper := NewKeyMapper()
	require.NoError(t, mapper.mapKeys(Project, GlobalScope, "42", "1"))
	require.NoError(t, mapper.mapKeys(Template, "42", "7", "1"))
	// An empty-key alias must never be registered or consumed.
	require.NoError(t, mapper.mapKeys(legacyRoleSlugNamespace, "42", "", "builtin:guest"))

	exporter := &TemplateRoleExporter{}
	err := exporter.restoreValue(EntityObject[db.TemplateRolePerm]{
		value: db.TemplateRolePerm{ProjectID: 42, TemplateID: 7}, scope: "42",
	}, store, &ExporterChain{KeyMapper: mapper})
	require.ErrorContains(t, err, "has no slug")
}

func TestLegacyRoleSlugAliasesUseUniqueFoldedFallbackWithoutOverwritingExactRoles(t *testing.T) {
	mapper := NewKeyMapper()
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "Deploy", "upper"))

	roleID, err := legacyRoleSlugAlias(mapper, 42, "deploy")
	require.NoError(t, err)
	assert.Equal(t, EntityKey("upper"), roleID, "a historical case-folded template slug resolves only when unique")

	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "deploy", "lower"))
	roleID, err = legacyRoleSlugAlias(mapper, 42, "Deploy")
	require.NoError(t, err)
	assert.Equal(t, EntityKey("upper"), roleID, "exact aliases retain the upper-case source identity")
	roleID, err = legacyRoleSlugAlias(mapper, 42, "deploy")
	require.NoError(t, err)
	assert.Equal(t, EntityKey("lower"), roleID, "exact aliases retain the lower-case source identity")
	_, err = legacyRoleSlugAlias(mapper, 42, "DEPLOY")
	require.ErrorContains(t, err, "ambiguous")

	require.NoError(t, mapLegacyRoleSlugAliases(mapper, GlobalScope, "DEPLOY", "global"))
	roleID, err = legacyRoleSlugAlias(mapper, 42, "DEPLOY")
	require.NoError(t, err)
	assert.Equal(t, EntityKey("global"), roleID, "an exact global alias takes precedence over an ambiguous folded project alias")
}

func TestLegacyRoleSlugAliasesDoNotRegisterEmptySlugs(t *testing.T) {
	mapper := NewKeyMapper()
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "", "role"))
	_, err := mapper.getNewKey(legacyRoleSlugNamespace, "42", "")
	require.Error(t, err)
	_, err = mapper.getNewKey(legacyRoleSlugFoldedNamespace, "42", "")
	require.Error(t, err)
}

func TestLegacyAliasRoutingUsesExactGlobalBeforeFoldedProject(t *testing.T) {
	mapper := NewKeyMapper()
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "Deploy", "project"))
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, GlobalScope, "deploy", "global"))

	roleID, err := legacyRoleSlugAlias(mapper, 42, "deploy")
	require.NoError(t, err)
	assert.Equal(t, EntityKey("global"), roleID)
}

func TestLegacyAliasAmbiguityDoesNotFallbackToDestinationGlobalRole(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "alias routing"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "repository", GitURL: "git@example.test:alias/routing", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "template", Playbook: "site.yml"})
	require.NoError(t, err)
	_, err = store.CreateGlobalRole(db.Role{ID: "destination-global", Slug: "DEPLOY", Name: "Destination global", Revision: 1})
	require.NoError(t, err)

	mapper := NewKeyMapper()
	require.NoError(t, mapper.mapKeys(Project, GlobalScope, "42", NewKeyFromInt(project.ID)))
	require.NoError(t, mapper.mapKeys(Template, "42", "7", NewKeyFromInt(template.ID)))
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "Deploy", "first"))
	require.NoError(t, mapLegacyRoleSlugAliases(mapper, "42", "deploy", "second"))

	exporter := &TemplateRoleExporter{}
	err = exporter.restoreValue(EntityObject[db.TemplateRolePerm]{
		value: db.TemplateRolePerm{ProjectID: 42, TemplateID: 7, RoleSlug: "DEPLOY", Revision: 1}, scope: "42",
	}, store, &ExporterChain{KeyMapper: mapper})
	require.ErrorContains(t, err, "ambiguous")
	grants, err := store.GetTemplateRoles(project.ID, template.ID)
	require.NoError(t, err)
	assert.Empty(t, grants)
}
