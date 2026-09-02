package pro_interfaces

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionPreflightFingerprintIsCanonicalAndIgnoresResponseEnvelope(t *testing.T) {
	left := validExecutionPreflightPlan()
	right := validExecutionPreflightPlan()
	right.Inputs = []ExecutionPreflightInput{left.Inputs[1], left.Inputs[0]}
	right.References = []ExecutionPreflightReference{left.References[1], left.References[0]}
	right.ReviewToken = "different-response-token"
	right.ExpiresAt = left.ExpiresAt.Add(time.Hour)

	leftFingerprint, err := FingerprintExecutionPreflight(left)
	require.NoError(t, err)
	rightFingerprint, err := FingerprintExecutionPreflight(right)
	require.NoError(t, err)

	assert.Equal(t, leftFingerprint, rightFingerprint)
}

func TestExecutionPreflightPlanRejectsEveryBound(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExecutionPreflightPlan)
	}{
		{name: "inputs", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Inputs = make([]ExecutionPreflightInput, MaxExecutionPreflightInputs+1)
		}},
		{name: "references", mutate: func(plan *ExecutionPreflightPlan) {
			plan.References = make([]ExecutionPreflightReference, MaxExecutionPreflightReferences+1)
		}},
		{name: "commands", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Commands = make([]ExecutionPreflightCommand, MaxExecutionPreflightCommands+1)
		}},
		{name: "placements", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Placements = make([]ExecutionPreflightPlacement, MaxExecutionPreflightPlacements+1)
		}},
		{name: "candidates", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Placements[0].Candidates = make([]ExecutionPreflightCandidate, MaxExecutionPreflightCandidates+1)
		}},
		{name: "findings", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Findings = make([]ExecutionPreflightFinding, MaxExecutionPreflightFindings+1)
		}},
		{name: "string", mutate: func(plan *ExecutionPreflightPlan) {
			plan.Definition.Name = strings.Repeat("x", MaxExecutionPreflightStringBytes+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := validExecutionPreflightPlan()
			test.mutate(&plan)
			assert.Error(t, plan.Validate())
		})
	}
}

func TestExecutionPreflightJSONContainsIdentitiesButNoValues(t *testing.T) {
	plan := validExecutionPreflightPlan()
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	response := string(encoded)
	assert.Contains(t, response, `"name":"deploy_token"`)
	assert.Contains(t, response, `"binding_target":"deploy_token"`)
	for _, forbidden := range []string{"credential-value", "private_key", "passphrase", "webhook", "source_storage_key"} {
		assert.NotContains(t, response, forbidden)
	}
}

func TestDiffExecutionPreflightUsesBoundedStableCodes(t *testing.T) {
	before := validExecutionPreflightPlan()
	after := validExecutionPreflightPlan()
	after.Definition.Fingerprint = "sha256:changed"
	after.References[0].Revision = "2"
	after.Placements[0].SelectedRunnerID = intPointer(12)

	assert.Equal(t, []ExecutionPreflightChangeCode{
		ExecutionChangeDefinition, ExecutionChangeReference, ExecutionChangePlacement,
	}, DiffExecutionPreflight(before, after))
}

func validExecutionPreflightPlan() ExecutionPreflightPlan {
	return ExecutionPreflightPlan{
		ContractVersion: ExecutionPreflightContractVersion,
		Intent:          ExecutionPreflightTask, ProjectID: 7, ActorID: 9, TemplateID: 11,
		Definition: ExecutionPreflightDefinition{
			Kind: ExecutionReferenceTemplate, ID: 11, Name: "Deploy",
			Revision: "1", Fingerprint: "sha256:definition",
		},
		Inputs: []ExecutionPreflightInput{
			{Name: "region", Type: "string", Source: "task_override", Present: true},
			{Name: "deploy_token", Type: "secret", Source: "credential", Present: true, Sensitive: true},
		},
		References: []ExecutionPreflightReference{
			{Kind: ExecutionReferenceRepository, ID: 3, Name: "repo", Revision: "main", Visible: true},
			{Kind: ExecutionReferenceCredential, ID: 4, Name: "deploy key", BindingTarget: "deploy_token", Visible: true},
		},
		Commands: []ExecutionPreflightCommand{{TemplateID: 11, Application: db.AppAnsible, Playbook: "deploy.yml"}},
		Placements: []ExecutionPreflightPlacement{{
			SelectedRunnerID: intPointer(10), SelectedName: "runner", Decision: ExecutionReasonSelected,
			Provisional: true, Candidates: []ExecutionPreflightCandidate{},
		}},
		Findings: []ExecutionPreflightFinding{}, ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
}

func intPointer(value int) *int { return &value }
