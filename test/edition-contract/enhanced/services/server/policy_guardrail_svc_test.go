package server

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coreSQL "github.com/semaphoreui/semaphore/db/sql"
	proSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/require"
)

func TestPolicyGuardrailServiceAcceptsZeroPoliciesAndRejectsCorruptRevisions(t *testing.T) {
	input := completePolicyGuardrailInput()
	evaluation, err := evaluatePolicyGuardrailRevisions(nil, input)
	require.NoError(t, err)
	require.True(t, evaluation.Allowed)
	require.Empty(t, evaluation.Revisions)
	document := pro_interfaces.PolicyGuardrailDocument{Version: 1, Rules: nil}
	policy, err := NewCompiledPolicyGuardrailPolicy(pro_interfaces.PolicyGuardrailScopeGlobal, nil, 1, document)
	require.NoError(t, err)
	revision := db.PolicyGuardrailRevision{ID: 1, ScopeKey: "global", Scope: db.PolicyGuardrailScopeGlobal, Revision: 1, SourceYAML: "version: 1\nrules: []\n", CompiledJSON: `{"version":1,"rules":[]}`, Fingerprint: policy.RevisionRef().Fingerprint, CompilerVersion: pro_interfaces.PolicyGuardrailCompilerVersion, PublishedBy: 1, Created: time.Now().UTC()}
	_, _, err = compiledPoliciesFromRevisions([]db.PolicyGuardrailRevision{revision}, input.ProjectID)
	require.NoError(t, err)
	for _, corrupt := range []string{`{"version":1,"rules":[],"unknown":true}`, `{"version":1,"rules":[]} {}`, `[]`} {
		revision.CompiledJSON = corrupt
		_, _, err = compiledPoliciesFromRevisions([]db.PolicyGuardrailRevision{revision}, input.ProjectID)
		require.Error(t, err)
	}
	revision.CompiledJSON = `{"version":1,"rules":[]}`
	revision.CompilerVersion++
	_, _, err = compiledPoliciesFromRevisions([]db.PolicyGuardrailRevision{revision}, input.ProjectID)
	require.Error(t, err)
}

func TestPolicyGuardrailGovernanceRejectsOversizedImpactAndFixtureUsesProvidedYAML(t *testing.T) {
	service := &policyGuardrailGovernanceService{}
	inputs := make([]pro_interfaces.PolicyGuardrailEvaluationInput, 101)
	_, err := service.Impact(context.Background(), pro_interfaces.PolicyGuardrailScopeGlobal, nil, pro_interfaces.PolicyGuardrailImpactRequest{Inputs: inputs})
	require.Error(t, err)
	_, err = service.TestFixture(context.Background(), pro_interfaces.PolicyGuardrailScopeGlobal, nil, pro_interfaces.PolicyGuardrailFixtureRequest{SourceYAML: "invalid", Input: completePolicyGuardrailInput()})
	require.Error(t, err)
}

func TestPolicyGuardrailGovernancePublishesDiffsEvaluatesAndRollsBackWithSQLStore(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := proSQL.NewPolicyGuardrailStore(store.GetConnection())
	service := NewPolicyGuardrailGovernanceService(repository)
	admission := NewPolicyGuardrailAdmissionService(repository)
	actor, err := store.CreateUserWithoutPassword(db.User{Username: "policy-service", Name: "Policy Service", Email: "policy-service@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "policy service project"})
	require.NoError(t, err)
	projectID := project.ID

	firstSource := policyGuardrailServiceYAML("deny-manual", "deny", "manual")
	state, err := service.Get(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID)
	require.NoError(t, err)
	draft, err := service.SaveDraft(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, firstSource, state.Draft.Revision, actor.ID)
	require.NoError(t, err)
	validation := service.Validate(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, draft.SourceYAML)
	require.True(t, validation.Valid)
	require.Len(t, validation.Issues, 0)
	first, err := service.Publish(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, pro_interfaces.PolicyGuardrailPublishRequest{ExpectedDraftRevision: draft.Revision}, actor.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Revision)

	active, err := service.Get(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID)
	require.NoError(t, err)
	require.NotNil(t, active.Active)
	require.Equal(t, first.Fingerprint, active.Active.Fingerprint)

	input := completePolicyGuardrailInput()
	input.ProjectID = project.ID
	input.Template.Source = "manual"
	fixture, err := service.TestFixture(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, pro_interfaces.PolicyGuardrailFixtureRequest{SourceYAML: firstSource, Input: input})
	require.NoError(t, err)
	require.False(t, fixture.Allowed)
	require.Equal(t, "deny-manual", fixture.Findings[0].RuleID)

	claim, err := admission.ClaimPolicyGuardrailEvaluation(pro_interfaces.PolicyGuardrailAdmissionRequest{DecisionKey: "manual-1", Source: "manual", ActorUserID: &actor.ID, Input: input})
	require.NoError(t, err)
	require.False(t, claim.Evaluation.Allowed)
	require.True(t, claim.Inserted)

	secondSource := policyGuardrailServiceYAML("warn-manual", "warn", "manual")
	draft, err = service.SaveDraft(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, secondSource, active.Draft.Revision, actor.ID)
	require.NoError(t, err)
	second, err := service.Publish(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, pro_interfaces.PolicyGuardrailPublishRequest{ExpectedDraftRevision: draft.Revision}, actor.ID)
	require.NoError(t, err)
	diff, err := service.Diff(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, first.Revision, second.Revision)
	require.NoError(t, err)
	require.Equal(t, []string{"warn-manual"}, diff.Added)
	require.Equal(t, []string{"deny-manual"}, diff.Removed)

	current, err := service.Get(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID)
	require.NoError(t, err)
	rolledBack, err := service.Rollback(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, pro_interfaces.PolicyGuardrailRollbackRequest{Revision: first.Revision, ExpectedDraftRevision: current.Draft.Revision, Reason: "Restore the denied manual-start policy."}, actor.ID)
	require.NoError(t, err)
	require.Equal(t, 3, rolledBack.Revision)
	require.NotNil(t, rolledBack.RollbackOfRevision)
	require.Equal(t, first.Revision, *rolledBack.RollbackOfRevision)

	revisions, err := service.Revisions(context.Background(), pro_interfaces.PolicyGuardrailScopeProject, &projectID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, revisions, 3)
	evaluations, err := service.Evaluations(context.Background(), &projectID, db.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, evaluations, 1)
}

func policyGuardrailServiceYAML(id, effect, source string) string {
	return "version: 1\nrules:\n" +
		"  - id: " + id + "\n" +
		"    effect: " + effect + "\n" +
		"    severity: high\n" +
		"    message: Policy matched.\n" +
		"    match: all\n" +
		"    conditions:\n" +
		"      - field: task.source\n" +
		"        operator: equals\n" +
		"        string_value: " + source + "\n"
}
