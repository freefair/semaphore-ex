package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22018BackfillsWorkflowParameterSnapshotsAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.17"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Legacy workflow parameters"})
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
	var definitionParameters string
	require.NoError(t, store.Sql().SelectOne(&definitionParameters,
		"select parameter_definitions from project__workflow_template where id=?", workflowID))
	assert.Equal(t, "[]", definitionParameters)
	var overridePolicy string
	require.NoError(t, store.Sql().SelectOne(&overridePolicy,
		"select override_policy from project__workflow_node where id=?", nodeID))
	assert.Equal(t, "{}", overridePolicy)
	var parameterSnapshot string
	require.NoError(t, store.Sql().SelectOne(&parameterSnapshot,
		"select parameter_snapshot from project__workflow_run where id=?", runID))
	assert.Equal(t, "{}", parameterSnapshot)
	var overrideSnapshot string
	require.NoError(t, store.Sql().SelectOne(&overrideSnapshot,
		"select override_snapshot from project__workflow_run_node where workflow_run_id=?", runID))
	assert.Equal(t, "{}", overrideSnapshot)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_template"), "parameter_definitions")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "override_policy")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run"), "parameter_snapshot")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "override_snapshot")
}

func TestMigration22018UsesMySQLCompatibleSnapshotColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.18.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.18.err.sql", true), ";")
	for _, column := range []string{"parameter_definitions", "override_policy", "parameter_snapshot", "override_snapshot"} {
		assert.Contains(t, migration, "`"+column+"` longtext null")
		assert.Contains(t, migration, "modify column `"+column+"` longtext not null")
		assert.Contains(t, rollback, "drop column `"+column+"`")
	}
	assert.NotContains(t, migration, "longtext not null default")
	assert.NotContains(t, rollback, "drop index if exists")
}
