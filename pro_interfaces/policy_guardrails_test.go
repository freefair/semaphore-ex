package pro_interfaces

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyGuardrailConditionValidationUsesClosedTypedFieldOperatorPairs(t *testing.T) {
	stringValue := "ansible"
	integerValue := 30
	booleanValue := true
	tests := []struct {
		name      string
		condition PolicyGuardrailCondition
		valid     bool
	}{
		{"string equals", PolicyGuardrailCondition{Field: PolicyFieldTaskApplication, Operator: PolicyGuardrailOperatorEquals, StringValue: &stringValue}, true},
		{"integer comparison", PolicyGuardrailCondition{Field: PolicyFieldTimeMinuteOfDay, Operator: PolicyGuardrailOperatorAtLeast, IntegerValue: &integerValue}, true},
		{"boolean equals", PolicyGuardrailCondition{Field: PolicyFieldImagePresent, Operator: PolicyGuardrailOperatorEquals, BooleanValue: &booleanValue}, true},
		{"list contains", PolicyGuardrailCondition{Field: PolicyFieldRunnerRequestedTags, Operator: PolicyGuardrailOperatorContainsAll, StringValues: []string{"linux", "prod"}}, true},
		{"scalar one of", PolicyGuardrailCondition{Field: PolicyFieldTaskApplication, Operator: PolicyGuardrailOperatorOneOf, StringValues: []string{"ansible", "terraform"}}, true},
		{"presence", PolicyGuardrailCondition{Field: PolicyFieldRunnerSelectedID, Operator: PolicyGuardrailOperatorPresent}, true},
		{"secret field", PolicyGuardrailCondition{Field: "credential.secret_value", Operator: PolicyGuardrailOperatorEquals, StringValue: &stringValue}, false},
		{"regex operator", PolicyGuardrailCondition{Field: PolicyFieldTaskApplication, Operator: "matches", StringValue: &stringValue}, false},
		{"equals list", PolicyGuardrailCondition{Field: PolicyFieldTaskApplication, Operator: PolicyGuardrailOperatorEquals, StringValues: []string{"ansible"}}, false},
		{"one of scalar", PolicyGuardrailCondition{Field: PolicyFieldTaskApplication, Operator: PolicyGuardrailOperatorOneOf, StringValue: &stringValue}, false},
		{"contains scalar", PolicyGuardrailCondition{Field: PolicyFieldRunnerRequestedTags, Operator: PolicyGuardrailOperatorContainsAny, StringValue: &stringValue}, false},
		{"integer with string", PolicyGuardrailCondition{Field: PolicyFieldTimeMinuteOfDay, Operator: PolicyGuardrailOperatorAtLeast, StringValue: &stringValue}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.condition.Validate()
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPolicyGuardrailDocumentRequiresStableUniqueRulesAndBoundedMessages(t *testing.T) {
	value := "ansible"
	rule := PolicyGuardrailRule{
		ID: "deny-terraform", Effect: PolicyGuardrailEffectDeny, Severity: PolicyGuardrailSeverityHigh,
		Message: "Use the approved executor.", RemediationURL: "https://docs.example.test/policies/executors",
		Match: PolicyGuardrailMatchAll, Conditions: []PolicyGuardrailCondition{{
			Field: PolicyFieldTaskApplication, Operator: PolicyGuardrailOperatorEquals, StringValue: &value,
		}},
	}
	require.NoError(t, (PolicyGuardrailDocument{Version: 1, Rules: []PolicyGuardrailRule{rule}}).Validate())

	duplicate := PolicyGuardrailDocument{Version: 1, Rules: []PolicyGuardrailRule{rule, rule}}
	require.ErrorContains(t, duplicate.Validate(), "unique")

	rule.RemediationURL = "http://docs.example.test/policies/executors"
	require.ErrorContains(t, rule.Validate(), "HTTPS")
}

func TestFingerprintPolicyGuardrailInputCanonicalizesSetLikeMetadataAndExcludesClock(t *testing.T) {
	input := validPolicyGuardrailInput()
	input.EnvironmentIDs = []int{9, 3}
	input.Runner.RequestedTags = []string{"prod", "linux"}
	input.Credentials = []PolicyGuardrailCredentialReferenceMetadata{
		{ID: 8, Scope: "global", BindingTarget: "repository.ssh"},
		{ID: 2, Scope: "local", BindingTarget: "inventory.ssh"},
	}
	first, err := FingerprintPolicyGuardrailInput(input)
	require.NoError(t, err)

	input.EvaluatedAt = input.EvaluatedAt.Add(12 * time.Hour)
	input.EnvironmentIDs = []int{3, 9}
	input.Runner.RequestedTags = []string{"linux", "prod"}
	input.Credentials[0], input.Credentials[1] = input.Credentials[1], input.Credentials[0]
	second, err := FingerprintPolicyGuardrailInput(input)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestExecutionPreflightPolicyProvenanceChangesPolicyComponent(t *testing.T) {
	projectID := 7
	plan := ExecutionPreflightPlan{
		ContractVersion: ExecutionPreflightContractVersion,
		Intent:          ExecutionPreflightTask, ProjectID: projectID, ActorID: 3, TemplateID: 41,
		Definition: ExecutionPreflightDefinition{Kind: ExecutionReferenceTemplate, ID: 41, Revision: "r1", Fingerprint: "r1"},
		Inputs:     []ExecutionPreflightInput{}, References: []ExecutionPreflightReference{},
		Commands: []ExecutionPreflightCommand{}, Placements: []ExecutionPreflightPlacement{},
		Findings: []ExecutionPreflightFinding{{
			Severity: ExecutionFindingWarning, Code: ExecutionReasonPolicyWarning, Message: "Review the production target.",
			PolicyScope: PolicyGuardrailScopeProject, PolicyRevision: 2, PolicyRuleID: "warn-production",
			PolicyEffect: PolicyGuardrailEffectWarn, RemediationURL: "https://docs.example.test/policies/production",
		}},
		PolicyRevisions: []PolicyGuardrailRevisionRef{{
			Scope: PolicyGuardrailScopeProject, ProjectID: &projectID, Revision: 2,
			Fingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	require.NoError(t, plan.Validate())
	before, err := ExecutionPreflightComponentDigests(plan)
	require.NoError(t, err)

	plan.PolicyRevisions[0].Revision = 3
	after, err := ExecutionPreflightComponentDigests(plan)
	require.NoError(t, err)
	changes, err := DiffExecutionPreflightComponentDigests(before, after)
	require.NoError(t, err)
	assert.Equal(t, []ExecutionPreflightChangeCode{ExecutionChangePolicy}, changes)
}

func TestPolicyGuardrailEvaluationValidatesDecisionAndAppliesTypedPreflightFindings(t *testing.T) {
	projectID := 7
	evaluation := PolicyGuardrailEvaluation{
		Revisions: []PolicyGuardrailRevisionRef{{
			Scope: PolicyGuardrailScopeProject, ProjectID: &projectID, Revision: 2,
			Fingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		Findings: []PolicyGuardrailFinding{{
			Scope: PolicyGuardrailScopeProject, Revision: 2, RuleID: "deny-production",
			Effect: PolicyGuardrailEffectDeny, Severity: PolicyGuardrailSeverityHigh,
			Message: "Production starts are denied.", RemediationURL: "https://docs.example.test/policies/production",
		}},
		Allowed:          false,
		InputFingerprint: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EvaluatedAt:      time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
	}
	require.NoError(t, evaluation.Validate(projectID))

	plan := validPolicyPreflightPlan(projectID)
	require.NoError(t, ApplyPolicyGuardrailEvaluation(&plan, evaluation))
	require.Len(t, plan.Findings, 1)
	assert.Equal(t, ExecutionReasonPolicyDenied, plan.Findings[0].Code)
	assert.Equal(t, ExecutionFindingDenial, plan.Findings[0].Severity)
	assert.Equal(t, "deny-production", plan.Findings[0].PolicyRuleID)
	assert.Equal(t, evaluation.Revisions, plan.PolicyRevisions)

	evaluation.Allowed = true
	require.Error(t, evaluation.Validate(projectID), "a deny finding cannot be relabeled as allowed")
}

func TestApplyPolicyGuardrailEvaluationMapsAllowAndWarnWithoutOverridingDeny(t *testing.T) {
	projectID := 7
	evaluation := PolicyGuardrailEvaluation{
		Revisions: []PolicyGuardrailRevisionRef{{
			Scope: PolicyGuardrailScopeGlobal, Revision: 1,
			Fingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		Findings: []PolicyGuardrailFinding{
			{Scope: PolicyGuardrailScopeGlobal, Revision: 1, RuleID: "allow-reviewed", Effect: PolicyGuardrailEffectAllow, Severity: PolicyGuardrailSeverityInfo, Message: "Reviewed target."},
			{Scope: PolicyGuardrailScopeGlobal, Revision: 1, RuleID: "warn-runner", Effect: PolicyGuardrailEffectWarn, Severity: PolicyGuardrailSeverityMedium, Message: "Runner needs attention."},
		},
		Allowed:          true,
		InputFingerprint: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EvaluatedAt:      time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
	}
	plan := validPolicyPreflightPlan(projectID)
	require.NoError(t, ApplyPolicyGuardrailEvaluation(&plan, evaluation))
	assert.Equal(t, []ExecutionPreflightReasonCode{ExecutionReasonPolicyAllowed, ExecutionReasonPolicyWarning}, []ExecutionPreflightReasonCode{plan.Findings[0].Code, plan.Findings[1].Code})
	assert.False(t, planHasPolicyGuardrailDenial(plan))
}

func validPolicyPreflightPlan(projectID int) ExecutionPreflightPlan {
	return ExecutionPreflightPlan{
		ContractVersion: ExecutionPreflightContractVersion,
		Intent:          ExecutionPreflightTask, ProjectID: projectID, ActorID: 3, TemplateID: 41,
		Definition: ExecutionPreflightDefinition{Kind: ExecutionReferenceTemplate, ID: 41, Revision: "r1", Fingerprint: "r1"},
		Inputs:     []ExecutionPreflightInput{}, References: []ExecutionPreflightReference{},
		Commands: []ExecutionPreflightCommand{}, Placements: []ExecutionPreflightPlacement{}, Findings: []ExecutionPreflightFinding{},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
}

func validPolicyGuardrailInput() PolicyGuardrailEvaluationInput {
	return PolicyGuardrailEvaluationInput{
		ProjectID: 7, Intent: ExecutionPreflightTask,
		EvaluatedAt: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		Template:    &PolicyGuardrailTemplateMetadata{ID: 41, Application: "ansible", Source: "manual"},
		Executor:    PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"},
	}
}
