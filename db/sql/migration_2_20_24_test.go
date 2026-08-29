package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22024AddsAndRollsBackScheduleOccurrenceLeases(t *testing.T) {
	legacyVersion := "2.20.23"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__schedule_occurrence")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "cluster__schedule_occurrence")
	columns := sqliteColumnNames(t, store, "cluster__schedule_occurrence")
	for _, column := range []string{
		"occurrence_key", "schedule_id", "schedule_revision", "intended_at", "owner_boot_id",
		"fencing_token", "lease_expires_at", "task_id", "completed_at", "created", "updated",
	} {
		assert.Contains(t, columns, column)
	}
	assert.Contains(t, sqliteColumnNames(t, store, "task"), "schedule_occurrence_key")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__schedule_occurrence")
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "schedule_occurrence_key")
}
