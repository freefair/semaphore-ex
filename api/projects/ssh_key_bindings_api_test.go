package projects

import (
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

func TestSSHKeyBindingHandlersRejectUnavailableKeys(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "bindings"})
	require.NoError(t, err)
	nonSSH, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "password", Type: db.AccessKeyLoginPassword})
	require.NoError(t, err)
	foreignProject, err := store.CreateProject(db.Project{Name: "foreign"})
	require.NoError(t, err)
	foreignSSH, err := store.CreateAccessKey(db.AccessKey{ProjectID: &foreignProject.ID, Name: "foreign", Type: db.AccessKeySSH})
	require.NoError(t, err)
	repositoryKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repo", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: repositoryKey.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "site.yml"})
	require.NoError(t, err)

	for _, keyID := range []int{nonSSH.ID, foreignSSH.ID, 999_999} {
		body := `[{"access_key_id":` + strconv.Itoa(keyID) + `,"hosts":["git.example.test"]}]`
		t.Run("project", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/project", strings.NewReader(`{"id":`+strconv.Itoa(project.ID)+`,"default_ssh_keys":`+body+`}`))
			req = helpers.SetContextValue(req, "project", project)
			req = helpers.SetContextValue(req, "store", store)
			w := httptest.NewRecorder()
			(&ProjectController{}).UpdateProject(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
		t.Run("template", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/templates", strings.NewReader(`{"repository_id":`+strconv.Itoa(repository.ID)+`,"name":"bad","playbook":"site.yml","ssh_keys":`+body+`}`))
			req = helpers.SetContextValue(req, "project", project)
			req = helpers.SetContextValue(req, "store", store)
			w := httptest.NewRecorder()
			addTemplate(w, req, nil)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
		t.Run("task", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"template_id":`+strconv.Itoa(template.ID)+`,"ssh_keys":`+body+`}`))
			req = helpers.SetContextValue(req, "project", project)
			req = helpers.SetContextValue(req, "user", &db.User{ID: 1})
			req = helpers.SetContextValue(req, "permissions", db.CanManageProjectResources)
			req = helpers.SetContextValue(req, "store", store)
			req = helpers.SetContextValue(req, "task", db.Task{TemplateID: template.ID, SSHKeys: db.SSHKeyBindings{{AccessKeyID: keyID, Hosts: []string{"git.example.test"}}}})
			w := httptest.NewRecorder()
			NewTaskController(store, nil).AddTask(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestRemoveKeyRejectsProjectSSHKeyBindingsAndRetainsKeys(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "project SSH key deletion"})
	require.NoError(t, err)
	defaultKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "default", Type: db.AccessKeySSH})
	require.NoError(t, err)
	alwaysKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "always", Type: db.AccessKeySSH})
	require.NoError(t, err)
	project.DefaultSSHKeys = db.SSHKeyBindings{{AccessKeyID: defaultKey.ID, Hosts: []string{"app.example.test"}}}
	project.AlwaysSSHKeys = db.SSHKeyBindings{{AccessKeyID: alwaysKey.ID, Hosts: []string{"bastion.example.test"}}}
	require.NoError(t, store.UpdateProject(project))

	controller := NewKeyController(server.NewAccessKeyService(store, nil, nil))
	for _, key := range []db.AccessKey{defaultKey, alwaysKey} {
		t.Run(key.Name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/keys/"+strconv.Itoa(key.ID), nil)
			req = helpers.SetContextValue(req, "accessKey", key)
			w := httptest.NewRecorder()

			controller.RemoveKey(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.JSONEq(t, `{"error":"Access Key is in use by project settings or templates","inUse":true}`, w.Body.String())
			_, getErr := store.GetAccessKey(project.ID, key.ID)
			assert.NoError(t, getErr)
		})
	}
}

func TestAddProjectRejectsSSHKeyBindingsBeforePersistence(t *testing.T) {
	for _, tt := range []struct {
		name string
		user string
		body string
	}{
		{name: "default bindings", user: "default", body: `{"name":"blocked default","default_ssh_keys":[{"access_key_id":999,"hosts":["app.example.test"]}]}`},
		{name: "always bindings", user: "always", body: `{"name":"blocked always","always_ssh_keys":[{"access_key_id":999,"hosts":["bastion.example.test"]}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := sql.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			user, err := store.CreateUserWithoutPassword(db.User{Username: "project-create-" + tt.user, Name: "Project Creator", Email: "creator-" + tt.user + "@example.test", Admin: true})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(tt.body))
			req = helpers.SetContextValue(req, "store", store)
			req = helpers.SetContextValue(req, "user", &user)
			req = helpers.SetContextValue(req, "log_writer", executorImageLogWriter{})
			w := httptest.NewRecorder()

			NewProjectsController(&mockAccessKeyService{}).AddProject(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			projects, listErr := store.GetAllProjects()
			require.NoError(t, listErr)
			assert.Empty(t, projects)
		})
	}
}

func TestAddProjectAcceptsEmptySSHKeyBindings(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	user, err := store.CreateUserWithoutPassword(db.User{Username: "project-create-empty", Name: "Project Creator", Email: "creator-empty@example.test", Admin: true})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"name":"empty bindings","default_ssh_keys":[],"always_ssh_keys":[]}`))
	req = helpers.SetContextValue(req, "store", store)
	req = helpers.SetContextValue(req, "user", &user)
	req = helpers.SetContextValue(req, "log_writer", executorImageLogWriter{})
	w := httptest.NewRecorder()

	NewProjectsController(&mockAccessKeyService{}).AddProject(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	projects, listErr := store.GetAllProjects()
	require.NoError(t, listErr)
	require.Len(t, projects, 1)
	assert.Empty(t, projects[0].DefaultSSHKeys)
	assert.Empty(t, projects[0].AlwaysSSHKeys)
}
