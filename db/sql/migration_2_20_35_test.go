package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22035AddsDockerProvenance(t *testing.T) {
	legacy := "2.20.34"
	store := InitConfigCreateTestStoreAt(&legacy)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))
	columns := sqliteColumnNames(t, store, "task__runner_attempt")
	assert.Contains(t, columns, "docker_resolved_image")
	assert.Contains(t, columns, "docker_policy_hash")
	require.NoError(t, db.Rollback(store, legacy))
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "docker_resolved_image")
}
