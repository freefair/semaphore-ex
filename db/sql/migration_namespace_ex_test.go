package sql

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationNamespacesKeepUpstreamAndEXHistoryIndependent(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	require.NoError(t, store.ensureMigrationHistory(exMigrationTable))
	require.NoError(t, store.ensureMigrationHistory(exMigrationTable))
	const exIdentity = "2.20.9-ex1.999"
	_, err := store.Sql().Exec("insert into `migrations` (`version`) values (?)", exIdentity)
	require.NoError(t, err)
	_, err = store.Sql().Exec("insert into `ex_migrations` (`version`) values (?)", "9.99.1")
	require.NoError(t, err)

	upstream, err := store.IsMigrationApplied(db.Migration{Version: "9.99.1"})
	require.NoError(t, err)
	ex, err := store.IsMigrationApplied(db.Migration{Version: exIdentity})
	require.NoError(t, err)
	assert.False(t, upstream)
	assert.False(t, ex)

	_, err = store.Sql().Exec("insert into `migrations` (`version`) values (?)", "9.99.1")
	require.NoError(t, err)
	_, err = store.Sql().Exec("insert into `ex_migrations` (`version`) values (?)", exIdentity)
	require.NoError(t, err)
	upstream, err = store.IsMigrationApplied(db.Migration{Version: "9.99.1"})
	require.NoError(t, err)
	ex, err = store.IsMigrationApplied(db.Migration{Version: exIdentity})
	require.NoError(t, err)
	assert.True(t, upstream)
	assert.True(t, ex)
}

func TestEXMigration65SeparatesDelayFromDefinitionNodeIdentity(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			forward := strings.ToLower(strings.Join(getVersionSQL(dialect, "ex/v2.20.2-ex1.1.sql", false), ";"))
			rollback := strings.ToLower(strings.Join(getVersionSQL(dialect, "ex/v2.20.2-ex1.1.err.sql", false), ";"))

			assert.Contains(t, forward, "project__workflow_run_node")
			assert.Contains(t, forward, "workflow_run_id`, `workflow_node_id")
			assert.NotContains(t, forward, "references `project__workflow_node`(`id`)")
			assert.Contains(t, rollback, "references `project__workflow_node`(`id`)")
		})
	}
}

func TestEXMigrationIdentityRejectsMalformedAndTraversalValues(t *testing.T) {
	assert.True(t, func() bool {
		_, ok := exMigrationIdentity("2.20.2-ex2.2.1")
		return ok
	}())
	assert.True(t, func() bool {
		_, ok := exMigrationIdentity("2.20.2-ex2.2.10")
		return ok
	}())
	assert.True(t, func() bool {
		_, ok := exMigrationIdentity("2.20.2-ex2.0.1")
		return ok
	}())

	for _, version := range []string{"2.20.1-ex", "2.20.1-ex0.1", "2.20.1-ex01.1", "2.20.1-ex1.01", "2.20.1-ex1/../2", "2.20.1-ex1.1.sql", "2.20.1-ex1.1/"} {
		t.Run(version, func(t *testing.T) {
			_, err := migrationHistory(db.Migration{Version: version})
			require.Error(t, err)
		})
	}

	assert.Panics(t, func() {
		getVersionSQL("sqlite", "ex/v2.20.1-ex1.1/../v2.20.1-ex1.2.sql", false)
	})
}

func TestMigrationLockMatrixSerializesIndependentConnectionsAndCleansUp(t *testing.T) {
	config := migrationMatrixConfigFromEnvironment(t)
	if config.Dialect == "sqlite" {
		t.Skip("migration lock matrix requires PostgreSQL or MySQL")
	}
	configureMigrationMatrix(t, config)
	first, second := CreateDb(config.Dialect), CreateDb(config.Dialect)
	first.Connect()
	second.Connect()
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	blocked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithMigrationLock(func() error {
			close(blocked)
			<-release
			return nil
		})
	}()
	<-blocked

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithMigrationLock(func() error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("migration lock admitted a concurrent migration")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	expected := errors.New("expected")
	assert.ErrorIs(t, first.WithMigrationLock(func() error { return expected }), expected)
	require.NoError(t, second.WithMigrationLock(func() error { return nil }))
}

func TestRollbackPreflightRejectsMissingAssetBeforeMutation(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	missing := db.Migration{Version: "2.20.1-ex99.1"}
	_, err := store.Sql().Exec("insert into `ex_migrations` (`version`) values (?)", missing.Version)
	require.NoError(t, err)

	require.Error(t, store.ValidateMigrationPlan([]db.Migration{missing}, true))
	require.Error(t, store.TryRollbackMigration(missing))
	applied, err := store.IsMigrationApplied(missing)
	require.NoError(t, err)
	assert.True(t, applied)
}
