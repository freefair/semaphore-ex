package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22022AddsWorkflowReconciliationStateAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.20"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	columns := sqliteColumnNames(t, store, "project__workflow_run")
	assert.NotContains(t, columns, "desired_state")
	assert.NotContains(t, columns, "reconciliation_state")

	require.NoError(t, db.Migrate(store, nil))
	columns = sqliteColumnNames(t, store, "project__workflow_run")
	for _, column := range []string{
		"desired_state",
		"reconciliation_state",
		"reconciliation_attempts",
		"reconciliation_last_error",
		"reconciliation_next_retry_at",
		"reconciliation_quarantined_at",
	} {
		assert.Contains(t, columns, column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	columns = sqliteColumnNames(t, store, "project__workflow_run")
	assert.NotContains(t, columns, "desired_state")
	assert.NotContains(t, columns, "reconciliation_state")
}
