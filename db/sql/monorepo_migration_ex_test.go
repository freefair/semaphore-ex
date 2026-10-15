package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonorepoMigrationPreservesRepositoryAndRollsBack(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	require.NoError(t, db.Rollback(store, "2.20.11-ex1.2"))
	project, err := store.CreateProject(db.Project{Name: "monorepo-migration"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	_, err = store.Sql().Exec("insert into project__repository (project_id, name, git_url, git_branch, ssh_key_id) values (?, ?, ?, ?, ?)", project.ID, "existing", "https://example.com/repo.git", "main", key.ID)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(store, nil))
	repositories, err := store.GetRepositories(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, repositories, 1)
	assert.False(t, repositories[0].Monorepo)
	repositories[0].Monorepo = true
	require.NoError(t, store.UpdateRepository(repositories[0]))
	updated, err := store.GetRepository(project.ID, repositories[0].ID)
	require.NoError(t, err)
	assert.True(t, updated.Monorepo)
	require.NoError(t, db.Rollback(store, "2.20.11-ex1.2"))
	_, err = store.Sql().Exec("select monorepo from project__repository")
	require.Error(t, err)
	require.NoError(t, db.Migrate(store, nil))
	restored, err := store.GetRepository(project.ID, repositories[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "existing", restored.Name)
	assert.False(t, restored.Monorepo)
}
