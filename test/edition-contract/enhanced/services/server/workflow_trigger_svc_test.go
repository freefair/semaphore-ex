package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowsql "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowTriggerSignedWebhookClaimsBeforeMappingAndNeverFallsBackToBearer(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.service.(*workflowTriggerService).cipher = workflowTriggerTestCipher{}
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Inbound", Type: db.WorkflowTriggerWebhook, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &fixture.actor)
	require.NoError(t, err)
	require.NotEmpty(t, created.WebhookSigningSecret)
	require.Empty(t, created.Credential)

	body := []byte(`{"inputs":{"target":"eu"}}`)
	headers := pro_interfaces.WebhookSignatureHeaders{Version: pro_interfaces.WebhookSignatureProtocolVersion, EventID: "evt_1234567890123456", Timestamp: fmt.Sprintf("%d", time.Now().Unix()), KeyID: created.Trigger.CurrentSigningKeyID}
	bound, err := pro_interfaces.BindWebhookSignedRequest("POST", "/api/workflow-triggers/1/2/3/webhook?exact=1", headers, body)
	require.NoError(t, err)
	bound.Signature, err = pro_interfaces.SignWebhookRequest(created.WebhookSigningSecret, bound)
	require.NoError(t, err)
	_, err = fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, bound)
	require.NoError(t, err)
	assert.Equal(t, 1, fixture.starter.calls)
	_, err = fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, bound)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerWebhookReplay)
	assert.Equal(t, 1, fixture.starter.calls)

	invalid := bound
	invalid.EventID = "evt_abcdefghijklmnop"
	invalid.Payload = []byte(`{"unknown":true}`)
	invalid.Signature, err = pro_interfaces.SignWebhookRequest(created.WebhookSigningSecret, invalid)
	require.NoError(t, err)
	_, err = fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, invalid)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
	assert.Equal(t, 1, fixture.starter.calls, "a verified malformed payload is claimed but never started")
	history, err := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 2, "strict mapping failures are durably claimed after verification")
}

func TestWorkflowTriggerSigningLifecycleFailsClosedAndOverlapsOnlyDuringRotation(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	implementation := fixture.service.(*workflowTriggerService)
	implementation.cipher = workflowTriggerTestCipher{}
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Inbound", Type: db.WorkflowTriggerWebhook, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &fixture.actor)
	require.NoError(t, err)
	require.NotEmpty(t, created.WebhookSigningSecret)
	persisted, err := fixture.repository.GetWorkflowTrigger(fixture.projectID, fixture.workflow.ID, created.Trigger.ID)
	require.NoError(t, err)
	assert.NotEqual(t, created.WebhookSigningSecret, persisted.CurrentSigningSecretEncrypted, "the persistence boundary receives the cipher output, never the one-time response value")

	staged, err := fixture.service.StageWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, created.Trigger.Revision, &fixture.actor)
	require.NoError(t, err)
	require.NotEmpty(t, staged.WebhookSigningSecret)
	assert.Greater(t, staged.Trigger.NextSigningGeneration, staged.Trigger.CurrentSigningGeneration)
	assertSignedWebhookAccepted(t, fixture, created.Trigger.ID, created.WebhookSigningSecret, staged.Trigger.CurrentSigningKeyID, "evt_1234567890123456")
	assertSignedWebhookAccepted(t, fixture, created.Trigger.ID, staged.WebhookSigningSecret, staged.Trigger.NextSigningKeyID, "evt_1234567890123457")

	promoted, err := fixture.service.PromoteWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, staged.Trigger.Revision, &fixture.actor)
	require.NoError(t, err)
	assert.Less(t, promoted.NextSigningGeneration, promoted.CurrentSigningGeneration)
	assertSignedWebhookAccepted(t, fixture, created.Trigger.ID, created.WebhookSigningSecret, promoted.NextSigningKeyID, "evt_1234567890123458")
	assertSignedWebhookAccepted(t, fixture, created.Trigger.ID, staged.WebhookSigningSecret, promoted.CurrentSigningKeyID, "evt_1234567890123459")
	_, err = fixture.service.PromoteWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, promoted.Revision, &fixture.actor)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerSigningStateConflict)
	_, err = fixture.service.StageWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, promoted.Revision, &fixture.actor)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerSigningStateConflict)
	revoked, err := fixture.service.RevokeWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, promoted.Revision, &fixture.actor)
	require.NoError(t, err)
	assertSignedWebhookRejected(t, fixture, created.Trigger.ID, created.WebhookSigningSecret, promoted.NextSigningKeyID, "evt_1234567890123460")
	assertSignedWebhookAccepted(t, fixture, created.Trigger.ID, staged.WebhookSigningSecret, revoked.CurrentSigningKeyID, "evt_1234567890123461")

	corrupt := revoked
	corrupt.CurrentSigningKeyID = "not-a-key-id"
	_, err = implementation.workflowTriggerSigningKeys(corrupt)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerSigningUnavailable)
	_, err = fixture.service.StageWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, revoked.Revision, &fixture.actor)
	require.NoError(t, err, "the valid persisted state can still stage after revoked key removal")
}

