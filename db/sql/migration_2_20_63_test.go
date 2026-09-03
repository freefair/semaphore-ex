package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowPolicyGuardrailEvaluationMigrationCreatesAndRollsBackNodeProvenance(t *testing.T) {
	legacyVersion := "2.20.62"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))

	var column string
	require.NoError(t, store.GetConnection().SelectOne(&column,
		"select name from pragma_table_info('project__workflow_run_node') where name='policy_guardrail_evaluation_id'"))
	assert.Equal(t, "policy_guardrail_evaluation_id", column)
	require.NoError(t, db.Rollback(store, legacyVersion))
	var count int
	require.NoError(t, store.GetConnection().SelectOne(&count,
		"select count(*) from pragma_table_info('project__workflow_run_node') where name='policy_guardrail_evaluation_id'"))
	assert.Zero(t, count)
}

func TestWorkflowPolicyGuardrailEvaluationMigrationPreparesForEverySQLDialect(t *testing.T) {
	for _, test := range []struct {
		name, dialect string
		gorp          gorp.Dialect
	}{
		{"sqlite", "sqlite", gorp.SqliteDialect{}},
		{"mysql", "mysql", gorp.MySQLDialect{}},
		{"postgres", "postgres", gorp.PostgresDialect{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: test.gorp}}}
			queries := getVersionSQL(test.dialect, "v2.20.63.sql", false)
			joined := strings.ToLower(strings.Join(queries, ";"))
			assert.Contains(t, joined, "policy_guardrail_evaluation_id")
			assert.Contains(t, joined, "project__workflow_run_node__policy_guardrail_evaluation")
			assert.NotContains(t, store.prepareMigration(joined), "sqlite")
		})
	}
}
