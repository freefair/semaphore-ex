package server

import (
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"reflect"
)

type workflowDefinitionService struct {
	repository         db.WorkflowManager
	versionStore       db.WorkflowVersionStore
	grantStore         db.CrossProjectTemplateGrantStore
	referenceStore     db.CrossProjectWorkflowReferenceStore
	validationStore    db.WorkflowTemplateValidationStore
	authorizationStore pro_interfaces.WorkflowAuthorizationIdentityStore
}

func NewWorkflowDefinitionService(
	repository db.WorkflowManager,
	validationStore db.WorkflowTemplateValidationStore,
) pro_interfaces.WorkflowDefinitionService {
	authorizationStore, _ := validationStore.(pro_interfaces.WorkflowAuthorizationIdentityStore)
	versionStore, _ := repository.(db.WorkflowVersionStore)
	grantStore, _ := repository.(db.CrossProjectTemplateGrantStore)
	referenceStore, _ := repository.(db.CrossProjectWorkflowReferenceStore)
	return &workflowDefinitionService{
		repository: repository, versionStore: versionStore,
		grantStore: grantStore, referenceStore: referenceStore, validationStore: validationStore, authorizationStore: authorizationStore,
	}
}

func (s *workflowDefinitionService) List(projectID int, params db.RetrieveQueryParams, actors ...*db.User) ([]db.WorkflowTemplate, error) {
	actor := workflowDefinitionActor(actors)
	if actor == nil {
		return nil, db.ErrNotFound
	}
	workflows, err := s.repository.GetWorkflowTemplates(projectID, params)
	if err != nil || actor.Admin {
		return workflows, err
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(s.authorizationStore, projectID, actor.ID)
	if err != nil {
		return nil, db.ErrNotFound
	}
	return pro_interfaces.FilterWorkflowTemplatesByAccess(workflows, state.Identity, state.KnownRoles), nil
}

func (s *workflowDefinitionService) Get(projectID int, workflowID int, actors ...*db.User) (db.WorkflowTemplate, error) {
	workflow, err := s.repository.GetWorkflowTemplate(projectID, workflowID)
	if err != nil || s.require(workflow, workflowDefinitionActor(actors), pro_interfaces.PermissionViewWorkflow) != nil {
		return db.WorkflowTemplate{}, db.ErrNotFound
	}
	return workflow, nil
}

func (s *workflowDefinitionService) Validate(projectID int, workflow db.WorkflowTemplate, actors ...*db.User) (db.WorkflowValidationResult, error) {
	if s.require(db.WorkflowTemplate{ProjectID: projectID}, workflowDefinitionActor(actors), pro_interfaces.PermissionEditWorkflow) != nil {
		return db.WorkflowValidationResult{}, db.ErrNotFound
	}
	workflow.ProjectID = projectID
	if err := s.normalizeCrossProjectTemplateReferences(&workflow, db.CrossProjectTemplateGrantReference); err != nil {
		return db.WorkflowValidationResult{}, db.ErrNotFound
	}
	if err := validateNewWorkflowApprovalPolicies(workflow); err != nil {
		return db.WorkflowValidationResult{}, err
	}
	_, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	return result, err
}

func (s *workflowDefinitionService) Create(
	projectID int,
	workflow db.WorkflowTemplate,
	actors ...*db.User,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	actor := workflowDefinitionActor(actors)
	if s.require(db.WorkflowTemplate{ProjectID: projectID}, actor, pro_interfaces.PermissionEditWorkflow) != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
	}
	if s.versionStore == nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, errors.New("workflow version store is unavailable")
	}
	if err := validateWorkflowVersionMessage(workflow.VersionMessage); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	workflow.ID = 0
	workflow.ProjectID = projectID
	workflow.Revision = 0
	if err := s.normalizeCrossProjectTemplateReferences(&workflow, db.CrossProjectTemplateGrantReference); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
	}
	if err := validateNewWorkflowApprovalPolicies(workflow); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	initializeWorkflowPolicyRevisions(&workflow)
	workflow, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if !result.Valid {
		return db.WorkflowTemplate{}, result, nil
	}
	mutation := db.WorkflowVersionMutation{
		AuthorUserID: actor.ID, Message: workflow.VersionMessage,
	}
	var created db.WorkflowTemplate
	var version db.WorkflowVersion
	if workflowHasCrossProjectTemplateReferences(workflow) {
		if s.referenceStore == nil {
			return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, errors.New("cross-project workflow reference store is unavailable")
		}
		created, version, err = s.referenceStore.CreateWorkflowTemplateVersionedWithCrossProjectReferences(workflow, mutation)
	} else {
		created, version, err = s.versionStore.CreateWorkflowTemplateVersioned(workflow, mutation)
	}
	created.CurrentVersionID = version.ID
	created.VersionMessage = ""
	return created, result, err
}

