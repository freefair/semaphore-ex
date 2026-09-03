package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type ExecutionPreflightSnapshot = pro_interfaces.ExecutionPreflightSnapshot

type ExecutionPreflightStaleError = pro_interfaces.ExecutionPreflightStaleError
type ExecutionPreflightDeniedError = pro_interfaces.ExecutionPreflightDeniedError

// PreviewTaskExecution builds and signs a short-lived, value-free review.
func (p *TaskPool) PreviewTaskExecution(task db.Task, actor *db.User, projectID int) (pro_interfaces.ExecutionPreflightPlan, error) {
	snapshot, err := p.BuildTaskExecutionPreflight(task, actor, projectID)
	if err != nil {
		return pro_interfaces.ExecutionPreflightPlan{}, err
	}
	return p.sealExecutionPreflight(snapshot)
}

// AddTaskWithExecutionPreflight reauthorizes at the API layer, replans here,
// verifies the reviewed snapshot, and only then enters the existing enqueue
// path. Omitting both review fields keeps existing API clients compatible but
// still runs the same planner before enqueue.
func (p *TaskPool) AddTaskWithExecutionPreflight(
	task db.Task,
	actor *db.User,
	projectID int,
	needAlias bool,
	review pro_interfaces.ExecutionPreflightReview,
) (db.Task, error) {
	created, _, err := p.AddTaskWithExecutionPreflightPlan(task, actor, projectID, needAlias, review)
	return created, err
}

// AddTaskWithExecutionPreflightPlan returns the freshly server-planned,
// value-free execution plan alongside the enqueue result. Callers use that
// plan for audit only; request review headers never become audit provenance.
func (p *TaskPool) AddTaskWithExecutionPreflightPlan(
	task db.Task,
	actor *db.User,
	projectID int,
	needAlias bool,
	review pro_interfaces.ExecutionPreflightReview,
) (db.Task, pro_interfaces.ExecutionPreflightPlan, error) {
	return p.addTaskWithExecutionPreflightPlan(task, actor, projectID, needAlias, review, nil)
}

// AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride is the manual
// HTTP-only counterpart of the normal reviewed start. It keeps the request
// override out of db.Task and attaches it only to the final admission claim.
func (p *TaskPool) AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
	task db.Task,
	actor *db.User,
	projectID int,
	needAlias bool,
	review pro_interfaces.ExecutionPreflightReview,
	override *pro_interfaces.DeploymentWindowOverrideInput,
) (db.Task, pro_interfaces.ExecutionPreflightPlan, error) {
	return p.addTaskWithExecutionPreflightPlan(task, actor, projectID, needAlias, review, override)
}

func (p *TaskPool) addTaskWithExecutionPreflightPlan(
	task db.Task,
	actor *db.User,
	projectID int,
	needAlias bool,
	review pro_interfaces.ExecutionPreflightReview,
	overrideInput *pro_interfaces.DeploymentWindowOverrideInput,
) (db.Task, pro_interfaces.ExecutionPreflightPlan, error) {
	snapshot, executionSnapshot, err := p.buildTaskExecutionPreflightSnapshot(task, actor, projectID, tz.Now())
	if err != nil {
		return db.Task{}, pro_interfaces.ExecutionPreflightPlan{}, err
	}
	reviewed := review.Fingerprint != "" || review.ReviewToken != ""
	if reviewed && planHasExecutionDenial(snapshot.Plan) {
		plan := snapshot.Plan
		if p.executionPreflightIssuer != nil {
			if sealed, sealErr := p.sealExecutionPreflight(snapshot); sealErr == nil {
				plan = sealed
			}
		}
		return db.Task{}, plan, &ExecutionPreflightDeniedError{Preflight: plan}
	}
	if reviewed {
		if review.Fingerprint == "" || review.ReviewToken == "" || p.executionPreflightIssuer == nil {
			return db.Task{}, snapshot.Plan, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
		}
		sealed, sealErr := p.sealExecutionPreflight(snapshot)
		if sealErr != nil {
			return db.Task{}, snapshot.Plan, sealErr
		}
		binding := pro_interfaces.ExecutionPreflightReviewBinding{
			ActorID: actor.ID, ProjectID: projectID, Intent: pro_interfaces.ExecutionPreflightTask,
			TemplateID: task.TemplateID, Fingerprint: review.Fingerprint,
		}
		claims, verifyErr := p.executionPreflightIssuer.Verify(review.ReviewToken, binding)
		if verifyErr != nil {
			return db.Task{}, snapshot.Plan, verifyErr
		}
		changes, diffErr := p.executionPreflightIssuer.DiffVerifiedComponents(claims, snapshot.Plan, snapshot.Components)
		if diffErr != nil {
			return db.Task{}, snapshot.Plan, diffErr
		}
		if len(changes) > 0 || review.Fingerprint != sealed.Fingerprint {
			if len(changes) == 0 {
				changes = []pro_interfaces.ExecutionPreflightChangeCode{pro_interfaces.ExecutionChangeDefinition}
			}
			return db.Task{}, sealed, &ExecutionPreflightStaleError{Changes: changes, Preflight: sealed}
		}
	}
	templateID := task.TemplateID
	actorID := actor.ID
	override, overrideErr := pro_interfaces.NewManualDeploymentWindowOverride(overrideInput, actorID)
	if overrideErr != nil {
		return db.Task{}, snapshot.Plan, overrideErr
	}
	if override != nil && p.deploymentWindowAdmission == nil {
		return db.Task{}, snapshot.Plan, pro_interfaces.ErrDeploymentWindowOverrideForbidden
	}
	if p.deploymentWindowAdmission != nil {
		if err = p.claimDeploymentWindowTaskAdmission(&task, pro_interfaces.DeploymentWindowAdmissionRequest{
			ProjectID: projectID, DecisionKey: "manual-" + random.String(32), Source: pro_interfaces.DeploymentWindowSourceManual,
			Origin: pro_interfaces.DeploymentWindowOriginUser, TemplateID: &templateID, ActorUserID: &actorID, Override: override,
		}); err != nil {
			return db.Task{}, snapshot.Plan, err
		}
	}
	if reviewed {
		if executionSnapshot == nil {
			return db.Task{}, snapshot.Plan, errors.New("execution preflight snapshot is unavailable")
		}
		encoded, encodeErr := db.EncodeTaskExecutionSnapshot(*executionSnapshot)
		if encodeErr != nil {
			return db.Task{}, snapshot.Plan, errors.New("execution preflight snapshot is invalid")
		}
		task.ExecutionSnapshotJSON = &encoded
		created, createErr := p.addTask(task, &executionSnapshot.Template, &actor.ID, actor.Username, projectID, needAlias, nil, nil)
		return created, snapshot.Plan, createErr
	}
	created, err := p.addTask(task, nil, &actor.ID, actor.Username, projectID, needAlias, nil, nil)
	return created, snapshot.Plan, err
}