func TestWorkflowTriggerSignedWebhookConcurrentReplayStartsOnlyOnce(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.service.(*workflowTriggerService).cipher = workflowTriggerTestCipher{}
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Inbound", Type: db.WorkflowTriggerWebhook, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &fixture.actor)
	require.NoError(t, err)
	blocking := &blockingWorkflowStarter{WorkflowService: fixture.starter, delegate: fixture.starter, entered: make(chan struct{}), release: make(chan struct{})}
	fixture.service = NewWorkflowTriggerService(fixture.repository, fixture.repository, blocking, fixture.identity, fixture.capability)
	fixture.service.(*workflowTriggerService).cipher = workflowTriggerTestCipher{}
	request := signedWorkflowWebhookRequest(t, created.WebhookSigningSecret, created.Trigger.CurrentSigningKeyID, "evt_1234567890123499")
	errs := make(chan error, 4)
	go func() {
		_, callErr := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, request)
		errs <- callErr
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first signed request did not reach workflow start")
	}
	for index := 0; index < 3; index++ {
		go func() {
			_, callErr := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, request)
			errs <- callErr
		}()
	}
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		history, historyErr := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
		if historyErr == nil && len(history) == 1 && history[0].WebhookReplayCount == 3 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("concurrent replay claims did not complete before workflow start was released")
		case <-ticker.C:
		}
	}
	close(blocking.release)
	for index := 0; index < 4; index++ {
		callErr := <-errs
		if callErr != nil {
			require.ErrorIs(t, callErr, pro_interfaces.ErrWorkflowTriggerWebhookReplay)
		}
	}
	assert.Equal(t, 1, fixture.starter.calls)
	history, err := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, 3, history[0].WebhookReplayCount)
}

func TestWorkflowTriggerSignedWebhookRejectsTamperedAndExpiredRequestsBeforeClaim(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	implementation := fixture.service.(*workflowTriggerService)
	implementation.cipher = workflowTriggerTestCipher{}
	clock := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	implementation.now = func() time.Time { return clock }
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Inbound", Type: db.WorkflowTriggerWebhook, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &fixture.actor)
	require.NoError(t, err)
	request := signedWorkflowWebhookRequestAt(t, created.WebhookSigningSecret, created.Trigger.CurrentSigningKeyID, "evt_1234567890123470", clock)
	for _, test := range []struct {
		name   string
		mutate func(*pro_interfaces.WebhookSignedRequest)
	}{
		{"payload", func(value *pro_interfaces.WebhookSignedRequest) { value.Payload = []byte(`{"inputs":{"target":"us"}}`) }},
		{"target", func(value *pro_interfaces.WebhookSignedRequest) { value.RequestTarget = "/other" }},
		{"event", func(value *pro_interfaces.WebhookSignedRequest) { value.EventID = "evt_1234567890123471" }},
		{"method", func(value *pro_interfaces.WebhookSignedRequest) { value.Method = "PUT" }},
		{"signature", func(value *pro_interfaces.WebhookSignedRequest) { value.Signature = "v1=" + strings.Repeat("0", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			test.mutate(&candidate)
			_, callErr := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, candidate)
			require.ErrorIs(t, callErr, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
		})
	}
	for _, test := range []struct {
		name      string
		timestamp int64
	}{
		{"stale", clock.Add(-6 * time.Minute).Unix()},
		{"future", clock.Add(6 * time.Minute).Unix()},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := request
			candidate.Timestamp = test.timestamp
			candidate.Signature, err = pro_interfaces.SignWebhookRequest(created.WebhookSigningSecret, candidate)
			require.NoError(t, err)
			_, callErr := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, candidate)
			require.ErrorIs(t, callErr, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
		})
	}
	t.Run("unknown key", func(t *testing.T) {
		unknown, keyErr := pro_interfaces.NewWebhookSigningKey()
		require.NoError(t, keyErr)
		candidate := request
		candidate.KeyID = unknown.ID
		candidate.Signature, err = pro_interfaces.SignWebhookRequest(unknown.Secret, candidate)
		require.NoError(t, err)
		_, callErr := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, candidate)
		require.ErrorIs(t, callErr, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
	})
	assert.Zero(t, fixture.starter.calls)
	history, err := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, history)
}

