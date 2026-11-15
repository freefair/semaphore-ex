package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCredentialPrincipals(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "t", Playbook: "p.yml"})
	require.NoError(t, err)
	schedule, err := store.CreateSchedule(db.Schedule{ProjectID: projectID, TemplateID: template.ID, Name: "s", CronFormat: "* * * * *", Active: true, Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	integration, err := store.CreateIntegration(db.Integration{ProjectID: projectID, TemplateID: &template.ID, Name: "i", AuthMethod: db.IntegrationAuthNone, TaskParams: &db.TaskParams{}})
	require.NoError(t, err)
	first, err := store.CreateUserWithoutPassword(db.User{Username: "principal-first", Name: "First", Email: "principal-first@example.test"})
	require.NoError(t, err)
	second, err := store.CreateUserWithoutPassword(db.User{Username: "principal-second", Name: "Second", Email: "principal-second@example.test"})
	require.NoError(t, err)

	t.Run("a task without trigger has none", func(t *testing.T) {
		_, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID})
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("the last change wins", func(t *testing.T) {
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, first.ID))
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, second.ID))
		userID, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, second.ID, userID)
	})

	t.Run("integrations have their own principal", func(t *testing.T) {
		_, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, first.ID))
		userID, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, first.ID, userID)
	})

	t.Run("deleting the user removes the principal", func(t *testing.T) {
		require.NoError(t, store.DeleteUser(second.ID))
		_, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.False(t, ok, "unattended use must stop rather than fall back to nobody")
	})

	t.Run("invalid IDs are rejected", func(t *testing.T) {
		assert.Error(t, store.SetScheduleCredentialPrincipal(0, first.ID))
		assert.Error(t, store.SetIntegrationCredentialPrincipal(integration.ID, 0))
	})
}