func (p *TaskPool) sealExecutionPreflight(snapshot ExecutionPreflightSnapshot) (pro_interfaces.ExecutionPreflightPlan, error) {
	if p.executionPreflightIssuer == nil {
		return pro_interfaces.ExecutionPreflightPlan{}, errors.New("execution preflight review is unavailable")
	}
	review, err := p.executionPreflightIssuer.IssueWithComponents(snapshot.Plan, snapshot.Components)
	if err != nil {
		return pro_interfaces.ExecutionPreflightPlan{}, err
	}
	plan := snapshot.Plan
	plan.Fingerprint = review.Fingerprint
	plan.ReviewToken = review.ReviewToken
	plan.ExpiresAt = review.ExpiresAt
	return plan, nil
}

func (p *TaskPool) SealExecutionPreflight(snapshot pro_interfaces.ExecutionPreflightSnapshot) (pro_interfaces.ExecutionPreflightPlan, error) {
	return p.sealExecutionPreflight(snapshot)
}

func (p *TaskPool) VerifyExecutionPreflightReview(
	snapshot pro_interfaces.ExecutionPreflightSnapshot,
	review pro_interfaces.ExecutionPreflightReview,
) ([]pro_interfaces.ExecutionPreflightChangeCode, error) {
	if p.executionPreflightIssuer == nil || review.Fingerprint == "" || review.ReviewToken == "" {
		return nil, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid
	}
	binding := pro_interfaces.ExecutionPreflightReviewBindingFromPlan(snapshot.Plan)
	binding.Fingerprint = review.Fingerprint
	claims, err := p.executionPreflightIssuer.Verify(review.ReviewToken, binding)
	if err != nil {
		return nil, err
	}
	return p.executionPreflightIssuer.DiffVerifiedComponents(claims, snapshot.Plan, snapshot.Components)
}

func planHasExecutionDenial(plan pro_interfaces.ExecutionPreflightPlan) bool {
	for _, finding := range plan.Findings {
		if finding.Severity == pro_interfaces.ExecutionFindingDenial {
			return true
		}
	}
	return false
}

// BuildTaskExecutionPreflight resolves the same immutable task definition,
// executor entitlement, resource references, and runner placement inputs used
// by task enqueue. It performs reads only: no task, secret, event, assignment,
// reservation, webhook, or external provider is created or invoked.
func (p *TaskPool) BuildTaskExecutionPreflight(
	task db.Task,
	actor *db.User,
	projectID int,
	now ...time.Time,
) (ExecutionPreflightSnapshot, error) {
	if actor == nil || actor.ID <= 0 || projectID <= 0 {
		return ExecutionPreflightSnapshot{}, errors.New("execution preflight actor and project are required")
	}
	plannedAt := tz.Now()
	if len(now) > 0 {
		plannedAt = now[0].UTC()
	}

	snapshot, _, err := p.buildTaskExecutionPreflightSnapshot(task, actor, projectID, plannedAt)
	return snapshot, err
}

