package project

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upstreamFormat2Backup is a Semaphore UI format 2 export: roles and template
// grants refer to each other by slug.
const upstreamFormat2Backup = `{
  "version": 2,
  "keys": [{"name": "noop", "owner": "", "type": "none"}],
  "meta": {"name": "From Semaphore UI", "type": ""},
  "repositories": [{"git_branch": "main", "git_url": "git@example.com:test/test.git", "name": "Repo", "ssh_key": "noop"}],
  "roles": [{"name": "Ops", "slug": "ops", "permissions": 1}],
  "templates": [{
    "app": "", "name": "Tpl", "playbook": "test.yml", "repository": "Repo", "type": "",
    "roles": [
      {"slug": "ops", "permissions": 1},
      {"slug": "task_runner", "permissions": 1},
      {"slug": "deployer", "permissions": 1},
      {"slug": "nobody", "permissions": 4}
    ],
    "vaults": [], "view": null, "environments": []
  }]
}`

func restoreUser(t *testing.T, store db.Store) db.User {
	t.Helper()
	user, err := store.CreateUser(db.UserWithPwd{Pwd: "3412341234123", User: db.User{
		Username: "restorer", Name: "Restorer", Email: "restorer@example.com", Admin: true,
	}})
	require.NoError(t, err)
	return user
}

func TestRestoreUpstreamFormat2ResolvesGrantsToFileAndBuiltinRolesOnly(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	// A global custom role with a slug from the file must never receive the grant.
	_, err := store.CreateGlobalRole(db.Role{ID: "global-deployer", Slug: "deployer", Name: "Deployer", Revision: 1})
	require.NoError(t, err)
	var backup BackupFormat
	require.NoError(t, backup.Unmarshal(upstreamFormat2Backup))
	require.NoError(t, backup.Verify())

	restored, err := backup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))

	require.NoError(t, err)
	roles, err := store.GetProjectRoles(restored.ID)
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, "Ops", roles[0].Name)
	assert.NotEqual(t, "ops", roles[0].Slug, "EX keeps generated internal slugs")
	templates, err := store.GetTemplates(restored.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	grants, err := store.GetTemplateRoles(restored.ID, templates[0].ID)
	require.NoError(t, err)
	granted := map[string]db.TemplatePermission{}
	for _, grant := range grants {
		granted[grant.RoleSlug] = grant.AllowedPermissions
	}
	assert.Equal(t, map[string]db.TemplatePermission{
		roles[0].Slug: db.CanRunTemplate | db.CanReadTemplate,
		"task_runner": db.CanRunTemplate | db.CanReadTemplate,
	}, granted)
	assert.Equal(t, []UnassignedRole{
		{Slug: "deployer", Reason: ReasonNotInFile, Templates: []UnassignedTemplate{{ID: templates[0].ID, Name: "Tpl", Permissions: db.ProjectUserPermission(db.CanReadTemplate | db.CanRunTemplate)}}, Workflows: []UnassignedWorkflow{}},
		{Slug: "nobody", Reason: ReasonNotInFile, Templates: []UnassignedTemplate{{ID: templates[0].ID, Name: "Tpl", Permissions: db.ProjectUserPermission(db.CanReadTemplate | db.CanEditTemplate | db.CanDeleteTemplate)}}, Workflows: []UnassignedWorkflow{}},
	}, restored.UnassignedRoles)
}

