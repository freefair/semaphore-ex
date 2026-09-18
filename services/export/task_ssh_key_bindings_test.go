package export

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportRestoreRemapsSSHKeyBindingsAfterKeysExist(t *testing.T) {
	source := sql.InitConfigCreateTestStore()
	t.Cleanup(source.Close)
	project, err := source.CreateProject(db.Project{Name: "source"})
	require.NoError(t, err)
	repositoryKey, err := source.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repo", Type: db.AccessKeyNone})
	require.NoError(t, err)
	dependencyKey, err := source.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "dependency", Type: db.AccessKeySSH})
	require.NoError(t, err)
	project.DefaultSSHKeys = db.SSHKeyBindings{{AccessKeyID: dependencyKey.ID, Hosts: []string{"git.example.test"}}}
	project.AlwaysSSHKeys = db.SSHKeyBindings{{AccessKeyID: dependencyKey.ID, Hosts: []string{"always.example.test"}}}
	require.NoError(t, source.UpdateProject(project))
	repository, err := source.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: repositoryKey.ID})
	require.NoError(t, err)
	template, err := source.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "deploy", Playbook: "site.yml", SSHKeys: db.SSHKeyBindings{}})
	require.NoError(t, err)
	_, err = source.CreateTask(db.Task{ProjectID: project.ID, TemplateID: template.ID, Playbook: "site.yml", SSHKeys: db.SSHKeyBindings{{AccessKeyID: dependencyKey.ID, Hosts: []string{"task.example.test"}}}}, 0)
	require.NoError(t, err)

	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.Load(source))

	destination := sql.InitConfigCreateTestStore()
	t.Cleanup(destination.Close)
	dummy, err := destination.CreateProject(db.Project{Name: "existing"})
	require.NoError(t, err)
	_, err = destination.CreateAccessKey(db.AccessKey{ProjectID: &dummy.ID, Name: "existing", Type: db.AccessKeyNone})
	require.NoError(t, err)
	_, err = destination.CreateAccessKey(db.AccessKey{ProjectID: &dummy.ID, Name: "existing-two", Type: db.AccessKeyNone})
	require.NoError(t, err)
	require.NoError(t, chain.Restore(destination, 0))

	projects, err := destination.GetAllProjects()
	require.NoError(t, err)
	var restored db.Project
	for _, candidate := range projects {
		if candidate.Name == "source" {
			restored = candidate
		}
	}
	require.NotZero(t, restored.ID)
	require.Len(t, restored.DefaultSSHKeys, 1)
	assert.NotEqual(t, dependencyKey.ID, restored.DefaultSSHKeys[0].AccessKeyID)
	assert.Equal(t, restored.DefaultSSHKeys[0].AccessKeyID, restored.AlwaysSSHKeys[0].AccessKeyID)

	templates, err := destination.GetTemplates(restored.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	assert.NotNil(t, templates[0].SSHKeys)
	assert.Empty(t, templates[0].SSHKeys)
	tasks, err := destination.GetProjectTasks(restored.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, restored.DefaultSSHKeys[0].AccessKeyID, tasks[0].SSHKeys[0].AccessKeyID)
}