func (s *workflowDefinitionService) Update(
	projectID int,
	workflowID int,
	workflow db.WorkflowTemplate,
	actors ...*db.User,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	actor := workflowDefinitionActor(actors)
	mutation := db.WorkflowVersionMutation{Message: workflow.VersionMessage}
	if actor != nil {
		mutation.AuthorUserID = actor.ID
	}
	return s.update(projectID, workflowID, workflow, actor, mutation)
}

func (s *workflowDefinitionService) update(
	projectID int,
	workflowID int,
	workflow db.WorkflowTemplate,
	actor *db.User,
	mutation db.WorkflowVersionMutation,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	cloned, cloneErr := cloneWorkflowDefinitionInput(workflow)
	if cloneErr != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, cloneErr
	}
	workflow = cloned
	current, err := s.repository.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if err = validateWorkflowApprovalPolicyTransition(current, workflow); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	policyChanged, revisionErr := applyWorkflowPolicyRevisions(current, &workflow)
	if revisionErr != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, revisionErr
	}
	permission := pro_interfaces.PermissionEditWorkflow
	if policyChanged {
		permission = pro_interfaces.PermissionAdministerWorkflow
	}
	if s.require(current, actor, permission) != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	if s.versionStore == nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, errors.New("workflow version store is unavailable")
	}
	if err := validateWorkflowVersionMessage(workflow.VersionMessage); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	workflow.ID = workflowID
	workflow.ProjectID = projectID
	if err := s.normalizeCrossProjectTemplateReferences(&workflow, db.CrossProjectTemplateGrantReference); err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
	}
	workflow, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if !result.Valid {
		return db.WorkflowTemplate{}, result, nil
	}
	mutation.AuthorUserID = actor.ID
	mutation.Message = workflow.VersionMessage
	var updated db.WorkflowTemplate
	var version db.WorkflowVersion
	if workflowHasCrossProjectTemplateReferences(workflow) {
		if s.referenceStore == nil {
			return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, errors.New("cross-project workflow reference store is unavailable")
		}
		updated, version, err = s.referenceStore.UpdateWorkflowTemplateVersionedWithCrossProjectReferences(workflow, mutation)
	} else {
		updated, version, err = s.versionStore.UpdateWorkflowTemplateVersioned(workflow, mutation)
	}
	updated.CurrentVersionID = version.ID
	updated.VersionMessage = ""
	return updated, result, err
}

// cloneWorkflowDefinitionInput gives the service exclusive ownership of the
// request graph before policy normalization and persistence assign server-side
// IDs or encoded fields. Callers may safely reuse a value in concurrent CAS
// attempts without sharing mutable node or edge backing storage.
func cloneWorkflowDefinitionInput(workflow db.WorkflowTemplate) (db.WorkflowTemplate, error) {
	payload, err := json.Marshal(workflow)
	if err != nil {
		return db.WorkflowTemplate{}, err
	}
	var cloned db.WorkflowTemplate
	if err = json.Unmarshal(payload, &cloned); err != nil {
		return db.WorkflowTemplate{}, err
	}
	cloned.AccessPolicy.ViewRoleIDs = cloneWorkflowRoleReferences(workflow.AccessPolicy.ViewRoleIDs)
	cloned.AccessPolicy.StartRoleIDs = cloneWorkflowRoleReferences(workflow.AccessPolicy.StartRoleIDs)
	for index := range cloned.Nodes {
		cloned.Nodes[index].ApprovalRolePolicy.RoleIDs = cloneWorkflowRoleReferences(
			workflow.Nodes[index].ApprovalRolePolicy.RoleIDs,
		)
	}
	return cloned, nil
}

func cloneWorkflowRoleReferences(values []db.ProjectRoleReference) []db.ProjectRoleReference {
	if values == nil {
		return nil
	}
	return append([]db.ProjectRoleReference{}, values...)
}

