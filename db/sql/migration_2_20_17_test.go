package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22017BackfillsWorkflowArtifactMetadataAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.16"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Legacy workflow artifacts"})
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
	var definitionMetadata struct {
		Outputs string `db:"artifact_outputs"`
		Inputs  string `db:"artifact_inputs"`
	}
	require.NoError(t, store.Sql().SelectOne(&definitionMetadata,
		"select artifact_outputs, artifact_inputs from project__workflow_node where id=?", nodeID))
	assert.Equal(t, "[]", definitionMetadata.Outputs)
	assert.Equal(t, "[]", definitionMetadata.Inputs)
	var runInputs string
	require.NoError(t, store.Sql().SelectOne(&runInputs,
		"select artifact_inputs from project__workflow_run_node where workflow_run_id=?", runID))
	assert.Equal(t, "[]", runInputs)
	assert.Contains(t, sqliteTableNames(t, store), "project__workflow_artifact")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "artifact_outputs")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "artifact_inputs")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "artifact_inputs")
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_artifact")
}

func TestMigration22017UsesMySQLCompatibleArtifactColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.17.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.17.err.sql", true), ";")
	assert.Contains(t, migration, "`artifact_outputs` longtext null")
	assert.Contains(t, migration, "modify column `artifact_outputs` longtext not null")
	assert.Contains(t, migration, "`artifact_inputs` longtext null")
	assert.Contains(t, migration, "modify column `artifact_inputs` longtext not null")
	assert.Contains(t, migration, "`value_json` longtext null")
	assert.Contains(t, migration, "`encrypted_value` longtext null")
	assert.NotContains(t, migration, "longtext not null default")
	assert.Contains(t, rollback, "drop table if exists `project__workflow_artifact`")
	assert.NotContains(t, rollback, "drop index if exists")
}

func sqliteTableNames(t *testing.T, store *SqlDb) []string {
	t.Helper()
	var names []string
	_, err := store.Sql().Select(&names,
		"select name from sqlite_master where type='table' order by name")
	require.NoError(t, err)
	return names
}
