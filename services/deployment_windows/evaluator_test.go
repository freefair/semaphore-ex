package deployment_windows

import (
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateFreezePrecedesAllowAndUsesHalfOpenIntervals(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	policy := testPolicy(
		db.DeploymentWindowRule{ID: 1, Revision: 1, Name: "Allow", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope, Recurrence: "0 9 * * *", DurationMinutes: 120},
		db.DeploymentWindowRule{ID: 2, Revision: 1, Name: "Freeze", Active: true, Kind: db.DeploymentWindowFreeze, Scope: db.DeploymentWindowTemplateScope, TemplateID: intPointer(42), Recurrence: "0 10 * * *", DurationMinutes: 30},
	)

	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionBlocked, decision.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonFreezeActive, decision.Reason)
	assert.Equal(t, []int{1, 2}, decision.Provenance.MatchedRuleIDs)

	decision, err = evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 30, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, decision.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonAllowWindow, decision.Reason)
}

func TestEvaluateUsesScopedRulesAndFindsBoundedNextEligibleInstant(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone), WithMaxNextEligibleMinutes(180))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Workflow allow", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowWorkflowScope,
		WorkflowID: intPointer(9), Recurrence: "15 10 * * *", DurationMinutes: 30,
	})
	policy.Default = db.DeploymentWindowDefaultDeny

	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, WorkflowID: 9, Source: pro_interfaces.DeploymentWindowSourceWorkflowNode,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionBlocked, decision.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonDefaultDeny, decision.Reason)
	require.NotNil(t, decision.NextEligibleAt)
	assert.Equal(t, "2026-09-02T10:15:00Z", decision.NextEligibleAt.Format(time.RFC3339))
}

func TestEvaluateHonorsLocalEffectiveDateAndCrossMidnightWindow(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	from, until := "2026-09-02", "2026-09-02"
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Maintenance", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "30 23 * * *", DurationMinutes: 90, EffectiveFrom: &from, EffectiveUntil: &until,
	})
	policy.Default = db.DeploymentWindowDefaultDeny

	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 3, 0, 15, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, decision.State)
}

func TestEvaluateMarksNextEligibleUnknownWhenOperationBudgetIsExhausted(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone), WithMaxEvaluationOperations(3))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Freeze", Active: true, Kind: db.DeploymentWindowFreeze, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "* * * * *", DurationMinutes: 1,
	})
	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Nil(t, decision.NextEligibleAt)
	assert.False(t, decision.NextEligibleKnown)
}

func TestEvaluateJumpsToMonthlyCandidateWithoutFixedHorizon(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Monthly", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "0 9 1 * *", DurationMinutes: 30,
	})
	policy.Default = db.DeploymentWindowDefaultDeny
	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, decision.NextEligibleAt)
	assert.True(t, decision.NextEligibleKnown)
	assert.Equal(t, "2026-02-01T09:00:00Z", decision.NextEligibleAt.Format(time.RFC3339))
}

func TestEvaluateReportsNoFutureEligibilityAfterAnAllowRuleExpires(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	until := "2026-09-01"
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Expired allow", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "0 9 * * *", DurationMinutes: 30, EffectiveUntil: &until,
	})
	policy.Default = db.DeploymentWindowDefaultDeny

	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.True(t, decision.NextEligibleKnown)
	assert.Nil(t, decision.NextEligibleAt)
}

func TestEvaluateReportsNoFutureEligibilityWhenOnlyFreezesExistUnderDenyDefault(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone), WithMaxEvaluationOperations(100))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Freeze only", Active: true, Kind: db.DeploymentWindowFreeze, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "0 9 * * *", DurationMinutes: 30,
	})
	policy.Default = db.DeploymentWindowDefaultDeny

	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.True(t, decision.NextEligibleKnown)
	assert.Nil(t, decision.NextEligibleAt)
}

func TestEvaluateUsesSlice064DSTSemanticsForGapsAndOverlaps(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "DST", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "30 2 * * *", DurationMinutes: 30,
	})
	policy.Timezone = "Europe/Berlin"
	policy.Default = db.DeploymentWindowDefaultDeny

	gap, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionBlocked, gap.State)

	first, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, first.State)
	between, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 10, 25, 1, 5, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionBlocked, between.State)
	second, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, second.State)
}

func TestPolicyRejectsHostDependentTimezoneDespitePermissiveInjectedValidator(t *testing.T) {
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Allow", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "0 9 * * *", DurationMinutes: 30,
	})
	policy.Timezone = "Local"
	require.Error(t, policy.Validate(testTimezone))
}

