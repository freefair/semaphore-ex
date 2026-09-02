package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleTimezoneMigrationPreservesExistingRowsAndRollsBack(t *testing.T) {
	legacyVersion := "2.20.57"
	store := InitConfigCreateTestStoreAt(&legacyVersion)
	t.Cleanup(store.Close)

	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "Scheduled", Playbook: "site.yml",
	})
	require.NoError(t, err)
	_, err = store.exec(
		"insert into project__schedule (project_id, template_id, cron_format, `name`, `active`, `type`, delete_after_run) values (?, ?, ?, ?, ?, ?, ?)",
		projectID, template.ID, "0 9 * * *", "Legacy", true, "", false,
	)
	require.NoError(t, err)
	assert.NotContains(t, sqliteColumnNames(t, store, "project__schedule"), "timezone")

	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "project__schedule"), "timezone")
	schedules, err := store.GetSchedules()
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Nil(t, schedules[0].Timezone)

	require.NoError(t, db.Rollback(store, legacyVersion))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__schedule"), "timezone")
}

func TestScheduleTimezoneRoundTripsThroughRepository(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "Scheduled", Playbook: "site.yml",
	})
	require.NoError(t, err)

	berlin := "Europe/Berlin"
	created, err := store.CreateSchedule(db.Schedule{
		ProjectID: projectID, TemplateID: template.ID, Name: "Morning", CronFormat: "0 9 * * *", Timezone: &berlin, Active: true,
	})
	require.NoError(t, err)
	require.NotNil(t, created.Timezone)
	assert.Equal(t, berlin, *created.Timezone)

	loaded, err := store.GetSchedule(projectID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.Timezone)
	assert.Equal(t, berlin, *loaded.Timezone)

	tokyo := "Asia/Tokyo"
	loaded.Timezone = &tokyo
	require.NoError(t, store.UpdateSchedule(loaded))
	updated, err := store.GetSchedule(projectID, created.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.Timezone)
	assert.Equal(t, tokyo, *updated.Timezone)
}

func TestScheduleTimezoneMigrationPreparesForEverySupportedDialect(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		queries := getVersionSQL(dialect, "v2.20.58.sql", false)
		require.NotEmpty(t, queries)
		assert.Contains(t, queries[0], "timezone")
		assert.Contains(t, queries[0], "varchar(128)")
	}
}
