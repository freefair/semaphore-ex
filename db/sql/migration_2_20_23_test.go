package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22023AddsAndRollsBackClusterNodeHistory(t *testing.T) {
	legacyVersion := "2.20.22"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__node")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "cluster__node")
	columns := sqliteColumnNames(t, store, "cluster__node")
	for _, column := range []string{
		"boot_id", "node_id", "edition", "version", "build", "protocol_version", "schema_version",
		"capabilities", "started_at", "last_seen_at", "draining", "retired_at",
	} {
		assert.Contains(t, columns, column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "cluster__node")
}
