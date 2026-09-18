package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAccessKeyRefsIncludesSSHBindingOwners(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &projectID, Name: "dependency", Type: db.AccessKeySSH})
	require.NoError(t, err)
	project, err := store.GetProject(projectID)
	require.NoError(t, err)
	project.DefaultSSHKeys = db.SSHKeyBindings{{AccessKeyID: key.ID, Hosts: []string{"git.example.test"}}}
	require.NoError(t, store.UpdateProject(project))
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "deploy", Playbook: "site.yml",
		SSHKeys: db.SSHKeyBindings{{AccessKeyID: key.ID, Hosts: []string{"modules.example.test"}}},
	})
	require.NoError(t, err)
	_, err = store.CreateTask(db.Task{ProjectID: projectID, TemplateID: template.ID, Playbook: "site.yml", SSHKeys: db.SSHKeyBindings{{AccessKeyID: key.ID, Hosts: []string{"task.example.test"}}}}, 0)
	require.NoError(t, err)

	refs, err := store.GetAccessKeyRefs(projectID, key.ID)
	require.NoError(t, err)
	require.Len(t, refs.Projects, 1)
	assert.Equal(t, projectID, refs.Projects[0].ID)
	require.Len(t, refs.Templates, 1)
	assert.Equal(t, template.ID, refs.Templates[0].ID)
	require.Len(t, refs.Tasks, 1)
}
