package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLocalRepositorySharedCheckoutNormalization(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "local repository"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	for _, url := range []string{"/srv/repository", `C:\repository`, `\\server\share`} {
		t.Run(url, func(t *testing.T) {
			repo, createErr := store.CreateRepository(db.Repository{Name: url, ProjectID: project.ID, SSHKeyID: key.ID, GitURL: url, Monorepo: true})
			require.NoError(t, createErr)
			assert.False(t, repo.Monorepo)
			_, updateErr := store.Sql().Exec(store.PrepareQuery("update project__repository set monorepo=? where id=?"), true, repo.ID)
			require.NoError(t, updateErr)
			loaded, readErr := store.GetRepository(project.ID, repo.ID)
			require.NoError(t, readErr)
			assert.False(t, loaded.Monorepo, "legacy row must not activate shared-checkout execution")
			all, listErr := store.GetRepositories(project.ID, db.RetrieveQueryParams{})
			require.NoError(t, listErr)
			for _, item := range all {
				assert.False(t, item.Monorepo)
			}
			loaded.Monorepo = true
			require.NoError(t, store.UpdateRepository(loaded), "legacy backup restoration remains valid")
			var persisted bool
			require.NoError(t, store.Sql().QueryRow(store.PrepareQuery("select monorepo from project__repository where id=?"), repo.ID).Scan(&persisted))
			assert.False(t, persisted)
		})
	}
}
