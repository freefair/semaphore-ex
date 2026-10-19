package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_2_20_14_MappingTables(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "p"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "u", Name: "u", Email: "u@example.com"})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "migration-role-dev", Slug: "dev", Name: "Dev", Permissions: db.CanRunProjectTasks, ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)

	_, err = store.exec("insert into group_role_mapping (provider, group_name, project_id, role_id, created) values ('okta', 'devops', ?, ?, ?)",
		project.ID, role.ID, time.Now().UTC())
	require.NoError(t, err)
	mappingID, err := store.Sql().SelectInt("select id from group_role_mapping")
	require.NoError(t, err)
	_, err = store.exec("insert into project__user_mapped_role (project_id, user_id, mapping_id) values (?, ?, ?)", project.ID, user.ID, mappingID)
	require.NoError(t, err)

	_, err = store.exec("insert into group_role_mapping (provider, group_name, project_id, role_id, created) values ('okta', 'devops', ?, ?, ?)",
		project.ID, role.ID, time.Now().UTC())
	assert.Error(t, err, "a mapping exists once")
	_, err = store.exec("insert into group_role_mapping (provider, group_name, project_id, role_id, created) values ('okta', 'DevOps', ?, ?, ?)",
		project.ID, role.ID, time.Now().UTC())
	assert.NoError(t, err, "groups are case-sensitive")

	_, err = store.SaveOIDCGroupMapping(db.OIDCGroupMapping{
		ID: "mapping-table-delete-protection", ProviderID: "okta", ClaimValue: "devops",
		TargetScope: "project", ProjectID: &project.ID, RoleID: string(role.ID),
		Enabled: true, Revision: 1, Created: time.Now().UTC(), Updated: time.Now().UTC(),
	}, 0)
	require.NoError(t, err)
	assert.Error(t, store.DeleteRole(role.ID), "an authoritative OIDC mapping protects its target role")

	_, err = store.exec("delete from group_role_mapping where id=?", mappingID)
	require.NoError(t, err)
	left, err := store.Sql().SelectInt("select count(*) from project__user_mapped_role")
	require.NoError(t, err)
	assert.Zero(t, left, "mapped rows go with their mapping")
}

func TestMigration_2_20_14_AllowsVirtualBuiltInRoleID(t *testing.T) {
	store := InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "built-in mapping"})
	require.NoError(t, err)

	_, err = store.exec("insert into group_role_mapping (provider, group_name, project_id, role_id, created) values ('okta', 'admins', ?, ?, ?)",
		project.ID, db.ProjectRoleID("builtin:owner"), time.Now().UTC())
	assert.NoError(t, err)
}

func TestMigration_2_20_14_Rollback(t *testing.T) {
	store := InitConfigCreateTestStore()
	require.NoError(t, db.Rollback(store, "2.20.13"))

	tables, err := store.Sql().SelectInt(`
		select
		  count(*)
		from sqlite_master
		where type = 'table'
		  and name in ('group_role_mapping', 'project__user_mapped_role')
	`)
	require.NoError(t, err)
	assert.Zero(t, tables)
}
