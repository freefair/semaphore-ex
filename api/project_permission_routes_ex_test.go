package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	taskServices "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectPermissionRoute is the security contract between the project permission
// catalog and the router.  The route need not complete its domain operation in
// this test: several handlers deliberately need an external service.  It must,
// however, pass the authorization middleware for a principal holding permission.
type projectPermissionRoute struct {
	name       string
	method     string
	path       string
	body       string
	permission db.ProjectUserPermission
	read       bool
}

func (route projectPermissionRoute) key() string {
	return route.method + " " + route.path
}

// membershipRead reports endpoints that were, and remain, visible to every
// project member. Keeping them in the matrix prevents a fine-grained action
// from silently replacing membership visibility with a new requirement.
func (route projectPermissionRoute) membershipRead() bool {
	switch route.key() {
	case "GET /api/project/{project_id}",
		"GET /api/project/{project_id}/templates",
		"GET /api/project/{project_id}/workflows",
		"GET /api/project/{project_id}/views":
		return true
	default:
		return false
	}
}

// projectPermissionRoutes has one representative router route for every
// project-scoped catalog action.  The specialised child routes below remain in
// this table as well: their parent resource permission is their contract.
func projectPermissionRoutes() []projectPermissionRoute {
	return []projectPermissionRoute{
		{"read project", http.MethodGet, "/api/project/{project_id}", "", db.CanReadProject, true},
		{"run task", http.MethodPost, "/api/project/{project_id}/tasks", `{"template_id":%template_id%}`, db.CanRunProjectTasks, false},
		{"update settings", http.MethodPut, "/api/project/{project_id}", `{}`, db.CanUpdateProject, false},
		{"manage users", http.MethodGet, "/api/project/{project_id}/roles", "", db.CanManageProjectUsers, false},
		{"view workflows", http.MethodGet, "/api/project/{project_id}/workflows", "", db.CanViewWorkflows, true},
		{"edit workflows", http.MethodPost, "/api/project/{project_id}/workflows", `{}`, db.CanEditWorkflows, false},
		{"start workflows", http.MethodPost, "/api/project/{project_id}/workflows/1/start", `{}`, db.CanStartWorkflows, false},
		{"stop workflows", http.MethodPost, "/api/project/{project_id}/workflows/1/stop", `{}`, db.CanStopWorkflows, false},
		{"administer workflows", http.MethodDelete, "/api/project/{project_id}/workflows/1", "", db.CanAdministerWorkflows, false},
		{"approve workflows", http.MethodPost, "/api/project/{project_id}/workflow-approvals/1/approve", `{}`, db.CanApproveWorkflows, false},
		{"read task groups", http.MethodGet, "/api/project/{project_id}/task_groups", "", db.CanReadTaskGroups, true},
		{"create task groups", http.MethodPost, "/api/project/{project_id}/task_groups", `{}`, db.CanCreateTaskGroups, false},
		{"update task groups", http.MethodPut, "/api/project/{project_id}/task_groups/1", `{}`, db.CanUpdateTaskGroups, false},
		{"delete task groups", http.MethodDelete, "/api/project/{project_id}/task_groups/1", "", db.CanDeleteTaskGroups, false},
		{"share task groups", http.MethodPost, "/api/project/{project_id}/task_groups/1/projects", `{}`, db.CanShareTaskGroups, false},

		{"view templates", http.MethodGet, "/api/project/{project_id}/templates", "", db.CanViewTemplates, true},
		{"create templates", http.MethodPost, "/api/project/{project_id}/templates", `{}`, db.CanCreateTemplates, false},
		{"update templates", http.MethodPut, "/api/project/{project_id}/templates/1", `{}`, db.CanUpdateTemplates, false},
		{"delete templates", http.MethodDelete, "/api/project/{project_id}/templates/1", "", db.CanDeleteTemplates, false},
		{"view inventories", http.MethodGet, "/api/project/{project_id}/hosts", "", db.CanViewInventories, true},
		{"create inventories", http.MethodPost, "/api/project/{project_id}/inventory", `{}`, db.CanCreateInventories, false},
		{"update inventories", http.MethodPut, "/api/project/{project_id}/inventory/1", `{}`, db.CanUpdateInventories, false},
		{"delete inventories", http.MethodDelete, "/api/project/{project_id}/inventory/1", "", db.CanDeleteInventories, false},
		{"view repositories", http.MethodGet, "/api/project/{project_id}/repositories", "", db.CanViewRepositories, true},
		{"create repositories", http.MethodPost, "/api/project/{project_id}/repositories", `{}`, db.CanCreateRepositories, false},
		{"update repositories", http.MethodPut, "/api/project/{project_id}/repositories/1", `{}`, db.CanUpdateRepositories, false},
		{"delete repositories", http.MethodDelete, "/api/project/{project_id}/repositories/1", "", db.CanDeleteRepositories, false},
		{"view access keys", http.MethodGet, "/api/project/{project_id}/keys", "", db.CanViewAccessKeys, true},
		{"create access keys", http.MethodPost, "/api/project/{project_id}/keys", `{}`, db.CanCreateAccessKeys, false},
		{"update access keys", http.MethodPut, "/api/project/{project_id}/keys/1", `{}`, db.CanUpdateAccessKeys, false},
		{"delete access keys", http.MethodDelete, "/api/project/{project_id}/keys/1", "", db.CanDeleteAccessKeys, false},
		{"use access keys", http.MethodPost, "/api/project/{project_id}/tasks", `{"template_id":%template_id%,"ssh_keys":[{"access_key_id":%access_key_id%,"hosts":["example.invalid"]}]}`, db.CanUseAccessKeys, false},
		{"view environments", http.MethodGet, "/api/project/{project_id}/environment", "", db.CanViewEnvironments, true},
		{"create environments", http.MethodPost, "/api/project/{project_id}/environment", `{}`, db.CanCreateEnvironments, false},
		{"update environments", http.MethodPut, "/api/project/{project_id}/environment/1", `{}`, db.CanUpdateEnvironments, false},
		{"delete environments", http.MethodDelete, "/api/project/{project_id}/environment/1", "", db.CanDeleteEnvironments, false},
		{"view schedules", http.MethodGet, "/api/project/{project_id}/schedules", "", db.CanViewSchedules, true},
		{"create schedules", http.MethodPost, "/api/project/{project_id}/schedules", `{}`, db.CanCreateSchedules, false},
		{"update schedules", http.MethodPut, "/api/project/{project_id}/schedules/1", `{}`, db.CanUpdateSchedules, false},
		{"delete schedules", http.MethodDelete, "/api/project/{project_id}/schedules/1", "", db.CanDeleteSchedules, false},
		{"view integrations", http.MethodGet, "/api/project/{project_id}/integrations", "", db.CanViewIntegrations, true},
		{"create integrations", http.MethodPost, "/api/project/{project_id}/integrations", `{}`, db.CanCreateIntegrations, false},
		{"update integrations", http.MethodPut, "/api/project/{project_id}/integrations/1", `{}`, db.CanUpdateIntegrations, false},
		{"delete integrations", http.MethodDelete, "/api/project/{project_id}/integrations/1", "", db.CanDeleteIntegrations, false},
		{"view views", http.MethodGet, "/api/project/{project_id}/views", "", db.CanViewViews, true},
		{"create views", http.MethodPost, "/api/project/{project_id}/views", `{}`, db.CanCreateViews, false},
		{"update views", http.MethodPost, "/api/project/{project_id}/views/positions", `{}`, db.CanUpdateViews, false},
		{"delete views", http.MethodDelete, "/api/project/{project_id}/views/1", "", db.CanDeleteViews, false},

		// Child resources intentionally use their owning resource permission.
		{"host configurations use inventory view", http.MethodGet, "/api/project/{project_id}/host_configs", "", db.CanViewInventories, true},
		{"host configurations use inventory create", http.MethodPost, "/api/project/{project_id}/host_configs", `{}`, db.CanCreateInventories, false},
		{"host configurations use inventory update", http.MethodPut, "/api/project/{project_id}/host_configs/1", `{}`, db.CanUpdateInventories, false},
		{"host configurations use inventory delete", http.MethodDelete, "/api/project/{project_id}/host_configs/1", "", db.CanDeleteInventories, false},
		{"secret storages use key view", http.MethodGet, "/api/project/{project_id}/secret_storages", "", db.CanViewAccessKeys, true},
		{"secret storages use key create", http.MethodPost, "/api/project/{project_id}/secret_storages", `{}`, db.CanCreateAccessKeys, false},
		{"secret storages use key update", http.MethodPut, "/api/project/{project_id}/secret_storages/1", `{}`, db.CanUpdateAccessKeys, false},
		{"secret storages use key delete", http.MethodDelete, "/api/project/{project_id}/secret_storages/1", "", db.CanDeleteAccessKeys, false},
		{"browse a secret storage when creating keys", http.MethodGet, "/api/project/{project_id}/secret_storages/1/browse", "", db.CanCreateAccessKeys, false},
		{"test a secret storage's identity", http.MethodPost, "/api/project/{project_id}/secret_storages/1/identity", `{}`, db.CanUpdateAccessKeys, false},
	}
}

