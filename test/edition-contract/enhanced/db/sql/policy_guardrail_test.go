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

func TestPolicyGuardrailBatchPreviewUsesOneDatabaseTimestampWithoutPersisting(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	project, err := store.CreateProject(coreDB.Project{Name: "policy batch preview project"})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 3, 10, 59, 59, 0, time.UTC)
	inputs := []pro_interfaces.PolicyGuardrailEvaluationInput{
		{ProjectID: project.ID, Intent: pro_interfaces.ExecutionPreflightWorkflow, EvaluatedAt: now,
			Workflow: &pro_interfaces.PolicyGuardrailWorkflowMetadata{ID: 3, Revision: 1, TriggerSource: "manual"},
			Executor: pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "workflow", ImageReferenceKind: "none"}},
		{ProjectID: project.ID, Intent: pro_interfaces.ExecutionPreflightTask, EvaluatedAt: now,
			Template: &pro_interfaces.PolicyGuardrailTemplateMetadata{ID: 4, Application: "ansible", Source: "workflow_node"},
			Workflow: &pro_interfaces.PolicyGuardrailWorkflowMetadata{ID: 3, Revision: 1, NodeID: 5, NodeKind: "task", TriggerSource: "manual"},
			Executor: pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"}},
	}
	evaluations, err := repository.PreviewPolicyGuardrailEvaluations(inputs, func(_ []coreDB.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
		fingerprint, fingerprintErr := pro_interfaces.FingerprintPolicyGuardrailInput(input)
		if fingerprintErr != nil {
			return pro_interfaces.PolicyGuardrailEvaluation{}, fingerprintErr
		}
		return pro_interfaces.PolicyGuardrailEvaluation{Revisions: []pro_interfaces.PolicyGuardrailRevisionRef{}, Findings: []pro_interfaces.PolicyGuardrailFinding{}, Allowed: true, InputFingerprint: fingerprint, EvaluatedAt: input.EvaluatedAt}, nil
	})
	require.NoError(t, err)
	require.Len(t, evaluations, 2)
	assert.True(t, evaluations[0].EvaluatedAt.Equal(evaluations[1].EvaluatedAt), "time predicates must see one coherent batch time")
	history, err := repository.GetPolicyGuardrailEvaluationHistory(&project.ID, coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, history, "preview must not create durable admission records")
}

func TestPolicyGuardrailGlobalEvaluationHistoryIsBoundedAcrossProjects(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	actor, err := store.CreateUserWithoutPassword(coreDB.User{Username: "policy-global-history", Name: "Policy Global History", Email: "policy-global-history@example.test"})
	require.NoError(t, err)
	first, err := store.CreateProject(coreDB.Project{Name: "policy-global-history-first"})
	require.NoError(t, err)
	second, err := store.CreateProject(coreDB.Project{Name: "policy-global-history-second"})
	require.NoError(t, err)
	evaluate := func(_ []coreDB.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
		fingerprint, fingerprintErr := pro_interfaces.FingerprintPolicyGuardrailInput(input)
		if fingerprintErr != nil {
			return pro_interfaces.PolicyGuardrailEvaluation{}, fingerprintErr
		}
		return pro_interfaces.PolicyGuardrailEvaluation{Allowed: true, InputFingerprint: fingerprint, EvaluatedAt: input.EvaluatedAt}, nil
	}
	_, err = repository.ClaimPolicyGuardrailEvaluation(policyGuardrailBatchRequest(first.ID, actor.ID, "global-history-first", "first"), evaluate)
	require.NoError(t, err)
	_, err = repository.ClaimPolicyGuardrailEvaluation(policyGuardrailBatchRequest(second.ID, actor.ID, "global-history-second", "second"), evaluate)
	require.NoError(t, err)

	history, err := repository.GetPolicyGuardrailEvaluationHistory(nil, coreDB.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, second.ID, history[0].ProjectID, "global history remains ordered and paginated")

	projectHistory, err := repository.GetPolicyGuardrailEvaluationHistory(&first.ID, coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, projectHistory, 1)
	assert.Equal(t, first.ID, projectHistory[0].ProjectID)
}

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

