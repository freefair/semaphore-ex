package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22027AddsAndRollsBackTaskRecoveryDiagnostics(t *testing.T) {
	legacyVersion := "2.20.26"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	columns := []string{
		"last_evidence_status", "previous_owner_boot_id", "ownership_transferred_at", "assignment_revoked_at",
		"last_recovery_decision", "last_recovery_reason", "last_recovery_safe_replacement", "last_recovery_at",
	}
	for _, column := range columns {
		assert.NotContains(t, sqliteColumnNames(t, store, "cluster__task_control"), column)
	}
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "task_control_fencing_token")

	require.NoError(t, db.Migrate(store, nil))
	for _, column := range columns {
		assert.Contains(t, sqliteColumnNames(t, store, "cluster__task_control"), column)
	}
	assert.Contains(t, sqliteColumnNames(t, store, "task"), "task_control_fencing_token")

	require.NoError(t, db.Rollback(store, legacyVersion))
	for _, column := range columns {
		assert.NotContains(t, sqliteColumnNames(t, store, "cluster__task_control"), column)
	}
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "task_control_fencing_token")
}
