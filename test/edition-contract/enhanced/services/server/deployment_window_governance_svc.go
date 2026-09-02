package server

import (
	"context"
	"encoding/json"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/deployment_windows"
)

// deploymentWindowGovernanceService contains the bounded project-settings
// operations. It does not enqueue work and it deliberately does not implement
// emergency override admission; those remain at the final start boundary.
type deploymentWindowGovernanceService struct {
	repository pro_interfaces.DeploymentWindowGovernanceRepository
	evaluator  *deployment_windows.Evaluator
}

var _ pro_interfaces.DeploymentWindowGovernanceServiceFacade = (*deploymentWindowGovernanceService)(nil)

func NewDeploymentWindowGovernanceService(repository pro_interfaces.DeploymentWindowGovernanceRepository) pro_interfaces.DeploymentWindowGovernanceServiceFacade {
	return &deploymentWindowGovernanceService{
		repository: repository,
		evaluator:  deployment_windows.NewEvaluator(deployment_windows.WithTimezoneValidator(validateAdmissionTimezone)),
	}
}

func (s *deploymentWindowGovernanceService) GetPolicy(ctx context.Context, projectID int) (db.DeploymentWindowPolicy, error) {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	if s == nil || s.repository == nil || projectID <= 0 {
		return db.DeploymentWindowPolicy{}, db.ErrInvalidOperation
	}
	return s.repository.GetDeploymentWindowPolicy(projectID)
}

func (s *deploymentWindowGovernanceService) SavePolicy(ctx context.Context, policy db.DeploymentWindowPolicy, expectedRevision int) (db.DeploymentWindowPolicy, error) {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	if s == nil || s.repository == nil || expectedRevision <= 0 || policy.ValidateDraft(validateAdmissionTimezone) != nil {
		return db.DeploymentWindowPolicy{}, db.ErrInvalidOperation
	}
	return s.repository.SaveDeploymentWindowPolicy(policy, expectedRevision)
}

func (s *deploymentWindowGovernanceService) ResetPolicy(ctx context.Context, projectID int, expectedRevision int) error {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return err
	}
	if s == nil || s.repository == nil || projectID <= 0 || expectedRevision <= 0 {
		return db.ErrInvalidOperation
	}
	return s.repository.DeleteDeploymentWindowPolicy(projectID, expectedRevision)
}

func (s *deploymentWindowGovernanceService) CurrentStatus(ctx context.Context, request pro_interfaces.DeploymentWindowStatusRequest) (pro_interfaces.DeploymentWindowAdminDecision, error) {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, err
	}
	if s == nil || s.repository == nil || s.evaluator == nil || request.Validate() != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, db.ErrInvalidOperation
	}
	decision, err := s.repository.EvaluateDeploymentWindowStatus(request, s.evaluator.Evaluate)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, err
	}
	return decision.AdminView(), nil
}

func (s *deploymentWindowGovernanceService) Preview(ctx context.Context, policy db.DeploymentWindowPolicy, request pro_interfaces.DeploymentWindowStatusRequest) (pro_interfaces.DeploymentWindowAdminDecision, error) {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, err
	}
	if s == nil || s.repository == nil || s.evaluator == nil || policy.ProjectID != request.ProjectID || policy.ValidateDraft(validateAdmissionTimezone) != nil || request.Validate() != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, db.ErrInvalidOperation
	}
	decision, err := s.repository.PreviewDeploymentWindowPolicy(policy, request, s.evaluator.Evaluate)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdminDecision{}, err
	}
	return decision.AdminView(), nil
}

