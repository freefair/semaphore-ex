package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpstreamTemplateMigrationsPreserveForkUpgradeAndRollback(t *testing.T) {
	previous := "2.20.67"
	store := InitConfigCreateTestStoreAt(&previous)
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	for _, name := range []string{"Build", "Build", "Build (2)"} {
		_, err := store.exec("insert into project__template (project_id, repository_id, name, playbook, app) values (?, ?, ?, 'site.yml', '')", projectID, repositoryID, name)
		require.NoError(t, err)
	}
	require.NoError(t, db.Migrate(store, nil))
	var names []string
	_, err := store.Sql().Select(&names, "select name from project__template order by id")
	require.NoError(t, err)
	assert.Equal(t, []string{"Build", "Build (3)", "Build (2)"}, names)
	template, err := store.GetTemplateByName(projectID, "Build (3)")
	require.NoError(t, err)
	assert.False(t, template.SuppressErrorAlerts)
	template.SuppressErrorAlerts = true
	require.NoError(t, store.UpdateTemplate(template))
	loaded, err := store.GetTemplate(projectID, template.ID)
	require.NoError(t, err)
	assert.True(t, loaded.SuppressErrorAlerts)
	require.NoError(t, db.Rollback(store, previous))
	assert.NotContains(t, sqliteColumnNames(t, store, "project__template"), "suppress_error_alerts")
	indexCount, err := store.Sql().SelectInt("select count(*) from sqlite_master where type='index' and name='project__template__project_id_name'")
	require.NoError(t, err)
	require.Zero(t, indexCount, "rollback must remove the new index before re-upgrade")
	require.NoError(t, db.Migrate(store, nil))
	_, err = store.GetTemplateByName(projectID, "Build (3)")
	require.NoError(t, err)
}
