package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectUpdatePreservesAndClearsSSHBindingFieldsByPresence(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "presence"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeySSH, Name: "key"})
	require.NoError(t, err)
	project.DefaultSSHKeys = db.SSHKeyBindings{{AccessKeyID: key.ID, Hosts: []string{"default.example.test"}}}
	project.AlwaysSSHKeys = db.SSHKeyBindings{{AccessKeyID: key.ID, Hosts: []string{"always.example.test"}}}
	require.NoError(t, store.UpdateProject(project))
	controller := &ProjectController{ProjectService: server.NewProjectService(store, store)}
	update := func(body string) db.Project {
		req := httptest.NewRequest(http.MethodPut, "/project", strings.NewReader(body))
		req = helpers.SetContextValue(req, "project", project)
		req = helpers.SetContextValue(req, "store", store)
		w := httptest.NewRecorder()
		controller.UpdateProject(w, req)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		loaded, loadErr := store.GetProject(project.ID)
		require.NoError(t, loadErr)
		project = loaded
		return loaded
	}
	loaded := update(`{"id":1,"name":"presence"}`)
	assert.Len(t, loaded.DefaultSSHKeys, 1)
	assert.Len(t, loaded.AlwaysSSHKeys, 1)
	loaded = update(`{"id":1,"name":"presence","default_ssh_keys":null}`)
	assert.Nil(t, loaded.DefaultSSHKeys)
	assert.Len(t, loaded.AlwaysSSHKeys, 1)
	loaded = update(`{"id":1,"name":"presence","always_ssh_keys":[]}`)
	assert.NotNil(t, loaded.AlwaysSSHKeys)
	assert.Empty(t, loaded.AlwaysSSHKeys)
}

func TestTemplateUpdatePreservesAndClearsSSHBindingsByPresence(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, _ := store.CreateProject(db.Project{Name: "template presence"})
	key, _ := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	repo, _ := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo", GitURL: "https://example.test/r.git", GitBranch: "main"})
	ssh, _ := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeySSH, Name: "ssh"})
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repo.ID, Name: "deploy", Playbook: "site.yml", SSHKeys: db.SSHKeyBindings{{AccessKeyID: ssh.ID, Hosts: []string{"git.example.test"}}}})
	require.NoError(t, err)
	update := func(mode string) db.Template {
		payload, marshalErr := json.Marshal(template)
		require.NoError(t, marshalErr)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &fields))
		switch mode {
		case "omit":
			delete(fields, "ssh_keys")
		case "null":
			fields["ssh_keys"] = json.RawMessage("null")
		case "empty":
			fields["ssh_keys"] = json.RawMessage("[]")
		}
		body, marshalErr := json.Marshal(fields)
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPut, "/templates", strings.NewReader(string(body)))
		req = helpers.SetContextValue(req, "project", project)
		req = helpers.SetContextValue(req, "template", template)
		req = helpers.SetContextValue(req, "store", store)
		req = helpers.SetContextValue(req, "user", &db.User{ID: 1})
		req = helpers.SetContextValue(req, "log_writer", executorImageLogWriter{})
		w := httptest.NewRecorder()
		updateTemplate(w, req, nil)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		loaded, loadErr := store.GetTemplate(project.ID, template.ID)
		require.NoError(t, loadErr)
		template = loaded
		return loaded
	}
	assert.Len(t, update("omit").SSHKeys, 1)
	assert.Nil(t, update("null").SSHKeys)
	assert.NotNil(t, update("empty").SSHKeys)
	assert.Empty(t, template.SSHKeys)
}