func (s *workflowDefinitionService) ListVersions(
	projectID int,
	workflowID int,
	params db.RetrieveQueryParams,
	actors ...*db.User,
) ([]db.WorkflowVersion, error) {
	if s.versionStore == nil {
		return nil, db.ErrNotFound
	}
	if _, err := s.Get(projectID, workflowID, actors...); err != nil {
		return nil, err
	}
	if _, err := s.versionStore.EnsureCurrentWorkflowVersion(projectID, workflowID); err != nil {
		return nil, err
	}
	return s.versionStore.GetWorkflowVersions(projectID, workflowID, params)
}

func (s *workflowDefinitionService) GetVersion(
	projectID int,
	workflowID int,
	versionNumber int,
	actors ...*db.User,
) (db.WorkflowVersion, error) {
	if s.versionStore == nil || versionNumber <= 0 {
		return db.WorkflowVersion{}, db.ErrNotFound
	}
	if _, err := s.Get(projectID, workflowID, actors...); err != nil {
		return db.WorkflowVersion{}, err
	}
	if _, err := s.versionStore.EnsureCurrentWorkflowVersion(projectID, workflowID); err != nil {
		return db.WorkflowVersion{}, err
	}
	return s.versionStore.GetWorkflowVersion(projectID, workflowID, versionNumber)
}

func (s *workflowDefinitionService) DiffVersions(
	projectID int,
	workflowID int,
	beforeVersion int,
	afterVersion int,
	actors ...*db.User,
) (pro_interfaces.WorkflowDefinitionDiff, error) {
	before, err := s.GetVersion(projectID, workflowID, beforeVersion, actors...)
	if err != nil {
		return pro_interfaces.WorkflowDefinitionDiff{}, err
	}
	after, err := s.GetVersion(projectID, workflowID, afterVersion, actors...)
	if err != nil {
		return pro_interfaces.WorkflowDefinitionDiff{}, err
	}
	return pro_interfaces.DiffWorkflowDefinitions(before.DefinitionSnapshot, after.DefinitionSnapshot)
}

func (s *workflowDefinitionService) RestoreVersion(
	projectID int,
	workflowID int,
	versionNumber int,
	message string,
	actors ...*db.User,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
	actor := workflowDefinitionActor(actors)
	source, err := s.GetVersion(projectID, workflowID, versionNumber, actor)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	current, err := s.repository.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	restored := workflowRestoreCandidate(source.DefinitionSnapshot, current)
	restored.VersionMessage = message
	return s.update(projectID, workflowID, restored, actor, db.WorkflowVersionMutation{
		AuthorUserID: actor.ID, Message: message, RestoredFromVersionID: &source.ID,
	})
}

func workflowRestoreCandidate(source db.WorkflowTemplate, current db.WorkflowTemplate) db.WorkflowTemplate {
	restored := source
	restored.ID = current.ID
	restored.ProjectID = current.ProjectID
	restored.Revision = current.Revision
	restored.CurrentVersionID = current.CurrentVersionID
	restored.AccessPolicyRevision = current.AccessPolicyRevision
	restored.AccessPolicy.Revision = current.AccessPolicyRevision
	restored.LastRun = nil

	nodeIDs := make(map[int]int, len(restored.Nodes))
	for index := range restored.Nodes {
		oldID := restored.Nodes[index].ID
		newID := -(index + 1)
		nodeIDs[oldID] = newID
		restored.Nodes[index].ID = newID
		restored.Nodes[index].WorkflowTemplateID = current.ID
		restored.Nodes[index].ApprovalRolePolicyRevision = 0
		restored.Nodes[index].ApprovalRolePolicy.Revision = 0
	}
	for index := range restored.Nodes {
		for inputIndex := range restored.Nodes[index].ArtifactInputs {
			if mapped, exists := nodeIDs[restored.Nodes[index].ArtifactInputs[inputIndex].SourceNodeID]; exists {
				restored.Nodes[index].ArtifactInputs[inputIndex].SourceNodeID = mapped
			}
		}
	}
	for index := range restored.Edges {
		restored.Edges[index].ID = -(index + 1)
		restored.Edges[index].WorkflowTemplateID = current.ID
		restored.Edges[index].SourceNodeID = nodeIDs[restored.Edges[index].SourceNodeID]
		restored.Edges[index].DestinationNodeID = nodeIDs[restored.Edges[index].DestinationNodeID]
	}
	return restored
}