func (p *TaskPool) buildTaskExecutionPreflightSnapshot(
	task db.Task,
	actor *db.User,
	projectID int,
	plannedAt time.Time,
) (ExecutionPreflightSnapshot, *db.TaskExecutionSnapshot, error) {
	template, err := p.store.GetTemplate(projectID, task.TemplateID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return hiddenTaskReferencePlan(task.TemplateID, actor.ID, projectID, plannedAt), nil, nil
		}
		return ExecutionPreflightSnapshot{}, nil, err
	}
	return p.buildTaskExecutionPreflight(task, template, actor, projectID, plannedAt)
}

func (p *TaskPool) BuildWorkflowTaskExecutionPreflight(
	task db.Task,
	template db.Template,
	actor *db.User,
	projectID int,
	plannedAt time.Time,
) (pro_interfaces.ExecutionPreflightSnapshot, error) {
	if actor == nil || actor.ID <= 0 || projectID <= 0 || template.ID <= 0 {
		return ExecutionPreflightSnapshot{}, errors.New("workflow execution preflight scope is invalid")
	}
	snapshot, _, err := p.buildTaskExecutionPreflight(task, template, actor, projectID, plannedAt.UTC())
	return snapshot, err
}

func (p *TaskPool) BuildWorkflowTaskExecutionPreflightSnapshot(
	task db.Task,
	template db.Template,
	actor *db.User,
	projectID int,
	plannedAt time.Time,
) (pro_interfaces.ExecutionPreflightSnapshot, string, error) {
	if actor == nil || actor.ID <= 0 || projectID <= 0 || template.ID <= 0 {
		return ExecutionPreflightSnapshot{}, "", errors.New("workflow execution preflight scope is invalid")
	}
	snapshot, executionSnapshot, err := p.buildTaskExecutionPreflight(task, template, actor, projectID, plannedAt.UTC())
	if err != nil {
		return ExecutionPreflightSnapshot{}, "", err
	}
	if executionSnapshot == nil {
		return snapshot, "", nil
	}
	encoded, err := db.EncodeTaskExecutionSnapshot(*executionSnapshot)
	if err != nil {
		return ExecutionPreflightSnapshot{}, "", errors.New("execution preflight snapshot is invalid")
	}
	return snapshot, encoded, nil
}

