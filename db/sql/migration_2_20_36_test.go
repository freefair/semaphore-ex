package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22036AddsDenialRuleIDAndRollsBack(t *testing.T) {
	legacy := "2.20.35"
	store := InitConfigCreateTestStoreAt(&legacy)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "denial_rule_id")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "denial_rule_id")
	require.NoError(t, db.Rollback(store, legacy))
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "denial_rule_id")
}
