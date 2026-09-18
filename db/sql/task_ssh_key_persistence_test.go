package sql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectSSHKeyBindingsRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	project, err := store.CreateProject(db.Project{Name: "ssh-key project"})
	require.NoError(t, err)

	t.Run("nil inherits", func(t *testing.T) {
		loaded, err := store.GetProject(project.ID)
		require.NoError(t, err)
		assert.Nil(t, loaded.DefaultSSHKeys)
		assert.Nil(t, loaded.AlwaysSSHKeys)
	})

	t.Run("empty slices explicitly replace defaults", func(t *testing.T) {
		project.DefaultSSHKeys = db.SSHKeyBindings{}
		project.AlwaysSSHKeys = db.SSHKeyBindings{}
		require.NoError(t, store.UpdateProject(project))
		loaded, err := store.GetProject(project.ID)
		require.NoError(t, err)
		assert.NotNil(t, loaded.DefaultSSHKeys)
		assert.Empty(t, loaded.DefaultSSHKeys)
		assert.NotNil(t, loaded.AlwaysSSHKeys)
		assert.Empty(t, loaded.AlwaysSSHKeys)
	})

	t.Run("bindings survive an update", func(t *testing.T) {
		bindings := testSSHKeyBindings()
		project.DefaultSSHKeys = bindings
		project.AlwaysSSHKeys = db.SSHKeyBindings{{AccessKeyID: 3, Hosts: []string{"always.example.test"}}}
		require.NoError(t, store.UpdateProject(project))
		loaded, err := store.GetProject(project.ID)
		require.NoError(t, err)
		assert.Equal(t, bindings, loaded.DefaultSSHKeys)
		assert.Equal(t, project.AlwaysSSHKeys, loaded.AlwaysSSHKeys)
	})
}

func TestTemplateSSHKeyBindingsRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)

	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "ssh-key template", Playbook: "site.yml",
	})
	require.NoError(t, err)

	t.Run("nil inherits", func(t *testing.T) {
		loaded, err := store.GetTemplate(projectID, template.ID)
		require.NoError(t, err)
		assert.Nil(t, loaded.SSHKeys)
	})

	t.Run("empty slice is explicit", func(t *testing.T) {
		template.SSHKeys = db.SSHKeyBindings{}
		require.NoError(t, store.UpdateTemplate(template))
		loaded, err := store.GetTemplate(projectID, template.ID)
		require.NoError(t, err)
		assert.NotNil(t, loaded.SSHKeys)
		assert.Empty(t, loaded.SSHKeys)
	})

	t.Run("bindings survive an update", func(t *testing.T) {
		template.SSHKeys = testSSHKeyBindings()
		require.NoError(t, store.UpdateTemplate(template))
		loaded, err := store.GetTemplate(projectID, template.ID)
		require.NoError(t, err)
		assert.Equal(t, template.SSHKeys, loaded.SSHKeys)
	})
}

func TestTaskSSHKeyBindingsRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "ssh-key task", Playbook: "site.yml",
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		bindings db.SSHKeyBindings
	}{
		{name: "nil inherits", bindings: nil},
		{name: "empty slice is explicit", bindings: db.SSHKeyBindings{}},
		{name: "bindings persist", bindings: testSSHKeyBindings()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created, err := store.CreateTask(db.Task{
				ProjectID: projectID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus,
				Playbook: "site.yml", Created: time.Now(), SSHKeys: tc.bindings,
			}, 0)
			require.NoError(t, err)
			loaded, err := store.GetTask(projectID, created.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.bindings, loaded.SSHKeys)
		})
	}
}

func TestSSHKeyBindingsJSONPreservesInheritanceAndExplicitEmptyOverride(t *testing.T) {
	tests := []struct {
		name  string
		value any
		field string
		want  string
	}{
		{name: "project nil inherits", value: db.Project{}, field: "default_ssh_keys", want: `null`},
		{name: "project empty default overrides", value: db.Project{DefaultSSHKeys: db.SSHKeyBindings{}}, field: "default_ssh_keys", want: `[]`},
		{name: "project empty always persists", value: db.Project{AlwaysSSHKeys: db.SSHKeyBindings{}}, field: "always_ssh_keys", want: `[]`},
		{name: "template nil inherits", value: db.Template{}, field: "ssh_keys", want: `null`},
		{name: "template empty overrides", value: db.Template{SSHKeys: db.SSHKeyBindings{}}, field: "ssh_keys", want: `[]`},
		{name: "task nil inherits", value: db.Task{}, field: "ssh_keys", want: `null`},
		{name: "task empty overrides", value: db.Task{SSHKeys: db.SSHKeyBindings{}}, field: "ssh_keys", want: `[]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			require.NoError(t, err)
			var payload map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &payload))
			value, ok := payload[tc.field]
			require.True(t, ok)
			assert.JSONEq(t, tc.want, string(value))
		})
	}
}

func testSSHKeyBindings() db.SSHKeyBindings {
	return db.SSHKeyBindings{
		{AccessKeyID: 1, Hosts: []string{"git.example.test"}},
		{AccessKeyID: 2, Hosts: []string{"forge.example.test", "gitlab.example.test"}},
	}
}
