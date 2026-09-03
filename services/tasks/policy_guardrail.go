package tasks

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const (
	policyGuardrailCredentialScopeLocal  = "local"
	policyGuardrailCredentialScopeGlobal = "global"
)

// buildTaskPolicyGuardrailEvaluationInput derives the allow-listed metadata
// supplied to policy evaluation. It intentionally accepts only resolved task
// planning state and returns neither task/environment values nor credential
// material, names, types, or provider references.
func (p *TaskPool) buildTaskPolicyGuardrailEvaluationInput(
	task db.Task,
	template db.Template,
	plan pro_interfaces.ExecutionPreflightPlan,
	projectID int,
	plannedAt time.Time,
	source pro_interfaces.DeploymentWindowSource,
) (pro_interfaces.PolicyGuardrailEvaluationInput, error) {
	if p == nil || projectID <= 0 || plan.ProjectID != projectID || plan.Intent != pro_interfaces.ExecutionPreflightTask || !taskPolicyGuardrailSourceValid(source) {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, errors.New("policy guardrail preflight scope is invalid")
	}
	requestedImage, err := template.ResolveExecutorImage()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	canOverrideInventory, err := template.CanOverrideInventory()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	inventory, inventoryID, err := p.taskPreflightInventory(template, task)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	application := string(template.App)
	if application == "" {
		application = string(db.AppAnsible)
	}

	input := pro_interfaces.PolicyGuardrailEvaluationInput{
		ProjectID:   projectID,
		Intent:      pro_interfaces.ExecutionPreflightTask,
		EvaluatedAt: plannedAt.UTC(),
		Template: &pro_interfaces.PolicyGuardrailTemplateMetadata{
			ID:                template.ID,
			Application:       application,
			Source:            string(source),
			InventoryOverride: canOverrideInventory && task.InventoryID != nil,
			BranchOverride:    template.AllowOverrideBranchInTask && task.GitBranch != nil,
			CommitOverride:    task.CommitHash != nil,
			ArgumentKeyCount:  len(task.Params),
			InputKeyCount:     policyGuardrailInputKeyCount(template),
		},
		EnvironmentIDs: append([]int(nil), template.EnvironmentIDs...),
		Executor:       policyGuardrailExecutorMetadata(requestedImage),
		Credentials:    policyGuardrailCredentialReferences(plan, task),
	}
	environmentCredentials, err := p.policyGuardrailEnvironmentCredentialReferences(projectID, template.EnvironmentIDs)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	input.Credentials = append(input.Credentials, environmentCredentials...)
	sort.Slice(input.Credentials, func(left, right int) bool {
		if input.Credentials[left].ID != input.Credentials[right].ID {
			return input.Credentials[left].ID < input.Credentials[right].ID
		}
		if input.Credentials[left].Scope != input.Credentials[right].Scope {
			return input.Credentials[left].Scope < input.Credentials[right].Scope
		}
		return input.Credentials[left].BindingTarget < input.Credentials[right].BindingTarget
	})
	sort.Ints(input.EnvironmentIDs)
	if inventoryID != nil {
		runnerTagCount := 0
		if inventory.RunnerTag != nil {
			runnerTagCount = 1
		}
		input.Inventory = &pro_interfaces.PolicyGuardrailInventoryMetadata{
			ID: inventory.ID, Type: string(inventory.Type), RunnerTagCount: runnerTagCount,
		}
	}
	if len(plan.Placements) > 0 {
		placement := plan.Placements[0]
		input.Runner = pro_interfaces.PolicyGuardrailRunnerMetadata{
			RequestedTags:  append([]string(nil), placement.RequestedTags...),
			CandidateCount: len(placement.Candidates),
		}
	}
	if err := input.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	return input, nil
}

func taskPolicyGuardrailSourceValid(source pro_interfaces.DeploymentWindowSource) bool {
	switch source {
	case pro_interfaces.DeploymentWindowSourceManual,
		pro_interfaces.DeploymentWindowSourceSchedule,
		pro_interfaces.DeploymentWindowSourceAPI,
		pro_interfaces.DeploymentWindowSourceWebhook,
		pro_interfaces.DeploymentWindowSourceWorkflowNode,
		pro_interfaces.DeploymentWindowSourceIntegration,
		pro_interfaces.DeploymentWindowSourceAutorun:
		return true
	default:
		return false
	}
}

