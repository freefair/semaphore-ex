package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTaskExecutionPreflightIsSideEffectFreeAndValueFree(t *testing.T) {
	store, pool, actor, template, environment := createTaskPreflightFixture(t)
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{
		TemplateID:  template.ID,
		Environment: `{"region":"eu-central-1","password":"not-secret-target"}`,
		Secret:      `{"password":"super-secret-preview-value"}`,
		Params:      db.MapStringAnyField{"limit": []string{"production"}},
	}, &actor, template.ProjectID, now)

	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.ExecutionPreflightTask, snapshot.Plan.Intent)
	assert.Equal(t, template.ID, snapshot.Plan.TemplateID)
	require.Len(t, snapshot.Plan.Placements, 1)
	assert.True(t, snapshot.Plan.Placements[0].Provisional)
	require.NotNil(t, snapshot.Plan.Placements[0].SelectedRunnerID)
	assert.Equal(t, pro_interfaces.ExecutionReasonSelected, snapshot.Plan.Placements[0].Decision)
	require.Len(t, snapshot.Plan.Findings, 1)
	assert.Equal(t, pro_interfaces.ExecutionFindingWarning, snapshot.Plan.Findings[0].Severity)
	assert.Equal(t, pro_interfaces.ExecutionReasonProvisionalPlacement, snapshot.Plan.Findings[0].Code)
	assert.NotEmpty(t, snapshot.Components[pro_interfaces.ExecutionChangeReference])

	encoded, err := json.Marshal(snapshot.Plan)
	require.NoError(t, err)
	for _, forbidden := range []string{
		"super-secret-preview-value", "not-secret-target", "environment-value", "runner-token", "https://runner.invalid/hook",
	} {
		assert.NotContains(t, string(encoded), forbidden)
	}
	assert.Contains(t, string(encoded), `"name":"password"`)
	assert.Contains(t, string(encoded), `"sensitive":true`)

	tasks, err := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, tasks, "preview must not create a task")

	previousReference := snapshot.Components[pro_interfaces.ExecutionChangeReference]
	environment.JSON = `{"stored":"changed-environment-value"}`
	require.NoError(t, store.UpdateEnvironment(environment))
	changed, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, now)
	require.NoError(t, err)
	assert.NotEqual(t, previousReference, changed.Components[pro_interfaces.ExecutionChangeReference])
	assert.Equal(t, []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeInput, pro_interfaces.ExecutionChangeReference}, componentChanges(snapshot.Components, changed.Components))
}

func TestBuildTaskExecutionPreflightReturnsBoundedNoCandidateFinding(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	pool.state = NewMemoryTaskStateStore()
	template.RunnerTags = []string{"unavailable"}
	require.NoError(t, store.UpdateTemplate(template))

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, now)

	require.NoError(t, err)
	require.Len(t, snapshot.Plan.Findings, 1)
	assert.Equal(t, pro_interfaces.ExecutionReasonNoCandidate, snapshot.Plan.Findings[0].Code)
	assert.Len(t, snapshot.Plan.Placements[0].Candidates, 1)
	assert.Contains(t, snapshot.Plan.Placements[0].Candidates[0].RejectedReasons, pro_interfaces.ExecutionReasonTagMismatch)
}

func TestBuildTaskExecutionPreflightHidesMissingReference(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	missing := 999_999

	snapshot, err := pool.BuildTaskExecutionPreflight(db.Task{TemplateID: template.ID, InventoryID: &missing}, &actor, template.ProjectID)

	require.NoError(t, err)
	require.Len(t, snapshot.Plan.Findings, 1)
	assert.Equal(t, pro_interfaces.ExecutionReasonHiddenReference, snapshot.Plan.Findings[0].Code)
	encoded, err := json.Marshal(snapshot.Plan)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(encoded)), "no rows")
	assert.NotContains(t, strings.ToLower(string(encoded)), "inventory #")

	issuer, issuerErr := NewExecutionPreflightReviewTokenIssuer(
		[]byte("01234567890123456789012345678901"), time.Minute,
	)
	require.NoError(t, issuerErr)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	missingTemplate, previewErr := pool.PreviewTaskExecution(
		db.Task{TemplateID: 999_998}, &actor, template.ProjectID,
	)
	require.NoError(t, previewErr)
	assert.NotEmpty(t, missingTemplate.ReviewToken)
	require.Len(t, missingTemplate.Findings, 1)
	assert.Equal(t, pro_interfaces.ExecutionReasonHiddenReference, missingTemplate.Findings[0].Code)
}

func TestAddTaskWithExecutionPreflightRejectsStaleDependencyBeforeInsert(t *testing.T) {
	store, pool, actor, template, environment := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	issuedAt := time.Now().UTC()
	issuer.now = func() time.Time { return issuedAt }
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID, Environment: `{"region":"eu-central-1"}`}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)

	environment.JSON = `{"stored":"changed-after-review"}`
	require.NoError(t, store.UpdateEnvironment(environment))
	_, err = pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})

	var stale *ExecutionPreflightStaleError
	require.ErrorAs(t, err, &stale)
	assert.Contains(t, stale.Changes, pro_interfaces.ExecutionChangeReference)
	assert.NotEmpty(t, stale.Preflight.ReviewToken)
	stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, listErr)
	assert.Empty(t, stored)
}

func TestAddTaskWithExecutionPreflightRejectsChangedCredentialState(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID, Secret: `{"password":"snapshot-must-not-contain-this"}`}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)

	repository, err := store.GetRepository(template.ProjectID, template.RepositoryID)
	require.NoError(t, err)
	key, err := store.GetAccessKey(template.ProjectID, repository.SSHKeyID)
	require.NoError(t, err)
	key.Name = "rotated repository key"
	require.NoError(t, store.UpdateAccessKey(key))

	_, err = pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})
	var stale *ExecutionPreflightStaleError
	require.ErrorAs(t, err, &stale)
	assert.Contains(t, stale.Changes, pro_interfaces.ExecutionChangeReference)
}

