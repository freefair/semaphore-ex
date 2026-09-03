package sql

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTriggerRepositoryRoundTripAndRevisionGuard(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)

	created, err := repository.CreateWorkflowTrigger(repositoryTrigger(projectID, workflow.ID, now))
	require.NoError(t, err)
	assert.Equal(t, 1, created.Revision)
	require.Len(t, created.InputMappings, 1)

	reloaded, err := repository.GetWorkflowTrigger(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "target", reloaded.InputMappings[0].Key)
	assert.True(t, db.WorkflowTriggerCredentialMatches(reloaded.CredentialHash, "swt_repository"))

	created.Enabled = false
	updated, err := repository.UpdateWorkflowTrigger(created, created.Revision)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	_, err = repository.UpdateWorkflowTrigger(created, created.Revision)
	assert.ErrorIs(t, err, db.ErrWorkflowTriggerRevisionConflict)

	triggers, err := repository.GetWorkflowTriggers(projectID, workflow.ID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, triggers, 1)
	assert.False(t, triggers[0].Enabled)
	assert.ErrorIs(t, repository.DeleteWorkflowTrigger(projectID+1, workflow.ID, created.ID), db.ErrNotFound)
}

func TestWorkflowTriggerRepositoryClaimsExternalAndScheduledInvocationsOnce(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)
	trigger, err := repository.CreateWorkflowTrigger(repositoryTrigger(projectID, workflow.ID, now))
	require.NoError(t, err)

	requestHash := db.HashWorkflowTriggerRequestKey(trigger.ID, trigger.CredentialGeneration, "once")
	invocation := repositoryInvocation(t, trigger, workflow, now)
	invocation.RequestKeyHash = &requestHash
	invocation.ExpiresAt = timePointer(now.Add(time.Hour))
	claimed, inserted, err := repository.ClaimWorkflowTriggerInvocation(invocation, now)
	require.NoError(t, err)
	assert.True(t, inserted)
	duplicate, inserted, err := repository.ClaimWorkflowTriggerInvocation(invocation, now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, inserted)
	assert.Equal(t, claimed.ID, duplicate.ID)

	scheduleTrigger, err := repository.CreateWorkflowTrigger(db.WorkflowTrigger{
		ProjectID: projectID, WorkflowTemplateID: workflow.ID, Name: "Nightly",
		Type: db.WorkflowTriggerSchedule, OwnerUserID: 7, Enabled: true, CronFormat: "30 8 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
		Created: now, Updated: now,
	})
	require.NoError(t, err)
	activeSchedules, err := repository.GetActiveWorkflowScheduleTriggers()
	require.NoError(t, err)
	require.Len(t, activeSchedules, 1)
	assert.Equal(t, scheduleTrigger.ID, activeSchedules[0].ID)
	occurrence := db.WorkflowTriggerScheduleOccurrenceIdentity(scheduleTrigger.ID, scheduleTrigger.Revision, workflow.Revision, now)
	scheduled := repositoryInvocation(t, scheduleTrigger, workflow, now)
	scheduled.OccurrenceIdentity = &occurrence
	firstSchedule, inserted, err := repository.ClaimWorkflowTriggerInvocation(scheduled, now)
	require.NoError(t, err)
	assert.True(t, inserted)
	secondSchedule, inserted, err := repository.ClaimWorkflowTriggerInvocation(scheduled, now.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, inserted)
	assert.Equal(t, firstSchedule.ID, secondSchedule.ID)

	history, err := repository.GetWorkflowTriggerInvocations(projectID, trigger.ID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Empty(t, history[0].TriggerSnapshotJSON)
	assert.Equal(t, trigger.ID, history[0].TriggerSnapshot.ID)
	scheduleHistory, err := repository.GetWorkflowTriggerInvocations(projectID, scheduleTrigger.ID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, scheduleHistory, 1)
	assert.Equal(t, scheduleTrigger.ID, scheduleHistory[0].TriggerSnapshot.ID)
}