func TestVerifyUpstreamFormat2RoleSlugs(t *testing.T) {
	slug := func(s string) *string { return &s }
	tests := []struct {
		name    string
		roles   []BackupRole
		wantErr string
	}{
		{"duplicate", []BackupRole{
			{Role: db.Role{Name: "Ops"}, UpstreamSlug: slug("ops")},
			{Role: db.Role{Name: "Ops again"}, UpstreamSlug: slug("OPS")},
		}, "Role slug is duplicate: ops"},
		{"empty", []BackupRole{{Role: db.Role{Name: "Ops"}, UpstreamSlug: slug("")}}, "Role slug must be 1 to 100 characters"},
		{"too long", []BackupRole{{Role: db.Role{Name: "Ops"}, UpstreamSlug: slug(strings.Repeat("x", 101))}}, "Role slug must be 1 to 100 characters"},
		{"reserved route", []BackupRole{{Role: db.Role{Name: "All"}, UpstreamSlug: slug("all")}}, "Role slug is reserved: all"},
		{"shadows a built-in", []BackupRole{{Role: db.Role{Name: "Owner"}, UpstreamSlug: slug("owner")}}, "Role slug is reserved: owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := BackupFormatVersion
			backup := &BackupFormat{Version: &version, Meta: BackupMeta{Project: db.Project{Name: "p"}}, Roles: tt.roles}

			err := backup.Verify()

			assert.ErrorContains(t, err, "error at roles[0]: "+tt.wantErr)
		})
	}
}

func TestEXBackupIdentifiesSchemaAndAddressesRolesByID(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	p, tpl := createBackupFixture(t, store)
	role, err := store.CreateProjectRole(db.Role{ID: "local-ops", Name: "Ops", ProjectID: &p.ID, Permissions: db.CanRunProjectTasks, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: p.ID, TemplateID: tpl.ID, RoleID: &role.ID, Permissions: db.CanRunProjectTasks})
	require.NoError(t, err)
	backup, err := GetBackup(p.ID, store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)

	str, err := backup.Marshal()

	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(str), &doc))
	assert.Equal(t, float64(4), doc["version"])
	assert.Equal(t, "semaphore-ex", doc["permission_schema"])
	grant := doc["templates"].([]any)[0].(map[string]any)["roles"].([]any)[0].(map[string]any)
	assert.Equal(t, "local-ops", grant["role_id"])
	assert.NotContains(t, grant, "slug")
	assert.NotContains(t, doc["roles"].([]any)[0].(map[string]any), "slug")
}

func TestRestore_UnassignedRolesIsEmptyList(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	backup := &BackupFormat{Meta: BackupMeta{Project: db.Project{Name: "plain"}}}

	restored, err := backup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))

	require.NoError(t, err)
	assert.NotNil(t, restored.UnassignedRoles)
	assert.Empty(t, restored.UnassignedRoles)
}

func TestRestoreRemovesTheProjectWhenAnEntryFails(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	backup := &BackupFormat{
		Meta:      BackupMeta{Project: db.Project{Name: "half"}},
		Templates: []BackupTemplate{{Template: db.Template{Name: "deploy"}, Repository: "gone"}},
	}

	restored, err := backup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))

	require.ErrorContains(t, err, "error at templates[0]")
	assert.Nil(t, restored)
	projects, err := store.GetAllProjects()
	require.NoError(t, err)
	assert.Empty(t, projects, "a failed restore leaves no project behind")
}

type failingDelete struct{ db.Store }

func (failingDelete) DeleteProject(int) error { return errors.New("locked") }
func (failingDelete) CreateProjectUserWithRoleRefs(db.ProjectUser, []db.ProjectRoleID) (db.ProjectUser, error) {
	return db.ProjectUser{}, errors.New("membership failed")
}

func TestRestore_ReportsAFailedCleanup(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	backup := &BackupFormat{
		Meta: BackupMeta{Project: db.Project{Name: "stuck"}},
	}

	_, err := backup.Restore(restoreUser(t, store), failingDelete{store}, proFactory.NewWorkflowStore(store))

	require.ErrorContains(t, err, "membership failed")
	assert.ErrorContains(t, err, "could not be removed: locked")
	var cleanup *CleanupError
	assert.ErrorAs(t, err, &cleanup)
}

func TestVerify_RefusesANewerFormatVersion(t *testing.T) {
	newer := BackupFormatVersion + 1
	backup := &BackupFormat{Version: &newer, Meta: BackupMeta{Project: db.Project{Name: "p"}}}

	err := backup.Verify()

	assert.ErrorContains(t, err, "newer version of Semaphore")
}

func TestVerify_ReadsAVersionlessFile(t *testing.T) {
	backup := &BackupFormat{Meta: BackupMeta{Project: db.Project{Name: "p"}}}

	assert.NoError(t, backup.Verify())
}