func validateTaskPolicyGuardrailEvaluation(
	input pro_interfaces.PolicyGuardrailEvaluationInput,
	evaluation pro_interfaces.PolicyGuardrailEvaluation,
) error {
	fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil {
		return err
	}
	if evaluation.InputFingerprint != fingerprint || !evaluation.EvaluatedAt.Equal(input.EvaluatedAt) {
		return errors.New("policy guardrail evaluation does not match preflight input")
	}
	return nil
}

type automaticTaskPolicyGuardrailDescriptor struct {
	plan                   pro_interfaces.ExecutionPreflightPlan
	snapshot               db.TaskExecutionSnapshot
	environmentCredentials []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata
}

// buildAutomaticTaskPolicyGuardrailDescriptor resolves automatic execution
// state once. Both policy metadata and the persisted execution snapshot are
// derived from this descriptor so a post-claim resource reread cannot change
// what was admitted.
func (p *TaskPool) buildAutomaticTaskPolicyGuardrailDescriptor(
	task db.Task,
	template db.Template,
	projectID int,
	plannedAt time.Time,
) (automaticTaskPolicyGuardrailDescriptor, error) {
	if p == nil || projectID <= 0 || template.ID <= 0 || template.ProjectID != projectID || template.RepositoryID <= 0 {
		return automaticTaskPolicyGuardrailDescriptor{}, errors.New("automatic policy guardrail preflight scope is invalid")
	}
	repository, err := p.store.GetRepository(projectID, template.RepositoryID)
	if err != nil {
		return automaticTaskPolicyGuardrailDescriptor{}, err
	}
	descriptor := automaticTaskPolicyGuardrailDescriptor{
		plan: pro_interfaces.ExecutionPreflightPlan{
			Intent: pro_interfaces.ExecutionPreflightTask, ProjectID: projectID, TemplateID: template.ID,
			References: []pro_interfaces.ExecutionPreflightReference{}, Placements: []pro_interfaces.ExecutionPreflightPlacement{},
		},
		snapshot: db.TaskExecutionSnapshot{
			Version: db.TaskExecutionSnapshotVersion, ProjectID: projectID,
			Template: sanitizedExecutionSnapshotTemplate(template), Repository: sanitizedExecutionSnapshotRepository(repository),
			Environments: make([]db.Environment, 0, len(template.EnvironmentIDs)),
		},
		environmentCredentials: make([]pro_interfaces.PolicyGuardrailCredentialReferenceMetadata, 0),
	}
	appendCredentialReference(&descriptor.plan, &repository.SSHKeyID, "repository.ssh")
	inventory, inventoryID, err := p.taskPreflightInventory(template, task)
	if err != nil {
		return automaticTaskPolicyGuardrailDescriptor{}, err
	}
	if inventoryID != nil {
		inventorySnapshot := sanitizedExecutionSnapshotInventory(inventory)
		descriptor.snapshot.Inventory = &inventorySnapshot
		appendCredentialReference(&descriptor.plan, inventory.SSHKeyID, "inventory.ssh")
		appendCredentialReference(&descriptor.plan, inventory.BecomeKeyID, "inventory.become")
		if inventory.RepositoryID != nil {
			inventoryRepository, repositoryErr := p.store.GetRepository(projectID, *inventory.RepositoryID)
			if repositoryErr != nil {
				return automaticTaskPolicyGuardrailDescriptor{}, repositoryErr
			}
			inventoryRepositorySnapshot := sanitizedExecutionSnapshotRepository(inventoryRepository)
			descriptor.snapshot.InventoryRepository = &inventoryRepositorySnapshot
			appendCredentialReference(&descriptor.plan, &inventoryRepository.SSHKeyID, "inventory.repository.ssh")
		}
	}
	for _, environmentID := range template.EnvironmentIDs {
		environment, environmentErr := p.store.GetEnvironment(projectID, environmentID)
		if environmentErr != nil {
			return automaticTaskPolicyGuardrailDescriptor{}, environmentErr
		}
		keys, keyErr := p.store.GetEnvironmentSecrets(projectID, environmentID)
		if keyErr != nil {
			return automaticTaskPolicyGuardrailDescriptor{}, keyErr
		}
		for _, key := range keys {
			if key.ID > 0 {
				descriptor.environmentCredentials = append(descriptor.environmentCredentials, pro_interfaces.PolicyGuardrailCredentialReferenceMetadata{
					ID: key.ID, Scope: policyGuardrailCredentialScopeLocal, BindingTarget: "environment",
				})
			}
		}
		environmentSnapshot := sanitizedExecutionSnapshotEnvironment(environment)
		environmentSnapshot.Secrets = executionSnapshotEnvironmentSecrets(keys)
		descriptor.snapshot.Environments = append(descriptor.snapshot.Environments, environmentSnapshot)
	}
	for _, vault := range template.Vaults {
		appendCredentialReference(&descriptor.plan, vault.VaultKeyID, "vault.password")
	}
	requestedImage, err := template.ResolveExecutorImage()
	if err != nil {
		return automaticTaskPolicyGuardrailDescriptor{}, err
	}
	remote := util.Config.IsUseRemoteRunner() || len(template.EffectiveRunnerTags()) > 0 || inventory.RunnerTag != nil || requestedImage != nil
	if remote {
		placement, _, placementErr := p.taskPreflightPlacement(template, inventory, requestedImage, projectID, plannedAt)
		if placementErr != nil {
			return automaticTaskPolicyGuardrailDescriptor{}, placementErr
		}
		descriptor.plan.Placements = append(descriptor.plan.Placements, placement)
	} else {
		descriptor.plan.Placements = append(descriptor.plan.Placements, pro_interfaces.ExecutionPreflightPlacement{
			SelectedName: "Local executor", Decision: pro_interfaces.ExecutionReasonSelected,
			Provisional: true, Candidates: []pro_interfaces.ExecutionPreflightCandidate{},
		})
	}
	return descriptor, nil
}

