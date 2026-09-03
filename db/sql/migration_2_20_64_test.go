package sql

import (
	"strings"
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignedWebhookPersistenceMigrationFailsClosedForLegacyInboundRows(t *testing.T) {
	legacyVersion := "2.20.63"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	connection := store.GetConnection()
	project, err := store.CreateProject(db.Project{Name: "Signed webhook migration"})
	require.NoError(t, err)
	result, err := connection.Exec("insert into project__workflow_template(project_id, name, definition_version, revision, parameter_definitions) values (?, ?, ?, ?, ?)", project.ID, "Webhook", 1, 1, "[]")
	require.NoError(t, err)
	workflowID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = connection.Exec("insert into audit_webhook_config(id, endpoint, encrypted_credential, credential_configured, paused, created, updated) values (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", 1, "https://audit.example.test/events", "legacy-bearer", true, false)
	require.NoError(t, err)
	result, err = connection.Exec("insert into project__workflow_trigger(project_id, workflow_template_id, revision, name, type, owner_user_id, enabled, input_mappings, credential_hash, credential_generation, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", project.ID, workflowID, 1, "legacy-webhook", db.WorkflowTriggerWebhook, 1, true, "[]", db.HashWorkflowTriggerCredential("legacy-webhook"), 7)
	require.NoError(t, err)
	webhookID, err := result.LastInsertId()
	require.NoError(t, err)
	result, err = connection.Exec("insert into project__workflow_trigger(project_id, workflow_template_id, revision, name, type, owner_user_id, enabled, input_mappings, credential_hash, credential_generation, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", project.ID, workflowID, 1, "legacy-api", db.WorkflowTriggerAPI, 1, true, "[]", db.HashWorkflowTriggerCredential("legacy-api"), 8)
	require.NoError(t, err)
	apiID, err := result.LastInsertId()
	require.NoError(t, err)

	require.NoError(t, db.Migrate(store, nil))
	for table, columns := range map[string][]string{
		"audit_webhook_config":                 {"current_signing_secret_encrypted", "next_signing_secret_encrypted", "current_signing_key_id", "next_signing_key_id", "current_signing_generation", "next_signing_generation", "signing_state_revision"},
		"audit_webhook_delivery":               {"last_signed_at"},
		"project__workflow_trigger":            {"current_signing_secret_encrypted", "next_signing_secret_encrypted", "current_signing_key_id", "next_signing_key_id"},
		"project__workflow_trigger_invocation": {"webhook_event_hash", "webhook_event_id", "webhook_key_id", "webhook_signed_at", "webhook_replay_count", "webhook_last_replayed_at"},
	} {
		for _, column := range columns {
			assert.Contains(t, sqliteColumnNames(t, store, table), column, table)
		}
	}
	assert.Contains(t, sqliteTableNames(t, store), "audit_webhook_delivery_attempt")

	var bearer string
	require.NoError(t, connection.SelectOne(&bearer, "select encrypted_credential from audit_webhook_config where id=1"))
	assert.Equal(t, "legacy-bearer", bearer, "receiver Bearer config stays separate from signing state")
	var webhookHash, apiHash string
	require.NoError(t, connection.SelectOne(&webhookHash, "select credential_hash from project__workflow_trigger where id=?", webhookID))
	require.NoError(t, connection.SelectOne(&apiHash, "select credential_hash from project__workflow_trigger where id=?", apiID))
	assert.Equal(t, db.HashWorkflowTriggerCredential("legacy-webhook"), webhookHash, "legacy webhook credential remains dormant for rollback compatibility")
	assert.Equal(t, db.HashWorkflowTriggerCredential("legacy-api"), apiHash, "API trigger credentials remain compatible")

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteTableNames(t, store), "audit_webhook_delivery_attempt")
	assert.NotContains(t, sqliteColumnNames(t, store, "project__workflow_trigger_invocation"), "webhook_event_hash")
}

func TestSignedWebhookPersistenceMigrationPreparesForEverySQLDialect(t *testing.T) {
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
			queries := getVersionSQL(test.dialect, "v2.20.64.sql", false)
			joined := strings.ToLower(strings.Join(queries, ";"))
			assert.Contains(t, joined, "audit_webhook_delivery_attempt")
			assert.Contains(t, joined, "webhook_event_hash")
			assert.Contains(t, joined, "current_signing_secret_encrypted")
			assert.NotContains(t, store.prepareMigration(joined), "sqlite")
		})
	}
}