func (s *deploymentWindowGovernanceService) DecisionHistory(ctx context.Context, projectID int, params db.RetrieveQueryParams) ([]pro_interfaces.DeploymentWindowDecisionHistoryDTO, error) {
	if err := deploymentWindowGovernanceContext(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil || projectID <= 0 || params.Offset < 0 || params.Count < 0 || params.Count > db.MaxDeploymentWindowHistoryPage || params.BeforeID < 0 {
		return nil, db.ErrInvalidOperation
	}
	records, err := s.repository.GetDeploymentWindowDecisionHistory(projectID, params)
	if err != nil {
		return nil, err
	}
	history := make([]pro_interfaces.DeploymentWindowDecisionHistoryDTO, len(records))
	for index, record := range records {
		entry, projectionErr := deploymentWindowDecisionHistoryDTO(record)
		if projectionErr != nil {
			return nil, projectionErr
		}
		history[index] = entry
	}
	return history, nil
}

func deploymentWindowDecisionHistoryDTO(record db.DeploymentWindowDecisionRecord) (pro_interfaces.DeploymentWindowDecisionHistoryDTO, error) {
	if record.ID <= 0 || record.ProjectID <= 0 || record.EvaluatedAt.IsZero() || record.Created.IsZero() ||
		!validDeploymentWindowHistorySource(record.Source) || !validDeploymentWindowHistoryOrigin(record.Origin) ||
		!validDeploymentWindowHistoryState(record.State) || !validDeploymentWindowHistoryReason(record.Reason) {
		return pro_interfaces.DeploymentWindowDecisionHistoryDTO{}, db.ErrInvalidOperation
	}
	matchedRules := []pro_interfaces.DeploymentWindowMatchedRule{}
	if err := json.Unmarshal([]byte(record.MatchedRulesJSON), &matchedRules); err != nil {
		return pro_interfaces.DeploymentWindowDecisionHistoryDTO{}, db.ErrInvalidOperation
	}
	matchedRuleIDs := make([]int, len(matchedRules))
	for index, rule := range matchedRules {
		if rule.ID <= 0 || rule.Revision <= 0 || (rule.Kind != db.DeploymentWindowAllow && rule.Kind != db.DeploymentWindowFreeze) {
			return pro_interfaces.DeploymentWindowDecisionHistoryDTO{}, db.ErrInvalidOperation
		}
		matchedRuleIDs[index] = rule.ID
	}
	provenance := pro_interfaces.DeploymentWindowDecisionProvenance{
		PolicyRevision: record.PolicyRevision, EffectiveTimezone: record.EffectiveTimezone, EvaluatedAt: record.EvaluatedAt,
		MatchedRuleIDs: matchedRuleIDs, MatchedRules: matchedRules,
	}
	if record.OverrideActorID != nil {
		provenance.OverrideActorID = *record.OverrideActorID
	}
	decision := pro_interfaces.DeploymentWindowDecision{
		State: pro_interfaces.DeploymentWindowDecisionState(record.State), Reason: pro_interfaces.DeploymentWindowReason(record.Reason),
		NextEligibleAt: record.NextEligibleAt, NextEligibleKnown: record.NextEligibleKnown, Provenance: provenance,
	}
	return pro_interfaces.DeploymentWindowDecisionHistoryDTO{
		ID: record.ID, Source: pro_interfaces.DeploymentWindowSource(record.Source), Origin: pro_interfaces.DeploymentWindowOrigin(record.Origin),
		TemplateID: record.TemplateID, WorkflowID: record.WorkflowTemplateID, ScheduleID: record.ScheduleID, TaskID: record.TaskID,
		WorkflowRunID: record.WorkflowRunID, WorkflowRunNodeID: record.WorkflowRunNodeID, Decision: decision.AdminView(), CreatedAt: record.Created,
	}, nil
}

func deploymentWindowGovernanceContext(ctx context.Context) error {
	if ctx == nil {
		return db.ErrInvalidOperation
	}
	return ctx.Err()
}

func validDeploymentWindowHistorySource(value string) bool {
	switch pro_interfaces.DeploymentWindowSource(value) {
	case pro_interfaces.DeploymentWindowSourceManual, pro_interfaces.DeploymentWindowSourceSchedule, pro_interfaces.DeploymentWindowSourceAPI,
		pro_interfaces.DeploymentWindowSourceWebhook, pro_interfaces.DeploymentWindowSourceWorkflowNode, pro_interfaces.DeploymentWindowSourceIntegration,
		pro_interfaces.DeploymentWindowSourceAutorun:
		return true
	default:
		return false
	}
}

func validDeploymentWindowHistoryOrigin(value string) bool {
	switch pro_interfaces.DeploymentWindowOrigin(value) {
	case pro_interfaces.DeploymentWindowOriginUser, pro_interfaces.DeploymentWindowOriginSchedule, pro_interfaces.DeploymentWindowOriginWorkflowTrigger,
		pro_interfaces.DeploymentWindowOriginWorkflowNode, pro_interfaces.DeploymentWindowOriginIntegration, pro_interfaces.DeploymentWindowOriginAutorun:
		return true
	default:
		return false
	}
}

func validDeploymentWindowHistoryState(value string) bool {
	return value == string(pro_interfaces.DeploymentWindowDecisionAllowed) || value == string(pro_interfaces.DeploymentWindowDecisionBlocked) || value == string(pro_interfaces.DeploymentWindowDecisionOverridden)
}

func validDeploymentWindowHistoryReason(value string) bool {
	switch pro_interfaces.DeploymentWindowReason(value) {
	case pro_interfaces.DeploymentWindowReasonAllowWindow, pro_interfaces.DeploymentWindowReasonFreezeActive, pro_interfaces.DeploymentWindowReasonDefaultAllow,
		pro_interfaces.DeploymentWindowReasonDefaultDeny, pro_interfaces.DeploymentWindowReasonOverride:
		return true
	default:
		return false
	}
}
