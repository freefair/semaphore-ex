package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22025AddsAndRollsBackTaskControlLeases(t *testing.T) {
	legacyVersion := "2.20.24"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__task_control")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "cluster__task_control")
	for _, column := range []string{
		"task_id", "owner_boot_id", "fencing_token", "lease_expires_at", "runner_id",
		"assignment_generation", "execution_stable_id", "last_observed_at", "created", "updated",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "cluster__task_control"), column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__task_control")
}