type projectPermissionFixture struct {
	store     db.Store
	router    http.Handler
	project   db.Project
	template  db.Template
	accessKey db.AccessKey
	pool      *taskServices.TaskPool
}

type permissionMatrixWorkflowDefinitions struct {
	pro_interfaces.WorkflowDefinitionService
}

type permissionMatrixWorkflowStore struct {
	db.WorkflowManager
	projectID int
}

func (store permissionMatrixWorkflowStore) GetWorkflowTemplate(projectID, workflowID int) (db.WorkflowTemplate, error) {
	if projectID != store.projectID || workflowID != 1 {
		return db.WorkflowTemplate{}, db.ErrNotFound
	}
	return db.WorkflowTemplate{ID: workflowID, ProjectID: projectID, Name: "matrix workflow"}, nil
}

func (permissionMatrixWorkflowDefinitions) List(int, db.RetrieveQueryParams, ...*db.User) ([]db.WorkflowTemplate, error) {
	return []db.WorkflowTemplate{}, nil
}

func newProjectPermissionFixture(t *testing.T) projectPermissionFixture {
	t.Helper()
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.Debugging = &util.DebuggingConfig{}
	project, err := store.CreateProject(db.Project{Name: "permission matrix"})
	require.NoError(t, err)
	accessKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: accessKey.ID, Name: "matrix repository", GitURL: "https://example.invalid/matrix.git", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "matrix template", Playbook: "site.yml"})
	require.NoError(t, err)
	pool := poolWithTasks()
	return projectPermissionFixture{
		store: store, project: project, template: template, accessKey: accessKey, pool: pool,
		router: Route(store, nil, permissionMatrixWorkflowStore{projectID: project.ID}, nil, pool, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, permissionMatrixWorkflowDefinitions{}, nil, nil, nil, metrics.NewMetrics(), nil, nil),
	}
}

