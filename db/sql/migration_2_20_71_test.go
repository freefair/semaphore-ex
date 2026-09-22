package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskSSHKeyBindingsMigrationAddsNullableColumnsAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.5-ex1.1"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "project"), "default_ssh_keys")
	assert.Contains(t, sqliteColumnNames(t, store, "project"), "always_ssh_keys")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template"), "ssh_keys")
	assert.Contains(t, sqliteColumnNames(t, store, "task"), "ssh_keys")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project"), "default_ssh_keys")
	assert.NotContains(t, sqliteColumnNames(t, store, "project"), "always_ssh_keys")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__template"), "ssh_keys")
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "ssh_keys")
}

func TestTaskSSHKeyBindingsMigrationUsesNullableLargeColumns(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			migration := strings.ToLower(strings.Join(getVersionSQL(dialect, "ex/v2.20.5-ex1.2.sql", false), ";"))
			assert.Contains(t, migration, "default_ssh_keys")
			assert.Contains(t, migration, "always_ssh_keys")
			assert.Contains(t, migration, "ssh_keys")
			assert.NotContains(t, migration, "not null")
		})
	}
}
