package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22047AddsKubernetesReconciliationFenceTables(t *testing.T) {
	legacyVersion := "2.20.46"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteColumnNames(t, store, "runner"), "k8s_namespace")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "runner"), "k8s_namespace")
	for _, table := range []string{"kubernetes_reconciliation_session", "kubernetes_reconciliation_target", "kubernetes_reconciliation_observation", "kubernetes_reconciliation_candidate", "kubernetes_reconciliation_command"} {
		assert.Contains(t, sqliteTableNames(t, store), table)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "kubernetes_reconciliation_session")
}
