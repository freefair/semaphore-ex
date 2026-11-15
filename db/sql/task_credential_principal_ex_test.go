package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
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

	t.Run("a failed write leaves no principal", func(t *testing.T) {
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, first.ID))
		assert.Error(t, store.SetIntegrationCredentialPrincipal(integration.ID, 999_999), "the user does not exist")
		_, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, ok, "the previous principal must not answer for the failed change")
	})

	t.Run("a claim hides the principal and serializes changes", func(t *testing.T) {
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, first.ID))
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.False(t, found, "no principal while a change is in progress")

		_, ok, err = store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		assert.False(t, ok, "a second change is refused")

		require.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, token, first.ID))
		userID, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, first.ID, userID)
	})

	t.Run("finishing with a claim that is not held invalidates the trigger", func(t *testing.T) {
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, first.ID))
		assert.Error(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID, "not-held", first.ID))
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, found)
		_, err = store.exec("update project__integration_credential_principal set pending_since=? where integration_id=?", tz.Now().Add(-2*time.Minute), integration.ID)
		require.NoError(t, err)
	})

	t.Run("an aborted change leaves no principal", func(t *testing.T) {
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, first.ID))
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.AbortCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID, token))
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, found)
		_, ok, err = store.BeginCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID)
		require.NoError(t, err)
		assert.True(t, ok, "the released trigger can be claimed again")
	})

	t.Run("a stale claim expires", func(t *testing.T) {
		_, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-2*time.Minute), schedule.ID)
		require.NoError(t, err)
		_, ok, err = store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("a taken-over claim leaves neither change as principal", func(t *testing.T) {
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, first.ID))
		slow, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-2*time.Minute), schedule.ID)
		require.NoError(t, err)
		fast, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok, "the stale claim is taken over")

		// The slow change writes its configuration last, then finishes.
		assert.Error(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, slow, first.ID))
		assert.Error(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, fast, first.ID),
			"the change that took over cannot finish after the loss was detected")
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("a takeover finished first is invalidated by the late change", func(t *testing.T) {
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, first.ID))
		slow, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-2*time.Minute), schedule.ID)
		require.NoError(t, err)
		fast, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, fast, first.ID))

		assert.Error(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, slow, first.ID))
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.False(t, found, "the late change's configuration has no principal")
	})

	t.Run("a trigger without row can be claimed", func(t *testing.T) {
		fresh, err := store.CreateSchedule(db.Schedule{ProjectID: projectID, TemplateID: template.ID, Name: "fresh", CronFormat: "* * * * *", Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
		require.NoError(t, err)
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, fresh.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, fresh.ID, token, first.ID))
	})

	t.Run("invalid IDs are rejected", func(t *testing.T) {
		assert.Error(t, store.SetScheduleCredentialPrincipal(0, first.ID))
		assert.Error(t, store.SetIntegrationCredentialPrincipal(integration.ID, 0))
	})
}
