package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// integrationBranchTaskStore captures the task only at the store's persistence
// boundary. Returning an error keeps the test synchronous and prevents task
// queue goroutines from outliving the test.
type integrationBranchTaskStore struct {
	db.Store
	task     db.Task
	captured bool
}

func (s *integrationBranchTaskStore) CreateTask(task db.Task, _ int) (db.Task, error) {
	s.task = task
	s.captured = true
	return db.Task{}, errors.New("integration branch task captured")
}

type integrationBranchPolicyAdmissionCapture struct {
	input pro_interfaces.PolicyGuardrailEvaluationInput
}

func (s *integrationBranchPolicyAdmissionCapture) EvaluatePolicyGuardrails(input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	s.input = input
	fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	return pro_interfaces.PolicyGuardrailEvaluation{Allowed: true, InputFingerprint: fingerprint, EvaluatedAt: input.EvaluatedAt}, nil
}

func (s *integrationBranchPolicyAdmissionCapture) PreviewPolicyGuardrailEvaluations(inputs []pro_interfaces.PolicyGuardrailEvaluationInput) ([]pro_interfaces.PolicyGuardrailEvaluation, error) {
	evaluations := make([]pro_interfaces.PolicyGuardrailEvaluation, len(inputs))
	for index, input := range inputs {
		evaluation, err := s.EvaluatePolicyGuardrails(input)
		if err != nil {
			return nil, err
		}
		evaluations[index] = evaluation
	}
	return evaluations, nil
}

func (s *integrationBranchPolicyAdmissionCapture) ClaimPolicyGuardrailEvaluation(request pro_interfaces.PolicyGuardrailAdmissionRequest) (pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	evaluation, err := s.EvaluatePolicyGuardrails(request.Input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, err
	}
	templateID := request.Input.Template.ID
	return pro_interfaces.PolicyGuardrailEvaluationClaim{
		Record: db.PolicyGuardrailEvaluationRecord{
			ID: 1, ProjectID: request.Input.ProjectID, DecisionKey: request.DecisionKey, Intent: "task", Source: request.Source,
			TemplateID: &templateID, InputFingerprint: evaluation.InputFingerprint, RevisionsJSON: "[]", FindingsJSON: "[]",
			Decision: db.PolicyGuardrailDecisionAllow, EvaluatedAt: evaluation.EvaluatedAt, Created: evaluation.EvaluatedAt,
		},
		Evaluation: evaluation,
		Inserted:   true,
	}, nil
}

func (s *integrationBranchPolicyAdmissionCapture) ClaimPolicyGuardrailEvaluations(requests []pro_interfaces.PolicyGuardrailAdmissionRequest) ([]pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	claims := make([]pro_interfaces.PolicyGuardrailEvaluationClaim, len(requests))
	for index, request := range requests {
		claim, err := s.ClaimPolicyGuardrailEvaluation(request)
		if err != nil {
			return nil, err
		}
		claims[index] = claim
	}
	return claims, nil
}

type integrationBranchDeploymentAdmissionCapture struct {
	request pro_interfaces.DeploymentWindowAdmissionRequest
}

func (s *integrationBranchDeploymentAdmissionCapture) Claim(request pro_interfaces.DeploymentWindowAdmissionRequest) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	s.request = request
	templateID := *request.TemplateID
	return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: db.DeploymentWindowDecisionRecord{
		ID: 1, ProjectID: request.ProjectID, TemplateID: &templateID,
		State: string(pro_interfaces.DeploymentWindowDecisionAllowed),
	}}, nil
}

func newIntegrationBranchFixture(t *testing.T, allowBranchOverride bool, taskStore *integrationBranchTaskStore) (*sql.SqlDb, db.Project, db.Integration, *tasks.TaskPool) {
	t.Helper()

	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "integration branch"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "integration repository",
		GitURL: "https://example.invalid/integration.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "integration deploy", Playbook: "deploy.yml",
		AllowOverrideBranchInTask: allowBranchOverride,
	})
	require.NoError(t, err)
	staticBranch := "static"
	integration, err := store.CreateIntegration(db.Integration{
		Name: "branch webhook", ProjectID: project.ID, TemplateID: &template.ID,
		TaskParams: &db.TaskParams{GitBranch: &staticBranch},
	})
	require.NoError(t, err)
	_, err = store.CreateIntegrationExtractValue(project.ID, db.IntegrationExtractValue{
		Name: "branch", IntegrationID: integration.ID, ValueSource: db.IntegrationExtractBodyValue,
		BodyDataType: db.IntegrationBodyDataJSON, Key: "ref", Variable: db.TaskParamGitBranch,
		VariableType: db.IntegrationVariableTaskParam,
	})
	require.NoError(t, err)

	var poolStore db.Store = store
	if taskStore != nil {
		taskStore.Store = store
		poolStore = taskStore
	}
	pool := tasks.CreateTaskPool(poolStore, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, nil, nil, nil)

	return store, project, integration, &pool
}

func captureIntegrationBranchTask(t *testing.T, allowBranchOverride bool, ref string) db.Task {
	t.Helper()
	capture := &integrationBranchTaskStore{}
	store, project, integration, pool := newIntegrationBranchFixture(t, allowBranchOverride, capture)
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/branch", nil)
	request = helpers.SetContextValue(request, "task_pool", pool)

	result := NewIntegrationController(store, nil).RunIntegration(integration, project, request, []byte(`{"ref":"`+ref+`"}`))
	assert.Nil(t, result.Task)
	require.True(t, capture.captured)
	return capture.task
}

func TestIntegrationGitBranch_ReachesPersistenceBoundaryNormalizedAndStripped(t *testing.T) {
	task := captureIntegrationBranchTask(t, true, "refs/heads/release/2026-10")

	require.NotNil(t, task.GitBranch)
	assert.Equal(t, "release/2026-10", *task.GitBranch)
	assert.NotContains(t, task.Params, db.TaskParamGitBranch)
}

func TestIntegrationGitBranch_DisabledOverrideKeepsStaticBranchAtPersistenceBoundary(t *testing.T) {
	task := captureIntegrationBranchTask(t, false, "refs/heads/untrusted")

	require.NotNil(t, task.GitBranch)
	assert.Equal(t, "static", *task.GitBranch)
	assert.NotContains(t, task.Params, db.TaskParamGitBranch)
}

func TestIntegrationGitBranch_ConfiguredAdmissionsSeeNormalizedInputBeforeInvalidBranchRejection(t *testing.T) {
	store, project, integration, pool := newIntegrationBranchFixture(t, true, nil)
	policy := &integrationBranchPolicyAdmissionCapture{}
	deployment := &integrationBranchDeploymentAdmissionCapture{}
	pool.ConfigurePolicyGuardrailAdmission(policy)
	pool.ConfigureDeploymentWindowAdmission(deployment)
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/branch", nil)
	request = helpers.SetContextValue(request, "task_pool", pool)

	result := NewIntegrationController(store, nil).RunIntegration(integration, project, request, []byte(`{"ref":"refs/heads/release..candidate"}`))

	assert.Nil(t, result.Task)
	require.NotNil(t, policy.input.Template)
	assert.True(t, policy.input.Template.BranchOverride)
	assert.Equal(t, 0, policy.input.Template.ArgumentKeyCount, "the reserved parameter is removed before policy evaluation")
	assert.Equal(t, project.ID, deployment.request.ProjectID)
	assert.Equal(t, pro_interfaces.DeploymentWindowSourceIntegration, deployment.request.Source)
	created, err := store.GetProjectTasks(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, created)
}
