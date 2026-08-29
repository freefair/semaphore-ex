package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22020AddsImmutableWorkflowApprovalFieldsAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.19"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_approval"), "deadline")
	require.NoError(t, db.Migrate(store, nil))
	columns := sqliteColumnNames(t, store, "project__workflow_approval")
	for _, column := range []string{"deadline", "prompt", "eligible_permission", "separation_of_duties", "request_actor_user_id", "timeout_outcome", "decision_comment", "decision_source", "correlation_id"} {
		assert.Contains(t, columns, column)
	}
	nodeColumns := sqliteColumnNames(t, store, "project__workflow_node")
	for _, column := range []string{"approval_permission", "approval_timeout_outcome", "approval_separation_of_duties"} {
		assert.Contains(t, nodeColumns, column)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_approval"), "deadline")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "approval_permission")
}