func (p *TaskPool) buildTaskExecutionPreflight(
	task db.Task,
	template db.Template,
	actor *db.User,
	projectID int,
	plannedAt time.Time,
) (ExecutionPreflightSnapshot, *db.TaskExecutionSnapshot, error) {
	if err := task.ValidateNewTask(template); err != nil {
		return ExecutionPreflightSnapshot{}, nil, err
	}

	templateVersionSnapshot, err := db.NewTemplateVersionSnapshot(template)
	if err != nil {
		return ExecutionPreflightSnapshot{}, nil, err
	}
	definitionFingerprint, err := db.TemplateVersionFingerprint(templateVersionSnapshot)
	if err != nil {
		return ExecutionPreflightSnapshot{}, nil, err
	}
	plan := pro_interfaces.ExecutionPreflightPlan{
		ContractVersion: pro_interfaces.ExecutionPreflightContractVersion,
		Intent:          pro_interfaces.ExecutionPreflightTask,
		ProjectID:       projectID,
		ActorID:         actor.ID,
		TemplateID:      template.ID,
		Definition: pro_interfaces.ExecutionPreflightDefinition{
			Kind:        pro_interfaces.ExecutionReferenceTemplate,
			ID:          template.ID,
			Name:        template.Name,
			Revision:    definitionFingerprint,
			Fingerprint: definitionFingerprint,
		},
		Inputs:     taskPreflightInputs(template, task),
		References: make([]pro_interfaces.ExecutionPreflightReference, 0, 4+len(template.EnvironmentIDs)+len(template.Vaults)),
		Commands: []pro_interfaces.ExecutionPreflightCommand{{
			TemplateID:     template.ID,
			Application:    template.App,
			Playbook:       effectiveTaskPlaybook(template, task),
			ArgumentKeys:   sortedMapKeys(task.Params),
			InputKeys:      taskPreflightInputNames(template),
			BranchOverride: task.GitBranch != nil && template.AllowOverrideBranchInTask,
		}},
		Placements: make([]pro_interfaces.ExecutionPreflightPlacement, 0, 1),
		Findings:   make([]pro_interfaces.ExecutionPreflightFinding, 0, 2),
		ExpiresAt:  plannedAt.Add(5 * time.Minute),
	}
	components := map[pro_interfaces.ExecutionPreflightChangeCode]string{
		pro_interfaces.ExecutionChangeDefinition: definitionFingerprint,
		pro_interfaces.ExecutionChangeInput: hashPreflightComponent(
			task.Environment, task.Secret, task.Params, task.Arguments, task.GitBranch,
			task.CommitHash, task.InventoryID, task.BuildTaskID, task.GlobalCredentialBindingsJSON,
		),
	}

	resourceState := make([]any, 0, 4+len(template.EnvironmentIDs)+len(template.Vaults))
	var snapshotInventory *db.Inventory
	var snapshotInventoryRepository *db.Repository
	var snapshotRepository *db.Repository
	snapshotEnvironments := make([]db.Environment, 0, len(template.EnvironmentIDs))
	inventory, inventoryID, err := p.taskPreflightInventory(template, task)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			addHiddenReference(&plan, pro_interfaces.ExecutionReferenceInventory, pointerValue(inventoryID), nil)
			components[pro_interfaces.ExecutionChangeReference] = hashPreflightComponent("hidden_inventory", pointerValue(inventoryID))
			finalized, finalizeErr := finalizeTaskPreflight(plan, components)
			return finalized, nil, finalizeErr
		}
		return ExecutionPreflightSnapshot{}, nil, err
	}
	if inventoryID != nil {
		inventorySnapshot := sanitizedExecutionSnapshotInventory(inventory)
		if inventory.RepositoryID != nil {
			inventoryRepository, repositoryErr := p.store.GetRepository(template.ProjectID, *inventory.RepositoryID)
			if repositoryErr != nil {
				return ExecutionPreflightSnapshot{}, nil, repositoryErr
			}
			inventoryRepositorySnapshot := sanitizedExecutionSnapshotRepository(inventoryRepository)
			snapshotInventoryRepository = &inventoryRepositorySnapshot
			plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
				Kind: pro_interfaces.ExecutionReferenceRepository, ID: inventoryRepository.ID,
				Name: inventoryRepository.Name, Revision: inventoryRepository.GitBranch, Visible: true,
			})
			resourceState = append(resourceState, inventoryRepository.ID, inventoryRepository.Name,
				inventoryRepository.GitURL, inventoryRepository.GitBranch, inventoryRepository.SSHKeyID)
			appendCredentialReference(&plan, &inventoryRepository.SSHKeyID, "inventory.repository.ssh")
			if err = p.appendExecutionPreflightCredentialState(&resourceState, template.ProjectID, &inventoryRepository.SSHKeyID); err != nil {
				return ExecutionPreflightSnapshot{}, nil, err
			}
		}
		snapshotInventory = &inventorySnapshot
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceInventory, ID: inventory.ID,
			Name: inventory.Name, Revision: string(inventory.Type), Visible: true,
		})
		resourceState = append(resourceState, inventory.ID, inventory.Name, inventory.Type, inventory.Inventory,
			inventory.SSHKeyID, inventory.BecomeKeyID, inventory.RepositoryID, inventory.RunnerTag)
		appendCredentialReference(&plan, inventory.SSHKeyID, "inventory.ssh")
		appendCredentialReference(&plan, inventory.BecomeKeyID, "inventory.become")
		if err = p.appendExecutionPreflightCredentialState(&resourceState, template.ProjectID, inventory.SSHKeyID); err != nil {
			return ExecutionPreflightSnapshot{}, nil, err
		}
		if err = p.appendExecutionPreflightCredentialState(&resourceState, template.ProjectID, inventory.BecomeKeyID); err != nil {
			return ExecutionPreflightSnapshot{}, nil, err
		}
	}

	if template.RepositoryID > 0 {
		repository, repositoryErr := p.store.GetRepository(template.ProjectID, template.RepositoryID)
		if repositoryErr != nil {
			if errors.Is(repositoryErr, db.ErrNotFound) {
				addHiddenReference(&plan, pro_interfaces.ExecutionReferenceRepository, template.RepositoryID, nil)
				components[pro_interfaces.ExecutionChangeReference] = hashPreflightComponent("hidden_repository", template.RepositoryID)
				finalized, finalizeErr := finalizeTaskPreflight(plan, components)
				return finalized, nil, finalizeErr
			}
			return ExecutionPreflightSnapshot{}, nil, repositoryErr
		}
		repositorySnapshot := sanitizedExecutionSnapshotRepository(repository)
		snapshotRepository = &repositorySnapshot
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceRepository, ID: repository.ID,
			Name: repository.Name, Revision: repository.GitBranch, Visible: true,
		})
		resourceState = append(resourceState, repository.ID, repository.Name, repository.GitURL, repository.GitBranch, repository.SSHKeyID)
		appendCredentialReference(&plan, &repository.SSHKeyID, "repository.ssh")
		if err = p.appendExecutionPreflightCredentialState(&resourceState, template.ProjectID, &repository.SSHKeyID); err != nil {
			return ExecutionPreflightSnapshot{}, nil, err
		}
	}

	if len(template.EnvironmentIDs) > pro_interfaces.MaxExecutionPreflightReferences {
		return planLimitTaskPreflight(plan, components), nil, nil
	}
	for _, environmentID := range template.EnvironmentIDs {
		environment, environmentErr := p.store.GetEnvironment(template.ProjectID, environmentID)
		if environmentErr != nil {
			if errors.Is(environmentErr, db.ErrNotFound) {
				addHiddenReference(&plan, pro_interfaces.ExecutionReferenceEnvironment, environmentID, nil)
				components[pro_interfaces.ExecutionChangeReference] = hashPreflightComponent("hidden_environment", environmentID)
				finalized, finalizeErr := finalizeTaskPreflight(plan, components)
				return finalized, nil, finalizeErr
			}
			return ExecutionPreflightSnapshot{}, nil, environmentErr
		}
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceEnvironment, ID: environment.ID,
			Name: environment.Name, Visible: true,
		})
		resourceState = append(resourceState, environment.ID, environment.Name, environment.JSON, environment.ENV,
			environment.SecretStorageID, environment.SecretStorageKeyPrefix)
		keys, keyErr := p.store.GetEnvironmentSecrets(template.ProjectID, environment.ID)
		if keyErr != nil {
			return ExecutionPreflightSnapshot{}, nil, keyErr
		}
		sort.Slice(keys, func(left, right int) bool { return keys[left].ID < keys[right].ID })
		for _, key := range keys {
			resourceState = append(resourceState, executionPreflightCredentialState(key))
		}
		snapshotEnvironment := sanitizedExecutionSnapshotEnvironment(environment)
		snapshotEnvironment.Secrets = executionSnapshotEnvironmentSecrets(keys)
		snapshotEnvironments = append(snapshotEnvironments, snapshotEnvironment)
	}
	for _, vault := range template.Vaults {
		plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
			Kind: pro_interfaces.ExecutionReferenceVault, ID: vault.ID,
			Name: pointerString(vault.Name), Revision: string(vault.Type), Visible: true,
		})
		resourceState = append(resourceState, vault.ID, vault.Type, vault.Name, vault.Script, vault.VaultKeyID)
		appendCredentialReference(&plan, vault.VaultKeyID, "vault.password")
		if err = p.appendExecutionPreflightCredentialState(&resourceState, template.ProjectID, vault.VaultKeyID); err != nil {
			return ExecutionPreflightSnapshot{}, nil, err
		}
	}
	if len(plan.References) > pro_interfaces.MaxExecutionPreflightReferences {
		return planLimitTaskPreflight(plan, components), nil, nil
	}
	components[pro_interfaces.ExecutionChangeReference] = hashPreflightComponent(resourceState...)

	requestedImage, err := template.ResolveExecutorImage()
	if err != nil {
		return ExecutionPreflightSnapshot{}, nil, err
	}
	imageAvailable := requestedImage == nil || (p.executorImageAvailable != nil && p.executorImageAvailable(actor))
	components[pro_interfaces.ExecutionChangeCapability] = hashPreflightComponent(imageAvailable)
	if !imageAvailable {
		plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
			Severity: pro_interfaces.ExecutionFindingDenial,
			Code:     pro_interfaces.ExecutionReasonCapabilityUnavailable,
			Message:  "The requested executor image capability is unavailable.",
		})
	}

	remote := util.Config.IsUseRemoteRunner() || len(template.EffectiveRunnerTags()) > 0 ||
		inventory.RunnerTag != nil || requestedImage != nil
	if remote {
		placement, placementState, placementErr := p.taskPreflightPlacement(template, inventory, requestedImage, projectID, plannedAt)
		if placementErr != nil {
			return ExecutionPreflightSnapshot{}, nil, placementErr
		}
		plan.Placements = append(plan.Placements, placement)
		components[pro_interfaces.ExecutionChangePlacement] = placementState
		if placement.SelectedRunnerID == nil {
			plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
				Severity: pro_interfaces.ExecutionFindingDenial,
				Code:     pro_interfaces.ExecutionReasonNoCandidate,
				Message:  "No eligible runner is currently available.",
			})
		} else {
			plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
				Severity: pro_interfaces.ExecutionFindingWarning,
				Code:     pro_interfaces.ExecutionReasonProvisionalPlacement,
				Message:  "Runner selection remains provisional until the task is queued.",
			})
		}
	} else {
		plan.Placements = append(plan.Placements, pro_interfaces.ExecutionPreflightPlacement{
			SelectedName: "Local executor", Decision: pro_interfaces.ExecutionReasonSelected,
			Provisional: true, Candidates: []pro_interfaces.ExecutionPreflightCandidate{},
		})
		components[pro_interfaces.ExecutionChangePlacement] = hashPreflightComponent("local")
	}
	components[pro_interfaces.ExecutionChangePolicy] = hashPreflightComponent(requestedImage, template.RunnerTags,
		template.RunnerTagMatchMode, util.Config.Runner, util.Config.MaxParallelTasks)
	finalized, err := finalizeTaskPreflight(plan, components)
	if err != nil {
		return ExecutionPreflightSnapshot{}, nil, err
	}
	if snapshotRepository == nil {
		return ExecutionPreflightSnapshot{}, nil, errors.New("execution preflight repository snapshot is unavailable")
	}
	executionSnapshot := db.TaskExecutionSnapshot{
		Version:             db.TaskExecutionSnapshotVersion,
		ProjectID:           projectID,
		Fingerprint:         finalized.Plan.Fingerprint,
		Template:            sanitizedExecutionSnapshotTemplate(template),
		Inventory:           snapshotInventory,
		InventoryRepository: snapshotInventoryRepository,
		Repository:          *snapshotRepository,
		Environments:        snapshotEnvironments,
	}
	return finalized, &executionSnapshot, nil
}

