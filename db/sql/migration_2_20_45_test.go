package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMigration22045AddsKubernetesPolicyState(t *testing.T) {
	legacyVersion := "2.20.44"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "runner"), "k8s_policy_hash")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "runner"), "k8s_policy_hash")
	assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "k8s_denial_rule_id")
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "runner"), "k8s_policy_hash")
}
