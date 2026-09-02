package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22060CreatesAndRollsBackDeploymentWindowStorage(t *testing.T) {
	legacyVersion := "2.20.59"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))
	for _, table := range []string{
		"project__deployment_window_policy", "project__deployment_window_rule", "project__deployment_window_decision",
	} {
		assert.Contains(t, sqliteTableNames(t, store), table)
	}
	for _, column := range []string{
		"project_id", "decision_key", "source", "origin", "template_id", "workflow_template_id", "schedule_id",
		"task_id", "workflow_run_id", "workflow_run_node_id", "policy_revision", "effective_timezone",
		"evaluated_at", "next_eligible_at", "matched_rules",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "project__deployment_window_decision"), column)
	}
	require.NoError(t, db.Rollback(store, legacyVersion))
	for _, table := range []string{
		"project__deployment_window_policy", "project__deployment_window_rule", "project__deployment_window_decision",
	} {
		assert.NotContains(t, sqliteTableNames(t, store), table)
	}
}

func TestMigration22060UsesPortableMySQLIndexRollback(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.60.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.60.err.sql", true), ";")
	assert.Contains(t, migration, "`matched_rules` longtext not null")
	assert.Contains(t, rollback, "drop index `project__deployment_window_decision__project_created` on `project__deployment_window_decision`")
	assert.Contains(t, rollback, "drop index `project__deployment_window_rule__policy` on `project__deployment_window_rule`")
	assert.Contains(t, migration, "foreign key (`project_id`) references `project`(`id`) on delete cascade")
}

func TestMigration22060PreparesForPostgres(t *testing.T) {
	store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: gorp.PostgresDialect{}}}}
	queries := getVersionSQL(util.DbDriverPostgres, "v2.20.60.sql", false)
	prepared := make([]string, 0, len(queries))
	for _, query := range queries {
		prepared = append(prepared, store.prepareMigration(query))
	}
	migration := strings.Join(prepared, ";")
	assert.Contains(t, migration, "serial primary key")
	assert.Contains(t, migration, "foreign key (\"project_id\") references \"project\"(\"id\") on delete cascade")
	assert.NotContains(t, migration, "autoincrement")
	assert.NotContains(t, migration, "datetime")
	assert.NotContains(t, migration, "tinyint")
	assert.NotContains(t, migration, "longtext")
}