func (p *TaskPool) buildAutomaticTaskPolicyGuardrailEvaluationInput(
	task db.Task,
	template db.Template,
	descriptor automaticTaskPolicyGuardrailDescriptor,
	projectID int,
	plannedAt time.Time,
	source pro_interfaces.DeploymentWindowSource,
) (pro_interfaces.PolicyGuardrailEvaluationInput, error) {
	if !taskPolicyGuardrailSourceValid(source) {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, errors.New("automatic policy guardrail source is invalid")
	}
	requestedImage, err := template.ResolveExecutorImage()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	canOverrideInventory, err := template.CanOverrideInventory()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	application := string(template.App)
	if application == "" {
		application = string(db.AppAnsible)
	}
	input := pro_interfaces.PolicyGuardrailEvaluationInput{
		ProjectID: projectID, Intent: pro_interfaces.ExecutionPreflightTask, EvaluatedAt: plannedAt.UTC(),
		Template: &pro_interfaces.PolicyGuardrailTemplateMetadata{
			ID: template.ID, Application: application, Source: string(source),
			InventoryOverride: canOverrideInventory && task.InventoryID != nil, BranchOverride: template.AllowOverrideBranchInTask && task.GitBranch != nil,
			CommitOverride: task.CommitHash != nil, ArgumentKeyCount: len(task.Params), InputKeyCount: policyGuardrailInputKeyCount(template),
		},
		EnvironmentIDs: append([]int(nil), template.EnvironmentIDs...), Executor: policyGuardrailExecutorMetadata(requestedImage),
		Credentials: append(policyGuardrailCredentialReferences(descriptor.plan, task), descriptor.environmentCredentials...),
	}
	if descriptor.snapshot.Inventory != nil {
		inventory := descriptor.snapshot.Inventory
		runnerTagCount := 0
		if inventory.RunnerTag != nil {
			runnerTagCount = 1
		}
		input.Inventory = &pro_interfaces.PolicyGuardrailInventoryMetadata{ID: inventory.ID, Type: string(inventory.Type), RunnerTagCount: runnerTagCount}
	}
	if len(descriptor.plan.Placements) > 0 {
		placement := descriptor.plan.Placements[0]
		input.Runner = pro_interfaces.PolicyGuardrailRunnerMetadata{RequestedTags: append([]string(nil), placement.RequestedTags...), CandidateCount: len(placement.Candidates)}
	}
	sort.Slice(input.Credentials, func(left, right int) bool {
		if input.Credentials[left].ID != input.Credentials[right].ID {
			return input.Credentials[left].ID < input.Credentials[right].ID
		}
		if input.Credentials[left].Scope != input.Credentials[right].Scope {
			return input.Credentials[left].Scope < input.Credentials[right].Scope
		}
		return input.Credentials[left].BindingTarget < input.Credentials[right].BindingTarget
	})
	sort.Ints(input.EnvironmentIDs)
	if err = input.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	return input, nil
}

