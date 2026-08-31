package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22048AddsKubernetesTelemetryFenceStorage(t *testing.T) {
	legacyVersion := "2.20.47"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "kubernetes_reconciliation_session"), "telemetry_highest_sequence")
	assert.Contains(t, sqliteTableNames(t, store), "kubernetes_telemetry_event")
	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "kubernetes_telemetry_event")
}