// appendExecutionPreflightCredentialState binds encrypted local credential
// state and external-reference metadata into a server-only component digest.
// The raw material is neither returned in the plan nor serialized in review
// claims (the issuer HMAC-blinds component digests before issuing a token).
func (p *TaskPool) appendExecutionPreflightCredentialState(state *[]any, projectID int, credentialID *int) error {
	if credentialID == nil || *credentialID <= 0 {
		return nil
	}
	key, err := p.store.GetAccessKey(projectID, *credentialID)
	if err != nil {
		return err
	}
	*state = append(*state, executionPreflightCredentialState(key))
	return nil
}

func executionPreflightCredentialState(key db.AccessKey) any {
	return struct {
		ID                int
		Name              string
		Type              db.AccessKeyType
		Owner             db.AccessKeyOwner
		Ciphertext        string
		SourceStorageID   *int
		SourceStorageKey  *string
		SourceStorageType *db.AccessKeySourceStorageType
		Synchronized      bool
		ExpireAt          *time.Time
	}{
		ID: key.ID, Name: key.Name, Type: key.Type, Owner: key.Owner, Ciphertext: pointerString(key.Secret),
		SourceStorageID: key.SourceStorageID, SourceStorageKey: key.SourceStorageKey,
		SourceStorageType: key.SourceStorageType, Synchronized: key.Synchronized, ExpireAt: key.ExpireAt,
	}
}