func (p *TaskPool) claimAutomaticTaskPolicyGuardrailAdmission(
	task db.Task,
	template db.Template,
	projectID int,
	source pro_interfaces.DeploymentWindowSource,
	decisionKey string,
) (pro_interfaces.PolicyGuardrailEvaluationClaim, db.Template, *db.TaskExecutionSnapshot, error) {
	if p == nil || p.policyGuardrailAdmission == nil || !taskPolicyGuardrailSourceValid(source) {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, errors.New("policy guardrail admission is unavailable")
	}
	plannedAt := tz.Now()
	descriptor, err := p.buildAutomaticTaskPolicyGuardrailDescriptor(task, template, projectID, plannedAt)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	input, err := p.buildAutomaticTaskPolicyGuardrailEvaluationInput(task, template, descriptor, projectID, plannedAt, source)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	preview, err := p.policyGuardrailAdmission.EvaluatePolicyGuardrails(input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	if err = validateTaskPolicyGuardrailEvaluation(input, preview); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	claim, err := p.policyGuardrailAdmission.ClaimPolicyGuardrailEvaluation(pro_interfaces.PolicyGuardrailAdmissionRequest{
		DecisionKey: decisionKey, Source: string(source), Input: input,
	})
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	if claim.Record.ID <= 0 || claim.Record.ProjectID != projectID || claim.Record.Source != string(source) ||
		claim.Record.DecisionKey != decisionKey || claim.Evaluation.Allowed != (claim.Record.Decision == db.PolicyGuardrailDecisionAllow) {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, errors.New("policy guardrail admission claim is invalid")
	}
	freshInput, err := p.buildAutomaticTaskPolicyGuardrailEvaluationInput(task, template, descriptor, projectID, claim.Evaluation.EvaluatedAt, source)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	if err = validateTaskPolicyGuardrailEvaluation(freshInput, claim.Evaluation); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	if !claim.Evaluation.Allowed {
		return claim, template, nil, nil
	}
	fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(freshInput)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	descriptor.snapshot.Fingerprint = fingerprint
	if err = descriptor.snapshot.Validate(projectID, template.ID); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.Template{}, nil, err
	}
	return claim, template, &descriptor.snapshot, nil
}

func policyGuardrailInputKeyCount(template db.Template) int {
	count := 0
	for _, variable := range template.SurveyVars {
		if string(variable.Type) != "secret" {
			count++
		}
	}
	return count
}

func policyGuardrailExecutorMetadata(
	requestedImage *string,
) pro_interfaces.PolicyGuardrailExecutorMetadata {
	// Runner selection is provisional until dispatch. The requested task does
	// not carry a stable runner executor class, so policy receives an explicit
	// unknown rather than a potentially false local/docker/kubernetes value.
	metadata := pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "unknown", ImageReferenceKind: "none"}
	if requestedImage == nil {
		return metadata
	}
	metadata.ImagePresent = true
	metadata.ImageReferenceKind = "name"
	if at := strings.LastIndex(*requestedImage, "@"); at >= 0 && at+1 < len(*requestedImage) {
		metadata.ImageReferenceKind = "digest"
		metadata.ImageDigest = (*requestedImage)[at+1:]
		return metadata
	}
	if colon := strings.LastIndex(*requestedImage, ":"); colon > strings.LastIndex(*requestedImage, "/") {
		metadata.ImageReferenceKind = "tag"
	}
	return metadata
}

