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

	// seed puts a trigger into the state "user made the last change".
	seed := func(kind db.CredentialPrincipalKind, triggerID int, userID int) error {
		table, column, err := credentialPrincipalTable(kind)
		if err != nil {
			return err
		}
		if _, err = store.exec("delete from "+table+" where "+column+"=?", triggerID); err != nil {
			return err
		}
		fingerprint, err := store.credentialPrincipalFingerprint(kind, triggerID)
		if err != nil {
			return err
		}
		_, err = store.exec("insert into "+table+" ("+column+", user_id, config_fingerprint) values (?, ?, ?)", triggerID, userID, fingerprint)
		return err
	}
	change := func(kind db.CredentialPrincipalKind, triggerID int, userID int) {
		token, ok, err := store.BeginCredentialPrincipalChange(kind, triggerID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.FinishCredentialPrincipalChange(kind, triggerID, token, userID))
	}

	t.Run("the last change wins", func(t *testing.T) {
		require.NoError(t, store.SetScheduleCredentialPrincipal(schedule.ID, first.ID))
		change(db.CredentialPrincipalSchedule, schedule.ID, second.ID)
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

	t.Run("creation does not replace a change that came first", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalIntegration, integration.ID, first.ID))
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, second.ID))
		userID, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, first.ID, userID)

		token, claimed, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, store.SetIntegrationCredentialPrincipal(integration.ID, second.ID))
		_, ok, err = store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, ok, "a claim in progress is not overwritten by the creator")
		require.NoError(t, store.AbortCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID, token))
	})

	t.Run("a failed creation leaves no principal", func(t *testing.T) {
		fresh, err := store.CreateSchedule(db.Schedule{ProjectID: projectID, TemplateID: template.ID, Name: "orphan", CronFormat: "* * * * *", Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
		require.NoError(t, err)
		assert.Error(t, store.SetScheduleCredentialPrincipal(fresh.ID, 999_999), "the user does not exist")
		_, ok, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &fresh.ID})
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("renewal keeps a claim from being taken over", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-db.CredentialPrincipalClaimLifetime-time.Minute), schedule.ID)
		require.NoError(t, err)
		require.NoError(t, store.RenewCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, token))
		_, ok, err = store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		assert.False(t, ok, "a renewed claim is not stale")
		assert.Error(t, store.RenewCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, "not-held"))
		require.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, token, first.ID))
	})

	t.Run("a claim hides the principal and serializes changes", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
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
		require.NoError(t, seed(db.CredentialPrincipalIntegration, integration.ID, first.ID))
		assert.Error(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalIntegration, integration.ID, "not-held", first.ID))
		_, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, IntegrationID: &integration.ID})
		require.NoError(t, err)
		assert.False(t, found)
		_, err = store.exec("update project__integration_credential_principal set pending_since=? where integration_id=?", tz.Now().Add(-db.CredentialPrincipalClaimLifetime-time.Minute), integration.ID)
		require.NoError(t, err)
	})

	t.Run("an aborted change leaves no principal", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalIntegration, integration.ID, first.ID))
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
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-db.CredentialPrincipalClaimLifetime-time.Minute), schedule.ID)
		require.NoError(t, err)
		_, ok, err = store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("a taken-over claim leaves neither change as principal", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		slow, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-db.CredentialPrincipalClaimLifetime-time.Minute), schedule.ID)
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
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		slow, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = store.exec("update project__schedule_credential_principal set pending_since=? where schedule_id=?", tz.Now().Add(-db.CredentialPrincipalClaimLifetime-time.Minute), schedule.ID)
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

	t.Run("a later write ends the principal's responsibility", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		task := db.Task{ProjectID: projectID, ScheduleID: &schedule.ID}
		_, found, err := store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		require.True(t, found)

		current, err := store.GetSchedule(projectID, schedule.ID)
		require.NoError(t, err)
		current.CronFormat = "*/5 * * * *"
		require.NoError(t, store.UpdateSchedule(current))
		_, found, err = store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		assert.False(t, found, "a write outside the principal's change must not run under it")
	})

	t.Run("a change that writes the trigger answers for what it wrote", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID)
		require.NoError(t, err)
		require.True(t, ok)
		current, err := store.GetSchedule(projectID, schedule.ID)
		require.NoError(t, err)
		current.CronFormat = "0 3 * * *"
		require.NoError(t, store.UpdateSchedule(current))
		require.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, schedule.ID, token, first.ID))
		userID, found, err := store.GetTaskCredentialPrincipal(db.Task{ProjectID: projectID, ScheduleID: &schedule.ID})
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, first.ID, userID)
	})

	t.Run("operational changes keep the principal", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalSchedule, schedule.ID, first.ID))
		task := db.Task{ProjectID: projectID, ScheduleID: &schedule.ID}
		require.NoError(t, store.SetScheduleCommitHash(projectID, schedule.ID, "0123abcd"))
		require.NoError(t, store.SetScheduleActive(projectID, schedule.ID, false))
		require.NoError(t, store.SetScheduleActive(projectID, schedule.ID, true))
		userID, found, err := store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, first.ID, userID)
	})

	t.Run("an integration matcher written later ends the principal's responsibility", func(t *testing.T) {
		require.NoError(t, seed(db.CredentialPrincipalIntegration, integration.ID, first.ID))
		task := db.Task{ProjectID: projectID, IntegrationID: &integration.ID}
		_, found, err := store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		require.True(t, found)
		_, err = store.CreateIntegrationMatcher(projectID, db.IntegrationMatcher{IntegrationID: integration.ID, Name: "branch", MatchType: db.IntegrationMatchBody, Method: db.IntegrationMatchMethodEquals, BodyDataType: db.IntegrationBodyDataJSON, Key: "ref", Value: "main"})
		require.NoError(t, err)
		_, found, err = store.GetTaskCredentialPrincipal(task)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("finishing on a deleted trigger is no error", func(t *testing.T) {
		gone, err := store.CreateSchedule(db.Schedule{ProjectID: projectID, TemplateID: template.ID, Name: "gone", CronFormat: "* * * * *", Type: db.ScheduleTypeCron, TaskParams: &db.TaskParams{}})
		require.NoError(t, err)
		token, ok, err := store.BeginCredentialPrincipalChange(db.CredentialPrincipalSchedule, gone.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, store.DeleteSchedule(projectID, gone.ID))
		assert.NoError(t, store.FinishCredentialPrincipalChange(db.CredentialPrincipalSchedule, gone.ID, token, first.ID))
	})

	t.Run("invalid IDs are rejected", func(t *testing.T) {
		assert.Error(t, store.SetScheduleCredentialPrincipal(0, first.ID))
		assert.Error(t, store.SetIntegrationCredentialPrincipal(integration.ID, 0))
	})
}
