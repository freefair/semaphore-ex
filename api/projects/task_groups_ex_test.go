package projects

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type taskGroupManagerStub struct {
	group   db.TaskGroup
	created []db.TaskGroup
	updated []db.TaskGroup
}

type taskGroupRunnerCatalogStub struct {
	project []db.Runner
	global  []db.Runner
}

type taskGroupProjectCatalogStub struct {
	projects []db.Project
}

func (s taskGroupProjectCatalogStub) GetProjects(_ int) ([]db.Project, error) {
	return s.projects, nil
}

func (s taskGroupProjectCatalogStub) GetAllProjects() ([]db.Project, error) {
	return s.projects, nil
}

func (s taskGroupRunnerCatalogStub) GetRunners(_ int, _ bool, _ db.RunnerTagFilterMode, _ *string) ([]db.Runner, error) {
	return s.project, nil
}

func (s taskGroupRunnerCatalogStub) GetAllRunners(_ bool, _ bool, _ db.RunnerTagFilterMode, _ *string) ([]db.Runner, error) {
	return s.global, nil
}

func (s *taskGroupManagerStub) GetTaskGroups(_ int) ([]db.TaskGroup, error) {
	return []db.TaskGroup{s.group}, nil
}

func (s *taskGroupManagerStub) GetTaskGroup(_ int, groupID int) (db.TaskGroup, error) {
	if groupID != s.group.ID {
		return db.TaskGroup{}, db.ErrNotFound
	}
	return s.group, nil
}

func (s *taskGroupManagerStub) CreateTaskGroup(group db.TaskGroup) (db.TaskGroup, error) {
	s.created = append(s.created, group)
	group.ID = 99
	return group, nil
}

func (s *taskGroupManagerStub) UpdateTaskGroup(group db.TaskGroup) (db.TaskGroup, error) {
	s.updated = append(s.updated, group)
	return group, nil
}

func (s *taskGroupManagerStub) DeleteTaskGroup(_, _, _ int) error { return nil }

func (s *taskGroupManagerStub) ResolveTaskGroups(_ int, _ db.TaskGroupBindings) ([]db.TaskGroup, error) {
	return nil, nil
}

func taskGroupRequest(method, body string, project db.Project, permissions db.ProjectUserPermission) *http.Request {
	req := httptest.NewRequest(method, "/api/project/7/task_groups", strings.NewReader(body))
	req = helpers.SetContextValue(req, "project", project)
	req = helpers.SetContextValue(req, "permissions", permissions)
	req = helpers.SetContextValue(req, "user", &db.User{ID: 17})
	return req
}

func configuredTaskGroupController(manager *taskGroupManagerStub, visible ...int) *TaskGroupController {
	projects := make([]db.Project, 0, len(visible))
	for _, id := range visible {
		projects = append(projects, db.Project{ID: id})
	}
	controller := NewTaskGroupController(manager)
	controller.ConfigureProjectCatalog(taskGroupProjectCatalogStub{projects: projects})
	return controller
}

func TestTaskGroupCreateRequiresSharePermissionForCrossProjectGrant(t *testing.T) {
	manager := &taskGroupManagerStub{}
	controller := configuredTaskGroupController(manager, 8)
	project := db.Project{ID: 7}
	body := `{"project_id":99,"name":"deploy","max_parallel_tasks":1,"shared_project_ids":[8]}`

	denied := httptest.NewRecorder()
	controller.CreateTaskGroup(denied, taskGroupRequest(http.MethodPost, body, project, db.CanCreateTaskGroups))
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Empty(t, manager.created)

	allowed := httptest.NewRecorder()
	controller.CreateTaskGroup(allowed, taskGroupRequest(http.MethodPost, body, project, db.CanCreateTaskGroups|db.CanShareTaskGroups))
	require.Equal(t, http.StatusCreated, allowed.Code, allowed.Body.String())
	require.Len(t, manager.created, 1)
	assert.Equal(t, project.ID, manager.created[0].ProjectID)
	assert.Zero(t, manager.created[0].ID)
}