func TestWorkflowTriggerWebhookBootstrapAndCipherFailuresFailClosed(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	implementation := fixture.service.(*workflowTriggerService)
	implementation.cipher = workflowTriggerTestCipher{}
	legacy, err := fixture.repository.CreateWorkflowTrigger(db.WorkflowTrigger{
		ProjectID: fixture.projectID, WorkflowTemplateID: fixture.workflow.ID, Name: "Migrated inbound",
		Type: db.WorkflowTriggerWebhook, OwnerUserID: fixture.actor.ID, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	})
	require.NoError(t, err)
	bootstrapped, err := fixture.service.BootstrapWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, legacy.ID, legacy.Revision, &fixture.actor)
	require.NoError(t, err)
	require.NotEmpty(t, bootstrapped.WebhookSigningSecret)
	assert.NotEqual(t, bootstrapped.WebhookSigningSecret, bootstrapped.Trigger.CurrentSigningSecretEncrypted)
	_, err = fixture.service.BootstrapWebhookSigningKey(context.Background(), fixture.projectID, fixture.workflow.ID, legacy.ID, bootstrapped.Trigger.Revision, &fixture.actor)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerSigningStateConflict)

	implementation.cipher = failingWorkflowTriggerCipher{}
	request := signedWorkflowWebhookRequest(t, bootstrapped.WebhookSigningSecret, bootstrapped.Trigger.CurrentSigningKeyID, "evt_1234567890123480")
	_, err = fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, legacy.ID, request)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
	assert.Zero(t, fixture.starter.calls)

	noCipher := newWorkflowTriggerServiceFixture(t)
	_, err = noCipher.service.Create(context.Background(), noCipher.projectID, noCipher.workflow.ID, db.WorkflowTrigger{Name: "No cipher", Type: db.WorkflowTriggerWebhook, Enabled: true}, &noCipher.actor)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerSigningUnavailable)
}

type blockingWorkflowStarter struct {
	pro_interfaces.WorkflowService
	delegate *workflowTriggerStarter
	entered  chan struct{}
	release  chan struct{}
}

type failingWorkflowTriggerCipher struct{}

func (failingWorkflowTriggerCipher) OptionEncryptionEnabled() bool { return true }
func (failingWorkflowTriggerCipher) EncryptOption([]byte) (string, error) {
	return "", errors.New("cipher unavailable")
}
func (failingWorkflowTriggerCipher) DecryptOption(string) ([]byte, error) {
	return nil, errors.New("cipher unavailable")
}

func (s *blockingWorkflowStarter) StartWorkflow(workflow db.WorkflowTemplate, user *db.User, correlationID string, inputs ...db.WorkflowRunInput) (db.WorkflowRun, error) {
	close(s.entered)
	<-s.release
	return s.delegate.StartWorkflow(workflow, user, correlationID, inputs...)
}

func assertSignedWebhookAccepted(t *testing.T, fixture *workflowTriggerServiceFixture, triggerID int, material, keyID, eventID string) {
	t.Helper()
	request := signedWorkflowWebhookRequest(t, material, keyID, eventID)
	_, err := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, triggerID, request)
	require.NoError(t, err)
}

func assertSignedWebhookRejected(t *testing.T, fixture *workflowTriggerServiceFixture, triggerID int, material, keyID, eventID string) {
	t.Helper()
	request := signedWorkflowWebhookRequest(t, material, keyID, eventID)
	_, err := fixture.service.FireSignedWebhook(context.Background(), fixture.projectID, fixture.workflow.ID, triggerID, request)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerWebhookRejected)
}

func signedWorkflowWebhookRequest(t *testing.T, material, keyID, eventID string) pro_interfaces.WebhookSignedRequest {
	return signedWorkflowWebhookRequestAt(t, material, keyID, eventID, time.Now().UTC())
}

