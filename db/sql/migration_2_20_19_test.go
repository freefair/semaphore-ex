package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22019AddsWorkflowTriggerPersistenceAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.18"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Legacy workflow triggers"})
	require.NoError(t, err)
	result, err := store.Sql().Exec(
		"insert into project__workflow_template(project_id, name, definition_version, revision, parameter_definitions) values (?, ?, ?, ?, ?)",
		project.ID, "Legacy", 1, 1, "[]",
	)
	require.NoError(t, err)
	workflowID, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = store.Sql().Exec(
		"insert into project__workflow_run(project_id, workflow_template_id, status, trigger_snapshot) values (?, ?, ?, ?)",
		project.ID, workflowID, "pending", "{}",
	)
	require.Error(t, err)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "project__workflow_trigger")
	assert.Contains(t, sqliteTableNames(t, store), "project__workflow_trigger_invocation")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_run"), "trigger_snapshot")

	result, err = store.Sql().Exec(
		"insert into project__workflow_trigger(project_id, workflow_template_id, revision, name, type, owner_user_id, enabled, input_mappings, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		project.ID, workflowID, 1, "Nightly", "schedule", 1, 1, "[]",
	)
	require.NoError(t, err)
	triggerID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"insert into project__workflow_trigger_invocation(project_id, workflow_trigger_id, workflow_template_id, trigger_revision, definition_revision, occurrence_identity, status, trigger_snapshot, input_snapshot, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		project.ID, triggerID, workflowID, 1, 1, "wts_000000000000000000000000000000000000000000000000000000000000", "claimed", "{}", "{}",
	)
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"insert into project__workflow_trigger_invocation(project_id, workflow_trigger_id, workflow_template_id, trigger_revision, definition_revision, occurrence_identity, status, trigger_snapshot, input_snapshot, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		project.ID, triggerID, workflowID, 1, 1, "wts_000000000000000000000000000000000000000000000000000000000000", "claimed", "{}", "{}",
	)
	require.Error(t, err)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_trigger_invocation")
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_trigger")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run"), "trigger_snapshot")
}

func TestMigration22019UsesPortableWorkflowTriggerColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.19.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.19.err.sql", true), ";")
	assert.Contains(t, migration, "`credential_hash` varchar(71)")
	assert.Contains(t, migration, "`request_key_hash` varchar(71) null")
	assert.Contains(t, migration, "`occurrence_identity` varchar(64) null")
	assert.Contains(t, migration, "`trigger_snapshot` longtext null")
	assert.NotContains(t, migration, "longtext not null default")
	assert.Contains(t, rollback, "drop table if exists `project__workflow_trigger_invocation`")
	assert.Contains(t, rollback, "drop table if exists `project__workflow_trigger`")
}
