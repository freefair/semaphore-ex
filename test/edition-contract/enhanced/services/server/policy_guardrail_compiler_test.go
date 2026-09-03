package server

import (
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompilePolicyGuardrailYAMLRejectsUnsafeOrAmbiguousInput(t *testing.T) {
	base := `version: 1
rules:
  - id: deny-example
    effect: deny
    severity: high
    message: Denied.
    match: all
    conditions:
      - field: task.application
        operator: equals
        string_value: ansible
`
	tests := map[string]string{
		"alias":         "shared: &shared ansible\n" + strings.Replace(base, "string_value: ansible", "string_value: *shared", 1),
		"anchor":        strings.Replace(base, "string_value: ansible", "string_value: &value ansible", 1),
		"custom tag":    strings.Replace(base, "string_value: ansible", "string_value: !evil ansible", 1),
		"merge key":     strings.Replace(base, "    conditions:", "    <<: {message: Denied.}\n    conditions:", 1),
		"duplicate key": strings.Replace(base, "    effect: deny", "    effect: deny\n    effect: warn", 1),
		"unknown key":   strings.Replace(base, "    match: all", "    match: all\n    callback: https://example.test", 1),
		"multiple docs": base + "---\nversion: 1\nrules: []\n",
		"wrong type":    strings.Replace(base, "string_value: ansible", "integer_value: 1", 1),
		"regex op":      strings.Replace(base, "operator: equals", "operator: matches", 1),
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := CompilePolicyGuardrailYAML([]byte(source))
			require.Error(t, err)
		})
	}

	over := make([]byte, pro_interfaces.MaxPolicyGuardrailYAMLBytes+1)
	for i := range over {
		over[i] = 'x'
	}
	_, _, err := CompilePolicyGuardrailYAML(over)
	require.Error(t, err)
}

func TestCompilePolicyGuardrailYAMLCanonicalizesRuleAndMappingOrder(t *testing.T) {
	first := `version: 1
rules:
  - id: zeta
    effect: warn
    severity: low
    message: Zeta.
    match: all
    conditions:
      - field: task.application
        operator: one_of
        string_values: [terraform, ansible]
  - id: alpha
    effect: deny
    severity: high
    message: Alpha.
    match: all
    conditions:
      - field: task.source
        operator: equals
        string_value: manual
`
	second := `rules:
  - conditions:
      - string_value: manual
        operator: equals
        field: task.source
    message: Alpha.
    severity: high
    effect: deny
    match: all
    id: alpha
  - conditions:
      - string_values: [ansible, terraform]
        field: task.application
        operator: one_of
    id: zeta
    match: all
    message: Zeta.
    effect: warn
    severity: low
version: 1
`
	firstDocument, firstFingerprint, err := CompilePolicyGuardrailYAML([]byte(first))
	require.NoError(t, err)
	secondDocument, secondFingerprint, err := CompilePolicyGuardrailYAML([]byte(second))
	require.NoError(t, err)
	assert.Equal(t, firstFingerprint, secondFingerprint)
	assert.Equal(t, []string{"alpha", "zeta"}, []string{firstDocument.Rules[0].ID, firstDocument.Rules[1].ID})
	assert.Equal(t, firstDocument, secondDocument)
}

func TestPolicyGuardrailEvaluatorSupportsEveryClosedFieldOperatorPair(t *testing.T) {
	input := completePolicyGuardrailInput()
	conditions := allSupportedPolicyGuardrailConditions()
	document := pro_interfaces.PolicyGuardrailDocument{Version: 1, Rules: []pro_interfaces.PolicyGuardrailRule{
		{ID: "all-supported-first", Effect: pro_interfaces.PolicyGuardrailEffectWarn, Severity: pro_interfaces.PolicyGuardrailSeverityInfo,
			Message: "Every first closed predicate matched.", Match: pro_interfaces.PolicyGuardrailMatchAll, Conditions: conditions[:pro_interfaces.MaxPolicyGuardrailPredicatesPerRule]},
		{ID: "all-supported-second", Effect: pro_interfaces.PolicyGuardrailEffectWarn, Severity: pro_interfaces.PolicyGuardrailSeverityInfo,
			Message: "Every remaining closed predicate matched.", Match: pro_interfaces.PolicyGuardrailMatchAll, Conditions: conditions[pro_interfaces.MaxPolicyGuardrailPredicatesPerRule:]},
	}}
	policy, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 1, document)
	require.NoError(t, err)
	evaluator, err := NewPolicyGuardrailEvaluator(policy, nil)
	require.NoError(t, err)

	evaluation, err := evaluator.EvaluatePolicyGuardrails(input)
	require.NoError(t, err)
	require.True(t, evaluation.Allowed)
	require.Len(t, evaluation.Findings, 2)
	assert.Equal(t, []string{"all-supported-first", "all-supported-second"}, findingIDs(evaluation.Findings))
}