func TestAddTaskWithExecutionPreflightEnqueuesUnchangedReviewedPlan(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	issuedAt := time.Now().UTC()
	issuer.now = func() time.Time { return issuedAt }
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID, Environment: `{"region":"eu-central-1"}`}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)

	created, err := pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})

	require.NoError(t, err)
	assert.Positive(t, created.ID)
	stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, listErr)
	require.Len(t, stored, 1)
	assert.Equal(t, created.ID, stored[0].ID)
}

func TestReviewedTaskPersistsEffectiveExecutionSnapshotForHydration(t *testing.T) {
	store, pool, actor, template, environment := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID, Secret: `{"password":"snapshot-must-not-contain-this"}`}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)

	created, err := pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})
	require.NoError(t, err)
	require.NotNil(t, created.ExecutionSnapshotJSON)
	assert.NotContains(t, *created.ExecutionSnapshotJSON, "snapshot-must-not-contain-this")
	assert.NotContains(t, *created.ExecutionSnapshotJSON, preview.ReviewToken)

	template.Playbook = "changed-after-review.yml"
	require.NoError(t, store.UpdateTemplate(template))
	environment.JSON = `{"changed":"after-review"}`
	require.NoError(t, store.UpdateEnvironment(environment))

	restartedPool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	runner, err := restartedPool.HydrateTaskRunnerFromDB(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "deploy.yml", runner.Template.Playbook)
	assert.Equal(t, `{"stored":"environment-value"}`, runner.Environment.JSON)
}

func TestReviewedTaskUsesPersistedEnvironmentSecretBindings(t *testing.T) {
	store, pool, actor, template, environment := createTaskPreflightFixture(t)
	first, err := store.CreateAccessKey(db.AccessKey{
		ProjectID: &template.ProjectID, EnvironmentID: &environment.ID, Owner: db.AccessKeyVariable,
		Name: "var.PINNED", Type: db.AccessKeyNone,
	})
	require.NoError(t, err)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)
	created, err := pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})
	require.NoError(t, err)
	_, err = store.CreateAccessKey(db.AccessKey{
		ProjectID: &template.ProjectID, EnvironmentID: &environment.ID, Owner: db.AccessKeyVariable,
		Name: "var.LATE", Type: db.AccessKeyNone,
	})
	require.NoError(t, err)

	restartedPool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	runner, err := restartedPool.HydrateTaskRunnerFromDB(created.ID)
	require.NoError(t, err)
	require.Len(t, runner.Environment.Secrets, 1)
	assert.Equal(t, first.ID, runner.Environment.Secrets[0].ID)
	assert.Equal(t, "PINNED", runner.Environment.Secrets[0].Name)
}

func TestReviewedTaskRehydratesVaultAndInventorySnapshotReferences(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	vaultKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &template.ProjectID, Name: "vault", Type: db.AccessKeyNone})
	require.NoError(t, err)
	_, err = store.CreateTemplateVault(db.TemplateVault{
		ProjectID: template.ProjectID, TemplateID: template.ID, Type: db.TemplateVaultPassword, VaultKeyID: &vaultKey.ID,
	})
	require.NoError(t, err)
	inventoryRepository, err := store.CreateRepository(db.Repository{
		ProjectID: template.ProjectID, SSHKeyID: vaultKey.ID, Name: "inventory repo",
		GitURL: "https://example.invalid/inventory.git", GitBranch: "main",
	})
	require.NoError(t, err)
	inventory, err := store.GetInventory(template.ProjectID, *template.InventoryID)
	require.NoError(t, err)
	inventory.RepositoryID = &inventoryRepository.ID
	require.NoError(t, store.UpdateInventory(inventory))
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	task := db.Task{TemplateID: template.ID}
	preview, err := pool.PreviewTaskExecution(task, &actor, template.ProjectID)
	require.NoError(t, err)
	created, err := pool.AddTaskWithExecutionPreflight(task, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{
		Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
	})
	require.NoError(t, err)

	restartedPool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	runner, err := restartedPool.HydrateTaskRunnerFromDB(created.ID)
	require.NoError(t, err)
	require.Len(t, runner.Template.Vaults, 1)
	require.NotNil(t, runner.Template.Vaults[0].Vault)
	assert.Equal(t, vaultKey.ID, runner.Template.Vaults[0].Vault.ID)
	require.NotNil(t, runner.Inventory.Repository)
	assert.Equal(t, inventoryRepository.ID, runner.Inventory.Repository.ID)
	require.NotNil(t, runner.Inventory.SSHKeyID)
	assert.Equal(t, *runner.Inventory.SSHKeyID, runner.Inventory.SSHKey.ID)
}

func TestAddTaskWithExecutionPreflightPreservesHeaderlessLegacyQueueing(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	template.RunnerTags = []string{"currently-unavailable"}
	require.NoError(t, store.UpdateTemplate(template))

	created, err := pool.AddTaskWithExecutionPreflight(
		db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{},
	)

	require.NoError(t, err)
	assert.Positive(t, created.ID, "legacy clients must retain queue-and-wait behavior")
}