func (f projectPermissionFixture) customActor(t *testing.T, name string, permissions db.ProjectUserPermission) string {
	t.Helper()
	role, err := f.store.CreateProjectRole(db.Role{
		ID: db.ProjectRoleID("matrix_" + name), Slug: "matrix_" + name, Name: "Matrix " + name,
		ProjectID: &f.project.ID, Permissions: permissions, Revision: 1,
	})
	require.NoError(t, err)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "matrix-" + name, Name: "Matrix " + name, Email: "matrix-" + name + "@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	apiKey := "matrix-" + name + "-key"
	_, err = f.store.CreateAPIToken(db.APIToken{ID: apiKey, UserID: user.ID, Name: "permission matrix"})
	require.NoError(t, err)
	return apiKey
}

func (f projectPermissionFixture) request(t *testing.T, route projectPermissionRoute, apiKey string) (response *httptest.ResponseRecorder) {
	t.Helper()
	path := strings.ReplaceAll(route.path, "{project_id}", fmt.Sprint(f.project.ID))
	body := strings.ReplaceAll(route.body, "%template_id%", fmt.Sprint(f.template.ID))
	body = strings.ReplaceAll(body, "%access_key_id%", fmt.Sprint(f.accessKey.ID))
	request := httptest.NewRequest(route.method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "store", f.store)
	request = helpers.SetContextValue(request, "task_pool", f.pool)
	response = httptest.NewRecorder()
	defer func() {
		if recover() != nil {
			// Route deliberately uses nil external services in this matrix. Reaching
			// the handler after its authorization middleware is the assertion here;
			// route-specific handler tests cover its service failure behaviour.
			response.Header().Set("X-Permission-Matrix-Handler-Reached", "true")
		}
	}()
	f.router.ServeHTTP(response, request)
	return response
}