func signedWorkflowWebhookRequestAt(t *testing.T, material, keyID, eventID string, now time.Time) pro_interfaces.WebhookSignedRequest {
	t.Helper()
	headers := pro_interfaces.WebhookSignatureHeaders{Version: pro_interfaces.WebhookSignatureProtocolVersion, EventID: eventID, Timestamp: fmt.Sprintf("%d", now.Unix()), KeyID: keyID}
	request, err := pro_interfaces.BindWebhookSignedRequest("POST", "/api/workflow-triggers/1/2/3/webhook", headers, []byte(`{"inputs":{"target":"eu"}}`))
	require.NoError(t, err)
	request.Signature, err = pro_interfaces.SignWebhookRequest(material, request)
	require.NoError(t, err)
	return request
}

type workflowTriggerTestCipher struct{}

func (workflowTriggerTestCipher) OptionEncryptionEnabled() bool { return true }
func (workflowTriggerTestCipher) EncryptOption(value []byte) (string, error) {
	return "sealed:" + string(value), nil
}
func (workflowTriggerTestCipher) DecryptOption(value string) ([]byte, error) {
	return []byte(value[len("sealed:"):]), nil
}

func TestWorkflowTriggerServiceIssuesCredentialOnceAndDeduplicatesExternalStart(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	require.NotEmpty(t, created.Credential)
	assert.NotContains(t, created.Trigger.CredentialHash, created.Credential)
	persisted, err := fixture.repository.GetWorkflowTrigger(fixture.projectID, fixture.workflow.ID, created.Trigger.ID)
	require.NoError(t, err)
	assert.True(t, db.WorkflowTriggerCredentialMatches(persisted.CredentialHash, created.Credential))

	request := map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)}
	first, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "deploy-once", request)
	require.NoError(t, err)
	second, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "deploy-once", request)
	require.NoError(t, err)
	assert.Equal(t, first.Run.ID, second.Run.ID)
	assert.Equal(t, first.Invocation.ID, second.Invocation.ID)
	assert.True(t, second.Duplicate)
	assert.Equal(t, 1, fixture.starter.calls)
	require.NotNil(t, fixture.starter.input.TriggerSnapshot)
	assert.Equal(t, created.Trigger.ID, fixture.starter.input.TriggerSnapshot.ID)
	assert.JSONEq(t, `"eu"`, string(fixture.starter.input.TriggerValues["region"]))
}

func TestWorkflowTriggerServiceRotationRevokesOldCredential(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	rotated, err := fixture.service.RotateCredential(
		context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, created.Trigger.Revision, &fixture.actor,
	)
	require.NoError(t, err)
	assert.NotEqual(t, created.Credential, rotated.Credential)
	assert.Equal(t, created.Trigger.CredentialGeneration+1, rotated.Trigger.CredentialGeneration)

	request := map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)}
	_, err = fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "old", request)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerCredentialRejected)
	_, err = fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, rotated.Credential, "new", request)
	require.NoError(t, err)
}

func TestWorkflowTriggerServiceRevalidatesDefinitionAtFireTime(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	fixture.workflow.ParameterDefinitions = nil
	fixture.workflow.ParameterDefinitionsJSON = "[]"
	updated, err := fixture.repository.UpdateWorkflowTemplate(fixture.workflow)
	require.NoError(t, err)
	fixture.workflow = updated

	_, err = fixture.service.FireExternal(
		context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID,
		db.WorkflowTriggerAPI, created.Credential, "definition-change",
		map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)},
	)
	assert.ErrorContains(t, err, "unknown parameter")
	assert.Zero(t, fixture.starter.calls)
}

func TestWorkflowTriggerServiceRetriesFailedScheduledOccurrenceWithoutSecondRun(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.starter.failures = 1
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "0 1 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	scheduledAt := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)

	_, err = fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.Error(t, err)
	result, err := fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.NoError(t, err)
	duplicate, err := fixture.service.FireScheduled(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, scheduledAt)
	require.NoError(t, err)
	assert.Equal(t, result.Run.ID, duplicate.Run.ID)
	assert.Equal(t, result.Invocation.ID, duplicate.Invocation.ID)
	assert.Equal(t, 2, fixture.starter.calls)
}