func TestAddTaskWithExecutionPreflightPolicyGuardrailDeniesHeaderlessStart(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	pool.ConfigurePolicyGuardrailAdmission(&executionPreflightPolicyGuardrailStub{
		effects: []pro_interfaces.PolicyGuardrailEffect{pro_interfaces.PolicyGuardrailEffectDeny},
	})

	_, plan, err := pool.AddTaskWithExecutionPreflightPlan(
		db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{},
	)

	var denied *ExecutionPreflightDeniedError
	require.ErrorAs(t, err, &denied)
	assert.Equal(t, plan, denied.Preflight)
	assert.Contains(t, policyGuardrailReasonCodes(plan.Findings), pro_interfaces.ExecutionReasonPolicyDenied)
	stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, listErr)
	assert.Empty(t, stored)
}

func TestAddTaskWithExecutionPreflightPolicyClaimFencesReviewedAllowAndWarn(t *testing.T) {
	for _, effect := range []pro_interfaces.PolicyGuardrailEffect{
		pro_interfaces.PolicyGuardrailEffectAllow,
		pro_interfaces.PolicyGuardrailEffectWarn,
	} {
		t.Run(string(effect), func(t *testing.T) {
			store, pool, actor, template, _ := createTaskPreflightFixture(t)
			issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
			require.NoError(t, err)
			pool.SetExecutionPreflightReviewTokenIssuer(issuer)
			policy := &executionPreflightPolicyGuardrailStub{effects: []pro_interfaces.PolicyGuardrailEffect{effect}}
			pool.ConfigurePolicyGuardrailAdmission(policy)
			var persisted db.Task
			pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(task db.Task, _ int) (db.Task, error) {
				persisted = task
				task.ID = 401
				return task, nil
			}}
			preview, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
			require.NoError(t, err)

			created, plan, err := pool.AddTaskWithExecutionPreflightPlan(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
				pro_interfaces.ExecutionPreflightReview{Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken},
			)

			require.NoError(t, err)
			assert.Equal(t, 401, created.ID)
			require.NotNil(t, persisted.PolicyGuardrailEvaluationID)
			require.NotNil(t, persisted.ExecutionSnapshotJSON)
			assert.Equal(t, 1, *persisted.PolicyGuardrailEvaluationID)
			require.Len(t, policy.claimRequests, 1)
			assert.Equal(t, "manual", policy.claimRequests[0].Source)
			assert.Equal(t, "manual", policy.claimRequests[0].Input.Template.Source)
			if effect == pro_interfaces.PolicyGuardrailEffectAllow {
				assert.Contains(t, policyGuardrailReasonCodes(plan.Findings), pro_interfaces.ExecutionReasonPolicyAllowed)
			} else {
				assert.Contains(t, policyGuardrailReasonCodes(plan.Findings), pro_interfaces.ExecutionReasonPolicyWarning)
			}
		})
	}
}

func TestAddTaskWithExecutionPreflightBindsPolicyBeforeDeploymentAdmission(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	policy := &executionPreflightPolicyGuardrailStub{}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	pool.ConfigureDeploymentWindowAdmission(executionPreflightDeploymentWindowAdmissionStub{
		claim: pro_interfaces.DeploymentWindowAdmissionClaim{Decision: executionPreflightDeploymentWindowDecision(
			template.ProjectID, actor.ID, pro_interfaces.DeploymentWindowDecisionAllowed,
		)},
	})
	var persisted db.Task
	pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(task db.Task, _ int) (db.Task, error) {
		persisted = task
		task.ID = 402
		return task, nil
	}}

	_, _, err := pool.AddTaskWithExecutionPreflightPlan(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{})

	require.NoError(t, err)
	require.NotNil(t, persisted.PolicyGuardrailEvaluationID)
	require.NotNil(t, persisted.DeploymentWindowDecisionID)
	require.NotNil(t, persisted.ExecutionSnapshotJSON, "policy-admitted headerless starts must not re-resolve mutable execution state")
	assert.Equal(t, 1, *persisted.PolicyGuardrailEvaluationID)
	assert.Equal(t, 97, *persisted.DeploymentWindowDecisionID)
}

func TestAddTaskWithExecutionPreflightPolicyFailuresCloseBeforeTaskPersistence(t *testing.T) {
	for name, policy := range map[string]*executionPreflightPolicyGuardrailStub{
		"evaluator": {err: errors.New("policy evaluator unavailable")},
		"claim":     {claimErr: errors.New("policy claim unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			store, pool, actor, template, _ := createTaskPreflightFixture(t)
			pool.ConfigurePolicyGuardrailAdmission(policy)
			_, _, err := pool.AddTaskWithExecutionPreflightPlan(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{})
			require.EqualError(t, err, "policy "+name+" unavailable")
			stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
			require.NoError(t, listErr)
			assert.Empty(t, stored)
		})
	}
}

func TestAddTaskWithExecutionPreflightPolicyClaimChangeMakesReviewedStartStale(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	policy := &executionPreflightPolicyGuardrailStub{revision: 1}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	preview, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)
	policy.revision = 2

	_, _, err = pool.AddTaskWithExecutionPreflightPlan(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken},
	)

	var stale *ExecutionPreflightStaleError
	require.ErrorAs(t, err, &stale)
	assert.Contains(t, stale.Changes, pro_interfaces.ExecutionChangePolicy)
	assert.Empty(t, policy.claimRequests, "a stale review must not create an admission claim")
}

func TestAddTaskWithExecutionPreflightPolicyDoesNotBypassReviewedNativeDenial(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	policy := &executionPreflightPolicyGuardrailStub{}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	template.RunnerTags = []string{"currently-unavailable"}
	require.NoError(t, store.UpdateTemplate(template))
	preview, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)

	_, _, err = pool.AddTaskWithExecutionPreflightPlan(db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken},
	)

	var denied *ExecutionPreflightDeniedError
	require.ErrorAs(t, err, &denied)
	assert.Empty(t, policy.claimRequests, "native reviewed denials must stop before a policy admission claim")
}