func TestTemplateUpdateRequiresAccessibleManagedTaskGroups(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "task group authorization"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo", GitURL: "https://example.test/r.git", GitBranch: "main"})
	require.NoError(t, err)
	group, err := store.CreateTaskGroup(db.TaskGroup{
		ProjectID: project.ID, Name: "production", MaxParallelTasks: 1,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repo.ID, Name: "deploy", Playbook: "site.yml",
		TaskGroups: db.TaskGroupBindings{group.ID},
	})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "template-editor", Name: "Template editor", Email: "template-editor@example.test"})
	require.NoError(t, err)

	update := func(fields map[string]json.RawMessage) *httptest.ResponseRecorder {
		body, marshalErr := json.Marshal(fields)
		require.NoError(t, marshalErr)
		req := httptest.NewRequest(http.MethodPut, "/templates", strings.NewReader(string(body)))
		req = helpers.SetContextValue(req, "project", project)
		req = helpers.SetContextValue(req, "template", template)
		req = helpers.SetContextValue(req, "store", store)
		req = helpers.SetContextValue(req, "user", &user)
		req = helpers.SetContextValue(req, "log_writer", executorImageLogWriter{})
		response := httptest.NewRecorder()
		updateTemplate(response, req, nil)
		return response
	}
	fields := make(map[string]json.RawMessage)
	encoded, err := json.Marshal(template)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &fields))

	delete(fields, "task_groups")
	assert.Equal(t, http.StatusNoContent, update(fields).Code)
	loaded, err := store.GetTemplate(project.ID, template.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskGroupBindings{group.ID}, loaded.TaskGroups)

	fields["task_groups"] = json.RawMessage("[]")
	assert.Equal(t, http.StatusNoContent, update(fields).Code)

	fields["task_groups"] = json.RawMessage("[999999]")
	assert.Equal(t, http.StatusBadRequest, update(fields).Code)
}

func TestTemplateUpdateReportsConflictingTaskGroupRunnerPolicies(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "task group runner conflict"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo", GitURL: "https://example.test/r.git", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repo.ID, Name: "deploy", Playbook: "site.yml"})
	require.NoError(t, err)
	runnerA, err := store.CreateRunner(db.Runner{ProjectID: &project.ID, Name: "runner-a", Active: true, Token: db.GenerateRunnerToken()})
	require.NoError(t, err)
	runnerB, err := store.CreateRunner(db.Runner{ProjectID: &project.ID, Name: "runner-b", Active: true, Token: db.GenerateRunnerToken()})
	require.NoError(t, err)
	groupA, err := store.CreateTaskGroup(db.TaskGroup{ProjectID: project.ID, Name: "runner-a-only", MaxParallelTasks: 1, RunnerIDs: db.TaskGroupBindings{runnerA.ID}})
	require.NoError(t, err)
	groupB, err := store.CreateTaskGroup(db.TaskGroup{ProjectID: project.ID, Name: "runner-b-only", MaxParallelTasks: 1, RunnerIDs: db.TaskGroupBindings{runnerB.ID}})
	require.NoError(t, err)

	payload, err := json.Marshal(template)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	fields["task_groups"] = json.RawMessage("[" + strconv.Itoa(groupA.ID) + "," + strconv.Itoa(groupB.ID) + "]")
	body, err := json.Marshal(fields)
	require.NoError(t, err)
	user := &db.User{ID: 1}
	req := httptest.NewRequest(http.MethodPut, "/templates", strings.NewReader(string(body)))
	req = helpers.SetContextValue(req, "project", project)
	req = helpers.SetContextValue(req, "template", template)
	req = helpers.SetContextValue(req, "store", store)
	req = helpers.SetContextValue(req, "user", user)
	req = helpers.SetContextValue(req, "log_writer", executorImageLogWriter{})
	response := httptest.NewRecorder()

	updateTemplate(response, req, nil)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "task group runner requirements have no common runner")
}

func TestProjectUpdateRejectsAlwaysBindingConflictWithExistingTemplate(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "conflict"})
	require.NoError(t, err)
	repositoryKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: repositoryKey.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main"})
	require.NoError(t, err)
	templateKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeySSH, Name: "template"})
	require.NoError(t, err)
	alwaysKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeySSH, Name: "always"})
	require.NoError(t, err)
	_, err = store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "site.yml", SSHKeys: db.SSHKeyBindings{{AccessKeyID: templateKey.ID, Hosts: []string{"git.example.test"}}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/project", strings.NewReader(`{"id":1,"name":"conflict","always_ssh_keys":[{"access_key_id":`+strconv.Itoa(alwaysKey.ID)+`,"hosts":["git.example.test"]}]}`))
	req = helpers.SetContextValue(req, "project", project)
	req = helpers.SetContextValue(req, "store", store)
	w := httptest.NewRecorder()
	(&ProjectController{ProjectService: server.NewProjectService(store, store)}).UpdateProject(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "deploy")
	loaded, err := store.GetProject(project.ID)
	require.NoError(t, err)
	assert.Nil(t, loaded.AlwaysSSHKeys)
}