func TestWorkflowTriggerPersistsBlockedAdmissionAndDeduplicatesIt(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	next := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	fixture.starter.blocked = &pro_interfaces.DeploymentWindowBlockedError{DecisionID: 77, NextEligibleAt: &next, NextEligibleKnown: true}

	first, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "blocked-once", map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)})
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowTriggerInvocationBlocked, first.Invocation.Status)
	require.NotNil(t, first.Invocation.DeploymentWindowDecisionID)
	assert.Equal(t, 77, *first.Invocation.DeploymentWindowDecisionID)
	assert.Equal(t, "deployment_window_blocked", first.Invocation.Reason)
	assert.Nil(t, first.Invocation.RunID)

	duplicate, err := fixture.service.FireExternal(context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID, db.WorkflowTriggerAPI, created.Credential, "blocked-once", map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)})
	require.NoError(t, err)
	assert.True(t, duplicate.Duplicate)
	assert.Equal(t, first.Invocation.ID, duplicate.Invocation.ID)
	assert.Equal(t, 1, fixture.starter.calls, "a blocked invocation is terminal and must not re-evaluate")
}

func TestWorkflowTriggerSchedulerDrainRejectsNewRunsUntilResume(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "0 1 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	require.NotZero(t, created.Trigger.ID)
	scheduler := NewWorkflowTriggerScheduler(fixture.repository, fixture.service)
	drainer, ok := scheduler.(pro_interfaces.ClusterDrainer)
	require.True(t, ok)
	require.NoError(t, drainer.Drain())

	scheduledAt := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	scheduler.RunOnce(context.Background(), scheduledAt)
	assert.Zero(t, fixture.starter.calls)
	drainer.Resume()
	scheduler.RunOnce(context.Background(), scheduledAt)
	assert.Equal(t, 1, fixture.starter.calls)
}

func TestWorkflowTriggerServiceEnforcesCapabilityAndCurrentProjectPermission(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	fixture.identity.member.Role = db.ProjectGuest
	_, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Manual", Type: db.WorkflowTriggerManual, Enabled: true,
	}, &fixture.actor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowTriggerPermissionDenied)

	fixture.identity.member.Role = db.ProjectOwner
	fixture.capability.allowed = false
	_, err = fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Manual", Type: db.WorkflowTriggerManual, Enabled: true,
	}, &fixture.actor)
	var denied pro_interfaces.CapabilityDeniedError
	assert.ErrorAs(t, err, &denied)
}

func TestWorkflowTriggerServiceScopesHistoryToWorkflow(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created := fixture.createAPITrigger(t)
	otherWorkflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Other", DefinitionVersion: db.WorkflowDefinitionVersion,
	})
	require.NoError(t, err)

	_, err = fixture.service.History(
		context.Background(), fixture.projectID, otherWorkflow.ID, created.Trigger.ID,
		db.RetrieveQueryParams{}, &fixture.actor,
	)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestWorkflowTriggerSchedulerRunsMatchingUTCMinuteOnce(t *testing.T) {
	fixture := newWorkflowTriggerServiceFixture(t)
	created, err := fixture.service.Create(context.Background(), fixture.projectID, fixture.workflow.ID, db.WorkflowTrigger{
		Name: "Nightly", Type: db.WorkflowTriggerSchedule, Enabled: true, CronFormat: "30 8 * * *",
		InputMappings: []db.WorkflowTriggerInputMapping{{
			Parameter: "region", Source: db.WorkflowTriggerInputFixed, Value: json.RawMessage(`"eu"`),
		}},
	}, &fixture.actor)
	require.NoError(t, err)
	scheduler := NewWorkflowTriggerScheduler(fixture.repository, fixture.service)
	require.NotNil(t, scheduler)

	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 29, 59, 0, time.UTC))
	assert.Zero(t, fixture.starter.calls)
	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 30, 2, 0, time.UTC))
	scheduler.RunOnce(context.Background(), time.Date(2026, 8, 29, 8, 30, 45, 0, time.UTC))
	assert.Equal(t, 1, fixture.starter.calls)
	history, err := fixture.repository.GetWorkflowTriggerInvocations(fixture.projectID, created.Trigger.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, created.Trigger.ID, history[0].WorkflowTriggerID)
}

type workflowTriggerServiceFixture struct {
	store      *coresql.SqlDb
	repository *workflowsql.WorkflowStoreImpl
	service    pro_interfaces.WorkflowTriggerService
	starter    *workflowTriggerStarter
	identity   *workflowTriggerIdentity
	capability *workflowTriggerCapability
	actor      db.User
	projectID  int
	workflow   db.WorkflowTemplate
}