func TestPolicyGuardrailEvaluatorKeepsAllMatchesInGlobalThenProjectLexicalOrder(t *testing.T) {
	input := completePolicyGuardrailInput()
	global, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 3, policyDocument(
		policyRule("z-global", pro_interfaces.PolicyGuardrailEffectWarn),
		policyRule("a-global", pro_interfaces.PolicyGuardrailEffectDeny),
	))
	require.NoError(t, err)
	projectID := input.ProjectID
	project, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeProject, &projectID, 5, policyDocument(
		policyRule("z-project", pro_interfaces.PolicyGuardrailEffectAllow),
		policyRule("a-project", pro_interfaces.PolicyGuardrailEffectWarn),
	))
	require.NoError(t, err)
	evaluator, err := NewPolicyGuardrailEvaluator(global, project)
	require.NoError(t, err)

	evaluation, err := evaluator.EvaluatePolicyGuardrails(input)
	require.NoError(t, err)
	assert.False(t, evaluation.Allowed)
	assert.Equal(t, []string{"a-global", "z-global", "a-project", "z-project"}, findingIDs(evaluation.Findings))
	assert.Equal(t, []pro_interfaces.PolicyGuardrailEffect{
		pro_interfaces.PolicyGuardrailEffectDeny, pro_interfaces.PolicyGuardrailEffectWarn,
		pro_interfaces.PolicyGuardrailEffectWarn, pro_interfaces.PolicyGuardrailEffectAllow,
	}, findingEffects(evaluation.Findings))
}

func TestPolicyGuardrailEvaluatorRejectsMutatedCompiledPolicyOrdering(t *testing.T) {
	input := completePolicyGuardrailInput()
	policy, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 1, policyDocument(
		policyRule("z-rule", pro_interfaces.PolicyGuardrailEffectWarn),
		policyRule("a-rule", pro_interfaces.PolicyGuardrailEffectWarn),
	))
	require.NoError(t, err)
	evaluator, err := NewPolicyGuardrailEvaluator(policy, nil)
	require.NoError(t, err)
	policy.document.Rules[0], policy.document.Rules[1] = policy.document.Rules[1], policy.document.Rules[0]
	_, err = evaluator.EvaluatePolicyGuardrails(input)
	require.Error(t, err)
}

func TestPolicyGuardrailEvaluatorUsesExplicitValueFreeInputOnly(t *testing.T) {
	input := completePolicyGuardrailInput()
	_, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 1, policyDocument(
		pro_interfaces.PolicyGuardrailRule{ID: "deny-secret-path", Effect: pro_interfaces.PolicyGuardrailEffectDeny, Severity: pro_interfaces.PolicyGuardrailSeverityHigh, Message: "No secrets.", Match: pro_interfaces.PolicyGuardrailMatchAll, Conditions: []pro_interfaces.PolicyGuardrailCondition{{
			Field: pro_interfaces.PolicyGuardrailField("credential.secret_value"), Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: stringPtrPolicy("secret"),
		}}},
	))
	require.Error(t, err, "the closed compiler must refuse values outside the public metadata contract")

	valid, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 2, policyDocument(policyRule("allow-explicit", pro_interfaces.PolicyGuardrailEffectAllow)))
	require.NoError(t, err)
	evaluator, err := NewPolicyGuardrailEvaluator(valid, nil)
	require.NoError(t, err)
	evaluation, err := evaluator.EvaluatePolicyGuardrails(input)
	require.NoError(t, err)
	assert.NotEmpty(t, evaluation.InputFingerprint)
	assert.True(t, evaluation.Allowed)
}

