package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runTemplatesMiddleware(f taskControlFixture, templateID int, user *db.User, permissions db.ProjectUserPermission) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/project/1/templates/"+strconv.Itoa(templateID), nil)
	r = mux.SetURLVars(r, map[string]string{"template_id": strconv.Itoa(templateID)})
	r = helpers.SetContextValue(r, "store", f.store)
	r = helpers.SetContextValue(r, "user", user)
	r = helpers.SetContextValue(r, "project", f.project)
	r = helpers.SetContextValue(r, "permissions", permissions)
	w := httptest.NewRecorder()
	handler := GetMustHaveTemplatePermissionMiddleware(db.CanReadTemplate)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	TemplatesMiddleware(handler).ServeHTTP(w, r)
	return w
}

// A template the member may not read answers exactly like one that does not exist.
func TestTemplatesMiddleware_HidesWhatTheMemberMayNotRead(t *testing.T) {
	f := newTaskControlFixture(t)
	role, err := f.store.CreateProjectRole(db.Role{ID: "hidden-template-role", Slug: "hidden-template-role", Name: "Hidden template", ProjectID: &f.project.ID, Revision: 1})
	require.NoError(t, err)
	bob, err := f.store.CreateUserWithoutPassword(db.User{Username: "hidden-template-user", Name: "Hidden template", Email: "hidden-template@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: bob.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	missing := runTemplatesMiddleware(f, 9999, &bob, 0)
	assert.Equal(t, http.StatusNotFound, missing.Code)
	hidden := runTemplatesMiddleware(f, f.template.ID, &bob, 0)
	assert.Equal(t, http.StatusNotFound, hidden.Code)
	assert.Equal(t, missing.Body.String(), hidden.Body.String())
	_, err = f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: f.template.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, runTemplatesMiddleware(f, f.template.ID, &bob, 0).Code)
	assert.Equal(t, http.StatusNoContent, runTemplatesMiddleware(f, f.template.ID, &db.User{ID: 1, Admin: true}, 0).Code)
}

// A build template may be seen through a grant while the deploy templates built on it stay hidden.
func TestGetTemplateRefs_HidesReferrersTheMemberMayNotRead(t *testing.T) {
	f := newTaskControlFixture(t)
	build := f.template
	inventory, err := f.store.CreateInventory(db.Inventory{ProjectID: f.project.ID, Name: "linked inventory", Type: db.InventoryStatic, TemplateID: &build.ID})
	require.NoError(t, err)
	require.Positive(t, inventory.ID)
	_, err = f.store.CreateSchedule(db.Schedule{ProjectID: f.project.ID, TemplateID: build.ID, Name: "linked schedule", CronFormat: "* * * * *", Active: true, Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	_, err = f.store.CreateTemplate(db.Template{Name: "release", Playbook: "site.yml", Type: db.TemplateDeploy, ProjectID: f.project.ID, RepositoryID: build.RepositoryID, InventoryID: build.InventoryID, BuildTemplateID: &build.ID})
	require.NoError(t, err)
	role, err := f.store.CreateRole(db.Role{Slug: "builder", Name: "Builder", Permissions: 0, ProjectID: &f.project.ID})
	require.NoError(t, err)
	bob, err := f.store.CreateUserWithoutPassword(db.User{Username: "bob", Name: "Bob", Email: "bob@example.com"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUserWithRoleRefs(db.ProjectUser{ProjectID: f.project.ID, UserID: bob.ID, Revision: 1}, []db.ProjectRoleID{role.ID})
	require.NoError(t, err)
	_, err = f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: build.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanEditTemplate | db.CanDeleteTemplate})
	require.NoError(t, err)

	refsFor := func(user *db.User) db.ObjectReferrers {
		r := httptest.NewRequest(http.MethodGet, "/api/project/1/templates/1/refs", nil)
		r = helpers.SetContextValue(r, "store", f.store)
		r = helpers.SetContextValue(r, "user", user)
		r = helpers.SetContextValue(r, "template", build)
		w := httptest.NewRecorder()
		GetTemplateRefs(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		var refs db.ObjectReferrers
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refs))
		return refs
	}

	adminRefs := refsFor(&db.User{ID: 0, Admin: true})
	assert.Len(t, adminRefs.Templates, 1)
	assert.Len(t, adminRefs.Inventories, 1)
	assert.Len(t, adminRefs.Schedules, 1)
	guestRefs := refsFor(&bob)
	assert.Empty(t, guestRefs.Templates)
	assert.Empty(t, guestRefs.Inventories)
	assert.Empty(t, guestRefs.Schedules)
}

func TestGetTemplate_HidesBuildTaskTheMemberMayNotRead(t *testing.T) {
	f := newTaskControlFixture(t)
	build := f.template
	release, err := f.store.CreateTemplate(db.Template{Name: "release", Playbook: "site.yml", Type: db.TemplateDeploy, ProjectID: f.project.ID, RepositoryID: build.RepositoryID, InventoryID: build.InventoryID, BuildTemplateID: &build.ID})
	require.NoError(t, err)
	buildTask, err := f.store.CreateTask(db.Task{ProjectID: f.project.ID, TemplateID: build.ID}, 0)
	require.NoError(t, err)
	_, err = f.store.CreateTask(db.Task{ProjectID: f.project.ID, TemplateID: release.ID, BuildTaskID: &buildTask.ID}, 0)
	require.NoError(t, err)
	role, err := f.store.CreateRole(db.Role{Slug: "releaser", Name: "Releaser", Permissions: 0, ProjectID: &f.project.ID})
	require.NoError(t, err)
	bob, err := f.store.CreateUserWithoutPassword(db.User{Username: "bob", Name: "Bob", Email: "bob@example.com"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUserWithRoleRefs(db.ProjectUser{ProjectID: f.project.ID, UserID: bob.ID, Revision: 1}, []db.ProjectRoleID{role.ID})
	require.NoError(t, err)
	_, err = f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: release.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate})
	require.NoError(t, err)
	loaded, err := f.store.GetTemplate(f.project.ID, release.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.LastTask.BuildTask, "the store fills the build task")

	templateFor := func(user *db.User) db.TemplateWithPerms {
		r := httptest.NewRequest(http.MethodGet, "/api/project/1/templates/2", nil)
		r = helpers.SetContextValue(r, "store", f.store)
		r = helpers.SetContextValue(r, "user", user)
		r = helpers.SetContextValue(r, "template", loaded)
		r = helpers.SetContextValue(r, "permissions", db.ProjectUserPermission(0))
		w := httptest.NewRecorder()
		GetTemplate(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		var res db.TemplateWithPerms
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		return res
	}

	assert.NotNil(t, templateFor(&db.User{ID: 0, Admin: true}).LastTask.BuildTask, "the admin sees the build task")
	assert.Nil(t, templateFor(&bob).LastTask.BuildTask, "the grantee may not read the build template")
}

func TestGetTemplates_HidesBuildTaskTheMemberMayNotRead(t *testing.T) {
	f := newTaskControlFixture(t)
	build := f.template
	release, err := f.store.CreateTemplate(db.Template{Name: "release", Playbook: "site.yml", Type: db.TemplateDeploy, ProjectID: f.project.ID, RepositoryID: build.RepositoryID, InventoryID: build.InventoryID, BuildTemplateID: &build.ID})
	require.NoError(t, err)
	buildTask, err := f.store.CreateTask(db.Task{ProjectID: f.project.ID, TemplateID: build.ID}, 0)
	require.NoError(t, err)
	_, err = f.store.CreateTask(db.Task{ProjectID: f.project.ID, TemplateID: release.ID, BuildTaskID: &buildTask.ID}, 0)
	require.NoError(t, err)
	role, err := f.store.CreateRole(db.Role{Slug: "releaser", Name: "Releaser", Permissions: 0, ProjectID: &f.project.ID})
	require.NoError(t, err)
	bob, err := f.store.CreateUserWithoutPassword(db.User{Username: "bob", Name: "Bob", Email: "bob@example.com"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUserWithRoleRefs(db.ProjectUser{ProjectID: f.project.ID, UserID: bob.ID, Revision: 1}, []db.ProjectRoleID{role.ID})
	require.NoError(t, err)
	_, err = f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: release.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate})
	require.NoError(t, err)

	templatesFor := func(user *db.User) []db.TemplateWithPerms {
		r := httptest.NewRequest(http.MethodGet, "/api/project/1/templates", nil)
		r = helpers.SetContextValue(r, "store", f.store)
		r = helpers.SetContextValue(r, "user", user)
		r = helpers.SetContextValue(r, "project", f.project)
		w := httptest.NewRecorder()
		GetTemplates(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		var res []db.TemplateWithPerms
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		return res
	}

	adminUser, err := f.store.CreateUserWithoutPassword(db.User{Username: "admin-list", Name: "Admin", Email: "admin-list@example.com", Admin: true})
	require.NoError(t, err)
	admin := templatesFor(&adminUser)
	require.Len(t, admin, 2)
	assert.NotNil(t, admin[1].LastTask.BuildTask, "the admin sees the build task")
	mine := templatesFor(&bob)
	require.Len(t, mine, 1, "only the release template is visible")
	assert.Nil(t, mine[0].LastTask.BuildTask, "the grantee may not read the build template")
}