func TestAddTaskWithDeploymentWindowAdmissionPolicyUsesAutomaticSourceAndDecisionKey(t *testing.T) {
	for _, source := range []pro_interfaces.DeploymentWindowSource{
		pro_interfaces.DeploymentWindowSourceSchedule,
		pro_interfaces.DeploymentWindowSourceIntegration,
		pro_interfaces.DeploymentWindowSourceAutorun,
	} {
		t.Run(string(source), func(t *testing.T) {
			store, pool, _, template, _ := createTaskPreflightFixture(t)
			policy := &executionPreflightPolicyGuardrailStub{effects: []pro_interfaces.PolicyGuardrailEffect{pro_interfaces.PolicyGuardrailEffectWarn}}
			pool.ConfigurePolicyGuardrailAdmission(policy)
			var persisted db.Task
			pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(task db.Task, _ int) (db.Task, error) {
				persisted = task
				task.ID = 403
				return task, nil
			}}
			decisionKey := string(source) + "-decision"
			created, err := pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false,
				pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: template.ProjectID, DecisionKey: decisionKey, Source: source, TemplateID: &template.ID},
			)

			require.NoError(t, err)
			assert.Equal(t, 403, created.ID)
			require.NotNil(t, persisted.PolicyGuardrailEvaluationID)
			require.Len(t, policy.claimRequests, 1)
			assert.Equal(t, decisionKey, policy.claimRequests[0].DecisionKey)
			assert.Equal(t, string(source), policy.claimRequests[0].Source)
			assert.Equal(t, string(source), policy.claimRequests[0].Input.Template.Source)
		})
	}
}

func TestAutomaticTaskPolicyPreviewRebasesDatabaseTimestampBeforeClaim(t *testing.T) {
	_, pool, _, template, _ := createTaskPreflightFixture(t)
	databaseTime := time.Date(2026, 9, 3, 14, 30, 0, 0, time.UTC)
	policy := &executionPreflightPolicyGuardrailStub{evaluatedAt: databaseTime}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	pool.store = executionPreflightTaskStoreStub{Store: pool.store, createTask: func(task db.Task, _ int) (db.Task, error) {
		task.ID = 404
		return task, nil
	}}

	created, err := pool.AddTaskWithDeploymentWindowAdmission(
		db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false,
		pro_interfaces.DeploymentWindowAdmissionRequest{
			ProjectID: template.ProjectID, DecisionKey: "database-time-preview",
			Source: pro_interfaces.DeploymentWindowSourceIntegration, TemplateID: &template.ID,
		},
	)

	require.NoError(t, err)
	assert.Equal(t, 404, created.ID)
	require.Len(t, policy.claimRequests, 1)
	assert.NotEqual(t, databaseTime, policy.claimRequests[0].Input.EvaluatedAt)
}

func TestAddTaskWithDeploymentWindowAdmissionPolicyPersistsClaimedAutomaticDescriptor(t *testing.T) {
	store, pool, _, template, environment := createTaskPreflightFixture(t)
	policy := &executionPreflightPolicyGuardrailStub{}
	policy.onClaim = func(pro_interfaces.PolicyGuardrailAdmissionRequest) {
		environment.JSON = `{"stored":"changed-after-policy-claim"}`
		require.NoError(t, store.UpdateEnvironment(environment))
	}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	var persisted db.Task
	pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(task db.Task, _ int) (db.Task, error) {
		persisted = task
		task.ID = 404
		return task, nil
	}}

	_, err := pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false,
		pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: template.ProjectID, DecisionKey: "integration-descriptor", Source: pro_interfaces.DeploymentWindowSourceIntegration, TemplateID: &template.ID},
	)

	require.NoError(t, err)
	require.NotNil(t, persisted.ExecutionSnapshotJSON)
	snapshot, decodeErr := db.DecodeTaskExecutionSnapshot(*persisted.ExecutionSnapshotJSON, template.ProjectID, template.ID)
	require.NoError(t, decodeErr)
	require.Len(t, snapshot.Environments, 1)
	assert.Equal(t, `{"stored":"environment-value"}`, snapshot.Environments[0].JSON)
}

func TestAddTaskWithDeploymentWindowAdmissionPolicyDenyAndClaimFailureCloseWithoutWindow(t *testing.T) {
	for name, policy := range map[string]*executionPreflightPolicyGuardrailStub{
		"deny":  {effects: []pro_interfaces.PolicyGuardrailEffect{pro_interfaces.PolicyGuardrailEffectDeny}},
		"claim": {claimErr: errors.New("automatic policy claim unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			store, pool, _, template, _ := createTaskPreflightFixture(t)
			pool.ConfigurePolicyGuardrailAdmission(policy)
			_, err := pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false,
				pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: template.ProjectID, DecisionKey: "automatic-" + name, Source: pro_interfaces.DeploymentWindowSourceIntegration, TemplateID: &template.ID},
			)
			if name == "deny" {
				var denied *pro_interfaces.PolicyGuardrailDeniedError
				require.ErrorAs(t, err, &denied)
				assert.False(t, denied.Evaluation.Allowed)
				assert.Len(t, denied.Evaluation.Revisions, 1)
				require.Len(t, denied.Evaluation.Findings, 1)
				assert.Equal(t, pro_interfaces.PolicyGuardrailEffectDeny, denied.Evaluation.Findings[0].Effect)
			} else {
				require.EqualError(t, err, "automatic policy claim unavailable")
			}
			stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
			require.NoError(t, listErr)
			assert.Empty(t, stored)
		})
	}
}

