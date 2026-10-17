package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v2.20.12 is upstream-shaped but EX-adapted: it adds the plural relation
// while preserving the existing CAS-protected project__user membership row.
func newProjectUserRoleMigrationStore() *SqlDb {
	before := "2.20.11"
	return InitConfigCreateTestStoreAt(&before)
}

func migrateProjectUserRoles(t *testing.T, store *SqlDb) {
	t.Helper()
	target := "2.20.12"
	require.NoError(t, db.Migrate(store, &target))
}

func createProjectUserRoleMigrationFixture(t *testing.T, store *SqlDb) (db.Project, db.User) {
	t.Helper()
	project, err := store.CreateProject(db.Project{Name: "project user role migration"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "migration-user", Name: "Migration User", Email: "migration-user@example.test",
	})
	require.NoError(t, err)
	return project, user
}

type projectUserRoleRef struct {
	RoleID db.ProjectRoleID `db:"role_id"`
	LDAP   *int             `db:"ldap_group_managed_assignment_id"`
	OIDC   *int             `db:"oidc_group_managed_assignment_id"`
}

func projectUserRoleRefs(t *testing.T, store *SqlDb, projectID, userID int) []projectUserRoleRef {
	t.Helper()
	var refs []projectUserRoleRef
	_, err := store.selectAll(&refs, `
		select role_id, ldap_group_managed_assignment_id, oidc_group_managed_assignment_id
		from project__user_role where project_id=? and user_id=? order by role_id`, projectID, userID)
	require.NoError(t, err)
	return refs
}

func TestMigration_2_20_12CreatesStableRoleReferenceWithoutReplacingMembership(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	created, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectManager, Revision: 7,
	})
	require.NoError(t, err)

	migrateProjectUserRoles(t, store)

	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Role, membership.Role)
	assert.Equal(t, created.Revision, membership.Revision)
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	assert.Equal(t, db.ProjectRoleID("builtin:manager"), refs[0].RoleID)
}

func TestMigration_2_20_12CanonicalizesLegacyBuiltinCase(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	_, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectManager, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.exec("update project__user set role=? where project_id=? and user_id=?", "MANAGER", project.ID, user.ID)
	require.NoError(t, err)

	migrateProjectUserRoles(t, store)

	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.ProjectManager, membership.Role)
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	assert.Equal(t, db.ProjectRoleID("builtin:manager"), refs[0].RoleID)
}

func TestMigration_2_20_12PreservesCustomRoleIdentityAndDirectoryProvenance(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	role, err := store.CreateProjectRole(db.Role{
		ID: "role_migration_provenance", Name: "Provisioner", ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)
	ldapAssignmentID := 41
	created, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectNone, RoleID: &role.ID,
		Revision: 3,
	})
	require.NoError(t, err)
	_, err = store.exec("update project__user set ldap_group_managed_assignment_id=? where project_id=? and user_id=?", ldapAssignmentID, project.ID, user.ID)
	require.NoError(t, err)

	migrateProjectUserRoles(t, store)

	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, membership.RoleID)
	assert.Equal(t, role.ID, *membership.RoleID)
	assert.Equal(t, created.Revision, membership.Revision)
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	assert.Equal(t, role.ID, refs[0].RoleID)
	require.NotNil(t, refs[0].LDAP)
	assert.Equal(t, ldapAssignmentID, *refs[0].LDAP)
	assert.Nil(t, refs[0].OIDC)
}

func TestMigration_2_20_12ResolvesLegacyGlobalRoleSlug(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "role_migration_global", Slug: "migration-global", Name: "Migration global", Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectUserRole(role.Slug), Revision: 2,
	})
	require.NoError(t, err)

	migrateProjectUserRoles(t, store)

	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	assert.Equal(t, role.ID, refs[0].RoleID)
	roles, err := store.GetProjectUserRoles(project.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, role.ID, roles[0].ID)
}

func TestMigration_2_20_12RefusesDanglingCustomIdentityWithoutMutation(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	unknownRoleID := db.ProjectRoleID("role_deleted_before_upgrade")
	_, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.exec("update project__user set role=?, role_id=? where project_id=? and user_id=?", db.ProjectNone, unknownRoleID, project.ID, user.ID)
	require.NoError(t, err)

	target := "2.20.12"
	err = db.Migrate(store, &target)
	require.ErrorContains(t, err, "missing or out-of-scope role ID")
	membership, getErr := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, getErr)
	require.NotNil(t, membership.RoleID)
	assert.Equal(t, unknownRoleID, *membership.RoleID)
	_, queryErr := store.Sql().SelectInt(store.PrepareQuery("select count(*) from project__user_role"))
	assert.Error(t, queryErr)
}

