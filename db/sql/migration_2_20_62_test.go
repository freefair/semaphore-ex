package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyGuardrailMigrationCreatesAndRollsBackGovernanceLedger(t *testing.T) {
	legacyVersion := "2.20.61"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))

	for _, table := range []string{"policy_guardrail_draft", "policy_guardrail_revision", "policy_guardrail_evaluation"} {
		var name string
		require.NoError(t, store.GetConnection().SelectOne(&name, "select name from sqlite_master where type='table' and name=?", table))
		assert.Equal(t, table, name)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	var count int
	require.NoError(t, store.GetConnection().SelectOne(&count, "select count(*) from sqlite_master where type='table' and name like 'policy_guardrail_%'"))
	assert.Zero(t, count)
}

func TestPolicyGuardrailMigrationPreparesForEverySQLDialect(t *testing.T) {
	for _, test := range []struct {
		name, dialect, primaryKey string
		gorp                      gorp.Dialect
	}{
		{"sqlite", "sqlite", "integer primary key autoincrement", gorp.SqliteDialect{}},
		{"mysql", "mysql", "integer primary key auto_increment", gorp.MySQLDialect{}},
		{"postgres", "postgres", "serial primary key", gorp.PostgresDialect{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: test.gorp}}}
			queries := getVersionSQL(test.dialect, "v2.20.62.sql", false)
			prepared := make([]string, len(queries))
			for index, query := range queries {
				prepared[index] = store.prepareMigration(query)
			}
			joined := strings.ToLower(strings.Join(prepared, ";"))
			rollback := strings.ToLower(strings.Join(getVersionSQL(test.dialect, "v2.20.62.err.sql", true), ";"))
			assert.Contains(t, joined, test.primaryKey)
			assert.Contains(t, joined, "policy_guardrail_evaluation__project_created")
			assert.NotContains(t, joined, "foreign key (`published_by`)")
			assert.NotContains(t, joined, "foreign key (`updated_by`)")
			assert.NotContains(t, rollback, "drop index")
			assert.Contains(t, rollback, "drop table `policy_guardrail_draft`")
		})
	}
}
