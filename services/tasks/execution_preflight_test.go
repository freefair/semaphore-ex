package tasks

import (
	"encoding/json"
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