func policyGuardrailCredentialReferences(
	plan pro_interfaces.ExecutionPreflightPlan,
	task db.Task,
) []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata {
	credentials := make([]pro_interfaces.PolicyGuardrailCredentialReferenceMetadata, 0)
	seen := make(map[policyGuardrailCredentialReference]struct{})
	appendCredential := func(id int, scope, target string) {
		if id <= 0 || target == "" {
			return
		}
		key := policyGuardrailCredentialReference{id: id, scope: scope, target: target}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		credentials = append(credentials, pro_interfaces.PolicyGuardrailCredentialReferenceMetadata{
			ID: id, Scope: scope, BindingTarget: target,
		})
	}
	for _, reference := range plan.References {
		if reference.Kind == pro_interfaces.ExecutionReferenceCredential {
			appendCredential(reference.ID, policyGuardrailCredentialScopeLocal, reference.BindingTarget)
		}
	}
	for _, binding := range taskPolicyGuardrailGlobalCredentialBindings(task) {
		appendCredential(binding, policyGuardrailCredentialScopeGlobal, "task.global")
	}
	sort.Slice(credentials, func(left, right int) bool {
		if credentials[left].ID != credentials[right].ID {
			return credentials[left].ID < credentials[right].ID
		}
		if credentials[left].Scope != credentials[right].Scope {
			return credentials[left].Scope < credentials[right].Scope
		}
		return credentials[left].BindingTarget < credentials[right].BindingTarget
	})
	return credentials
}

func (p *TaskPool) policyGuardrailEnvironmentCredentialReferences(projectID int, environmentIDs []int) ([]pro_interfaces.PolicyGuardrailCredentialReferenceMetadata, error) {
	credentials := make([]pro_interfaces.PolicyGuardrailCredentialReferenceMetadata, 0)
	for _, environmentID := range environmentIDs {
		keys, err := p.store.GetEnvironmentSecrets(projectID, environmentID)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if key.ID > 0 {
				credentials = append(credentials, pro_interfaces.PolicyGuardrailCredentialReferenceMetadata{
					ID: key.ID, Scope: policyGuardrailCredentialScopeLocal, BindingTarget: "environment",
				})
			}
		}
	}
	return credentials, nil
}

type policyGuardrailCredentialReference struct {
	id     int
	scope  string
	target string
}

func taskPolicyGuardrailGlobalCredentialBindings(task db.Task) []int {
	bindings := task.GlobalCredentialBindings
	if bindings == nil && task.GlobalCredentialBindingsJSON != "" {
		copy := task
		if copy.DecodeGlobalCredentialBindings() == nil {
			bindings = copy.GlobalCredentialBindings
		}
	}
	ids := make([]int, 0, len(bindings))
	for _, id := range bindings {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (p *TaskPool) enrichTaskPreflightWithPolicyGuardrails(
	snapshot ExecutionPreflightSnapshot,
	task db.Task,
	template db.Template,
	projectID int,
	plannedAt time.Time,
	source pro_interfaces.DeploymentWindowSource,
) (ExecutionPreflightSnapshot, error) {
	if p == nil || p.policyGuardrailAdmission == nil {
		return snapshot, nil
	}
	input, err := p.buildTaskPolicyGuardrailEvaluationInput(task, template, snapshot.Plan, projectID, plannedAt, source)
	if err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	expectedFingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	evaluation, err := p.policyGuardrailAdmission.EvaluatePolicyGuardrails(input)
	if err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	return applyTaskPolicyGuardrailEvaluation(snapshot, input, evaluation, expectedFingerprint)
}

// applyTaskPolicyGuardrailEvaluation adds only validated, value-free policy
// provenance to a newly planned execution snapshot. It is shared by preview
// evaluation and the durable DB-timed admission claim.
func applyTaskPolicyGuardrailEvaluation(
	snapshot ExecutionPreflightSnapshot,
	input pro_interfaces.PolicyGuardrailEvaluationInput,
	evaluation pro_interfaces.PolicyGuardrailEvaluation,
	expectedFingerprint string,
) (ExecutionPreflightSnapshot, error) {
	if evaluation.InputFingerprint != expectedFingerprint || !evaluation.EvaluatedAt.Equal(input.EvaluatedAt) {
		return ExecutionPreflightSnapshot{}, errors.New("policy guardrail evaluation does not match preflight input")
	}
	if err := pro_interfaces.ApplyPolicyGuardrailEvaluation(&snapshot.Plan, evaluation); err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	components := make(map[pro_interfaces.ExecutionPreflightChangeCode]string, len(snapshot.Components))
	for code, digest := range snapshot.Components {
		components[code] = digest
	}
	components[pro_interfaces.ExecutionChangePolicy] = hashPreflightComponent(
		components[pro_interfaces.ExecutionChangePolicy], expectedFingerprint, evaluation.Revisions, evaluation.Findings,
	)
	return finalizeTaskPreflight(snapshot.Plan, components)
}