func TestWorkflowTriggerRepositoryReclaimsExpiredExternalKeyAndPrunesBoundedly(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)
	trigger, err := repository.CreateWorkflowTrigger(repositoryTrigger(projectID, workflow.ID, now))
	require.NoError(t, err)
	requestHash := db.HashWorkflowTriggerRequestKey(trigger.ID, trigger.CredentialGeneration, "reusable")

	invocation := repositoryInvocation(t, trigger, workflow, now.Add(-2*time.Hour))
	invocation.RequestKeyHash = &requestHash
	invocation.ExpiresAt = timePointer(now.Add(-time.Hour))
	old, inserted, err := repository.ClaimWorkflowTriggerInvocation(invocation, now.Add(-2*time.Hour))
	require.NoError(t, err)
	assert.True(t, inserted)

	replacement := repositoryInvocation(t, trigger, workflow, now)
	replacement.RequestKeyHash = &requestHash
	replacement.ExpiresAt = timePointer(now.Add(time.Hour))
	current, inserted, err := repository.ClaimWorkflowTriggerInvocation(replacement, now)
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.NotEqual(t, old.ID, current.ID)

	deleted, err := repository.DeleteExpiredWorkflowTriggerInvocations(now, 1)
	require.NoError(t, err)
	assert.Zero(t, deleted)
}

func TestWorkflowTriggerRepositoryRejectsClaimWhenTriggerStateChanged(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)
	trigger, err := repository.CreateWorkflowTrigger(repositoryTrigger(projectID, workflow.ID, now))
	require.NoError(t, err)

	requestHash := db.HashWorkflowTriggerRequestKey(trigger.ID, trigger.CredentialGeneration, "state-change")
	invocation := repositoryInvocation(t, trigger, workflow, now)
	invocation.RequestKeyHash = &requestHash
	invocation.ExpiresAt = timePointer(now.Add(time.Hour))

	trigger.Enabled = false
	_, err = repository.UpdateWorkflowTrigger(trigger, trigger.Revision)
	require.NoError(t, err)

	_, _, err = repository.ClaimWorkflowTriggerInvocation(invocation, now)
	assert.ErrorIs(t, err, db.ErrWorkflowTriggerStateChanged)
}

func TestWorkflowTriggerRepositoryClaimsSignedWebhookEventAcrossRotationOnce(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	trigger, err := repository.CreateWorkflowTrigger(db.WorkflowTrigger{
		ProjectID: projectID, WorkflowTemplateID: workflow.ID, Name: "Inbound deploy",
		Type: db.WorkflowTriggerWebhook, OwnerUserID: 7, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target",
		}},
		CurrentSigningSecretEncrypted: "ciphertext-current", CurrentSigningKeyID: "swhkid_current",
		NextSigningSecretEncrypted: "ciphertext-next", NextSigningKeyID: "swhkid_next",
		Created: now, Updated: now,
	})
	require.NoError(t, err)
	assert.Empty(t, trigger.CredentialHash, "webhooks do not reuse API credentials")
	assert.Equal(t, "swhkid_current", trigger.CurrentSigningKeyID)

	eventHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	invocation := repositoryInvocation(t, trigger, workflow, now)
	invocation.WebhookEventHash = &eventHash
	invocation.WebhookEventID = "evt_1234567890123456"
	invocation.WebhookKeyID = "swhkid_current"
	invocation.WebhookSignedAt = timePointer(now)
	first, inserted, err := repository.ClaimWorkflowTriggerInvocation(invocation, now)
	require.NoError(t, err)
	require.True(t, inserted)

	trigger.CurrentSigningSecretEncrypted = trigger.NextSigningSecretEncrypted
	trigger.CurrentSigningKeyID = trigger.NextSigningKeyID
	trigger.NextSigningSecretEncrypted = ""
	trigger.NextSigningKeyID = ""
	trigger, err = repository.UpdateWorkflowTrigger(trigger, trigger.Revision)
	require.NoError(t, err)
	invocation.TriggerRevision = trigger.Revision
	invocation.TriggerSnapshotJSON = workflowTriggerSnapshotJSON(t, trigger, now.Add(time.Minute))
	invocation.WebhookKeyID = "swhkid_next"
	invocation.WebhookSignedAt = timePointer(now.Add(time.Minute))
	duplicate, inserted, err := repository.ClaimWorkflowTriggerInvocation(invocation, now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, inserted)
	assert.Equal(t, first.ID, duplicate.ID)
	assert.Equal(t, 1, duplicate.WebhookReplayCount)
	require.NotNil(t, duplicate.WebhookLastReplayedAt)
}

