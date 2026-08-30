package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExecutionPolicyStoreReturnsFailClosedDefaultUntilFirstSave(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	count, err := store.Sql().SelectInt(store.PrepareQuery("select count(1) from docker_execution_policy"))
	require.NoError(t, err)
	assert.Zero(t, count)

	initial, err := store.GetDockerExecutionPolicy()
	require.NoError(t, err)
	assert.Equal(t, db.DefaultDockerExecutionPolicy(), initial)

	initial.AllowedImages = []string{"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	saved, err := store.SaveDockerExecutionPolicy(initial, initial.Revision)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.Revision)
	assert.NotEmpty(t, saved.Hash)

	_, err = store.SaveDockerExecutionPolicy(saved, 0)
	assert.ErrorIs(t, err, db.ErrDockerExecutionPolicyRevisionConflict)
	loaded, err := store.GetDockerExecutionPolicy()
	require.NoError(t, err)
	assert.Equal(t, saved, loaded)
}
