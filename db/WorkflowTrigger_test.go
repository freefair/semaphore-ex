package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAndResolveWorkflowTrigger(t *testing.T) {
	declarations := []WorkflowParameterDeclaration{
		{Name: "region", Type: WorkflowParameterString, Required: true},
		{Name: "confirm", Type: WorkflowParameterBoolean},
	}
	trigger := WorkflowTrigger{
		ID: 3, ProjectID: 7, WorkflowTemplateID: 9, Revision: 1,
		Name: "Deploy API", Type: WorkflowTriggerAPI, OwnerUserID: 11, Enabled: true,
		InputMappings: []WorkflowTriggerInputMapping{
			{Parameter: "region", Source: WorkflowTriggerInputRequest, Key: "target"},
			{Parameter: "confirm", Source: WorkflowTriggerInputFixed, Value: json.RawMessage(`true`)},
		},
	}
	require.NoError(t, ValidateWorkflowTrigger(trigger, declarations))

	values, err := ResolveWorkflowTriggerValues(trigger, map[string]json.RawMessage{
		"target": json.RawMessage(`"eu"`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `"eu"`, string(values["region"]))
	assert.JSONEq(t, `true`, string(values["confirm"]))

	_, err = ResolveWorkflowTriggerValues(trigger, map[string]json.RawMessage{
		"target": json.RawMessage(`"eu"`), "other": json.RawMessage(`true`),
	})
	require.ErrorContains(t, err, "unknown workflow trigger input")
}

func TestValidateWorkflowTriggerRejectsUnsafeMappings(t *testing.T) {
	declarations := []WorkflowParameterDeclaration{{
		Name: "token", Type: WorkflowParameterSecretReference, Required: true,
		SecretOptions: []WorkflowSecretOption{{AccessKeyID: 41}},
	}}
	base := WorkflowTrigger{
		ProjectID: 7, WorkflowTemplateID: 9, Name: "nightly", Type: WorkflowTriggerSchedule,
		OwnerUserID: 11, Enabled: true, CronFormat: "0 1 * * *",
	}

	unsafe := base
	unsafe.InputMappings = []WorkflowTriggerInputMapping{{
		Parameter: "token", Source: WorkflowTriggerInputFixed, Value: json.RawMessage(`"plaintext"`),
	}}
	require.ErrorContains(t, ValidateWorkflowTrigger(unsafe, declarations), "approved secret reference")

	request := base
	request.InputMappings = []WorkflowTriggerInputMapping{{
		Parameter: "token", Source: WorkflowTriggerInputRequest, Key: "token",
	}}
	require.ErrorContains(t, ValidateWorkflowTrigger(request, declarations), "scheduled workflow triggers cannot read request fields")
}

func TestWorkflowTriggerIdentitiesAndCredentialHashes(t *testing.T) {
	credential := "swt_local-only"
	hash := HashWorkflowTriggerCredential(credential)
	assert.True(t, WorkflowTriggerCredentialMatches(hash, credential))
	assert.False(t, WorkflowTriggerCredentialMatches(hash, credential+"-wrong"))

	request := HashWorkflowTriggerRequestKey(3, 2, "deploy-once")
	assert.Equal(t, request, HashWorkflowTriggerRequestKey(3, 2, "deploy-once"))
	assert.NotEqual(t, request, HashWorkflowTriggerRequestKey(3, 3, "deploy-once"))

	at := time.Date(2026, 8, 29, 8, 30, 0, 0, time.UTC)
	occurrence := WorkflowTriggerScheduleOccurrenceIdentity(3, 4, 5, at)
	assert.Len(t, occurrence, 64)
	assert.Equal(t, occurrence, WorkflowTriggerScheduleOccurrenceIdentity(3, 4, 5, at.In(time.FixedZone("offset", 2*60*60))))
	assert.NotEqual(t, occurrence, WorkflowTriggerScheduleOccurrenceIdentity(3, 4, 6, at))
}

func TestDisabledWorkflowTriggerCannotFire(t *testing.T) {
	trigger := WorkflowTrigger{Enabled: false}
	require.ErrorContains(t, ValidateWorkflowTriggerCanFire(trigger), "disabled")
}

func TestWebhookWorkflowTriggerRequiresSigningMaterialAndDurableReplayIdentity(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	trigger := WorkflowTrigger{
		ProjectID: 1, WorkflowTemplateID: 2, Name: "Inbound", Type: WorkflowTriggerWebhook, OwnerUserID: 3, Enabled: true,
		CredentialHash: HashWorkflowTriggerCredential("legacy-webhook"), CredentialGeneration: 7,
	}
	require.NoError(t, ValidateWorkflowTrigger(trigger, nil), "legacy webhook credential metadata is dormant but valid")
	require.Error(t, ValidateWorkflowTriggerCanFire(trigger))
	trigger.CurrentSigningSecretEncrypted = "ciphertext-current"
	trigger.CurrentSigningKeyID = "swhkid_current"
	trigger.CurrentSigningGeneration = 1
	require.NoError(t, ValidateWorkflowTriggerCanFire(trigger))
	trigger.CurrentSigningGeneration = 0
	require.Error(t, ValidateWorkflowTriggerCanFire(trigger))
	trigger.CurrentSigningGeneration = 1
	trigger.CurrentSigningSecretEncrypted = ""
	trigger.NextSigningSecretEncrypted = "ciphertext-next"
	trigger.NextSigningKeyID = "swhkid_next"
	assert.Error(t, ValidateWorkflowTrigger(trigger, nil))

	eventHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	invocation := WorkflowTriggerInvocation{
		ProjectID: 1, WorkflowTriggerID: 2, WorkflowTemplateID: 3, TriggerRevision: 1,
		DefinitionRevision: 1, Status: WorkflowTriggerInvocationClaimed,
		WebhookEventHash: &eventHash, WebhookEventID: "evt_1234567890123456",
		WebhookKeyID: "swhkid_current", WebhookSignedAt: pointer(now),
		TriggerSnapshotJSON: `{}`, InputSnapshotJSON: `{}`, Created: now, Updated: now,
	}
	require.NoError(t, ValidateWorkflowTriggerInvocation(invocation))
	invocation.ExpiresAt = pointer(now.Add(time.Hour))
	assert.Error(t, ValidateWorkflowTriggerInvocation(invocation), "webhook replay identities cannot expire")
}

func TestWebhookCorrelationIDIsBoundedAndRejectsMalformedIdentity(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	invocation := WorkflowTriggerInvocation{WebhookEventHash: &hash}
	correlationID := WorkflowTriggerInvocationCorrelationID(invocation)
	assert.Equal(t, "swh_"+hash[:60], correlationID)
	assert.Len(t, correlationID, 64)
	malformed := "short"
	invocation.WebhookEventHash = &malformed
	assert.Empty(t, WorkflowTriggerInvocationCorrelationID(invocation))
}
