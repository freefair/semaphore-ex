package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22033AddsRunnerExecutorMetadata(t *testing.T) {
	legacyVersion := "2.20.32"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "executor_type")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "executor_type")
	assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "container_id")
	assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "container_name")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "executor_type")
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "container_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "container_name")
}