func TestTaskGroupUpdateRequiresSharePermissionWhenGrantChanges(t *testing.T) {
	manager := &taskGroupManagerStub{group: db.TaskGroup{ID: 3, ProjectID: 7, SharedProjectIDs: db.TaskGroupBindings{8}}}
	controller := configuredTaskGroupController(manager, 9)
	project := db.Project{ID: 7}
	body := `{"id":3,"name":"deploy","max_parallel_tasks":1,"shared_project_ids":[9]}`

	denied := httptest.NewRecorder()
	req := mux.SetURLVars(taskGroupRequest(http.MethodPut, body, project, db.CanUpdateTaskGroups), map[string]string{"group_id": "3"})
	controller.UpdateTaskGroup(denied, req)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Empty(t, manager.updated)

	allowed := httptest.NewRecorder()
	req = mux.SetURLVars(taskGroupRequest(http.MethodPut, body, project, db.CanUpdateTaskGroups|db.CanShareTaskGroups), map[string]string{"group_id": "3"})
	controller.UpdateTaskGroup(allowed, req)
	require.Equal(t, http.StatusNoContent, allowed.Code, allowed.Body.String())
	require.Len(t, manager.updated, 1)
	assert.Equal(t, project.ID, manager.updated[0].ProjectID)
	assert.Equal(t, 3, manager.updated[0].ID)
}

func TestTaskGroupUpdateRequiresSharePermissionWhenGrantIsRemoved(t *testing.T) {
	manager := &taskGroupManagerStub{group: db.TaskGroup{ID: 3, ProjectID: 7, SharedProjectIDs: db.TaskGroupBindings{8}}}
	controller := configuredTaskGroupController(manager)
	project := db.Project{ID: 7}
	body := `{"id":3,"name":"deploy","max_parallel_tasks":1,"shared_project_ids":[]}`

	denied := httptest.NewRecorder()
	req := mux.SetURLVars(taskGroupRequest(http.MethodPut, body, project, db.CanUpdateTaskGroups), map[string]string{"group_id": "3"})
	controller.UpdateTaskGroup(denied, req)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Empty(t, manager.updated)
}

func TestTaskGroupCreateRejectsInvisibleShareTarget(t *testing.T) {
	manager := &taskGroupManagerStub{}
	controller := configuredTaskGroupController(manager, 8)
	project := db.Project{ID: 7}
	body := `{"name":"deploy","max_parallel_tasks":1,"shared_project_ids":[9]}`
	response := httptest.NewRecorder()

	controller.CreateTaskGroup(response, taskGroupRequest(http.MethodPost, body, project, db.CanCreateTaskGroups|db.CanShareTaskGroups))

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, manager.created)
}

func TestTaskGroupRunnerCatalogExposesOnlySafeMetadata(t *testing.T) {
	projectID := 7
	secretWebhook := "https://runner.example.test/secret-webhook"
	publicKey := "ssh-ed25519 sensitive-runner-key"
	controller := NewTaskGroupController(nil, taskGroupRunnerCatalogStub{
		project: []db.Runner{{
			ID: 2, Name: "project runner", ProjectID: &projectID, Tags: []string{"deploy"},
			Token: "runner-token", Webhook: secretWebhook, PublicKey: &publicKey,
		}},
		global: []db.Runner{{ID: 1, Name: "global runner", Tags: []string{"shared"}}},
	})
	req := taskGroupRequest(http.MethodGet, "", db.Project{ID: projectID}, db.CanReadTaskGroups)
	response := httptest.NewRecorder()

	controller.GetTaskGroupRunners(response, req)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "runner-token")
	assert.NotContains(t, response.Body.String(), secretWebhook)
	assert.NotContains(t, response.Body.String(), publicKey)
	assert.JSONEq(t, `[
		{"id":1,"name":"global runner","project_id":null,"tags":["shared"]},
		{"id":2,"name":"project runner","project_id":7,"tags":["deploy"]}
	]`, response.Body.String())
}
