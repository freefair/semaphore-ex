package export

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExportRestorePreservesMonorepoWithLegacyTemplateBranches(t *testing.T) {
	source := sql.InitConfigCreateTestStore()
	t.Cleanup(source.Close)
	project, err := source.CreateProject(db.Project{Name: "monorepo-source"})
	require.NoError(t, err)
	key, err := source.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := source.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.test/repo.git", GitBranch: "main", SSHKeyID: key.ID})
	require.NoError(t, err)
	branch := "release"
	_, err = source.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repo.ID, Name: "legacy", App: db.AppBash, Playbook: "task.sh", GitBranch: &branch, AllowOverrideBranchInTask: true})
	require.NoError(t, err)
	repo.Monorepo = true
	require.NoError(t, source.UpdateRepository(repo))
	chain := InitProjectExporters(NewKeyMapper(), true, false)
	require.NoError(t, chain.Load(source))
	destination := sql.InitConfigCreateTestStore()
	t.Cleanup(destination.Close)
	_, err = destination.CreateProject(db.Project{Name: "existing"})
	require.NoError(t, err)
	require.NoError(t, chain.Restore(destination, 10))
	projects, err := destination.GetAllProjects()
	require.NoError(t, err)
	var restoredID int
	for _, candidate := range projects {
		if candidate.Name == project.Name {
			restoredID = candidate.ID
		}
	}
	require.NotZero(t, restoredID)
	assert.NotEqual(t, project.ID, restoredID)
	repos, err := destination.GetRepositories(restoredID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, repos, 1)
	assert.True(t, repos[0].Monorepo)
	templates, err := destination.GetTemplates(restoredID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.NotNil(t, templates[0].GitBranch)
	assert.Equal(t, branch, *templates[0].GitBranch)
	assert.True(t, templates[0].AllowOverrideBranchInTask)
}
