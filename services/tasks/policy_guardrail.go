package tasks

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
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
) (pro_interfaces.PolicyGuardrailEvaluationInput, error) {
	if p == nil || projectID <= 0 || plan.ProjectID != projectID || plan.Intent != pro_interfaces.ExecutionPreflightTask {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, errors.New("policy guardrail preflight scope is invalid")
	}
	requestedImage, err := template.ResolveExecutorImage()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	inventory, inventoryID, err := p.taskPreflightInventory(template, task)
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
		ProjectID:   projectID,
		Intent:      pro_interfaces.ExecutionPreflightTask,
		EvaluatedAt: plannedAt.UTC(),
		Template: &pro_interfaces.PolicyGuardrailTemplateMetadata{
			ID:                template.ID,
			Application:       application,
			Source:            "manual",
			InventoryOverride: canOverrideInventory && task.InventoryID != nil,
			BranchOverride:    template.AllowOverrideBranchInTask && task.GitBranch != nil,
			CommitOverride:    task.CommitHash != nil,
			ArgumentKeyCount:  len(task.Params),
			InputKeyCount:     policyGuardrailInputKeyCount(template),
		},
		EnvironmentIDs: append([]int(nil), template.EnvironmentIDs...),
		Executor:       policyGuardrailExecutorMetadata(requestedImage, plan.Placements),
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
			SelectedScope:  string(placement.SelectedScope),
			RequestedTags:  append([]string(nil), placement.RequestedTags...),
			CandidateCount: len(placement.Candidates),
		}
		if placement.SelectedRunnerID != nil {
			input.Runner.SelectedID = *placement.SelectedRunnerID
			for _, candidate := range placement.Candidates {
				if candidate.RunnerID == *placement.SelectedRunnerID {
					input.Runner.SelectedExecutor = string(candidate.Executor)
					break
				}
			}
		}
	}
	if input.Runner.SelectedExecutor == "" && input.Executor.Type == "local" && len(plan.Placements) > 0 {
		input.Runner.SelectedExecutor = "local"
	}
	if err := input.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationInput{}, err
	}
	return input, nil
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
	placements []pro_interfaces.ExecutionPreflightPlacement,
) pro_interfaces.PolicyGuardrailExecutorMetadata {
	metadata := pro_interfaces.PolicyGuardrailExecutorMetadata{Type: "local", ImageReferenceKind: "none"}
	for _, placement := range placements {
		if placement.SelectedRunnerID == nil {
			continue
		}
		for _, candidate := range placement.Candidates {
			if candidate.RunnerID == *placement.SelectedRunnerID && candidate.Executor != "" {
				metadata.Type = string(candidate.Executor)
				break
			}
		}
	}
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
) (ExecutionPreflightSnapshot, error) {
	if p == nil || p.policyGuardrailAdmission == nil {
		return snapshot, nil
	}
	input, err := p.buildTaskPolicyGuardrailEvaluationInput(task, template, snapshot.Plan, projectID, plannedAt)
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
	if evaluation.InputFingerprint != expectedFingerprint || !evaluation.EvaluatedAt.Equal(input.EvaluatedAt) {
		return ExecutionPreflightSnapshot{}, errors.New("policy guardrail evaluation does not match preflight input")
	}
	if err = pro_interfaces.ApplyPolicyGuardrailEvaluation(&snapshot.Plan, evaluation); err != nil {
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
