package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedTemplateRouteDoesNotExposeProjectSchedules(t *testing.T) {
	store := setupSessionTest(t)
	util.Config.Debugging = &util.DebuggingConfig{}
	util.Config.TmpPath = t.TempDir()
	project, err := store.CreateProject(db.Project{Name: "Scoped schedules"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.invalid/repo", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repo.ID, Name: "Scoped", Playbook: "site.yml"})
	require.NoError(t, err)
	_, err = store.CreateSchedule(db.Schedule{ProjectID: project.ID, TemplateID: template.ID, Name: "private schedule", CronFormat: "* * * * *", Active: true, Type: db.ScheduleTypeCron, RepositoryID: &repo.ID, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	role, err := store.CreateProjectRole(db.Role{ID: "scoped-schedules", Slug: "scoped-schedules", Name: "Scoped", ProjectID: &project.ID, Revision: 1})
	require.NoError(t, err)
	user := createUserOptionsTestUser(t, store, "scoped-schedules")
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	grant, err := store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: project.ID, TemplateID: template.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate})
	require.NoError(t, err)
	pool := poolWithTasks()
	router := Route(store, nil, nil, nil, pool, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, proServer.NewLogWriteService(), nil, metrics.NewMetrics(), nil, nil)
	send := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = addSessionCookie(t, store, request, user, db.SessionVerificationNone)
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "task_pool", pool)
		request, _ = withAuditRecorder(request)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	base := fmt.Sprintf("/api/project/%d/templates/%d", project.ID, template.ID)
	assert.Equal(t, http.StatusOK, send(base).Code)
	denied := send(base + "/schedules")
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.NotContains(t, denied.Body.String(), "private schedule")
	role.Permissions = db.CanReadProject
	role, err = store.UpdateProjectRole(project.ID, role, role.Revision)
	require.NoError(t, err)
	allowed := send(base + "/schedules")
	assert.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())
	assert.Contains(t, allowed.Body.String(), "private schedule")
	role.Permissions |= db.CanManageProjectResources
	role, err = store.UpdateProjectRole(project.ID, role, role.Revision)
	require.NoError(t, err)
	grant.AllowedPermissions = db.CanRunTemplate
	grant.DeniedPermissions = db.CanReadTemplate
	_, err = store.UpdateTemplateRole(grant, grant.Revision)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, send(base).Code, "explicit read-deny hides template content")
	assert.Equal(t, http.StatusOK, send(base+"/perms").Code, "ACL manager can inspect the revision needed to undo its own deny")
}