func validateWorkflowVersionMessage(message string) error {
	if len([]byte(message)) > db.MaxWorkflowVersionMessageBytes {
		return common_errors.NewValidationError("workflow version message must not exceed 512 bytes")
	}
	return nil
}

func (s *workflowDefinitionService) Delete(projectID int, workflowID int, actors ...*db.User) error {
	workflow, err := s.repository.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return err
	}
	if s.require(workflow, workflowDefinitionActor(actors), pro_interfaces.PermissionAdministerWorkflow) != nil {
		return pro_interfaces.ErrWorkflowPermissionDenied
	}
	return s.repository.DeleteWorkflowTemplate(projectID, workflowID)
}

func workflowDefinitionActor(actors []*db.User) *db.User {
	if len(actors) != 1 {
		return nil
	}
	return actors[0]
}
func (s *workflowDefinitionService) require(workflow db.WorkflowTemplate, actor *db.User, permission pro_interfaces.PermissionID) error {
	if actor == nil || s.authorizationStore == nil {
		return errors.New("workflow actor unavailable")
	}
	if actor.Admin {
		return nil
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(s.authorizationStore, workflow.ProjectID, actor.ID)
	if err != nil || !pro_interfaces.AuthorizeWorkflowRead(workflow, state.Identity, state.KnownRoles).Allowed {
		return errors.New("workflow hidden")
	}
	if !pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{Permission: permission, ProjectPermissions: state.Identity.Permissions, EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference}, KnownRoleReferences: state.KnownRoles, Policy: workflow.AccessPolicy}).Allowed {
		return errors.New("workflow denied")
	}
	return nil
}

// normalizeCrossProjectTemplateReferences accepts only grant ID and exact
// version number as caller input. All durable identity fields are overwritten
// from the live accepted grant and immutable version snapshot.
func (s *workflowDefinitionService) normalizeCrossProjectTemplateReferences(workflow *db.WorkflowTemplate, operation db.CrossProjectTemplateGrantOperation) error {
	if workflow == nil {
		return errors.New("workflow is required")
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.CrossProjectTemplateReference == nil {
			continue
		}
		if s.grantStore == nil {
			return errors.New("cross-project template grant store is unavailable")
		}
		normalized, version, err := s.grantStore.ResolveActiveCrossProjectTemplateGrant(workflow.ProjectID, *node.CrossProjectTemplateReference, operation)
		if err != nil {
			return err
		}
		if node.EffectiveKind() != db.WorkflowNodeTaskKind || len(node.OverridePolicy.InventoryIDs) > 0 ||
			len(node.OverridePolicy.EnvironmentIDs) > 0 || len(node.OverridePolicy.CredentialParameters) > 0 {
			return errors.New("cross-project template resource overrides are forbidden")
		}
		if node.OverridePolicy.AllowArguments && !version.Snapshot.Execution.AllowOverrideArgsInTask {
			return errors.New("cross-project template does not allow argument overrides")
		}
		if node.OverridePolicy.AllowBranch && !version.Snapshot.Execution.AllowOverrideBranchInTask {
			return errors.New("cross-project template does not allow branch overrides")
		}
		node.TemplateID = normalized.TemplateID
		node.CrossProjectTemplateReference = &normalized
	}
	return nil
}

func workflowHasCrossProjectTemplateReferences(workflow db.WorkflowTemplate) bool {
	for _, node := range workflow.Nodes {
		if node.CrossProjectTemplateReference != nil {
			return true
		}
	}
	return false
}
func approvalPoliciesChanged(current, requested db.WorkflowTemplate) bool {
	if len(current.Nodes) != len(requested.Nodes) {
		return true
	}
	for i := range current.Nodes {
		if !reflect.DeepEqual(current.Nodes[i].ApprovalRolePolicy, requested.Nodes[i].ApprovalRolePolicy) {
			return true
		}
	}
	return false
}

func initializeWorkflowPolicyRevisions(workflow *db.WorkflowTemplate) {
	workflow.AccessPolicyRevision = 1
	workflow.AccessPolicy.Revision = 1
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.EffectiveKind() == db.WorkflowNodeApprovalKind && (node.ApprovalRolePolicy.Mode != "" || len(node.ApprovalRolePolicy.RoleIDs) > 0) {
			node.ApprovalRolePolicyRevision = 1
			node.ApprovalRolePolicy.Revision = 1
		}
	}
}

