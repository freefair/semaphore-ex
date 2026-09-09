package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpstreamSeamMigrationsPreserveExistingForkTemplates(t *testing.T) {
	version := "2.20.65"
	store := InitConfigCreateTestStoreAt(&version)
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	templateID, err := store.insert("id", "insert into project__template (project_id, repository_id, name, playbook, app) values (?, ?, 'Preserved', 'site.yml', 'ansible')", projectID, repositoryID)
	require.NoError(t, err)
	require.NotContains(t, sqliteColumnNames(t, store, "project__template"), "working_directory")
	require.NoError(t, db.Migrate(store, nil))
	template, err := store.GetTemplate(projectID, templateID)
	require.NoError(t, err)
	require.Equal(t, "Preserved", template.Name)
	require.Nil(t, template.WorkingDirectory)
	directory := "deploy"
	_, err = store.exec("update project__template set working_directory=? where id=?", directory, templateID)
	require.NoError(t, err)
	loaded, err := store.GetTemplate(projectID, templateID)
	require.NoError(t, err)
	require.Equal(t, directory, *loaded.WorkingDirectory)
	require.Contains(t, sqliteColumnNames(t, store, "project__workflow_node"), "delay_seconds")
	require.Contains(t, matrixUserTables(t, store), "project__workflow_delay")
	require.NoError(t, db.Rollback(store, version))
	require.NotContains(t, sqliteColumnNames(t, store, "project__template"), "working_directory")
	require.NotContains(t, sqliteColumnNames(t, store, "project__workflow_node"), "delay_seconds")
	require.NotContains(t, matrixUserTables(t, store), "project__workflow_delay")
	require.NoError(t, db.Migrate(store, nil))
	loaded, err = store.GetTemplate(projectID, templateID)
	require.NoError(t, err)
	require.Equal(t, "Preserved", loaded.Name)
}