func TestProjectPermissionCatalogRouteMatrix(t *testing.T) {
	definitions := map[db.ProjectUserPermission]bool{}
	for _, definition := range pro_interfaces.ProjectPermissionCatalog() {
		definitions[definition.Permission] = true
	}
	for _, route := range projectPermissionRoutes() {
		require.True(t, definitions[route.permission], "%s references a catalog permission", route.name)
	}

	fixture := newProjectPermissionFixture(t)
	for index, route := range projectPermissionRoutes() {
		t.Run(route.name, func(t *testing.T) {
			noAccess := fixture.customActor(t, fmt.Sprintf("none_%d", index), 0)
			readPermissions := resourceReadPermission(route.permission)
			if route.read {
				readPermissions = route.permission
			}
			readAccess := fixture.customActor(t, fmt.Sprintf("read_%d", index), db.CanReadProject|readPermissions)
			fullPermissions := db.ProjectOwner.GetPermissions() | fullPermissionsForRoute(route)
			fullAccess := fixture.customActor(t, fmt.Sprintf("full_%d", index), fullPermissions)

			denied := fixture.request(t, route, noAccess)
			if route.membershipRead() {
				assert.Equal(t, http.StatusOK, denied.Code, "%s remains visible to any project member", route.name)
			} else {
				assert.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, denied.Code, "%s must not leak or allow without %d", route.name, route.permission)
			}

			read := fixture.request(t, route, readAccess)
			if route.read {
				if route.name == "view inventories" {
					assert.NotEqual(t, http.StatusForbidden, read.Code, "%s must pass its view middleware: %s", route.name, read.Body.String())
				} else {
					assert.Equal(t, http.StatusOK, read.Code, "%s must be readable with its view permission: %s", route.name, read.Body.String())
				}
			} else {
				assert.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, read.Code, "%s must refuse a read-only member", route.name)
			}

			allowed := fixture.request(t, route, fullAccess)
			assert.NotEqual(t, http.StatusUnauthorized, allowed.Code, "%s must authenticate its full-action role", route.name)
			assert.True(t, allowed.Code != http.StatusForbidden || allowed.Header().Get("X-Permission-Matrix-Handler-Reached") == "true", "%s must pass authorization for its full-action role: %s", route.name, allowed.Body.String())
		})
	}
}

func TestTemplateGrantCannotUseProjectAccessKeysWhenStartingATask(t *testing.T) {
	fixture := newProjectPermissionFixture(t)
	role, err := fixture.store.CreateProjectRole(db.Role{
		ID: "template-task-grant", Slug: "template-task-grant", Name: "Template task grant",
		ProjectID: &fixture.project.ID, Revision: 1,
	})
	require.NoError(t, err)
	user, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "template-task-grant", Name: "Template task grant", Email: "template-task-grant@example.invalid",
	})
	require.NoError(t, err)
	_, err = fixture.store.CreateProjectUser(db.ProjectUser{
		ProjectID: fixture.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1,
	})
	require.NoError(t, err)
	_, err = fixture.store.CreateTemplateRole(db.TemplateRolePerm{
		ProjectID: fixture.project.ID, TemplateID: fixture.template.ID, RoleID: &role.ID,
		AllowedPermissions: db.CanReadTemplate | db.CanRunTemplate | db.CanEditTemplate,
		Revision:           1,
	})
	require.NoError(t, err)
	apiKey := "template-task-grant-key"
	_, err = fixture.store.CreateAPIToken(db.APIToken{ID: apiKey, UserID: user.ID, Name: "template task grant"})
	require.NoError(t, err)

	response := fixture.request(t, projectPermissionRoute{
		method: http.MethodPost,
		path:   "/api/project/{project_id}/tasks",
		body:   `{"template_id":%template_id%,"ssh_keys":[{"access_key_id":%access_key_id%,"hosts":["example.invalid"]}]}`,
	}, apiKey)

	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

func TestTemplateGrantRoutesAreLimitedToTheGrantedTemplate(t *testing.T) {
	fixture := newProjectPermissionFixture(t)
	otherTemplate, err := fixture.store.CreateTemplate(db.Template{
		ProjectID: fixture.project.ID, RepositoryID: fixture.template.RepositoryID,
		Name: "other template", Playbook: "site.yml",
	})
	require.NoError(t, err)
	createUserWithGrant := func(name string, grant db.TemplatePermission) string {
		t.Helper()
		role, createErr := fixture.store.CreateProjectRole(db.Role{
			ID: db.ProjectRoleID(name), Slug: name, Name: name,
			ProjectID: &fixture.project.ID, Revision: 1,
		})
		require.NoError(t, createErr)
		user, createErr := fixture.store.CreateUserWithoutPassword(db.User{
			Username: name, Name: name, Email: name + "@example.invalid",
		})
		require.NoError(t, createErr)
		_, createErr = fixture.store.CreateProjectUser(db.ProjectUser{
			ProjectID: fixture.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1,
		})
		require.NoError(t, createErr)
		_, createErr = fixture.store.CreateTemplateRole(db.TemplateRolePerm{
			ProjectID: fixture.project.ID, TemplateID: fixture.template.ID, RoleID: &role.ID,
			AllowedPermissions: grant, Revision: 1,
		})
		require.NoError(t, createErr)
		apiKey := name + "-key"
		_, createErr = fixture.store.CreateAPIToken(db.APIToken{ID: apiKey, UserID: user.ID, Name: name})
		require.NoError(t, createErr)
		return apiKey
	}

	editKey := createUserWithGrant("template-route-editor", db.CanEditTemplate)
	assert.NotEqual(t, http.StatusForbidden, fixture.request(t, projectPermissionRoute{
		method: http.MethodPut,
		path:   fmt.Sprintf("/api/project/{project_id}/templates/%d/description", fixture.template.ID),
		body:   `{"description":"limited edit"}`,
	}, editKey).Code)
	assert.Equal(t, http.StatusForbidden, fixture.request(t, projectPermissionRoute{
		method: http.MethodPut,
		path:   fmt.Sprintf("/api/project/{project_id}/templates/%d/description", otherTemplate.ID),
		body:   `{"description":"forbidden edit"}`,
	}, editKey).Code)

	readKey := createUserWithGrant("template-route-reader", db.CanReadTemplate)
	assert.Equal(t, http.StatusOK, fixture.request(t, projectPermissionRoute{
		method: http.MethodGet,
		path:   fmt.Sprintf("/api/project/{project_id}/templates/%d", fixture.template.ID),
	}, readKey).Code)
	assert.Equal(t, http.StatusNotFound, fixture.request(t, projectPermissionRoute{
		method: http.MethodGet,
		path:   fmt.Sprintf("/api/project/{project_id}/templates/%d", otherTemplate.ID),
	}, readKey).Code)
}

