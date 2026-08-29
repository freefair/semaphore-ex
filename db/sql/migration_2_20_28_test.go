package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22028AddsAndRollsBackWorkflowProgressionOwnership(t *testing.T) {
	legacyVersion := "2.20.27"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__workflow_reconciliation")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "progression_fencing_token")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "cluster__workflow_reconciliation")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "progression_fencing_token")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__workflow_reconciliation")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "progression_fencing_token")
}
