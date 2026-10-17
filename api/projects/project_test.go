package projects

import (
	"fmt"
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

func runProjectMiddleware(store db.Store, projectID int, user *db.User) (int, db.ProjectUserPermission, []db.Role) {
	r := httptest.NewRequest(http.MethodGet, "/api/project/"+strconv.Itoa(projectID), nil)
	r = mux.SetURLVars(r, map[string]string{"project_id": strconv.Itoa(projectID)})
	r = helpers.SetContextValue(r, "store", store)
	r = helpers.SetContextValue(r, "user", user)
	w := httptest.NewRecorder()
	var permissions db.ProjectUserPermission
	var roles []db.Role
	ProjectMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		permissions = helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
		roles = helpers.GetFromContext(r, "projectRoles").([]db.Role)
	})).ServeHTTP(w, r)
	return w.Code, permissions, roles
}

func TestProjectMiddleware_PermissionsComeFromRoles(t *testing.T) {
	f := newMembershipFixture(t)
	global, err := f.store.CreateRole(db.Role{Slug: "global", Name: "Global", Permissions: db.CanManageProjectResources})
	require.NoError(t, err)
	local, err := f.store.CreateRole(db.Role{Slug: "local", Name: "Local", Permissions: db.CanUpdateProject, ProjectID: &f.project.ID})
	require.NoError(t, err)

	tests := []struct {
		name    string
		roleIDs []db.ProjectRoleID
		want    db.ProjectUserPermission
	}{
		{"built-in role", []db.ProjectRoleID{f.roles[db.ProjectManager].ID}, db.ProjectManager.GetPermissions()},
		{"global custom role", []db.ProjectRoleID{global.ID}, db.CanManageProjectResources},
		{"project custom role", []db.ProjectRoleID{local.ID}, db.CanUpdateProject},
		{"two roles", []db.ProjectRoleID{global.ID, local.ID}, db.CanManageProjectResources | db.CanUpdateProject},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := f.store.CreateUserWithoutPassword(db.User{Username: fmt.Sprintf("u%d", i), Name: "U", Email: fmt.Sprintf("u%d@example.com", i)})
			require.NoError(t, err)
			_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1})
			require.NoError(t, err)
			require.NoError(t, f.store.SetProjectUserRoleRefs(f.project.ID, user.ID, tt.roleIDs, 1))

			code, permissions, roles := runProjectMiddleware(f.store, f.project.ID, &user)

			assert.Equal(t, http.StatusOK, code)
			assert.Equal(t, tt.want, permissions)
			assert.Len(t, roles, len(tt.roleIDs))
		})
	}
}

func TestProjectMiddleware_NonMember(t *testing.T) {
	f := newMembershipFixture(t)
	stranger, err := f.store.CreateUserWithoutPassword(db.User{Username: "stranger", Name: "S", Email: "s@example.com"})
	require.NoError(t, err)

	code, _, _ := runProjectMiddleware(f.store, f.project.ID, &stranger)
	assert.Equal(t, http.StatusNotFound, code)

	admin := db.User{ID: stranger.ID, Admin: true}
	code, permissions, roles := runProjectMiddleware(f.store, f.project.ID, &admin)
	assert.Equal(t, http.StatusOK, code, "an admin enters a project without membership")
	assert.Zero(t, permissions)
	assert.Empty(t, roles)
}

func TestGetUserRole_RoleOnlyForOneRole(t *testing.T) {
	manager := db.Role{
		ID: "builtin:manager", Slug: "manager", Name: "Manager",
		Permissions: db.ProjectManager.GetPermissions(), Builtin: true, Revision: 1,
	}
	runner := db.Role{
		ID: "builtin:task_runner", Slug: "task_runner", Name: "Task Runner",
		Permissions: db.ProjectTaskRunner.GetPermissions(), Builtin: true, Revision: 1,
	}
	tests := []struct {
		name  string
		roles []db.Role
		want  string
	}{
		{"one role", []db.Role{manager}, `{"role":"manager","roles":["manager"],"role_name":"Manager","permissions":0}`},
		{"two roles", []db.Role{manager, runner}, `{"role":"","roles":["manager","task_runner"],"role_name":"","permissions":0}`},
		{"admin without roles", nil, `{"role":"","roles":[],"role_name":"","permissions":0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/project/1/role", nil)
			r = helpers.SetContextValue(r, "projectRoles", tt.roles)
			r = helpers.SetContextValue(r, "permissions", db.ProjectUserPermission(0))
			w := httptest.NewRecorder()

			GetUserRole(w, r)

			assert.JSONEq(t, tt.want, w.Body.String())
		})
	}
}