func TestAddTaskWithDeploymentWindowAdmissionPolicyReplayReturnsOnlyMatchingTask(t *testing.T) {
	store, pool, _, template, _ := createTaskPreflightFixture(t)
	existing, err := store.CreateTask(db.Task{ProjectID: template.ProjectID, TemplateID: template.ID, Status: "waiting", Created: time.Now().UTC()}, 0)
	require.NoError(t, err)
	policy := &executionPreflightPolicyGuardrailStub{claimRecord: &db.PolicyGuardrailEvaluationRecord{
		ID: 1, ProjectID: template.ProjectID, DecisionKey: "schedule-replay", Intent: "task", Source: "schedule",
		TemplateID: &template.ID, TaskID: &existing.ID, Decision: db.PolicyGuardrailDecisionAllow,
	}}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	request := pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: template.ProjectID, DecisionKey: "schedule-replay", Source: pro_interfaces.DeploymentWindowSourceSchedule, TemplateID: &template.ID}

	replayed, err := pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false, request)

	require.NoError(t, err)
	assert.Equal(t, existing.ID, replayed.ID)

	integrationID := 77
	_, err = pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID, IntegrationID: &integrationID}, nil, "", template.ProjectID, false,
		pro_interfaces.DeploymentWindowAdmissionRequest{ProjectID: template.ProjectID, DecisionKey: "schedule-replay", Source: pro_interfaces.DeploymentWindowSourceSchedule, TemplateID: &template.ID},
	)
	require.EqualError(t, err, "policy guardrail evaluation is bound to a different task")
}

func TestAddTaskWithDeploymentWindowAdmissionPreservesCommunityPathWithoutAdmissionServices(t *testing.T) {
	_, pool, _, template, _ := createTaskPreflightFixture(t)

	created, err := pool.AddTaskWithDeploymentWindowAdmission(db.Task{TemplateID: template.ID}, nil, "", template.ProjectID, false,
		pro_interfaces.DeploymentWindowAdmissionRequest{},
	)

	require.NoError(t, err)
	assert.Positive(t, created.ID)
}

func TestAddTaskWithExecutionPreflightBlocksReviewedNoCandidatePlan(t *testing.T) {
	store, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	template.RunnerTags = []string{"currently-unavailable"}
	require.NoError(t, store.UpdateTemplate(template))
	preview, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)

	_, err = pool.AddTaskWithExecutionPreflight(
		db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{
			Fingerprint: preview.Fingerprint, ReviewToken: preview.ReviewToken,
		},
	)

	var denied *ExecutionPreflightDeniedError
	require.ErrorAs(t, err, &denied)
	stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, listErr)
	assert.Empty(t, stored)
}

func createTaskPreflightFixture(t *testing.T) (*sql.SqlDb, TaskPool, db.User, db.Template, db.Environment) {
	t.Helper()
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "preflight"})
	require.NoError(t, err)
	actor, err := store.CreateUserWithoutPassword(db.User{Username: "reviewer", Name: "Reviewer", Email: "reviewer@example.invalid"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repository key", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repository",
		GitURL: "https://example.invalid/private.git", GitBranch: "main",
	})
	require.NoError(t, err)
	envValue := `{"DEPLOY_TOKEN":"environment-value"}`
	environment, err := store.CreateEnvironment(db.Environment{
		ProjectID: project.ID, Name: "production", JSON: `{"stored":"environment-value"}`, ENV: &envValue,
	})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: project.ID, Name: "production", Type: db.InventoryStatic,
		Inventory: "host.example.invalid", SSHKeyID: &key.ID,
	})
	require.NoError(t, err)
	runnerTag := "linux"
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, InventoryID: &inventory.ID, EnvironmentIDs: []int{environment.ID},
		Name: "deploy", App: db.AppAnsible, Playbook: "deploy.yml", RunnerTags: []string{runnerTag},
		TaskParams: db.MapStringAnyField{"allow_override_inventory": true},
		SurveyVars: []db.SurveyVar{{Name: "region", Type: db.SurveyVarStr}, {Name: "password", Type: db.SurveyVarType("secret")}},
	})
	require.NoError(t, err)
	touched := time.Date(2026, 9, 2, 15, 59, 30, 0, time.UTC)
	runner, err := store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "runner-a", Active: true, Token: "runner-token",
		Tags: []string{runnerTag}, Touched: &touched, ExecutorType: db.RunnerExecutorLocal,
		Webhook: "https://runner.invalid/hook",
	})
	require.NoError(t, err)
	runner.Touched = &touched
	require.NoError(t, store.UpdateRunner(runner))
	pool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	pool.register = make(chan *TaskRunner, 2)
	return store, pool, actor, template, environment
}

type deploymentWindowAdmissionStub struct{}

func (deploymentWindowAdmissionStub) Claim(pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	return pro_interfaces.DeploymentWindowAdmissionClaim{}, nil
}

