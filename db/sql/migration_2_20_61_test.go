package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22061AddsAndRollsBackBlockedOutcomeFields(t *testing.T) {
	legacyVersion := "2.20.60"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	_, err := store.GetConnection().Exec("insert into cluster__schedule_occurrence(occurrence_key, schedule_id, schedule_revision, intended_at, owner_boot_id, fencing_token, lease_expires_at, task_id, completed_at, created, updated) values (?, ?, ?, CURRENT_TIMESTAMP, ?, ?, CURRENT_TIMESTAMP, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", "legacy-completed", 5, "r1", "legacy", 1, 99)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(store, nil))

	for table, fields := range map[string][]string{
		"cluster__schedule_occurrence":         {"terminal_outcome", "deployment_window_decision_id", "next_eligible_at", "next_eligible_known", "blocked_at"},
		"project__workflow_trigger_invocation": {"deployment_window_decision_id", "next_eligible_at", "next_eligible_known", "blocked_at"},
		"project__workflow_run_node":           {"deployment_window_decision_id", "next_eligible_at", "next_eligible_known", "blocked_at"},
	} {
		columns := sqliteColumnNames(t, store, table)
		for _, field := range fields {
			assert.Contains(t, columns, field, table)
		}
	}
	var outcome string
	require.NoError(t, store.GetConnection().SelectOne(&outcome, "select terminal_outcome from cluster__schedule_occurrence where occurrence_key=?", "legacy-completed"))
	assert.Equal(t, "completed", outcome, "pre-existing completed rows retain their terminal meaning")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "cluster__schedule_occurrence"), "terminal_outcome")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_trigger_invocation"), "deployment_window_decision_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "deployment_window_decision_id")
}
