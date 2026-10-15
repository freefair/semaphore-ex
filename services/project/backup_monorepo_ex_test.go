package project

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBackupMonorepoKeepsLegacyTemplateBranchSettings(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "monorepo-backup"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "source", GitURL: "https://example.test/source.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	branch := "release"
	_, err = store.CreateTemplate(db.Template{ProjectID: project.ID, Name: "legacy", RepositoryID: repo.ID, App: db.AppBash, Playbook: "task.sh", GitBranch: &branch, AllowOverrideBranchInTask: true})
	require.NoError(t, err)
	repo.Monorepo = true
	require.NoError(t, store.UpdateRepository(repo))
	backup, err := GetBackup(project.ID, store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)
	encoded, err := backup.Marshal()
	require.NoError(t, err)
	imported := &BackupFormat{}
	require.NoError(t, imported.Unmarshal(encoded))
	imported.Meta.Name = "monorepo-restored"
	user, err := store.CreateUserWithoutPassword(db.User{Username: "restore", Name: "Restore", Email: "restore@example.test", Admin: true})
	require.NoError(t, err)
	restored, err := imported.Restore(user, store, proFactory.NewWorkflowStore(store))
	require.NoError(t, err)
	repos, err := store.GetRepositories(restored.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, repos, 1)
	assert.True(t, repos[0].Monorepo)
	templates, err := store.GetTemplates(restored.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.NotNil(t, templates[0].GitBranch)
	assert.Equal(t, branch, *templates[0].GitBranch)
	assert.True(t, templates[0].AllowOverrideBranchInTask)
}
