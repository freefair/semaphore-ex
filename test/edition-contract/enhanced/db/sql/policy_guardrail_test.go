package sql

import (
	"testing"
	"time"

	coreDB "github.com/semaphoreui/semaphore/db"
	coreSQL "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const emptyPolicyGuardrailYAML = "version: 1\nrules: []\n"

func TestPolicyGuardrailStoreDraftPublishCASAndRollbackAsNewRevision(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	actor, err := store.CreateUserWithoutPassword(coreDB.User{Username: "policy-publisher", Name: "Policy Publisher", Email: "policy-publisher@example.test"})
	require.NoError(t, err)

	draft, err := repository.GetPolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeGlobal, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, draft.Revision)
	assert.Equal(t, emptyPolicyGuardrailYAML, draft.SourceYAML)

	draft, err = repository.SavePolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeGlobal, nil, emptyPolicyGuardrailYAML, draft.Revision, actor.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, draft.Revision)

	_, err = repository.SavePolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeGlobal, nil, emptyPolicyGuardrailYAML, 1, actor.ID)
	assert.ErrorIs(t, err, coreDB.ErrPolicyGuardrailDraftRevisionConflict)

	published, err := repository.PublishPolicyGuardrailRevision(
		pro_interfaces.PolicyGuardrailScopeGlobal, nil, draft.Revision, actor.ID,
		emptyPolicyGuardrailYAML, `{"version":1,"rules":[]}`,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, published.Revision)
	assert.Nil(t, published.ParentRevision)

	state, err := repository.GetPolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeGlobal, nil)
	require.NoError(t, err)
	require.NotNil(t, state.ActiveRevision)
	assert.Equal(t, 1, *state.ActiveRevision)
	assert.Equal(t, 3, state.Revision)

	rolledBack, err := repository.RollbackPolicyGuardrailRevision(
		pro_interfaces.PolicyGuardrailScopeGlobal, nil, published.Revision, state.Revision,
		actor.ID, "Restore the last approved policy.",
	)
	require.NoError(t, err)
	assert.Equal(t, 2, rolledBack.Revision)
	require.NotNil(t, rolledBack.ParentRevision)
	assert.Equal(t, 1, *rolledBack.ParentRevision)
	require.NotNil(t, rolledBack.RollbackOfRevision)
	assert.Equal(t, 1, *rolledBack.RollbackOfRevision)
	assert.Equal(t, published.Fingerprint, rolledBack.Fingerprint)

	revisions, err := repository.GetPolicyGuardrailRevisions(pro_interfaces.PolicyGuardrailScopeGlobal, nil, coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	assert.Equal(t, 2, revisions[0].Revision)
	assert.Equal(t, 1, revisions[1].Revision)
}

func TestPolicyGuardrailStoreLoadsGlobalBeforeProjectAndClaimsIdempotently(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	actor, err := store.CreateUserWithoutPassword(coreDB.User{Username: "policy-evaluator", Name: "Policy Evaluator", Email: "policy-evaluator@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(coreDB.Project{Name: "policy evaluation project"})
	require.NoError(t, err)
	for _, scope := range []struct {
		scope       pro_interfaces.PolicyGuardrailScope
		projectID   *int
		fingerprint string
	}{
		{pro_interfaces.PolicyGuardrailScopeGlobal, nil, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{pro_interfaces.PolicyGuardrailScopeProject, policyGuardrailIntPointer(project.ID), "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	} {
		draft, getErr := repository.GetPolicyGuardrailDraft(scope.scope, scope.projectID)
		require.NoError(t, getErr)
		draft, saveErr := repository.SavePolicyGuardrailDraft(scope.scope, scope.projectID, emptyPolicyGuardrailYAML, draft.Revision, actor.ID)
		require.NoError(t, saveErr)
		_, publishErr := repository.PublishPolicyGuardrailRevision(scope.scope, scope.projectID, draft.Revision, actor.ID, emptyPolicyGuardrailYAML, `{"version":1,"rules":[]}`, scope.fingerprint, 1)
		require.NoError(t, publishErr)
	}

	input := pro_interfaces.PolicyGuardrailEvaluationInput{
		ProjectID: project.ID, Intent: pro_interfaces.ExecutionPreflightTask,
		EvaluatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Template:    &pro_interfaces.PolicyGuardrailTemplateMetadata{ID: 9, Application: "ansible", Source: "schedule"},
		Executor:    pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"},
	}
	evaluate := func(revisions []coreDB.PolicyGuardrailRevision, actual pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
		require.Len(t, revisions, 2)
		assert.Equal(t, coreDB.PolicyGuardrailScopeGlobal, revisions[0].Scope)
		assert.Equal(t, coreDB.PolicyGuardrailScopeProject, revisions[1].Scope)
		assert.NotEqual(t, input.EvaluatedAt, actual.EvaluatedAt, "repository supplies database time")
		fingerprint, fingerprintErr := pro_interfaces.FingerprintPolicyGuardrailInput(actual)
		if fingerprintErr != nil {
			return pro_interfaces.PolicyGuardrailEvaluation{}, fingerprintErr
		}
		return pro_interfaces.PolicyGuardrailEvaluation{
			Revisions: []pro_interfaces.PolicyGuardrailRevisionRef{
				{Scope: pro_interfaces.PolicyGuardrailScopeGlobal, Revision: revisions[0].Revision, Fingerprint: revisions[0].Fingerprint},
				{Scope: pro_interfaces.PolicyGuardrailScopeProject, ProjectID: policyGuardrailIntPointer(project.ID), Revision: revisions[1].Revision, Fingerprint: revisions[1].Fingerprint},
			},
			Findings: []pro_interfaces.PolicyGuardrailFinding{}, Allowed: true,
			InputFingerprint: fingerprint, EvaluatedAt: actual.EvaluatedAt,
		}, nil
	}
	request := pro_interfaces.PolicyGuardrailAdmissionRequest{DecisionKey: "schedule-9", Source: "schedule", ActorUserID: &actor.ID, Input: input}
	claim, err := repository.ClaimPolicyGuardrailEvaluation(request, evaluate)
	require.NoError(t, err)
	assert.True(t, claim.Inserted)
	assert.Positive(t, claim.Record.ID)
	assert.Equal(t, coreDB.PolicyGuardrailDecisionAllow, claim.Record.Decision)

	duplicate, err := repository.ClaimPolicyGuardrailEvaluation(request, evaluate)
	require.NoError(t, err)
	assert.False(t, duplicate.Inserted)
	assert.Equal(t, claim.Record.ID, duplicate.Record.ID)

	history, err := repository.GetPolicyGuardrailEvaluationHistory(policyGuardrailIntPointer(project.ID), coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, claim.Record.ID, history[0].ID)
}

func policyGuardrailIntPointer(value int) *int { return &value }