func newWorkflowTriggerServiceFixture(t *testing.T) *workflowTriggerServiceFixture {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Trigger service"})
	require.NoError(t, err)
	repository := workflowsql.NewWorkflowStore(store.GetConnection())
	workflow, err := repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions:     []db.WorkflowParameterDeclaration{{Name: "region", Type: db.WorkflowParameterString, Required: true}},
		ParameterDefinitionsJSON: `[{"name":"region","type":"string","required":true}]`,
	})
	require.NoError(t, err)
	actor := db.User{ID: 41, Username: "owner"}
	starter := &workflowTriggerStarter{runs: map[string]db.WorkflowRun{}, repository: repository}
	identity := &workflowTriggerIdentity{
		user: actor, member: db.ProjectUser{ProjectID: project.ID, UserID: actor.ID, Role: db.ProjectOwner},
	}
	capability := &workflowTriggerCapability{allowed: true}
	return &workflowTriggerServiceFixture{
		store: store, repository: repository, starter: starter, identity: identity, capability: capability,
		service: NewWorkflowTriggerService(repository, repository, starter, identity, capability),
		actor:   actor, projectID: project.ID, workflow: workflow,
	}
}

func (f *workflowTriggerServiceFixture) createAPITrigger(t *testing.T) pro_interfaces.WorkflowTriggerCredentialResult {
	t.Helper()
	created, err := f.service.Create(context.Background(), f.projectID, f.workflow.ID, db.WorkflowTrigger{
		Name: "Deploy API", Type: db.WorkflowTriggerAPI, Enabled: true,
		InputMappings: []db.WorkflowTriggerInputMapping{{Parameter: "region", Source: db.WorkflowTriggerInputRequest, Key: "target"}},
	}, &f.actor)
	require.NoError(t, err)
	return created
}

type workflowTriggerStarter struct {
	calls      int
	failures   int
	blocked    error
	input      db.WorkflowRunInput
	runs       map[string]db.WorkflowRun
	repository db.WorkflowManager
}

func (s *workflowTriggerStarter) StartWorkflow(workflow db.WorkflowTemplate, user *db.User, correlationID string, inputs ...db.WorkflowRunInput) (db.WorkflowRun, error) {
	if existing, ok := s.runs[correlationID]; ok {
		return existing, nil
	}
	s.calls++
	if s.blocked != nil {
		return db.WorkflowRun{}, s.blocked
	}
	if s.failures > 0 {
		s.failures--
		return db.WorkflowRun{}, errors.New("temporary workflow start outage")
	}
	s.input = inputs[0]
	now := time.Now().UTC()
	run, err := s.repository.CreateWorkflowRun(db.WorkflowRun{
		ProjectID: workflow.ProjectID, WorkflowTemplateID: workflow.ID, Status: db.WorkflowRunPending,
		ActorUserID: user.ID, DefinitionVersion: workflow.DefinitionVersion, DefinitionRevision: workflow.Revision,
		DefinitionSnapshotJSON: `{}`, ParameterSnapshotJSON: `{}`, TriggerSnapshotJSON: `{}`,
		CorrelationID: correlationID, Created: now,
	})
	if err != nil {
		return db.WorkflowRun{}, err
	}
	s.runs[correlationID] = run
	return run, nil
}

func (s *workflowTriggerStarter) ProgressWorkflowRun(int, int, *db.User) error { return nil }
func (s *workflowTriggerStarter) StopWorkflowRun(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) RequestWorkflowRunStop(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) ReconcileWorkflowRun(int, int) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) RetryWorkflowRunReconciliation(int, int, *db.User) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, nil
}
func (s *workflowTriggerStarter) GetWorkflowApprovalInbox(int, *db.User) ([]db.WorkflowApproval, error) {
	return nil, nil
}
func (s *workflowTriggerStarter) ResolveWorkflowApproval(int, int, int, int, db.WorkflowApprovalDecision, *db.User) (db.WorkflowApproval, error) {
	return db.WorkflowApproval{}, nil
}
func (s *workflowTriggerStarter) HandleWorkflowTaskOutputs(db.Task, map[string]json.RawMessage) error {
	return nil
}
func (s *workflowTriggerStarter) HandleWorkflowTaskCompletion(db.Task) error { return nil }
func (s *workflowTriggerStarter) GetWorkflowRunArtifacts(int, int, *int) ([]db.WorkflowArtifactMetadata, error) {
	return nil, nil
}

