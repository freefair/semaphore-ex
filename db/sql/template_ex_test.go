package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTemplateRunnerTagPolicyRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	projectID, repositoryID := newTemplateTestProject(t, store)

	created, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID,
		Name: "multi-tag", Playbook: "site.yml",
		RunnerTags:         db.StringArrayField{" GPU ", "linux", "gpu"},
		RunnerTagMatchMode: db.RunnerTagMatchAny,
	})
	require.NoError(t, err)

	loaded, err := store.GetTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, db.StringArrayField{"gpu", "linux"}, loaded.RunnerTags)
	assert.Equal(t, db.RunnerTagMatchAny, loaded.RunnerTagMatchMode)
	require.NotNil(t, loaded.RunnerTag)
	assert.Equal(t, "gpu", *loaded.RunnerTag, "legacy field keeps a rollback-compatible first tag")
}
