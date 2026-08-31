package sql

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22050AddsWorkflowVersionStorage(t *testing.T) {
	legacyVersion := "2.20.49"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "project__workflow_version")
	for _, column := range []string{
		"workflow_template_id", "version_number", "parent_version_id", "restored_from_version_id",
		"author_user_id", "message", "created", "content_fingerprint", "definition_snapshot",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_version"), column)
	}
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_run"), "workflow_version_id")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_node"), "cross_project_template_reference")
	assert.Contains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "cross_project_template_provenance")
	assert.Contains(t, sqliteColumnNames(t, store, "task"), "workflow_template_provenance")
	assert.Contains(t, sqliteTableNames(t, store), "project__template_version")
	for _, column := range []string{
		"owner_project_id", "template_id", "version_number", "author_user_id", "created",
		"content_fingerprint", "execution_snapshot",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "project__template_version"), column)
	}
	assert.Contains(t, sqliteTableNames(t, store), "project__cross_project_template_grant")
	for _, column := range []string{
		"owner_project_id", "consumer_project_id", "template_id", "min_template_version",
		"max_template_version", "operations", "status", "revision", "reason", "created_by_user_id",
		"accepted_by_user_id", "accepted_at", "revoked_by_user_id", "revoked_at", "revocation_reason",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "project__cross_project_template_grant"), column)
	}
	assert.Contains(t, sqliteTableNames(t, store), "project__cross_project_template_grant_version")
	for _, column := range []string{"grant_id", "template_version_id"} {
		assert.Contains(t, sqliteColumnNames(t, store, "project__cross_project_template_grant_version"), column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "project__workflow_version")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run"), "workflow_version_id")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "cross_project_template_reference")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_run_node"), "cross_project_template_provenance")
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "workflow_template_provenance")
	assert.NotContains(t, sqliteTableNames(t, store), "project__template_version")
	assert.NotContains(t, sqliteTableNames(t, store), "project__cross_project_template_grant")
	assert.NotContains(t, sqliteTableNames(t, store), "project__cross_project_template_grant_version")
}

func TestMigration22050UsesPortableCrossProjectProvenanceColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.50.sql", false), ";")
	rollback := strings.Join(getVersionSQL("mysql", "v2.20.50.err.sql", true), ";")

	for _, column := range []string{"cross_project_template_reference", "cross_project_template_provenance"} {
		assert.Contains(t, migration, "`"+column+"` longtext null")
		assert.Contains(t, migration, "modify column `"+column+"` longtext not null")
	}
	assert.NotContains(t, migration, "longtext not null default")
	assert.NotContains(t, rollback, "drop index `project__cross_project_template_grant_version__template_version`")
	assert.Contains(t, rollback, "drop table `project__cross_project_template_grant_version`")
}