func TestTaskPoolConfiguredAdmissionRejectsDirectPersistenceWithoutDecision(t *testing.T) {
	pool := TaskPool{deploymentWindowAdmission: deploymentWindowAdmissionStub{}}
	_, err := pool.addTask(db.Task{TemplateID: 1}, nil, nil, "", 1, false, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment window decision is required")
}

type executionPreflightDeploymentWindowAdmissionStub struct {
	claim pro_interfaces.DeploymentWindowAdmissionClaim
}

func (s executionPreflightDeploymentWindowAdmissionStub) Claim(pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	return s.claim, nil
}

type executionPreflightDeploymentWindowAuditStub struct {
	events []pro_interfaces.AuditEvent
}

func (s *executionPreflightDeploymentWindowAuditStub) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

type executionPreflightTaskStoreStub struct {
	db.Store
	createTask func(db.Task, int) (db.Task, error)
}

func (s executionPreflightTaskStoreStub) CreateTask(task db.Task, maxTasks int) (db.Task, error) {
	return s.createTask(task, maxTasks)
}

func TestManualExecutionPreflightDeploymentWindowAuditsSuccessfulAdmissionAndBinding(t *testing.T) {
	for _, state := range []pro_interfaces.DeploymentWindowDecisionState{
		pro_interfaces.DeploymentWindowDecisionAllowed,
		pro_interfaces.DeploymentWindowDecisionOverridden,
	} {
		t.Run(string(state), func(t *testing.T) {
			store, pool, actor, template, _ := createTaskPreflightFixture(t)
			audit := &executionPreflightDeploymentWindowAuditStub{}
			pool.ConfigureDeploymentWindowAudit(audit)
			pool.ConfigureDeploymentWindowAdmission(executionPreflightDeploymentWindowAdmissionStub{
				claim: pro_interfaces.DeploymentWindowAdmissionClaim{
					Decision: executionPreflightDeploymentWindowDecision(template.ProjectID, actor.ID, state), Inserted: true,
				},
			})
			pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(task db.Task, _ int) (db.Task, error) {
				task.ID = 401
				return task, nil
			}}

			created, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
				db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
				pro_interfaces.ExecutionPreflightReview{}, nil,
			)

			require.NoError(t, err)
			assert.Equal(t, 401, created.ID)
			require.Len(t, audit.events, 2)
			assert.Equal(t, pro_interfaces.AuditActionDeploymentWindowAdmission, audit.events[0].Action)
			assert.Equal(t, pro_interfaces.AuditActionDeploymentWindowBinding, audit.events[1].Action)
			assert.Equal(t, 401, audit.events[1].DeploymentWindowProvenance.TaskID)
		})
	}
}

func TestManualExecutionPreflightDeploymentWindowBlockedCarriesNewAuditClaim(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	audit := &executionPreflightDeploymentWindowAuditStub{}
	decision := executionPreflightDeploymentWindowDecision(template.ProjectID, actor.ID, pro_interfaces.DeploymentWindowDecisionBlocked)
	pool.ConfigureDeploymentWindowAudit(audit)
	pool.ConfigureDeploymentWindowAdmission(executionPreflightDeploymentWindowAdmissionStub{
		claim: pro_interfaces.DeploymentWindowAdmissionClaim{Decision: decision, Inserted: true},
	})

	_, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
		db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
		pro_interfaces.ExecutionPreflightReview{}, nil,
	)

	var blocked *pro_interfaces.DeploymentWindowBlockedError
	require.ErrorAs(t, err, &blocked)
	assert.Equal(t, decision.ID, blocked.DecisionID)
	assert.True(t, blocked.AuditInserted)
	require.NotNil(t, blocked.AuditDecision)
	assert.Equal(t, decision.ID, blocked.AuditDecision.ID)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionDeploymentWindowAdmission, audit.events[0].Action)
}

func TestManualExecutionPreflightDeploymentWindowSkipsBindingAfterPersistenceFailureAndReplayAdmission(t *testing.T) {
	t.Run("persistence failure", func(t *testing.T) {
		store, pool, actor, template, _ := createTaskPreflightFixture(t)
		audit := &executionPreflightDeploymentWindowAuditStub{}
		pool.ConfigureDeploymentWindowAudit(audit)
		pool.ConfigureDeploymentWindowAdmission(executionPreflightDeploymentWindowAdmissionStub{
			claim: pro_interfaces.DeploymentWindowAdmissionClaim{
				Decision: executionPreflightDeploymentWindowDecision(template.ProjectID, actor.ID, pro_interfaces.DeploymentWindowDecisionAllowed), Inserted: true,
			},
		})
		pool.store = executionPreflightTaskStoreStub{Store: store, createTask: func(db.Task, int) (db.Task, error) {
			return db.Task{}, errors.New("persistence failed")
		}}

		_, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
			db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
			pro_interfaces.ExecutionPreflightReview{}, nil,
		)

		require.EqualError(t, err, "persistence failed")
		require.Len(t, audit.events, 1)
		assert.Equal(t, pro_interfaces.AuditActionDeploymentWindowAdmission, audit.events[0].Action)
	})

	t.Run("blocked replay", func(t *testing.T) {
		_, pool, actor, template, _ := createTaskPreflightFixture(t)
		audit := &executionPreflightDeploymentWindowAuditStub{}
		decision := executionPreflightDeploymentWindowDecision(template.ProjectID, actor.ID, pro_interfaces.DeploymentWindowDecisionBlocked)
		pool.ConfigureDeploymentWindowAudit(audit)
		pool.ConfigureDeploymentWindowAdmission(executionPreflightDeploymentWindowAdmissionStub{
			claim: pro_interfaces.DeploymentWindowAdmissionClaim{Decision: decision},
		})

		_, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
			db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false,
			pro_interfaces.ExecutionPreflightReview{}, nil,
		)

		var blocked *pro_interfaces.DeploymentWindowBlockedError
		require.ErrorAs(t, err, &blocked)
		assert.False(t, blocked.AuditInserted)
		assert.Equal(t, decision.ID, blocked.AuditDecision.ID)
		assert.Empty(t, audit.events, "replayed decisions must not multiply audit events")
	})
}

func executionPreflightDeploymentWindowDecision(projectID, actorID int, state pro_interfaces.DeploymentWindowDecisionState) db.DeploymentWindowDecisionRecord {
	reason := pro_interfaces.DeploymentWindowReasonDefaultAllow
	var overrideActorID *int
	var overrideCategory *string
	if state == pro_interfaces.DeploymentWindowDecisionBlocked {
		reason = pro_interfaces.DeploymentWindowReasonFreezeActive
	}
	if state == pro_interfaces.DeploymentWindowDecisionOverridden {
		reason = pro_interfaces.DeploymentWindowReasonOverride
		category := string(pro_interfaces.DeploymentWindowOverrideIncident)
		overrideActorID = &actorID
		overrideCategory = &category
	}
	return db.DeploymentWindowDecisionRecord{
		ID: 97, ProjectID: projectID, ActorUserID: &actorID,
		Source: string(pro_interfaces.DeploymentWindowSourceManual), Origin: string(pro_interfaces.DeploymentWindowOriginUser),
		PolicyRevision: 1, EffectiveTimezone: "UTC", EvaluatedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		State: string(state), Reason: string(reason), MatchedRulesJSON: "[]",
		OverrideActorID: overrideActorID, OverrideCategory: overrideCategory,
	}
}

