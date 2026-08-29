package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22026AddsAndRollsBackTaskExecutionEvidence(t *testing.T) {
	legacyVersion := "2.20.25"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "cluster__task_control"), "last_evidence_state")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "cluster__task_control"), "last_evidence_state")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "cluster__task_control"), "last_evidence_state")
}