func policyDocument(rules ...pro_interfaces.PolicyGuardrailRule) pro_interfaces.PolicyGuardrailDocument {
	return pro_interfaces.PolicyGuardrailDocument{Version: pro_interfaces.PolicyGuardrailSchemaVersion, Rules: rules}
}

func policyRule(id string, effect pro_interfaces.PolicyGuardrailEffect) pro_interfaces.PolicyGuardrailRule {
	return pro_interfaces.PolicyGuardrailRule{ID: id, Effect: effect, Severity: pro_interfaces.PolicyGuardrailSeverityHigh, Message: "Policy matched.", Match: pro_interfaces.PolicyGuardrailMatchAll, Conditions: []pro_interfaces.PolicyGuardrailCondition{{
		Field: pro_interfaces.PolicyFieldTaskApplication, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: stringPtrPolicy("ansible"),
	}}}
}

func completePolicyGuardrailInput() pro_interfaces.PolicyGuardrailEvaluationInput {
	return pro_interfaces.PolicyGuardrailEvaluationInput{
		ProjectID: 7, Intent: pro_interfaces.ExecutionPreflightTask, EvaluatedAt: time.Date(2026, 9, 3, 10, 30, 0, 0, time.UTC),
		Template:  &pro_interfaces.PolicyGuardrailTemplateMetadata{ID: 11, Application: "ansible", Source: "manual", InventoryOverride: true, BranchOverride: true, CommitOverride: true, ArgumentKeyCount: 2, InputKeyCount: 2},
		Inventory: &pro_interfaces.PolicyGuardrailInventoryMetadata{ID: 12, Type: "static", RunnerTagCount: 2}, EnvironmentIDs: []int{3, 9},
		Workflow:    &pro_interfaces.PolicyGuardrailWorkflowMetadata{ID: 13, Revision: 2, NodeID: 14, NodeKind: "task", TriggerSource: "api", CrossProject: true},
		Runner:      pro_interfaces.PolicyGuardrailRunnerMetadata{SelectedID: 15, SelectedScope: "project", SelectedExecutor: "docker", RequestedTags: []string{"linux", "prod"}, CandidateCount: 2},
		Executor:    pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "docker", ImagePresent: true, ImageReferenceKind: "digest", ImageDigest: "sha256:abc"},
		Credentials: []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata{{ID: 16, Scope: "global", BindingTarget: "repository.ssh"}, {ID: 17, Scope: "local", BindingTarget: "inventory.ssh"}},
	}
}