func TestPreviewTaskExecutionEnrichesPolicyGuardrailWithoutLeakingValues(t *testing.T) {
	store, pool, actor, template, environment := createTaskPreflightFixture(t)
	secret := "task-secret-value-sentinel"
	name := "environment-name-sentinel"
	path := "provider/path-sentinel"
	environment.Name = name
	environment.JSON = `{"value":"environment-value-sentinel"}`
	require.NoError(t, store.UpdateEnvironment(environment))
	template.SurveyVars = append(template.SurveyVars, db.SurveyVar{Name: "survey-key-sentinel", Type: db.SurveyVarStr})
	require.NoError(t, store.UpdateTemplate(template))
	credential, err := store.CreateAccessKey(db.AccessKey{
		ProjectID: &template.ProjectID, EnvironmentID: &environment.ID, Owner: db.AccessKeyEnvironment,
		Name: "credential-name-sentinel", Type: db.AccessKeyString, Secret: &secret,
		SourceStorageKey: &path,
	})
	require.NoError(t, err)

	evaluator := &executionPreflightPolicyGuardrailStub{effects: []pro_interfaces.PolicyGuardrailEffect{
		pro_interfaces.PolicyGuardrailEffectAllow,
		pro_interfaces.PolicyGuardrailEffectWarn,
		pro_interfaces.PolicyGuardrailEffectDeny,
	}}
	pool.ConfigurePolicyGuardrailAdmission(evaluator)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	plan, err := pool.PreviewTaskExecution(db.Task{
		TemplateID:                   template.ID,
		Environment:                  `{"environment-value-sentinel":"task-value-sentinel"}`,
		Secret:                       `{"secret-name-sentinel":"task-secret-value-sentinel"}`,
		Params:                       db.MapStringAnyField{"argument-key-sentinel": "task-value-sentinel"},
		GlobalCredentialBindingsJSON: `{"secret-name-sentinel":44}`,
	}, &actor, template.ProjectID)

	require.NoError(t, err)
	require.NotNil(t, evaluator.input)
	assert.Len(t, plan.PolicyRevisions, 1)
	assert.ElementsMatch(t, []pro_interfaces.ExecutionPreflightReasonCode{
		pro_interfaces.ExecutionReasonPolicyAllowed,
		pro_interfaces.ExecutionReasonPolicyWarning,
		pro_interfaces.ExecutionReasonPolicyDenied,
	}, policyGuardrailReasonCodes(plan.Findings))
	assert.Equal(t, "manual", evaluator.input.Template.Source)
	assert.Zero(t, evaluator.input.Runner.SelectedID)
	assert.Empty(t, evaluator.input.Runner.SelectedScope)
	assert.Empty(t, evaluator.input.Runner.SelectedExecutor)
	assert.Equal(t, "unknown", evaluator.input.Executor.Type)
	assert.Equal(t, 1, evaluator.input.Template.ArgumentKeyCount)
	assert.Equal(t, 2, evaluator.input.Template.InputKeyCount)
	assert.Contains(t, evaluator.input.EnvironmentIDs, environment.ID)
	assert.Contains(t, evaluator.input.Credentials, pro_interfaces.PolicyGuardrailCredentialReferenceMetadata{
		ID: credential.ID, Scope: "local", BindingTarget: "environment",
	})

	encoded, marshalErr := json.Marshal(evaluator.input)
	require.NoError(t, marshalErr)
	for _, forbidden := range []string{
		secret, name, path, "credential-name-sentinel", "environment-value-sentinel",
		"task-value-sentinel", "secret-name-sentinel", "argument-key-sentinel", "survey-key-sentinel",
	} {
		assert.NotContains(t, string(encoded), forbidden)
	}

	workflowSnapshot, workflowErr := pool.BuildWorkflowTaskExecutionPreflight(
		db.Task{TemplateID: template.ID}, template, &actor, template.ProjectID,
		time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC),
	)
	require.NoError(t, workflowErr)
	assert.Len(t, workflowSnapshot.Plan.PolicyRevisions, 1)
	assert.Contains(t, policyGuardrailReasonCodes(workflowSnapshot.Plan.Findings), pro_interfaces.ExecutionReasonPolicyDenied)
}

func TestPreviewTaskExecutionPolicyGuardrailRevisionChangesFingerprintAndFailuresClose(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	allowed := &executionPreflightPolicyGuardrailStub{revision: 1}
	pool.ConfigurePolicyGuardrailAdmission(allowed)
	first, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)

	pool.ConfigurePolicyGuardrailAdmission(&executionPreflightPolicyGuardrailStub{revision: 2})
	second, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)
	assert.NotEqual(t, first.Fingerprint, second.Fingerprint)

	pool.ConfigurePolicyGuardrailAdmission(&executionPreflightPolicyGuardrailStub{err: errors.New("evaluator unavailable")})
	_, err = pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.EqualError(t, err, "evaluator unavailable")

	pool.ConfigurePolicyGuardrailAdmission(nil)
	withoutPolicy, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, withoutPolicy.PolicyRevisions)
	assert.NotContains(t, policyGuardrailReasonCodes(withoutPolicy.Findings), pro_interfaces.ExecutionReasonPolicyAllowed)
}

