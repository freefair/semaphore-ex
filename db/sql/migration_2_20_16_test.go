package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22016BackfillsConditionalWorkflowDefaultsAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.15"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Legacy conditional workflow"})
	require.NoError(t, err)
	result, err := store.Sql().Exec(
		"insert into project__workflow_template(project_id, name, definition_version, revision) values (?, ?, ?, ?)",
		project.ID, "Legacy", 1, 1,
	)
	require.NoError(t, err)
	workflowID, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = store.Sql().Exec(
		"insert into project__workflow_node(workflow_template_id, kind, convergence_mode, display_name) values (?, ?, ?, ?)",
		workflowID, "task", "all", "Legacy node",
	)
	require.NoError(t, err)
	nodeID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"insert into project__workflow_edge(workflow_template_id, source_node_id, destination_node_id, condition, label) values (?, ?, ?, ?, ?)",
		workflowID, nodeID, nodeID, "always", "legacy",
	)
	require.NoError(t, err)
	result, err = store.Sql().Exec(
		"insert into project__workflow_run(project_id, workflow_template_id, status) values (?, ?, ?)",
		project.ID, workflowID, "pending",
	)
	require.NoError(t, err)
	runID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"insert into project__workflow_run_node(project_id, workflow_run_id, workflow_node_id, template_id, status, template_snapshot, created) values (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
		project.ID, runID, nodeID, 0, "pending", "{}",
	)
	require.NoError(t, err)

	require.NoError(t, db.Migrate(store, nil))
	var maxParallel int
	require.NoError(t, store.Sql().SelectOne(&maxParallel,
		"select max_parallel_tasks from project__workflow_template where id=?", workflowID))
	assert.Equal(t, 4, maxParallel)
	var joinMode string
	require.NoError(t, store.Sql().SelectOne(&joinMode,
		"select join_mode from project__workflow_node where id=?", nodeID))
	assert.Equal(t, "all-successful", joinMode)
	var edgeValues struct {
		Expression string `db:"condition_expression"`
		Program    string `db:"condition_program"`
	}
	require.NoError(t, store.Sql().SelectOne(&edgeValues,
		"select condition_expression, condition_program from project__workflow_edge where workflow_template_id=?", workflowID))
	assert.Empty(t, edgeValues.Expression)
	assert.Equal(t, "{}", edgeValues.Program)
	var nodeResult string
	require.NoError(t, store.Sql().SelectOne(&nodeResult,
		"select result from project__workflow_run_node where workflow_run_id=?", runID))
	assert.Equal(t, "{}", nodeResult)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_template"), "max_parallel_tasks")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "join_mode")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_edge"), "condition_expression")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_edge"), "condition_program")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "result")
}

func TestMigration22016UsesMySQLCompatibleLargeResultColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.16.sql", false), ";")
	assert.Contains(t, migration, "`condition_program` longtext null")
	assert.Contains(t, migration, "modify column `condition_program` longtext not null")
	assert.Contains(t, migration, "`result` longtext null")
	assert.Contains(t, migration, "modify column `result` longtext not null")
	assert.NotContains(t, migration, "longtext not null default")
}

func sqliteColumnNames(t *testing.T, store *SqlDb, table string) []string {
	t.Helper()
	rows, err := store.Sql().Db.Query("pragma table_info(`" + table + "`)")
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck
	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	return columns
}