func applyWorkflowPolicyRevisions(current db.WorkflowTemplate, requested *db.WorkflowTemplate) (bool, error) {
	changed := false
	if current.AccessPolicyRevision != current.AccessPolicy.Revision ||
		requested.AccessPolicy.Revision != current.AccessPolicy.Revision {
		return false, pro_interfaces.ErrWorkflowRevisionConflict
	}
	// AccessPolicyRevision is a persistence mirror deliberately omitted from
	// the wire format. The nested revision is the only client CAS token.
	requested.AccessPolicyRevision = current.AccessPolicyRevision
	if workflowAccessPolicyEqual(current.AccessPolicy, requested.AccessPolicy) {
	} else {
		requested.AccessPolicyRevision = current.AccessPolicyRevision + 1
		requested.AccessPolicy.Revision = current.AccessPolicy.Revision + 1
		changed = true
	}
	currentByID := make(map[int]db.WorkflowNode, len(current.Nodes))
	for _, node := range current.Nodes {
		currentByID[node.ID] = node
	}
	for index := range requested.Nodes {
		node := &requested.Nodes[index]
		if node.EffectiveKind() != db.WorkflowNodeApprovalKind {
			continue
		}
		if node.ApprovalRolePolicy.Mode == "" && len(node.ApprovalRolePolicy.RoleIDs) == 0 {
			continue
		}
		previous, exists := currentByID[node.ID]
		if !exists {
			node.ApprovalRolePolicyRevision = 1
			node.ApprovalRolePolicy.Revision = 1
			changed = true
			continue
		}
		if previous.ApprovalRolePolicyRevision != previous.ApprovalRolePolicy.Revision ||
			node.ApprovalRolePolicy.Revision != previous.ApprovalRolePolicy.Revision {
			return false, pro_interfaces.ErrWorkflowRevisionConflict
		}
		// ApprovalRolePolicyRevision is also storage-only. Do not permit an
		// unrepresentable top-level request field to become a second CAS token.
		node.ApprovalRolePolicyRevision = previous.ApprovalRolePolicyRevision
		if workflowApprovalPolicyEqual(previous.ApprovalRolePolicy, node.ApprovalRolePolicy) {
			continue
		}
		node.ApprovalRolePolicyRevision = previous.ApprovalRolePolicyRevision + 1
		node.ApprovalRolePolicy.Revision = previous.ApprovalRolePolicy.Revision + 1
		changed = true
	}
	return changed, nil
}

func workflowAccessPolicyEqual(left, right db.WorkflowAccessPolicy) bool {
	left.Revision = 0
	right.Revision = 0
	return reflect.DeepEqual(left, right)
}
func workflowApprovalPolicyEqual(left, right db.WorkflowApprovalRolePolicy) bool {
	left.Revision = 0
	right.Revision = 0
	return reflect.DeepEqual(left, right)
}

func validateNewWorkflowApprovalPolicies(workflow db.WorkflowTemplate) error {
	for _, node := range workflow.Nodes {
		if node.EffectiveKind() == db.WorkflowNodeApprovalKind && len(node.ApprovalRolePolicy.RoleIDs) == 0 {
			return common_errors.NewValidationError("new workflow approval nodes require a role policy")
		}
	}
	return nil
}

func validateWorkflowApprovalPolicyTransition(current, requested db.WorkflowTemplate) error {
	currentByID := make(map[int]db.WorkflowNode, len(current.Nodes))
	for _, node := range current.Nodes {
		currentByID[node.ID] = node
	}
	for _, node := range requested.Nodes {
		previous, exists := currentByID[node.ID]
		if !exists {
			if node.EffectiveKind() == db.WorkflowNodeApprovalKind && len(node.ApprovalRolePolicy.RoleIDs) == 0 {
				return common_errors.NewValidationError("new workflow approval nodes require a role policy")
			}
			continue
		}
		if previous.EffectiveKind() == db.WorkflowNodeApprovalKind && len(previous.ApprovalRolePolicy.RoleIDs) > 0 &&
			(node.EffectiveKind() != db.WorkflowNodeApprovalKind || len(node.ApprovalRolePolicy.RoleIDs) == 0) {
			return common_errors.NewValidationError("workflow approval role policy cannot be cleared")
		}
	}
	return nil
}
