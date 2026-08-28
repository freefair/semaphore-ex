package sql

import (
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration22015BackfillsMultipleLegacyWorkflowRuns(t *testing.T) {
	legacyVersion := "2.20.14"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Legacy workflow runs"})
	require.NoError(t, err)
	result, err := store.Sql().Exec(
		"insert into project__workflow_template(project_id, name, definition_version, revision) values (?, ?, ?, ?)",
		project.ID, "Legacy", 1, 1,
	)
	require.NoError(t, err)
	workflowID, err := result.LastInsertId()
	require.NoError(t, err)
	for range 2 {
		_, err = store.Sql().Exec(
			"insert into project__workflow_run(project_id, workflow_template_id, status) values (?, ?, ?)",
			project.ID, workflowID, "success",
		)
		require.NoError(t, err)
	}

	require.NoError(t, db.Migrate(store, nil))
	var runs []struct {
		ID            int       `db:"id"`
		CorrelationID string    `db:"correlation_id"`
		Created       time.Time `db:"created"`
	}
	_, err = store.Sql().Select(&runs,
		"select id, correlation_id, created from project__workflow_run order by id")
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, "legacy-1", runs[0].CorrelationID)
	assert.Equal(t, "legacy-2", runs[1].CorrelationID)
	assert.NotZero(t, runs[0].Created)
	assert.NotZero(t, runs[1].Created)
}

func TestMigration22015UsesLargeMySQLSnapshotColumns(t *testing.T) {
	migration := strings.Join(getVersionSQL("mysql", "v2.20.15.sql", false), ";")
	assert.Contains(t, migration, "`definition_snapshot` longtext")
	assert.Contains(t, migration, "`template_snapshot` longtext")
	assert.Contains(t, migration, "`workflow_template_snapshot` longtext")
}
