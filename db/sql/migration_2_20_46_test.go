package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22046AddsKubernetesNetworkProvenance(t *testing.T) {
	legacyVersion := "2.20.45"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "k8s_network_policy_uid")
	require.NoError(t, db.Migrate(store, nil))
	for _, column := range []string{"k8s_service_account", "k8s_resource_policy_hash", "k8s_network_enforcement", "k8s_secret_uid", "k8s_network_policy_uid", "k8s_retention_deadline"} {
		assert.Contains(t, sqliteColumnNames(t, store, "task__runner_attempt"), column)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "k8s_network_policy_uid")
}