func sanitizedExecutionSnapshotTemplate(template db.Template) db.Template {
	copy := template
	copy.LastTask = nil
	for index := range copy.Vaults {
		copy.Vaults[index].Vault = nil
	}
	return copy
}

func sanitizedExecutionSnapshotInventory(inventory db.Inventory) db.Inventory {
	copy := inventory
	copy.SSHKey = db.AccessKey{}
	copy.BecomeKey = db.AccessKey{}
	copy.Repository = nil
	return copy
}

func sanitizedExecutionSnapshotRepository(repository db.Repository) db.Repository {
	copy := repository
	copy.SSHKey = db.AccessKey{}
	return copy
}

func sanitizedExecutionSnapshotEnvironment(environment db.Environment) db.Environment {
	copy := environment
	copy.Password = nil
	copy.Secrets = nil
	return copy
}

func executionSnapshotEnvironmentSecrets(keys []db.AccessKey) []db.EnvironmentSecret {
	secrets := make([]db.EnvironmentSecret, 0, len(keys))
	for _, key := range keys {
		secretType := db.EnvironmentSecretVar
		name := key.Name
		switch key.Owner {
		case db.AccessKeyVariable:
			name = strings.TrimPrefix(key.Name, string(db.EnvironmentSecretVar)+".")
		case db.AccessKeyEnvironment:
			secretType = db.EnvironmentSecretEnv
			name = strings.TrimPrefix(key.Name, string(db.EnvironmentSecretEnv)+".")
		default:
			continue
		}
		secrets = append(secrets, db.EnvironmentSecret{ID: key.ID, Type: secretType, Name: name})
	}
	return secrets
}

func (p *TaskPool) taskPreflightInventory(template db.Template, task db.Task) (db.Inventory, *int, error) {
	inventoryID := template.InventoryID
	canOverride, err := template.CanOverrideInventory()
	if err != nil {
		return db.Inventory{}, nil, err
	}
	if canOverride && task.InventoryID != nil {
		inventoryID = task.InventoryID
	}
	if inventoryID == nil {
		return db.Inventory{}, nil, nil
	}
	inventory, err := p.store.GetInventory(template.ProjectID, *inventoryID)
	return inventory, inventoryID, err
}

