package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/require"
)

// createLegacyProject seeds only the columns present since the SQLite base
// schema. Migration tests call it before applying newer migrations, so using
// the current project writer would incorrectly require future columns.
func createLegacyProject(t *testing.T, store *SqlDb, name string) db.Project {
	t.Helper()

	id, err := store.insert("id", "insert into project(name, created) values (?, ?)", name, time.Now().UTC())
	require.NoError(t, err)
	return db.Project{ID: id, Name: name}
}

func newLegacyTemplateTestProject(t *testing.T, store *SqlDb) (projectID int, repositoryID int) {
	t.Helper()

	project := createLegacyProject(t, store, "legacy template project")
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, Name: "legacy repository", GitURL: "https://example.com/repo.git",
		GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)

	return project.ID, repository.ID
}