func allSupportedPolicyGuardrailConditions() []pro_interfaces.PolicyGuardrailCondition {
	manual := stringPtrPolicy("manual")
	static := stringPtrPolicy("static")
	task := stringPtrPolicy("task")
	api := stringPtrPolicy("api")
	project := stringPtrPolicy("project")
	docker := stringPtrPolicy("docker")
	digest := stringPtrPolicy("digest")
	weekday := stringPtrPolicy("thursday")
	sha := stringPtrPolicy("sha256:abc")
	trueValue := boolPtrPolicy(true)
	intValue := func(value int) *int { return &value }
	return []pro_interfaces.PolicyGuardrailCondition{
		{Field: pro_interfaces.PolicyFieldTaskTemplateID, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, IntegerValue: intValue(11)},
		{Field: pro_interfaces.PolicyFieldTaskApplication, Operator: pro_interfaces.PolicyGuardrailOperatorOneOf, StringValues: []string{"terraform", "ansible"}},
		{Field: pro_interfaces.PolicyFieldTaskSource, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: manual},
		{Field: pro_interfaces.PolicyFieldTaskInventoryOverride, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldTaskBranchOverride, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldTaskCommitOverride, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldTaskArgumentKeyCount, Operator: pro_interfaces.PolicyGuardrailOperatorAtLeast, IntegerValue: intValue(2)},
		{Field: pro_interfaces.PolicyFieldTaskInputKeyCount, Operator: pro_interfaces.PolicyGuardrailOperatorLessThan, IntegerValue: intValue(3)},
		{Field: pro_interfaces.PolicyFieldInventoryPresent, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldInventoryID, Operator: pro_interfaces.PolicyGuardrailOperatorOneOf, IntegerValues: []int{1, 12}},
		{Field: pro_interfaces.PolicyFieldInventoryType, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: static},
		{Field: pro_interfaces.PolicyFieldInventoryRunnerTagCount, Operator: pro_interfaces.PolicyGuardrailOperatorAtMost, IntegerValue: intValue(2)},
		{Field: pro_interfaces.PolicyFieldEnvironmentIDs, Operator: pro_interfaces.PolicyGuardrailOperatorContainsAll, IntegerValues: []int{3, 9}},
		{Field: pro_interfaces.PolicyFieldEnvironmentCount, Operator: pro_interfaces.PolicyGuardrailOperatorGreaterThan, IntegerValue: intValue(1)},
		{Field: pro_interfaces.PolicyFieldWorkflowPresent, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldWorkflowID, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, IntegerValue: intValue(13)},
		{Field: pro_interfaces.PolicyFieldWorkflowRevision, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, IntegerValue: intValue(2)},
		{Field: pro_interfaces.PolicyFieldWorkflowNodeID, Operator: pro_interfaces.PolicyGuardrailOperatorPresent},
		{Field: pro_interfaces.PolicyFieldWorkflowNodeKind, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: task},
		{Field: pro_interfaces.PolicyFieldWorkflowTriggerSource, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: api},
		{Field: pro_interfaces.PolicyFieldWorkflowCrossProject, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldRunnerSelectedID, Operator: pro_interfaces.PolicyGuardrailOperatorPresent},
		{Field: pro_interfaces.PolicyFieldRunnerSelectedScope, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: project},
		{Field: pro_interfaces.PolicyFieldRunnerSelectedExecutor, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: docker},
		{Field: pro_interfaces.PolicyFieldRunnerRequestedTags, Operator: pro_interfaces.PolicyGuardrailOperatorContainsAny, StringValues: []string{"darwin", "prod"}},
		{Field: pro_interfaces.PolicyFieldRunnerCandidateCount, Operator: pro_interfaces.PolicyGuardrailOperatorAtLeast, IntegerValue: intValue(2)},
		{Field: pro_interfaces.PolicyFieldExecutorType, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: docker},
		{Field: pro_interfaces.PolicyFieldImagePresent, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, BooleanValue: trueValue},
		{Field: pro_interfaces.PolicyFieldImageReferenceKind, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: digest},
		{Field: pro_interfaces.PolicyFieldImageDigest, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: sha},
		{Field: pro_interfaces.PolicyFieldCredentialIDs, Operator: pro_interfaces.PolicyGuardrailOperatorContainsAll, IntegerValues: []int{16, 17}},
		{Field: pro_interfaces.PolicyFieldCredentialScopes, Operator: pro_interfaces.PolicyGuardrailOperatorContainsAny, StringValues: []string{"local"}},
		{Field: pro_interfaces.PolicyFieldCredentialBindingTargets, Operator: pro_interfaces.PolicyGuardrailOperatorContainsAll, StringValues: []string{"inventory.ssh", "repository.ssh"}},
		{Field: pro_interfaces.PolicyFieldCredentialCount, Operator: pro_interfaces.PolicyGuardrailOperatorAtLeast, IntegerValue: intValue(2)},
		{Field: pro_interfaces.PolicyFieldTimeWeekday, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, StringValue: weekday},
		{Field: pro_interfaces.PolicyFieldTimeMinuteOfDay, Operator: pro_interfaces.PolicyGuardrailOperatorEquals, IntegerValue: intValue(630)},
	}
}

func stringPtrPolicy(value string) *string { return &value }
func boolPtrPolicy(value bool) *bool       { return &value }

func findingIDs(findings []pro_interfaces.PolicyGuardrailFinding) []string {
	values := make([]string, len(findings))
	for index := range findings {
		values[index] = findings[index].RuleID
	}
	return values
}

func findingEffects(findings []pro_interfaces.PolicyGuardrailFinding) []pro_interfaces.PolicyGuardrailEffect {
	values := make([]pro_interfaces.PolicyGuardrailEffect, len(findings))
	for index := range findings {
		values[index] = findings[index].Effect
	}
	return values
}
