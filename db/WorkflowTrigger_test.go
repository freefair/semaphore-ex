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