func TestMigration_2_20_12RollbackDropsOnlyPluralRelation(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	created, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectTaskRunner, Revision: 4,
	})
	require.NoError(t, err)
	migrateProjectUserRoles(t, store)

	require.NoError(t, db.Rollback(store, "2.20.11"))
	membership, err := store.GetProjectUser(project.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Role, membership.Role)
	assert.Equal(t, created.Revision, membership.Revision)
	_, err = store.Sql().SelectInt(store.PrepareQuery("select count(*) from project__user_role"))
	assert.Error(t, err, "rollback removes only the plural relation")

	migrateProjectUserRoles(t, store)
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	assert.Equal(t, db.ProjectRoleID("builtin:task_runner"), refs[0].RoleID)
}

func TestMigration_2_20_12RollbackRefusesMultiRoleMembershipWithoutMutation(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	_, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1,
	})
	require.NoError(t, err)
	migrateProjectUserRoles(t, store)
	_, err = store.exec("insert into project__user_role (project_id, user_id, role_id) values (?, ?, ?)", project.ID, user.ID, "builtin:task_runner")
	require.NoError(t, err)

	err = db.Rollback(store, "2.20.11")
	require.ErrorContains(t, err, "multiple roles")
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 2)
	assert.ElementsMatch(t, []db.ProjectRoleID{"builtin:guest", "builtin:task_runner"}, []db.ProjectRoleID{refs[0].RoleID, refs[1].RoleID})
}

func TestMigration_2_20_12RollbackRefusesDirectoryProvenanceWithoutMutation(t *testing.T) {
	store := newProjectUserRoleMigrationStore()
	project, user := createProjectUserRoleMigrationFixture(t, store)
	_, err := store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1,
	})
	require.NoError(t, err)
	migrateProjectUserRoles(t, store)
	_, err = store.exec("update project__user_role set ldap_group_managed_assignment_id=? where project_id=? and user_id=?", 17, project.ID, user.ID)
	require.NoError(t, err)

	err = db.Rollback(store, "2.20.11")
	require.ErrorContains(t, err, "directory provenance")
	refs := projectUserRoleRefs(t, store, project.ID, user.ID)
	require.Len(t, refs, 1)
	require.NotNil(t, refs[0].LDAP)
	assert.Equal(t, 17, *refs[0].LDAP)
}

func TestMigration_2_20_12RollbackNormalizesBuiltinTemplateGrantsAndReapplies(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, user := createProjectUserRoleMigrationFixture(t, store)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "migration-repository", GitURL: "git@example.test:migration/repository", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "migration-template", Playbook: "site.yml"})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1})
	require.NoError(t, err)
	guest, err := store.GetGlobalRoleBySlug(string(db.ProjectGuest))
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: project.ID, TemplateID: template.ID, RoleID: &guest.ID,
		AllowedPermissions: db.CanRunTemplate, DeniedPermissions: db.CanReadTemplate, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.exec("update project__template_role set role_slug=? where project_id=? and template_id=?", "GUEST", project.ID, template.ID)
	require.NoError(t, err)

	require.NoError(t, db.Rollback(store, "2.20.11"))
	var legacyGrant db.TemplateRolePerm
	require.NoError(t, store.selectOne(&legacyGrant, "select * from project__template_role where project_id=? and template_id=?", project.ID, template.ID))
	assert.Nil(t, legacyGrant.RoleID)
	assert.Equal(t, string(db.ProjectGuest), legacyGrant.RoleSlug, "rollback canonicalizes case-variant built-in slugs for legacy lookup")
	assert.Equal(t, db.CanRunTemplate, legacyGrant.AllowedPermissions)
	assert.Equal(t, db.CanReadTemplate, legacyGrant.DeniedPermissions)

	migrateProjectUserRoles(t, store)
	context, err := store.GetTemplatePermissionContext(project.ID, template.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, db.CanRunTemplate, context.EffectivePermissions)
}
