package sql

import (
	"strings"
	"testing"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowFileArtifactMigrationCreatesAndRollsBackStorageAuthority(t *testing.T) {
	legacyVersion := "2.20.64"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	require.NoError(t, db.Migrate(store, nil))
	for _, table := range []string{
		"workflow_artifact_retention_lock",
		"workflow_artifact_retention_policy",
		"workflow_file_artifact_run_usage",
		"workflow_file_artifact",
		"workflow_file_artifact_chunk",
		"workflow_file_artifact_download_lease",
	} {
		assert.Contains(t, sqliteTableNames(t, store), table)
	}
	for _, column := range []string{
		"credential_provenance", "access_policy", "retention_global_revision",
		"retention_project_revision", "retention_seconds", "retention_max_artifact_bytes",
		"retention_max_run_bytes", "expires_at", "deleted_at",
	} {
		assert.Contains(t, sqliteColumnNames(t, store, "workflow_file_artifact"), column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	for _, table := range []string{
		"workflow_file_artifact_download_lease",
		"workflow_file_artifact_chunk",
		"workflow_file_artifact",
		"workflow_file_artifact_run_usage",
		"workflow_artifact_retention_policy",
		"workflow_artifact_retention_lock",
	} {
		assert.NotContains(t, sqliteTableNames(t, store), table)
	}
}

func TestWorkflowFileArtifactMigrationPreparesForEverySQLDialect(t *testing.T) {
	for _, test := range []struct {
		name, dialect string
		gorp          gorp.Dialect
		blobType      string
	}{
		{"sqlite", "sqlite", gorp.SqliteDialect{}, "longblob"},
		{"mysql", "mysql", gorp.MySQLDialect{}, "longblob"},
		{"postgres", "postgres", gorp.PostgresDialect{}, "bytea"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: test.gorp}}}
			queries := getVersionSQL(test.dialect, "v2.20.65.sql", false)
			joined := strings.ToLower(strings.Join(queries, ";"))
			prepared := strings.ToLower(store.prepareMigration(joined))
			assert.Contains(t, joined, "workflow_file_artifact_chunk")
			assert.Contains(t, joined, "workflow_file_artifact_download_lease")
			assert.Contains(t, joined, "workflow_artifact_retention_lock")
			assert.Contains(t, joined, "values ('global')")
			assert.Contains(t, joined, "credential_provenance")
			assert.Contains(t, prepared, test.blobType)
			assert.NotContains(t, prepared, "sqlite")
		})
	}
}

func TestWorkflowFileArtifactMigrationKeepsActiveDownloadLeaseAsDeleteFence(t *testing.T) {
	legacyVersion := "2.20.64"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))

	project, err := store.CreateProject(db.Project{Name: "artifact lease fence"})
	require.NoError(t, err)
	template, err := store.exec(
		"insert into project__workflow_template(project_id, name) values (?, ?)", project.ID, "artifact workflow",
	)
	require.NoError(t, err)
	templateID, err := template.LastInsertId()
	require.NoError(t, err)
	run, err := store.exec(
		"insert into project__workflow_run(project_id, workflow_template_id, status) values (?, ?, ?)", project.ID, templateID, "running",
	)
	require.NoError(t, err)
	runID, err := run.LastInsertId()
	require.NoError(t, err)

	now := time.Now().UTC()
	artifact, err := store.exec(`insert into workflow_file_artifact(
		project_id, workflow_template_id, workflow_run_id, workflow_node_id, workflow_definition_revision,
		task_id, attempt, logical_name, filename, media_type, size_bytes, uploaded_bytes, sha256, state,
		revision, producer_user_id, producer_template_id, producer_version, credential_provenance,
		access_policy, retention_global_revision, retention_project_revision, retention_seconds,
		retention_max_artifact_bytes, retention_max_run_bytes, created_at, finalized_at, expires_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		project.ID, templateID, runID, 1, 1,
		1, 1, "bundle", "bundle.tar.gz", "application/gzip", 1, 1, strings.Repeat("a", 64), "available",
		1, 1, templateID, "test", "[]",
		`{"revision":1}`, 1, 0, 3600,
		1, 1, now, now, now.Add(time.Hour),
	)
	require.NoError(t, err)
	artifactID, err := artifact.LastInsertId()
	require.NoError(t, err)
	_, err = store.exec(
		"insert into workflow_file_artifact_download_lease(lease_token, artifact_id, expires_at, created_at) values (?, ?, ?, ?)",
		"lease_active", artifactID, now.Add(time.Minute), now,
	)
	require.NoError(t, err)

	_, err = store.exec("delete from workflow_file_artifact where id=?", artifactID)
	require.Error(t, err, "an unexpired download lease must fence artifact deletion")

	migration := strings.ToLower(strings.Join(getVersionSQL("sqlite", "v2.20.65.sql", false), ";"))
	leaseDefinition := strings.Split(strings.Split(migration, "create table `workflow_file_artifact_download_lease`")[1], "create index")[0]
	assert.NotContains(t, leaseDefinition, "on delete cascade")
	assert.Contains(t, migration, "foreign key (`artifact_id`) references `workflow_file_artifact`(`id`) on delete cascade")
	assert.Contains(t, migration, "foreign key (`workflow_run_id`) references `project__workflow_run`(`id`) on delete cascade")
}