func (p *TaskPool) taskPreflightPlacement(
	template db.Template,
	inventory db.Inventory,
	requestedImage *string,
	projectID int,
	now time.Time,
) (pro_interfaces.ExecutionPreflightPlacement, string, error) {
	projectRunners, err := p.store.GetRunners(projectID, false, db.RunnerFilterIgnoreTags, nil)
	if err != nil {
		return pro_interfaces.ExecutionPreflightPlacement{}, "", err
	}
	globalRunners, err := p.store.GetAllRunners(false, true, db.RunnerFilterIgnoreTags, nil)
	if err != nil {
		return pro_interfaces.ExecutionPreflightPlacement{}, "", err
	}
	if len(projectRunners)+len(globalRunners) > pro_interfaces.MaxExecutionPreflightCandidates {
		return pro_interfaces.ExecutionPreflightPlacement{}, "", errors.New("execution preflight candidate limit exceeded")
	}
	candidates := make([]RunnerPlacementCandidate, 0, len(projectRunners)+len(globalRunners))
	state := make([]any, 0, (len(projectRunners)+len(globalRunners))*12)
	for _, runner := range append(projectRunners, globalRunners...) {
		load := p.GetNumberOfRunningTasksOfRunner(runner.ID)
		candidates = append(candidates, RunnerPlacementCandidate{Runner: runner, RunningTasks: load})
		state = append(state, runner.ID, runner.ProjectID, runner.Active, runner.IsRegistered(), runner.Touched,
			runner.MaxParallelTasks, load, runner.Tags, runner.ExecutorType, runner.DockerPolicyRevision,
			runner.DockerPolicyHash, runner.K8sPolicyRevision, runner.K8sPolicyHash)
	}
	tags, matchMode, _ := runnerPlacementPolicy(template, inventory)
	decision := DecideRunnerPlacement(projectID, tags, matchMode, candidates, now, util.Config.RunnersOfflineTimeout(), requestedImage)
	placement := pro_interfaces.ExecutionPreflightPlacement{
		RequestedTags: decision.RequestedTags, MatchMode: decision.MatchMode,
		RequestedImage: decision.RequestedImage, SelectedRunnerID: decision.SelectedRunnerID,
		SelectedName: decision.SelectedName, SelectedScope: decision.SelectedScope,
		Decision:    pro_interfaces.ExecutionPreflightReasonCode(decision.ReasonCode),
		Provisional: true,
		Candidates:  make([]pro_interfaces.ExecutionPreflightCandidate, 0, len(decision.Evaluations)),
	}
	runnerByID := make(map[int]db.Runner, len(candidates))
	for _, candidate := range candidates {
		runnerByID[candidate.Runner.ID] = candidate.Runner
	}
	for _, evaluation := range decision.Evaluations {
		runner := runnerByID[evaluation.RunnerID]
		placement.Candidates = append(placement.Candidates, pro_interfaces.ExecutionPreflightCandidate{
			RunnerID: evaluation.RunnerID, RunnerName: evaluation.RunnerName,
			Scope: evaluation.Scope, Executor: runner.EffectiveExecutorType(), Eligible: evaluation.Eligible,
			AcceptedReasons: mapPlacementReasons(evaluation.AcceptedReasonCodes),
			RejectedReasons: mapPlacementReasons(evaluation.RejectedReasonCodes),
		})
	}
	return placement, hashPreflightComponent(state...), nil
}