func TestVerify_NamesTheScheduleEntry(t *testing.T) {
	backup := &BackupFormat{
		Meta:      BackupMeta{Project: db.Project{Name: "p"}},
		Schedules: []BackupSchedule{{Schedule: db.Schedule{Name: "nightly"}}, {Schedule: db.Schedule{Name: "nightly"}}, {Schedule: db.Schedule{Name: "nightly"}}},
	}

	err := backup.Verify()

	assert.ErrorContains(t, err, "error at schedules[0]: nightly is duplicate")
}

// A string key travels through a backup like every other key: by name and
// type only. Its value stays behind, and the restored key is empty until the
// owner fills it in again.
func TestBackupProject_StringKey(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	proj, err := store.CreateProject(db.Project{Name: "String key"})
	require.NoError(t, err)
	ciphertext := "ciphertext-of-s3cr3t"
	_, err = store.CreateAccessKey(db.AccessKey{
		ProjectID: &proj.ID,
		Name:      "api-token",
		Type:      db.AccessKeyString,
		String:    "s3cr3t",
		Secret:    &ciphertext,
	})
	require.NoError(t, err)
	backup, err := GetBackup(proj.ID, store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)

	str, err := backup.Marshal()

	require.NoError(t, err)
	assert.NotContains(t, str, "s3cr3t", "a backup must not carry the value")
	assert.NotContains(t, str, ciphertext, "a backup must not carry the ciphertext either")
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal([]byte(str), &doc))
	require.Len(t, doc.Keys, 1)
	assert.Equal(t, "string", doc.Keys[0]["type"])
	assert.NotContains(t, doc.Keys[0], "string")
	assert.NotContains(t, doc.Keys[0], "secret")

	restoredBackup := &BackupFormat{}
	require.NoError(t, restoredBackup.Unmarshal(str))
	restoredBackup.Meta.Name = "String key restored"
	require.NoError(t, restoredBackup.Verify())
	restoredProj, err := restoredBackup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)
	restoredKeys, err := store.GetAccessKeys(restoredProj.ID, db.GetAccessKeyOptions{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, restoredKeys, 1)
	assert.Equal(t, db.AccessKeyString, restoredKeys[0].Type)
	assert.Nil(t, restoredKeys[0].Secret)
	assert.Empty(t, restoredKeys[0].String)
	assert.True(t, restoredKeys[0].Empty, "the restored key waits for a value")
}

func TestRestoreUpstreamFormat2KeepsRolesWithTheSameName(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	file := `{
  "version": 2,
  "meta": {"name": "Same names", "type": ""},
  "roles": [
    {"name": "Ops", "slug": "ops-a", "permissions": 1},
    {"name": "Ops", "slug": "ops-b", "permissions": 1},
    {"name": "Ops", "slug": "ops-c", "permissions": 1}
  ]
}`
	var backup BackupFormat
	require.NoError(t, backup.Unmarshal(file))
	require.NoError(t, backup.Verify())

	restored, err := backup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))

	require.NoError(t, err)
	roles, err := store.GetProjectRoles(restored.ID)
	require.NoError(t, err)
	names := []string{}
	for _, role := range roles {
		names = append(names, role.Name)
	}
	assert.ElementsMatch(t, []string{"Ops", "Ops (ops-b)", "Ops (ops-c)"}, names)
}

// recordingStore records the order of restore writes and can fail matchers.
type recordingStore struct {
	db.Store
	calls        []string
	failMatchers bool
}

func (s *recordingStore) CreateSchedule(schedule db.Schedule) (db.Schedule, error) {
	s.calls = append(s.calls, "schedule")
	return s.Store.CreateSchedule(schedule)
}

func (s *recordingStore) SetScheduleActive(projectID int, scheduleID int, active bool) error {
	s.calls = append(s.calls, "activate")
	return s.Store.SetScheduleActive(projectID, scheduleID, active)
}