func TestWorkflowTriggerRepositoryClaimsConcurrentSignedWebhookReplayOnce(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	trigger, err := repository.CreateWorkflowTrigger(db.WorkflowTrigger{
		ProjectID: projectID, WorkflowTemplateID: workflow.ID, Name: "Concurrent inbound",
		Type: db.WorkflowTriggerWebhook, OwnerUserID: 7, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target",
		}},
		CurrentSigningSecretEncrypted: "ciphertext-current", CurrentSigningKeyID: "swhkid_current",
		Created: now, Updated: now,
	})
	require.NoError(t, err)
	eventHash := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	baseInvocation := repositoryInvocation(t, trigger, workflow, now)
	baseInvocation.WebhookEventHash = &eventHash
	baseInvocation.WebhookEventID = "evt_1234567890123456"
	baseInvocation.WebhookKeyID = "swhkid_current"
	baseInvocation.WebhookSignedAt = timePointer(now)

	var group sync.WaitGroup
	type claimResult struct {
		invocation db.WorkflowTriggerInvocation
		inserted   bool
	}
	results := make(chan claimResult, 8)
	errors := make(chan error, 8)
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			invocation := baseInvocation
			claimed, inserted, claimErr := repository.ClaimWorkflowTriggerInvocation(invocation, now)
			if claimErr != nil {
				errors <- claimErr
				return
			}
			results <- claimResult{invocation: claimed, inserted: inserted}
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for claimErr := range errors {
		require.NoError(t, claimErr)
	}
	var ids []int
	insertedCount := 0
	for result := range results {
		ids = append(ids, result.invocation.ID)
		if result.inserted {
			insertedCount++
		}
	}
	require.Len(t, ids, 8)
	assert.Equal(t, 1, insertedCount, "exactly one concurrent webhook claim inserts a durable replay identity")
	for _, id := range ids {
		assert.Equal(t, ids[0], id)
	}
	history, err := repository.GetWorkflowTriggerInvocations(projectID, trigger.ID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, 7, history[0].WebhookReplayCount)
	require.NotNil(t, history[0].WebhookLastReplayedAt)
}

func repositoryTrigger(projectID int, workflowID int, now time.Time) db.WorkflowTrigger {
	return db.WorkflowTrigger{
		ProjectID: projectID, WorkflowTemplateID: workflowID, Name: "Deploy API",
		Type: db.WorkflowTriggerAPI, OwnerUserID: 7, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target",
		}},
		CredentialHash: db.HashWorkflowTriggerCredential("swt_repository"), CredentialGeneration: 1,
		Created: now, Updated: now,
	}
}

func repositoryInvocation(t *testing.T, trigger db.WorkflowTrigger, workflow db.WorkflowTemplate, now time.Time) db.WorkflowTriggerInvocation {
	t.Helper()
	snapshotJSON := workflowTriggerSnapshotJSON(t, trigger, now)
	return db.WorkflowTriggerInvocation{
		ProjectID: trigger.ProjectID, WorkflowTriggerID: trigger.ID, WorkflowTemplateID: workflow.ID,
		TriggerRevision: trigger.Revision, CredentialGeneration: trigger.CredentialGeneration,
		DefinitionRevision: workflow.Revision, Status: db.WorkflowTriggerInvocationClaimed,
		TriggerSnapshotJSON: snapshotJSON, InputSnapshotJSON: `{}`, Created: now, Updated: now,
	}
}

func workflowTriggerSnapshotJSON(t *testing.T, trigger db.WorkflowTrigger, now time.Time) string {
	t.Helper()
	snapshotJSON, err := json.Marshal(db.WorkflowTriggerSnapshot{
		ID: trigger.ID, Revision: trigger.Revision, CredentialGeneration: trigger.CredentialGeneration,
		Name: trigger.Name, Type: trigger.Type, OwnerUserID: trigger.OwnerUserID, TriggeredAt: now,
	})
	require.NoError(t, err)
	return string(snapshotJSON)
}

func timePointer(value time.Time) *time.Time { return &value }