func TestEvaluateDenseRuleDoesNotLinearlyScanEveryMinute(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone), WithMaxEvaluationOperations(128))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Dense", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "* * * * *", DurationMinutes: 24 * 60,
	})
	policy.Default = db.DeploymentWindowDefaultDeny
	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 30, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, decision.State)
}

func TestEvaluateRetainsDenseEffectiveUntilWindowAcrossMidnight(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone), WithMaxEvaluationOperations(128))
	from, until := "2026-09-02", "2026-09-02"
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Dense boundary", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "* * * * *", DurationMinutes: 24 * 60, EffectiveFrom: &from, EffectiveUntil: &until,
	})
	policy.Default = db.DeploymentWindowDefaultDeny
	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 3, 0, 10, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, decision.State)
}

func TestEvaluateAllowsOnlyAuthenticatedManualControlledOverride(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Freeze", Active: true, Kind: db.DeploymentWindowFreeze, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "* * * * *", DurationMinutes: 1,
	})
	request := pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual, At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		Override: &pro_interfaces.DeploymentWindowOverrideRequest{ActorID: 5, Authenticated: true, Authorized: true, Category: pro_interfaces.DeploymentWindowOverrideIncident, Reference: "INC-1234"},
	}
	decision, err := evaluator.Evaluate(policy, request)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionOverridden, decision.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonOverride, decision.Reason)
	assert.Equal(t, 5, decision.Provenance.OverrideActorID)

	request.Source = pro_interfaces.DeploymentWindowSourceWebhook
	_, err = evaluator.Evaluate(policy, request)
	require.Error(t, err)
}

func TestEvaluateIgnoresInactiveRules(t *testing.T) {
	evaluator := NewEvaluator(WithTimezoneValidator(testTimezone))
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Paused freeze", Active: false, Kind: db.DeploymentWindowFreeze, Scope: db.DeploymentWindowProjectScope,
		Recurrence: "* * * * *", DurationMinutes: 1,
	})
	decision, err := evaluator.Evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: 7, TemplateID: 42, Source: pro_interfaces.DeploymentWindowSourceManual,
		At: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionAllowed, decision.State)
	assert.Equal(t, pro_interfaces.DeploymentWindowReasonDefaultAllow, decision.Reason)
}

func TestPolicyAndOverrideValidationFailClosed(t *testing.T) {
	policy := testPolicy(db.DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "Template", Active: true, Kind: db.DeploymentWindowAllow, Scope: db.DeploymentWindowTemplateScope,
		Recurrence: "0 9 * * *", DurationMinutes: 30,
	})
	require.Error(t, policy.Validate(testTimezone), "template scope needs an ID")
	policy.Rules[0].TemplateID = intPointer(3)
	policy.Timezone = "Invalid/Zone"
	require.Error(t, policy.Validate(testTimezone))
	policy.Timezone = "UTC"
	policy.Rules[0].Recurrence = "@daily"
	require.Error(t, policy.Validate(testTimezone))

	invalid := pro_interfaces.DeploymentWindowOverrideRequest{ActorID: 5, Authenticated: true, Authorized: true, Category: pro_interfaces.DeploymentWindowOverrideIncident, Reference: "https://ticket.invalid/123"}
	require.Error(t, invalid.Validate())
}

func TestPublicDecisionViewExcludesAdministrativeProvenance(t *testing.T) {
	decision := pro_interfaces.DeploymentWindowDecision{
		State: pro_interfaces.DeploymentWindowDecisionBlocked, Reason: pro_interfaces.DeploymentWindowReasonFreezeActive,
		Provenance: pro_interfaces.DeploymentWindowDecisionProvenance{PolicyRevision: 9, EffectiveTimezone: "Europe/Berlin", MatchedRuleIDs: []int{11}, OverrideActorID: 7},
	}
	public := decision.PublicView()
	assert.Equal(t, pro_interfaces.DeploymentWindowDecisionBlocked, public.State)
	assert.Empty(t, public.Provenance.MatchedRuleIDs)
	assert.Zero(t, public.Provenance.PolicyRevision)
	assert.Empty(t, public.Provenance.EffectiveTimezone)
	assert.Empty(t, public.Provenance.MatchedRules)
}

func testPolicy(rules ...db.DeploymentWindowRule) db.DeploymentWindowPolicy {
	return db.DeploymentWindowPolicy{ProjectID: 7, Revision: 1, Timezone: "UTC", Default: db.DeploymentWindowDefaultAllow, Rules: rules}
}

func testTimezone(value string) error {
	if _, err := time.LoadLocation(value); err != nil {
		return errors.New("invalid timezone")
	}
	return nil
}

func intPointer(value int) *int { return &value }
