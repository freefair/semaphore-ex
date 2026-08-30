package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22034AddsDockerPolicyState(t *testing.T) {
	legacyVersion := "2.20.33"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	assert.NotContains(t, sqliteColumnNames(t, store, "runner"), "docker_policy_revision")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "runner"), "docker_policy_revision")
	assert.Contains(t, sqliteColumnNames(t, store, "runner"), "docker_policy_hash")
	policy, err := store.GetDockerExecutionPolicy()
	require.NoError(t, err)
	assert.NotEmpty(t, policy.Hash)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "runner"), "docker_policy_revision")
}
