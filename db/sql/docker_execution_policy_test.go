package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExecutionPolicyStoreUsesRevisionFence(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	initial, err := store.GetDockerExecutionPolicy()
	require.NoError(t, err)
	assert.Equal(t, 0, initial.Revision)

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