func TestEffectiveProjectPermissionsRouteEnforcesMembershipVisibility(t *testing.T) {
	fixture := newProjectPermissionFixture(t)
	managerRole, err := fixture.store.CreateProjectRole(db.Role{
		ID: "effective-route-manager", Slug: "effective-route-manager", Name: "Effective route manager",
		ProjectID: &fixture.project.ID, Permissions: db.CanManageProjectUsers, Revision: 1,
	})
	require.NoError(t, err)
	memberRole, err := fixture.store.CreateProjectRole(db.Role{
		ID: "effective-route-member", Slug: "effective-route-member", Name: "Effective route member",
		ProjectID: &fixture.project.ID, Permissions: db.CanViewTemplates, Revision: 1,
	})
	require.NoError(t, err)
	createMember := func(name string, role db.ProjectRoleID) (db.User, string) {
		t.Helper()
		user, createErr := fixture.store.CreateUserWithoutPassword(db.User{
			Username: name, Name: name, Email: name + "@example.invalid",
		})
		require.NoError(t, createErr)
		_, createErr = fixture.store.CreateProjectUser(db.ProjectUser{
			ProjectID: fixture.project.ID, UserID: user.ID, RoleID: &role, Revision: 1,
		})
		require.NoError(t, createErr)
		apiKey := name + "-key"
		_, createErr = fixture.store.CreateAPIToken(db.APIToken{ID: apiKey, UserID: user.ID, Name: name})
		require.NoError(t, createErr)
		return user, apiKey
	}
	_, managerKey := createMember("effective-route-manager", managerRole.ID)
	member, memberKey := createMember("effective-route-member", memberRole.ID)
	_, plainKey := createMember("effective-route-plain", memberRole.ID)
	nonMember, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "effective-route-outsider", Name: "Effective route outsider", Email: "effective-route-outsider@example.invalid",
	})
	require.NoError(t, err)

	request := func(apiKey string, userID int) *httptest.ResponseRecorder {
		return fixture.request(t, projectPermissionRoute{
			method: http.MethodGet,
			path:   fmt.Sprintf("/api/project/{project_id}/users/%d/effective-permissions", userID),
		}, apiKey)
	}

	assert.Equal(t, http.StatusOK, request(memberKey, member.ID).Code)
	assert.Equal(t, http.StatusOK, request(managerKey, member.ID).Code)
	assert.Equal(t, http.StatusForbidden, request(plainKey, member.ID).Code)
	assert.Equal(t, http.StatusNotFound, request(managerKey, nonMember.ID).Code)
}

// TestBuiltInRolesMatchLegacyRouteMatrix records the before/after compatibility
// contract at the same route granularity as the custom-role matrix.  D2 is the
// sole deliberate change: guests no longer approve workflows.
func TestBuiltInRolesMatchLegacyRouteMatrix(t *testing.T) {
	roles := []struct {
		name     string
		role     db.ProjectUserRole
		legacy   db.ProjectUserPermission
		d2Denied db.ProjectUserPermission
	}{
		{"owner", db.ProjectOwner, db.ProjectOwner.GetPermissions(), 0},
		{"manager", db.ProjectManager, db.ProjectManager.GetPermissions(), 0},
		{"task runner", db.ProjectTaskRunner, db.ProjectTaskRunner.GetPermissions(), 0},
		{"guest", db.ProjectGuest, db.ProjectGuest.GetPermissions() | db.CanApproveWorkflows, db.CanApproveWorkflows},
	}
	for _, role := range roles {
		t.Run(role.name, func(t *testing.T) {
			for _, route := range projectPermissionRoutes() {
				t.Run(route.name, func(t *testing.T) {
					before := role.legacy.Can(route.permission)
					if role.d2Denied.Can(route.permission) {
						before = false
					}
					assert.Equal(t, before, role.role.GetPermissions().Can(route.permission), "%s", route.key())
				})
			}
		})
	}
}