func TestPolicyGuardrailStoreClaimsBatchWithOneSnapshotAndAtomicReplay(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	actor, err := store.CreateUserWithoutPassword(coreDB.User{Username: "policy-batch", Name: "Policy Batch", Email: "policy-batch@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(coreDB.Project{Name: "policy batch project"})
	require.NoError(t, err)
	policyGuardrailPublishEmptyBatchPolicies(t, repository, actor.ID, project.ID)

	requests := []pro_interfaces.PolicyGuardrailAdmissionRequest{
		policyGuardrailBatchRequest(project.ID, actor.ID, "root", "root"),
		policyGuardrailBatchRequest(project.ID, actor.ID, "node-1", "node-1"),
	}
	var snapshotTime time.Time
	var snapshotRevisions []coreDB.PolicyGuardrailRevision
	evaluate := func(revisions []coreDB.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
		if snapshotTime.IsZero() {
			snapshotTime = input.EvaluatedAt
			snapshotRevisions = make([]coreDB.PolicyGuardrailRevision, len(revisions))
			copy(snapshotRevisions, revisions)
		} else {
			require.True(t, snapshotTime.Equal(input.EvaluatedAt), "every batch input uses the one database timestamp")
			require.Equal(t, snapshotRevisions, revisions, "every batch input uses the one active revision set")
		}
		fingerprint, fingerprintErr := pro_interfaces.FingerprintPolicyGuardrailInput(input)
		if fingerprintErr != nil {
			return pro_interfaces.PolicyGuardrailEvaluation{}, fingerprintErr
		}
		return pro_interfaces.PolicyGuardrailEvaluation{Revisions: policyGuardrailBatchRevisionRefs(revisions), Allowed: true, InputFingerprint: fingerprint, EvaluatedAt: input.EvaluatedAt}, nil
	}

	claims, err := repository.ClaimPolicyGuardrailEvaluations(requests, evaluate)
	require.NoError(t, err)
	require.Len(t, claims, 2)
	assert.True(t, claims[0].Inserted)
	assert.True(t, claims[1].Inserted)
	assert.Equal(t, "root", claims[0].Record.DecisionKey)
	assert.Equal(t, "node-1", claims[1].Record.DecisionKey)
	assert.True(t, claims[0].Evaluation.EvaluatedAt.Equal(claims[1].Evaluation.EvaluatedAt))
	require.Len(t, snapshotRevisions, 2)
	assert.Equal(t, coreDB.PolicyGuardrailScopeGlobal, snapshotRevisions[0].Scope)
	assert.Equal(t, coreDB.PolicyGuardrailScopeProject, snapshotRevisions[1].Scope)

	replay, err := repository.ClaimPolicyGuardrailEvaluations(requests, evaluate)
	require.NoError(t, err)
	require.Len(t, replay, 2)
	assert.False(t, replay[0].Inserted)
	assert.False(t, replay[1].Inserted)
	assert.Equal(t, claims[0].Record.ID, replay[0].Record.ID)
	assert.Equal(t, claims[1].Record.ID, replay[1].Record.ID)
	projectID := policyGuardrailIntPointer(project.ID)
	draft, err := repository.GetPolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeProject, projectID)
	require.NoError(t, err)
	draft, err = repository.SavePolicyGuardrailDraft(pro_interfaces.PolicyGuardrailScopeProject, projectID, emptyPolicyGuardrailYAML, draft.Revision, actor.ID)
	require.NoError(t, err)
	_, err = repository.PublishPolicyGuardrailRevision(pro_interfaces.PolicyGuardrailScopeProject, projectID, draft.Revision, actor.ID, emptyPolicyGuardrailYAML, `{"version":1,"rules":[]}`, "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", 1)
	require.NoError(t, err)
	_, err = repository.ClaimPolicyGuardrailEvaluations(requests, evaluate)
	require.ErrorIs(t, err, coreDB.ErrInvalidOperation, "a replay cannot reuse an outdated active revision set")

	mixed := append(append([]pro_interfaces.PolicyGuardrailAdmissionRequest(nil), requests[:1]...), policyGuardrailBatchRequest(project.ID, actor.ID, "node-2", "node-2"))
	_, err = repository.ClaimPolicyGuardrailEvaluations(mixed, evaluate)
	require.ErrorIs(t, err, coreDB.ErrInvalidOperation, "mixed replay/new batches are rejected before any insert")
	history, err := repository.GetPolicyGuardrailEvaluationHistory(policyGuardrailIntPointer(project.ID), coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Len(t, history, 2)
}

func TestPolicyGuardrailStoreRejectsInvalidBatchWithoutPartialWrites(t *testing.T) {
	store := coreSQL.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewPolicyGuardrailStore(store.GetConnection())
	actor, err := store.CreateUserWithoutPassword(coreDB.User{Username: "policy-batch-invalid", Name: "Policy Batch Invalid", Email: "policy-batch-invalid@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(coreDB.Project{Name: "policy batch invalid project"})
	require.NoError(t, err)
	requests := []pro_interfaces.PolicyGuardrailAdmissionRequest{
		policyGuardrailBatchRequest(project.ID, actor.ID, "first", "first"),
		policyGuardrailBatchRequest(project.ID, actor.ID, "corrupt", "corrupt"),
	}
	evaluate := func(_ []coreDB.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
		fingerprint, fingerprintErr := pro_interfaces.FingerprintPolicyGuardrailInput(input)
		if fingerprintErr != nil {
			return pro_interfaces.PolicyGuardrailEvaluation{}, fingerprintErr
		}
		evaluation := pro_interfaces.PolicyGuardrailEvaluation{Allowed: true, InputFingerprint: fingerprint, EvaluatedAt: input.EvaluatedAt}
		if input.Template.Source == "corrupt" {
			evaluation.InputFingerprint = "sha256:corrupt"
		}
		return evaluation, nil
	}
	_, err = repository.ClaimPolicyGuardrailEvaluations(requests, evaluate)
	require.ErrorIs(t, err, coreDB.ErrInvalidOperation)
	history, err := repository.GetPolicyGuardrailEvaluationHistory(policyGuardrailIntPointer(project.ID), coreDB.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, history)

	duplicate := append([]pro_interfaces.PolicyGuardrailAdmissionRequest(nil), requests...)
	duplicate[1].DecisionKey = duplicate[0].DecisionKey
	duplicate[1].Input.Template.Source = duplicate[0].Input.Template.Source
	_, err = repository.ClaimPolicyGuardrailEvaluations(duplicate, evaluate)
	require.ErrorIs(t, err, coreDB.ErrInvalidOperation)

	overLimit := make([]pro_interfaces.PolicyGuardrailAdmissionRequest, pro_interfaces.MaxPolicyGuardrailAdmissionBatch+1)
	_, err = repository.ClaimPolicyGuardrailEvaluations(overLimit, evaluate)
	require.ErrorIs(t, err, coreDB.ErrInvalidOperation)
}

func policyGuardrailBatchRequest(projectID, actorID int, decisionKey, source string) pro_interfaces.PolicyGuardrailAdmissionRequest {
	return pro_interfaces.PolicyGuardrailAdmissionRequest{
		DecisionKey: decisionKey,
		Source:      "workflow",
		ActorUserID: policyGuardrailIntPointer(actorID),
		Input: pro_interfaces.PolicyGuardrailEvaluationInput{
			ProjectID:   projectID,
			Intent:      pro_interfaces.ExecutionPreflightTask,
			EvaluatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			Template:    &pro_interfaces.PolicyGuardrailTemplateMetadata{ID: 1, Application: "ansible", Source: source},
			Executor:    pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"},
		},
	}
}

func policyGuardrailPublishEmptyBatchPolicies(t *testing.T, repository *PolicyGuardrailStore, actorID, projectID int) {
	t.Helper()
	for _, policy := range []struct {
		scope       pro_interfaces.PolicyGuardrailScope
		projectID   *int
		fingerprint string
	}{
		{scope: pro_interfaces.PolicyGuardrailScopeGlobal, fingerprint: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		{scope: pro_interfaces.PolicyGuardrailScopeProject, projectID: policyGuardrailIntPointer(projectID), fingerprint: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
	} {
		draft, err := repository.GetPolicyGuardrailDraft(policy.scope, policy.projectID)
		require.NoError(t, err)
		draft, err = repository.SavePolicyGuardrailDraft(policy.scope, policy.projectID, emptyPolicyGuardrailYAML, draft.Revision, actorID)
		require.NoError(t, err)
		_, err = repository.PublishPolicyGuardrailRevision(policy.scope, policy.projectID, draft.Revision, actorID, emptyPolicyGuardrailYAML, `{"version":1,"rules":[]}`, policy.fingerprint, 1)
		require.NoError(t, err)
	}
}

func policyGuardrailBatchRevisionRefs(revisions []coreDB.PolicyGuardrailRevision) []pro_interfaces.PolicyGuardrailRevisionRef {
	refs := make([]pro_interfaces.PolicyGuardrailRevisionRef, len(revisions))
	for index, revision := range revisions {
		refs[index] = pro_interfaces.PolicyGuardrailRevisionRef{Scope: pro_interfaces.PolicyGuardrailScope(revision.Scope), Revision: revision.Revision, Fingerprint: revision.Fingerprint}
		if revision.ProjectID != nil {
			refs[index].ProjectID = policyGuardrailIntPointer(*revision.ProjectID)
		}
	}
	return refs
}

func policyGuardrailIntPointer(value int) *int { return &value }
