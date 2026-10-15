package export

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImportLegacyLocalRepositorySharedCheckout(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "local import"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "none", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repo, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "local", GitURL: "/srv/local"})
	require.NoError(t, err)
	exporter := RepositoryExporter{pendingMonorepos: []db.Repository{repo}}
	require.NoError(t, exporter.postRestore(store, nil))
	loaded, err := store.GetRepository(project.ID, repo.ID)
	require.NoError(t, err)
	assert.False(t, loaded.Monorepo)
}