// TestCustomRolesPreserveLegacyResourceReadAccess covers the Phase 4 routes
// whose old read_project/coarse-view boundary was replaced by a fine view
// action. The migration projection is deliberately exercised with every
// combination of the old read, run, view, and manage bits, rather than only
// the four built-in masks.
func TestCustomRolesPreserveLegacyResourceReadAccess(t *testing.T) {
	type readRoute struct {
		name string
		old  db.ProjectUserPermission
		new  db.ProjectUserPermission
	}
	readRoutes := []readRoute{
		{"keys and secret storages", db.CanReadProject, db.CanReadProject | db.CanViewAccessKeys},
		{"environment", db.CanReadProject, db.CanReadProject | db.CanViewEnvironments},
		{"inventory hosts and host configurations", db.CanReadProject | db.CanViewProjectResources, db.CanReadProject | db.CanViewInventories},
		{"schedules", db.CanReadProject, db.CanReadProject | db.CanViewSchedules},
		{"integrations and aliases", db.CanReadProject, db.CanReadProject | db.CanViewIntegrations},
		{"repository details, refs, branches, and playbooks", db.CanReadProject | db.CanViewProjectResources, db.CanReadProject | db.CanViewRepositories},
		{"view details", db.CanReadProject, db.CanReadProject | db.CanViewViews},
	}
	legacyMasks := []db.ProjectUserPermission{
		0,
		db.CanReadProject,
		db.CanRunProjectTasks,
		db.CanViewProjectResources,
		db.CanManageProjectResources,
		db.CanReadProject | db.CanRunProjectTasks,
		db.CanReadProject | db.CanViewProjectResources,
		db.CanReadProject | db.CanManageProjectResources,
		db.CanRunProjectTasks | db.CanViewProjectResources,
		db.CanRunProjectTasks | db.CanManageProjectResources,
		db.CanViewProjectResources | db.CanManageProjectResources,
		db.CanReadProject | db.CanRunProjectTasks | db.CanViewProjectResources,
		db.CanReadProject | db.CanRunProjectTasks | db.CanManageProjectResources,
		db.CanReadProject | db.CanViewProjectResources | db.CanManageProjectResources,
		db.CanRunProjectTasks | db.CanViewProjectResources | db.CanManageProjectResources,
		db.CanReadProject | db.CanRunProjectTasks | db.CanViewProjectResources | db.CanManageProjectResources,
	}
	migrate := func(permissions db.ProjectUserPermission) db.ProjectUserPermission {
		migrated := permissions &^ (db.CanViewProjectResources | db.CanManageProjectResources)
		if permissions.Can(db.CanReadProject) {
			migrated |= db.CanViewTemplates | db.CanViewAccessKeys |
				db.CanViewEnvironments | db.CanViewSchedules | db.CanViewIntegrations | db.CanViewViews
		}
		if permissions.Can(db.CanViewProjectResources) {
			migrated |= db.AllProjectResourceViewPermissions
		}
		if permissions.Can(db.CanManageProjectResources) {
			migrated |= db.AllProjectResourceMutationPermissions
		}
		return migrated
	}

	for _, permissions := range legacyMasks {
		t.Run(fmt.Sprintf("mask_%d", permissions), func(t *testing.T) {
			for _, route := range readRoutes {
				t.Run(route.name, func(t *testing.T) {
					assert.Equal(t, permissions.Can(route.old), migrate(permissions).Can(route.new))
				})
			}
		})
	}
}

func resourceReadPermission(permission db.ProjectUserPermission) db.ProjectUserPermission {
	for _, definition := range pro_interfaces.ProjectPermissionCatalog() {
		if definition.Permission == permission && definition.Group != "" {
			for _, candidate := range pro_interfaces.ProjectPermissionCatalog() {
				if candidate.Group == definition.Group && candidate.Action == "view" {
					return candidate.Permission
				}
			}
		}
	}
	return 0
}

func fullPermissionsForRoute(route projectPermissionRoute) db.ProjectUserPermission {
	if route.permission == db.CanUseAccessKeys {
		return route.permission | db.CanRunProjectTasks
	}
	return route.permission
}

