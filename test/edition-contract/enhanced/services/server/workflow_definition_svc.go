package server

import (
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"reflect"
)

type workflowDefinitionService struct {
	repository         db.WorkflowManager
	validationStore    db.WorkflowTemplateValidationStore
	authorizationStore pro_interfaces.WorkflowAuthorizationIdentityStore
}

func NewWorkflowDefinitionService(
	repository db.WorkflowManager,
	validationStore db.WorkflowTemplateValidationStore,
) pro_interfaces.WorkflowDefinitionService {
	authorizationStore, _ := validationStore.(pro_interfaces.WorkflowAuthorizationIdentityStore)
	return &workflowDefinitionService{repository: repository, validationStore: validationStore, authorizationStore: authorizationStore}
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
	if s.require(db.WorkflowTemplate{ProjectID: projectID}, workflowDefinitionActor(actors), pro_interfaces.PermissionEditWorkflow) != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, db.ErrNotFound
	}
	workflow.ID = 0
	workflow.ProjectID = projectID
	workflow.Revision = 0
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
	created, err := s.repository.CreateWorkflowTemplate(workflow)
	return created, result, err
}

func (s *workflowDefinitionService) Update(
	projectID int,
	workflowID int,
	workflow db.WorkflowTemplate,
	actors ...*db.User,
) (db.WorkflowTemplate, db.WorkflowValidationResult, error) {
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
	if s.require(current, workflowDefinitionActor(actors), permission) != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	workflow.ID = workflowID
	workflow.ProjectID = projectID
	workflow, result, err := workflowDB.PrepareWorkflowTemplate(s.validationStore, workflow)
	if err != nil {
		return db.WorkflowTemplate{}, db.WorkflowValidationResult{}, err
	}
	if !result.Valid {
		return db.WorkflowTemplate{}, result, nil
	}
	updated, err := s.repository.UpdateWorkflowTemplate(workflow)
	return updated, result, err
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
	if workflowAccessPolicyEqual(current.AccessPolicy, requested.AccessPolicy) {
		if requested.AccessPolicyRevision != current.AccessPolicyRevision || requested.AccessPolicy.Revision != current.AccessPolicy.Revision {
			return false, pro_interfaces.ErrWorkflowRevisionConflict
		}
	} else {
		if requested.AccessPolicyRevision != current.AccessPolicyRevision || requested.AccessPolicy.Revision != current.AccessPolicy.Revision {
			return false, pro_interfaces.ErrWorkflowRevisionConflict
		}
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
		if workflowApprovalPolicyEqual(previous.ApprovalRolePolicy, node.ApprovalRolePolicy) {
			if node.ApprovalRolePolicyRevision != previous.ApprovalRolePolicyRevision || node.ApprovalRolePolicy.Revision != previous.ApprovalRolePolicy.Revision {
				return false, pro_interfaces.ErrWorkflowRevisionConflict
			}
			continue
		}
		if node.ApprovalRolePolicyRevision != previous.ApprovalRolePolicyRevision || node.ApprovalRolePolicy.Revision != previous.ApprovalRolePolicy.Revision {
			return false, pro_interfaces.ErrWorkflowRevisionConflict
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