type workflowTriggerIdentity struct {
	user   db.User
	member db.ProjectUser
}

func (s *workflowTriggerIdentity) GetUser(int) (db.User, error) { return s.user, nil }
func (s *workflowTriggerIdentity) GetProjectUser(int, int) (db.ProjectUser, error) {
	return s.member, nil
}
func (s *workflowTriggerIdentity) GetProjectOrGlobalRoleBySlug(int, string) (db.Role, error) {
	return db.Role{}, db.ErrNotFound
}

type workflowTriggerCapability struct{ allowed bool }

func (p *workflowTriggerCapability) Resolve(_ context.Context, request pro_interfaces.CapabilityRequest) (pro_interfaces.CapabilitySnapshot, error) {
	access := []pro_interfaces.CapabilityAccess(nil)
	state := pro_interfaces.CapabilityStateDisabled
	reason := pro_interfaces.CapabilityReasonDisabledByAdmin
	if p.allowed {
		access = []pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		}
		state = pro_interfaces.CapabilityStateActive
		reason = pro_interfaces.CapabilityReasonActive
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(pro_interfaces.CapabilityWorkflowTriggers, state, reason, access, nil),
	}), nil
}

func (p *workflowTriggerCapability) Configure(context.Context, pro_interfaces.CapabilityRequest, pro_interfaces.CapabilityConfiguration) (pro_interfaces.CapabilitySnapshot, error) {
	panic("not used")
}

type blockingWorkflowTriggerRepository struct {
	db.WorkflowTriggerManager
	claimEntered  chan struct{}
	continueClaim chan struct{}
}

func (r *blockingWorkflowTriggerRepository) ClaimWorkflowTriggerInvocation(
	invocation db.WorkflowTriggerInvocation,
	now time.Time,
) (db.WorkflowTriggerInvocation, bool, error) {
	close(r.claimEntered)
	<-r.continueClaim
	return r.WorkflowTriggerManager.ClaimWorkflowTriggerInvocation(invocation, now)
}

func TestWorkflowTriggerServiceRejectsInvocationClaimAfterRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke func(*workflowTriggerServiceFixture, pro_interfaces.WorkflowTriggerCredentialResult) error
	}{
		{
			name: "credential rotation",
			revoke: func(fixture *workflowTriggerServiceFixture, created pro_interfaces.WorkflowTriggerCredentialResult) error {
				_, err := fixture.service.RotateCredential(
					context.Background(), fixture.projectID, fixture.workflow.ID,
					created.Trigger.ID, created.Trigger.Revision, &fixture.actor,
				)
				return err
			},
		},
		{
			name: "disable",
			revoke: func(fixture *workflowTriggerServiceFixture, created pro_interfaces.WorkflowTriggerCredentialResult) error {
				_, err := fixture.service.SetEnabled(
					context.Background(), fixture.projectID, fixture.workflow.ID,
					created.Trigger.ID, created.Trigger.Revision, false, &fixture.actor,
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowTriggerServiceFixture(t)
			created := fixture.createAPITrigger(t)
			blocking := &blockingWorkflowTriggerRepository{
				WorkflowTriggerManager: fixture.repository,
				claimEntered:           make(chan struct{}),
				continueClaim:          make(chan struct{}),
			}
			fixture.service = NewWorkflowTriggerService(
				blocking, fixture.repository, fixture.starter, fixture.identity, fixture.capability,
			)

			fireResult := make(chan error, 1)
			go func() {
				_, err := fixture.service.FireExternal(
					context.Background(), fixture.projectID, fixture.workflow.ID, created.Trigger.ID,
					db.WorkflowTriggerAPI, created.Credential, "revocation-race",
					map[string]json.RawMessage{"target": json.RawMessage(`"eu"`)},
				)
				fireResult <- err
			}()

			select {
			case <-blocking.claimEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("external trigger did not reach the invocation claim")
			}

			require.NoError(t, test.revoke(fixture, created))
			close(blocking.continueClaim)

			select {
			case err := <-fireResult:
				require.ErrorIs(t, err, db.ErrWorkflowTriggerStateChanged)
			case <-time.After(5 * time.Second):
				t.Fatal("external trigger did not return after revocation")
			}
			assert.Zero(t, fixture.starter.calls)
		})
	}
}