func taskPreflightInputs(template db.Template, task db.Task) []pro_interfaces.ExecutionPreflightInput {
	publicKeys := decodeObjectKeys(task.Environment)
	secretKeys := decodeObjectKeys(task.Secret)
	publicSet := stringSet(publicKeys)
	secretSet := stringSet(secretKeys)
	inputs := make([]pro_interfaces.ExecutionPreflightInput, 0, len(template.SurveyVars))
	for _, variable := range template.SurveyVars {
		sensitive := string(variable.Type) == "secret"
		present := publicSet[variable.Name] || secretSet[variable.Name] || variable.DefaultValue != nil
		source := "default"
		if publicSet[variable.Name] || secretSet[variable.Name] {
			source = "task_override"
		}
		inputs = append(inputs, pro_interfaces.ExecutionPreflightInput{
			Name: variable.Name, Type: string(variable.Type), Source: source,
			Present: present, Sensitive: sensitive,
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return inputs
}

func taskPreflightInputNames(template db.Template) []string {
	result := make([]string, 0, len(template.SurveyVars))
	for _, variable := range template.SurveyVars {
		result = append(result, variable.Name)
	}
	sort.Strings(result)
	return result
}

func effectiveTaskPlaybook(template db.Template, task db.Task) string {
	if task.Playbook != "" {
		return task.Playbook
	}
	return template.Playbook
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func decodeObjectKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func appendCredentialReference(plan *pro_interfaces.ExecutionPreflightPlan, credentialID *int, target string) {
	if credentialID == nil || *credentialID <= 0 {
		return
	}
	plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
		Kind: pro_interfaces.ExecutionReferenceCredential, ID: *credentialID,
		Name: "Credential #" + strconv.Itoa(*credentialID), BindingTarget: target, Visible: true,
	})
}

func addHiddenReference(plan *pro_interfaces.ExecutionPreflightPlan, kind pro_interfaces.ExecutionPreflightReferenceKind, id int, nodeID *int) {
	plan.References = append(plan.References, pro_interfaces.ExecutionPreflightReference{
		Kind: kind, ID: id, Visible: false, Reason: pro_interfaces.ExecutionReasonHiddenReference,
	})
	plan.Findings = append(plan.Findings, pro_interfaces.ExecutionPreflightFinding{
		Severity: pro_interfaces.ExecutionFindingDenial, Code: pro_interfaces.ExecutionReasonHiddenReference,
		Message: "A required execution reference is unavailable.", NodeID: nodeID,
	})
}

func hiddenTaskReferencePlan(templateID, actorID, projectID int, now time.Time) ExecutionPreflightSnapshot {
	plan := pro_interfaces.ExecutionPreflightPlan{
		ContractVersion: pro_interfaces.ExecutionPreflightContractVersion,
		Intent:          pro_interfaces.ExecutionPreflightTask, ProjectID: projectID, ActorID: actorID,
		TemplateID: templateID, Inputs: []pro_interfaces.ExecutionPreflightInput{},
		References: []pro_interfaces.ExecutionPreflightReference{}, Commands: []pro_interfaces.ExecutionPreflightCommand{},
		Placements: []pro_interfaces.ExecutionPreflightPlacement{}, Findings: []pro_interfaces.ExecutionPreflightFinding{},
		ExpiresAt: now.Add(5 * time.Minute),
	}
	addHiddenReference(&plan, pro_interfaces.ExecutionReferenceTemplate, templateID, nil)
	plan.Fingerprint, _ = pro_interfaces.FingerprintExecutionPreflight(plan)
	return ExecutionPreflightSnapshot{Plan: plan, Components: map[pro_interfaces.ExecutionPreflightChangeCode]string{
		pro_interfaces.ExecutionChangeDefinition: hashPreflightComponent("hidden_template", templateID),
	}}
}

func planLimitTaskPreflight(plan pro_interfaces.ExecutionPreflightPlan, components map[pro_interfaces.ExecutionPreflightChangeCode]string) ExecutionPreflightSnapshot {
	plan.References = nil
	plan.Commands = nil
	plan.Placements = nil
	plan.Findings = []pro_interfaces.ExecutionPreflightFinding{{
		Severity: pro_interfaces.ExecutionFindingDenial, Code: pro_interfaces.ExecutionReasonPlanLimitExceeded,
		Message: "The execution plan exceeds the supported preview limits.",
	}}
	components[pro_interfaces.ExecutionChangePolicy] = hashPreflightComponent("plan_limit_exceeded")
	plan.Fingerprint, _ = pro_interfaces.FingerprintExecutionPreflight(plan)
	return ExecutionPreflightSnapshot{Plan: plan, Components: components}
}

func finalizeTaskPreflight(plan pro_interfaces.ExecutionPreflightPlan, components map[pro_interfaces.ExecutionPreflightChangeCode]string) (ExecutionPreflightSnapshot, error) {
	fingerprint, err := pro_interfaces.FingerprintExecutionPreflight(plan)
	if err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	plan.Fingerprint = fingerprint
	if err = plan.Validate(); err != nil {
		return ExecutionPreflightSnapshot{}, err
	}
	return ExecutionPreflightSnapshot{Plan: plan, Components: components}, nil
}

func mapPlacementReasons(values []db.RunnerPlacementReasonCode) []pro_interfaces.ExecutionPreflightReasonCode {
	result := make([]pro_interfaces.ExecutionPreflightReasonCode, 0, len(values))
	for _, value := range values {
		result = append(result, pro_interfaces.ExecutionPreflightReasonCode(value))
	}
	return result
}

func hashPreflightComponent(values ...any) string {
	encoded, _ := json.Marshal(values)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func pointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func componentChanges(previous, current map[pro_interfaces.ExecutionPreflightChangeCode]string) []pro_interfaces.ExecutionPreflightChangeCode {
	codes := []pro_interfaces.ExecutionPreflightChangeCode{
		pro_interfaces.ExecutionChangeDefinition,
		pro_interfaces.ExecutionChangeInput,
		pro_interfaces.ExecutionChangeReference,
		pro_interfaces.ExecutionChangePlacement,
		pro_interfaces.ExecutionChangePermission,
		pro_interfaces.ExecutionChangeCapability,
		pro_interfaces.ExecutionChangePolicy,
	}
	changes := make([]pro_interfaces.ExecutionPreflightChangeCode, 0, len(codes))
	for _, code := range codes {
		if previous[code] != current[code] {
			changes = append(changes, code)
		}
	}
	return changes
}

func executionPreflightResourceID(plan pro_interfaces.ExecutionPreflightPlan) int {
	if plan.Intent == pro_interfaces.ExecutionPreflightWorkflow {
		return plan.WorkflowID
	}
	return plan.TemplateID
}

func validateExecutionPreflightReviewScope(plan pro_interfaces.ExecutionPreflightPlan, intent pro_interfaces.ExecutionPreflightIntent, projectID, actorID, resourceID int) error {
	if plan.Intent != intent || plan.ProjectID != projectID || plan.ActorID != actorID || executionPreflightResourceID(plan) != resourceID {
		return fmt.Errorf("execution preflight review scope mismatch")
	}
	return nil
}