// additionalProjectPermissionRouteCoverage lists guarded project routes whose
// authorization is compound, workflow-policy scoped, or intentionally retains
// the legacy all-resource-management boundary. They are not representatives of
// one catalog action, but remain explicit router coverage entries.
func additionalProjectPermissionRouteCoverage() []string {
	return strings.Split(`DELETE /api/project/{project_id}
DELETE /api/project/{project_id}/deployment-windows
DELETE /api/project/{project_id}/integrations/aliases/{alias_id}
DELETE /api/project/{project_id}/keys/{key_id}
DELETE /api/project/{project_id}/notification-governance/destinations/{destination_id}
DELETE /api/project/{project_id}/notification-governance/rules/{rule_id}
DELETE /api/project/{project_id}/repositories/{repository_id}
DELETE /api/project/{project_id}/roles/{role_id}
DELETE /api/project/{project_id}/runners/{runner_id}
DELETE /api/project/{project_id}/runners/{runner_id}/cache
DELETE /api/project/{project_id}/task_groups/{group_id}
DELETE /api/project/{project_id}/templates/{template_id}
DELETE /api/project/{project_id}/templates/{template_id}/perms/{perm_id}
GET /api/project/{project_id}/backup
GET /api/project/{project_id}/deployment-windows
GET /api/project/{project_id}/deployment-windows/history
GET /api/project/{project_id}/environment/{environment_id}
GET /api/project/{project_id}/environment/{environment_id}/refs
GET /api/project/{project_id}/events
GET /api/project/{project_id}/events/last
GET /api/project/{project_id}/granted-credentials
GET /api/project/{project_id}/hosts
GET /api/project/{project_id}/hosts/tasks
GET /api/project/{project_id}/host_configs/{host_config_id}
GET /api/project/{project_id}/integrations/aliases
GET /api/project/{project_id}/integrations/{integration_id}
GET /api/project/{project_id}/integrations/{integration_id}/aliases
GET /api/project/{project_id}/integrations/{integration_id}/matchers
GET /api/project/{project_id}/integrations/{integration_id}/matchers/{matcher_id}
GET /api/project/{project_id}/integrations/{integration_id}/matchers/{matcher_id}/refs
GET /api/project/{project_id}/integrations/{integration_id}/refs
GET /api/project/{project_id}/integrations/{integration_id}/values
GET /api/project/{project_id}/integrations/{integration_id}/values/{value_id}
GET /api/project/{project_id}/integrations/{integration_id}/values/{value_id}/refs
GET /api/project/{project_id}/inventory
GET /api/project/{project_id}/inventory/{inventory_id}
GET /api/project/{project_id}/inventory/{inventory_id}/hosts/snapshots
GET /api/project/{project_id}/inventory/{inventory_id}/refs
GET /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases
GET /api/project/{project_id}/inventory/{inventory_id}/terraform/aliases/{alias_id}
GET /api/project/{project_id}/inventory/{inventory_id}/terraform/states
GET /api/project/{project_id}/inventory/{inventory_id}/terraform/states/latest
GET /api/project/{project_id}/inventory/{inventory_id}/terraform/states/{state_id}
GET /api/project/{project_id}/keys/{key_id}
GET /api/project/{project_id}/keys/{key_id}/refs
GET /api/project/{project_id}/notification-governance/deliveries
GET /api/project/{project_id}/notification-governance/destinations
GET /api/project/{project_id}/notification-governance/destinations/{destination_id}
GET /api/project/{project_id}/notification-governance/events
GET /api/project/{project_id}/notification-governance/rules
GET /api/project/{project_id}/policy-guardrails
GET /api/project/{project_id}/policy-guardrails/diff
GET /api/project/{project_id}/policy-guardrails/evaluations
GET /api/project/{project_id}/policy-guardrails/revisions
GET /api/project/{project_id}/repositories/{repository_id}
GET /api/project/{project_id}/repositories/{repository_id}/branches
GET /api/project/{project_id}/repositories/{repository_id}/playbooks
GET /api/project/{project_id}/repositories/{repository_id}/refs
GET /api/project/{project_id}/roles/permissions
GET /api/project/{project_id}/roles/{role_id}
GET /api/project/{project_id}/runner_tags
GET /api/project/{project_id}/runners
GET /api/project/{project_id}/runners/{runner_id}
GET /api/project/{project_id}/runners/{runner_id}/health
GET /api/project/{project_id}/runners/{runner_id}/history
GET /api/project/{project_id}/schedules/{schedule_id}
GET /api/project/{project_id}/secret_storages/{storage_id}
GET /api/project/{project_id}/secret_storages/{storage_id}/browse
GET /api/project/{project_id}/secret_storages/{storage_id}/refs
GET /api/project/{project_id}/secret_storages/{storage_id}/sync/history
GET /api/project/{project_id}/task_groups/runners
GET /api/project/{project_id}/task_groups/{group_id}
GET /api/project/{project_id}/templates/{template_id}/perms
GET /api/project/{project_id}/templates/{template_id}/perms/catalog
GET /api/project/{project_id}/templates/{template_id}/perms/{perm_id}
GET /api/project/{project_id}/templates/{template_id}/versions
GET /api/project/{project_id}/users
GET /api/project/{project_id}/views/{view_id}
GET /api/project/{project_id}/workflow-artifact-retention
POST /api/project/{project_id}/deployment-windows/preview
POST /api/project/{project_id}/integrations/aliases
POST /api/project/{project_id}/keys/generate
POST /api/project/{project_id}/keys/{key_id}/rotate
POST /api/project/{project_id}/notification-governance/deliveries/{delivery_id}/retry
POST /api/project/{project_id}/notification-governance/destinations
POST /api/project/{project_id}/notification-governance/destinations/{destination_id}/pause
POST /api/project/{project_id}/notification-governance/destinations/{destination_id}/resume
POST /api/project/{project_id}/notification-governance/destinations/{destination_id}/test
POST /api/project/{project_id}/notification-governance/routing/preview
POST /api/project/{project_id}/notification-governance/rules
POST /api/project/{project_id}/notifications/test
POST /api/project/{project_id}/policy-guardrails/impact
POST /api/project/{project_id}/policy-guardrails/publish
POST /api/project/{project_id}/policy-guardrails/rollback
POST /api/project/{project_id}/policy-guardrails/test
POST /api/project/{project_id}/policy-guardrails/validate
POST /api/project/{project_id}/roles
POST /api/project/{project_id}/roles/{role_id}
POST /api/project/{project_id}/runners
POST /api/project/{project_id}/runners/{runner_id}
POST /api/project/{project_id}/runners/{runner_id}/active
POST /api/project/{project_id}/runners/{runner_id}/registration-token
POST /api/project/{project_id}/schedules/validate
POST /api/project/{project_id}/templates/{template_id}/cross-project-grants
POST /api/project/{project_id}/templates/{template_id}/perms
POST /api/project/{project_id}/templates/{template_id}/stop_all_tasks
POST /api/project/{project_id}/templates/{template_id}/versions
POST /api/project/{project_id}/workflows/validate
PUT /api/project/{project_id}/deployment-windows
PUT /api/project/{project_id}/keys/{key_id}
PUT /api/project/{project_id}/notification-governance/destinations/{destination_id}
PUT /api/project/{project_id}/notification-governance/rules/{rule_id}
PUT /api/project/{project_id}/policy-guardrails/draft
PUT /api/project/{project_id}/repositories/{repository_id}
PUT /api/project/{project_id}/roles/{role_id}
PUT /api/project/{project_id}/runners/{runner_id}
PUT /api/project/{project_id}/task_groups/{group_id}
PUT /api/project/{project_id}/templates/{template_id}
PUT /api/project/{project_id}/templates/{template_id}/description
PUT /api/project/{project_id}/templates/{template_id}/perms/{perm_id}
PUT /api/project/{project_id}/workflow-artifact-retention`, "\n")
}