func (s *recordingStore) CreateIntegration(integration db.Integration) (db.Integration, error) {
	s.calls = append(s.calls, "integration")
	return s.Store.CreateIntegration(integration)
}

func (s *recordingStore) CreateIntegrationMatcher(projectID int, matcher db.IntegrationMatcher) (db.IntegrationMatcher, error) {
	if s.failMatchers {
		return db.IntegrationMatcher{}, errors.New("matcher refused")
	}
	return s.Store.CreateIntegrationMatcher(projectID, matcher)
}

func integrationBackup(t *testing.T, store db.Store, name string) *BackupFormat {
	t.Helper()
	p, tpl := createBackupFixture(t, store)
	_, err := store.CreateSchedule(db.Schedule{ProjectID: p.ID, TemplateID: tpl.ID, CronFormat: "0 0 * * *", Name: "nightly", Active: true})
	require.NoError(t, err)
	integration, err := store.CreateIntegration(db.Integration{Name: "hook", ProjectID: p.ID, TemplateID: &tpl.ID, AuthMethod: db.IntegrationAuthNone})
	require.NoError(t, err)
	_, err = store.CreateIntegrationMatcher(p.ID, db.IntegrationMatcher{
		IntegrationID: integration.ID, Name: "event", MatchType: db.IntegrationMatchHeader, Method: db.IntegrationMatchMethodEquals, Key: "X-Event", Value: "push",
	})
	require.NoError(t, err)
	backup, err := GetBackup(p.ID, store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)
	backup.Meta.Name = name
	return backup
}

func TestRestoreCreatesSchedulesAfterEveryOtherEntry(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	base := sql.InitConfigCreateTestStore()
	backup := integrationBackup(t, base, "Ordered")
	store := &recordingStore{Store: base}

	_, err := backup.Restore(restoreUser(t, base), store, proFactory.NewWorkflowStore(base))

	require.NoError(t, err)
	assert.Equal(t, []string{"integration", "schedule", "activate"}, store.calls)
}

func TestRestoreActivatesNoScheduleWhenALaterScheduleFails(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	base := sql.InitConfigCreateTestStore()
	backup := integrationBackup(t, base, "Second schedule broken")
	backup.Schedules = append(backup.Schedules, BackupSchedule{Schedule: db.Schedule{Name: "broken", CronFormat: "0 1 * * *", Active: true}, Template: "gone"})
	store := &recordingStore{Store: base}

	_, err := backup.Restore(restoreUser(t, base), store, proFactory.NewWorkflowStore(base))

	require.ErrorContains(t, err, "error at schedules[1]")
	assert.Equal(t, []string{"integration", "schedule"}, store.calls, "the first schedule was never active")
}

func TestRestoreKeepsAnInactiveScheduleInactive(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	store := sql.InitConfigCreateTestStore()
	backup := integrationBackup(t, store, "Inactive schedule")
	require.Len(t, backup.Schedules, 1)
	backup.Schedules[0].Active = false

	restored, err := backup.Restore(restoreUser(t, store), store, proFactory.NewWorkflowStore(store))

	require.NoError(t, err)
	schedules, err := store.GetProjectSchedules(restored.ID, false, false)
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.False(t, schedules[0].Active)
}

func TestRestoreRemovesTheProjectWhenAMatcherFails(t *testing.T) {
	util.Config = &util.ConfigType{TmpPath: "/tmp"}
	base := sql.InitConfigCreateTestStore()
	backup := integrationBackup(t, base, "Broken matcher")
	before, err := base.GetAllProjects()
	require.NoError(t, err)
	store := &recordingStore{Store: base, failMatchers: true}

	restored, err := backup.Restore(restoreUser(t, base), store, proFactory.NewWorkflowStore(base))

	require.ErrorContains(t, err, "error at integrations[0]: matchers[0]: matcher refused")
	assert.Nil(t, restored)
	after, err := base.GetAllProjects()
	require.NoError(t, err)
	assert.Len(t, after, len(before), "the half-restored project is deleted again")
	assert.NotContains(t, store.calls, "schedule", "no schedule exists before the restore succeeded")
}
