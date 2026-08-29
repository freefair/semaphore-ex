package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectRoleAssignmentAPIPreservesScopeRevisionAndEffectivePermissions(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Role assignment API project"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "Other role assignment API project"})
	require.NoError(t, err)
	actor := createProjectRoleAPIUser(t, store, "role-api-actor")
	target := createProjectRoleAPIUser(t, store, "role-api-target")
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectOwner,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: target.ID, Role: db.ProjectGuest,
	})
	require.NoError(t, err)
	viewer, err := store.CreateProjectRole(db.Role{
		ID: "role_api_viewer", Name: "API viewer", Permissions: db.CanViewProjectResources,
		ProjectID: &project.ID, Revision: 1,
	})
	require.NoError(t, err)
	foreign, err := store.CreateProjectRole(db.Role{
		ID: "role_api_foreign", Name: "Foreign API viewer", Permissions: db.CanViewProjectResources,
		ProjectID: &otherProject.ID, Revision: 1,
	})
	require.NoError(t, err)

	membership, err := store.GetProjectUser(project.ID, target.ID)
	require.NoError(t, err)
	missingRevision := serveProjectMembershipUpdate(
		t, store, project, actor, target, viewer.ID, 0,
	)
	assert.Equal(t, http.StatusBadRequest, missingRevision.Code)

	response := serveProjectMembershipUpdate(
		t, store, project, actor, target, viewer.ID, membership.Revision,
	)
	assert.Equal(t, http.StatusNoContent, response.Code)

	updated, err := store.GetProjectUser(project.ID, target.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.RoleID)
	assert.Equal(t, viewer.ID, *updated.RoleID)
	assert.Equal(t, membership.Revision+1, updated.Revision)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/project/1/users", nil)
	listRequest = helpers.SetContextValue(listRequest, "project", project)
	listRequest = helpers.SetContextValue(listRequest, "store", store)
	listResponse := httptest.NewRecorder()
	GetUsers(listResponse, listRequest)
	assert.Equal(t, http.StatusOK, listResponse.Code)
	var users []projUser
	require.NoError(t, json.Unmarshal(listResponse.Body.Bytes(), &users))
	var listed projUser
	for _, user := range users {
		if user.ID == target.ID {
			listed = user
			break
		}
	}
	require.NotZero(t, listed.ID)
	assert.Equal(t, db.ProjectUserRole(viewer.ID), listed.Role)
	assert.Equal(t, db.CanViewProjectResources, listed.EffectivePermissions)

	stale := serveProjectMembershipUpdate(
		t, store, project, actor, target, db.ProjectRoleID(db.ProjectGuest), membership.Revision,
	)
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, stale.Body.String(), "PROJECT_MEMBERSHIP_REVISION_CONFLICT")

	crossProject := serveProjectMembershipUpdate(
		t, store, project, actor, target, foreign.ID, updated.Revision,
	)
	assert.Equal(t, http.StatusBadRequest, crossProject.Code)
}

func TestProjectRoleAssignmentAPIProtectsLastAdministrator(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Last administrator API project"})
	require.NoError(t, err)
	administrator := createProjectRoleAPIUser(t, store, "last-role-api-admin")
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID, UserID: administrator.ID, Role: db.ProjectOwner,
	})
	require.NoError(t, err)
	membership, err := store.GetProjectUser(project.ID, administrator.ID)
	require.NoError(t, err)
	globalAdmin := db.User{ID: administrator.ID + 1000, Admin: true}

	response := serveProjectMembershipUpdate(
		t, store, project, globalAdmin, administrator,
		db.ProjectRoleID(db.ProjectGuest), membership.Revision,
	)
	assert.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), "LAST_PROJECT_ADMINISTRATOR")
}

func serveProjectMembershipUpdate(
	t *testing.T,
	store db.Store,
	project db.Project,
	actor db.User,
	target db.User,
	role db.ProjectRoleID,
	revision int,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"role": role, "revision": revision})
	require.NoError(t, err)
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/project/1/users/2",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &actor)
	request = helpers.SetContextValue(request, "projectUser", target)
	request = helpers.SetContextValue(request, "projectUserRole", db.ProjectOwner)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "log_writer", runtimeSecretLogWriter{})
	response := httptest.NewRecorder()
	UpdateUser(response, request)
	return response
}

func createProjectRoleAPIUser(t *testing.T, store *coresql.SqlDb, username string) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: username, Name: username, Email: username + "@example.test",
	})
	require.NoError(t, err)
	return user
}
