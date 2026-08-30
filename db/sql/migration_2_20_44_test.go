package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22044AddsKubernetesExecutorMetadata(t *testing.T) {
	legacyVersion := "2.20.43"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "k8s_job_uid")
	require.NoError(t, db.Migrate(store, nil))
	columns := sqliteColumnNames(t, store, "task__runner_attempt")
	for _, column := range []string{
		"k8s_cluster_alias", "k8s_namespace", "k8s_job_name", "k8s_job_uid", "k8s_pod_name",
		"k8s_pod_uid", "k8s_container_name", "k8s_lifecycle", "k8s_terminal_reason",
	} {
		assert.Contains(t, columns, column)
	}

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "task__runner_attempt"), "k8s_job_uid")
}