// TestProjectPermissionRoutesCoverGuardedRouterRoutes probes the real mux
// router. A route that denies a project member for a catalog permission must
// have an explicit entry above; new guarded endpoints cannot silently escape
// the matrix.
func TestProjectPermissionRoutesCoverGuardedRouterRoutes(t *testing.T) {
	fixture := newProjectPermissionFixture(t)
	plainKey := fixture.customActor(t, "probe", 0)
	covered := map[string]bool{}
	for _, route := range projectPermissionRoutes() {
		covered[route.key()] = true
	}
	for _, route := range additionalProjectPermissionRouteCoverage() {
		covered[route] = true
	}

	guarded := map[string]bool{}
	err := fixture.router.(*mux.Router).Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := route.GetPathTemplate()
		if err != nil || !strings.HasPrefix(template, "/api/project/{project_id}") {
			return nil
		}
		methods, err := route.GetMethods()
		if err != nil {
			return nil
		}
		path := strings.ReplaceAll(template, "{project_id}", fmt.Sprint(fixture.project.ID))
		path = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "1")
		for _, method := range methods {
			if method == http.MethodHead || method == http.MethodOptions {
				continue
			}
			probe := projectPermissionRoute{method: method, path: path}
			response := fixture.request(t, probe, plainKey)
			if response.Code == http.StatusForbidden {
				guarded[method+" "+template] = true
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, guarded, "the router walk found no guarded project routes")

	missing := make([]string, 0)
	for guardedRoute := range guarded {
		if !covered[guardedRoute] {
			missing = append(missing, guardedRoute)
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing, "guarded project routes missing from projectPermissionRoutes")
}