func TestPreviewTaskExecutionRebasesPolicyInputToDatabaseTimestamp(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	issuer, err := NewExecutionPreflightReviewTokenIssuer([]byte("01234567890123456789012345678901"), time.Minute)
	require.NoError(t, err)
	pool.SetExecutionPreflightReviewTokenIssuer(issuer)
	databaseTime := time.Date(2026, 9, 3, 14, 30, 0, 0, time.UTC)
	policy := &executionPreflightPolicyGuardrailStub{evaluatedAt: databaseTime}
	pool.ConfigurePolicyGuardrailAdmission(policy)

	plan, err := pool.PreviewTaskExecution(db.Task{TemplateID: template.ID}, &actor, template.ProjectID)

	require.NoError(t, err)
	assert.NotEmpty(t, plan.PolicyRevisions)
	require.NotNil(t, policy.input)
	assert.NotEqual(t, databaseTime, policy.input.EvaluatedAt)
}

type executionPreflightPolicyGuardrailStub struct {
	input         *pro_interfaces.PolicyGuardrailEvaluationInput
	claimRequests []pro_interfaces.PolicyGuardrailAdmissionRequest
	revision      int
	evaluatedAt   time.Time
	effects       []pro_interfaces.PolicyGuardrailEffect
	err           error
	claimErr      error
	claimRecord   *db.PolicyGuardrailEvaluationRecord
	onClaim       func(pro_interfaces.PolicyGuardrailAdmissionRequest)
}

func (s *executionPreflightPolicyGuardrailStub) EvaluatePolicyGuardrails(input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if s.err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, s.err
	}
	copy := input
	s.input = &copy
	fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	revision := s.revision
	if revision == 0 {
		revision = 1
	}
	effects := s.effects
	if len(effects) == 0 {
		effects = []pro_interfaces.PolicyGuardrailEffect{pro_interfaces.PolicyGuardrailEffectAllow}
	}
	revisions := []pro_interfaces.PolicyGuardrailRevisionRef{{
		Scope: pro_interfaces.PolicyGuardrailScopeGlobal, Revision: revision,
		Fingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	findings := make([]pro_interfaces.PolicyGuardrailFinding, 0, len(effects))
	allowed := true
	for index, effect := range effects {
		if effect == pro_interfaces.PolicyGuardrailEffectDeny {
			allowed = false
		}
		findings = append(findings, pro_interfaces.PolicyGuardrailFinding{
			Scope: pro_interfaces.PolicyGuardrailScopeGlobal, Revision: revision,
			RuleID: "rule-" + strconv.Itoa(index), Effect: effect,
			Severity: pro_interfaces.PolicyGuardrailSeverityLow, Message: "Policy result.",
		})
	}
	evaluatedAt := input.EvaluatedAt
	if !s.evaluatedAt.IsZero() {
		evaluatedAt = s.evaluatedAt
	}
	return pro_interfaces.PolicyGuardrailEvaluation{
		Revisions: revisions, Findings: findings, Allowed: allowed,
		InputFingerprint: fingerprint, EvaluatedAt: evaluatedAt,
	}, nil
}

func (s *executionPreflightPolicyGuardrailStub) PreviewPolicyGuardrailEvaluations(inputs []pro_interfaces.PolicyGuardrailEvaluationInput) ([]pro_interfaces.PolicyGuardrailEvaluation, error) {
	evaluations := make([]pro_interfaces.PolicyGuardrailEvaluation, 0, len(inputs))
	for _, input := range inputs {
		evaluation, err := s.EvaluatePolicyGuardrails(input)
		if err != nil {
			return nil, err
		}
		evaluations = append(evaluations, evaluation)
	}
	return evaluations, nil
}

func (s *executionPreflightPolicyGuardrailStub) ClaimPolicyGuardrailEvaluation(request pro_interfaces.PolicyGuardrailAdmissionRequest) (pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	s.claimRequests = append(s.claimRequests, request)
	if s.onClaim != nil {
		s.onClaim(request)
	}
	if s.claimErr != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, s.claimErr
	}
	evaluation, err := s.EvaluatePolicyGuardrails(request.Input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, err
	}
	record := db.PolicyGuardrailEvaluationRecord{
		ID: 1, ProjectID: request.Input.ProjectID, DecisionKey: request.DecisionKey,
		Intent: string(request.Input.Intent), Source: request.Source, TemplateID: &request.Input.Template.ID,
		ActorUserID: request.ActorUserID, Decision: db.PolicyGuardrailDecisionAllow,
		EvaluatedAt: evaluation.EvaluatedAt,
	}
	if !evaluation.Allowed {
		record.Decision = db.PolicyGuardrailDecisionDeny
	}
	if s.claimRecord != nil {
		record = *s.claimRecord
	}
	return pro_interfaces.PolicyGuardrailEvaluationClaim{Record: record, Evaluation: evaluation, Inserted: s.claimRecord == nil}, nil
}

func (s *executionPreflightPolicyGuardrailStub) ClaimPolicyGuardrailEvaluations(requests []pro_interfaces.PolicyGuardrailAdmissionRequest) ([]pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	claims := make([]pro_interfaces.PolicyGuardrailEvaluationClaim, 0, len(requests))
	for _, request := range requests {
		claim, err := s.ClaimPolicyGuardrailEvaluation(request)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func policyGuardrailReasonCodes(findings []pro_interfaces.ExecutionPreflightFinding) []pro_interfaces.ExecutionPreflightReasonCode {
	result := make([]pro_interfaces.ExecutionPreflightReasonCode, 0, len(findings))
	for _, finding := range findings {
		if finding.PolicyEffect != "" {
			result = append(result, finding.Code)
		}
	}
	return result
}
