package pro_interfaces

import (
	"errors"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

const MaxDeploymentWindowOverrideReferenceBytes = 128

type DeploymentWindowDecisionState string

const (
	DeploymentWindowDecisionAllowed    DeploymentWindowDecisionState = "allowed"
	DeploymentWindowDecisionBlocked    DeploymentWindowDecisionState = "blocked"
	DeploymentWindowDecisionOverridden DeploymentWindowDecisionState = "overridden"
)

type DeploymentWindowReason string

const (
	DeploymentWindowReasonAllowWindow  DeploymentWindowReason = "allow_window"
	DeploymentWindowReasonFreezeActive DeploymentWindowReason = "freeze_active"
	DeploymentWindowReasonDefaultAllow DeploymentWindowReason = "default_allow"
	DeploymentWindowReasonDefaultDeny  DeploymentWindowReason = "default_deny"
	DeploymentWindowReasonOverride     DeploymentWindowReason = "override"
)

type DeploymentWindowSource string

const (
	DeploymentWindowSourceManual       DeploymentWindowSource = "manual"
	DeploymentWindowSourceSchedule     DeploymentWindowSource = "schedule"
	DeploymentWindowSourceAPI          DeploymentWindowSource = "api"
	DeploymentWindowSourceWebhook      DeploymentWindowSource = "webhook"
	DeploymentWindowSourceWorkflowNode DeploymentWindowSource = "workflow_node"
	DeploymentWindowSourceIntegration  DeploymentWindowSource = "integration"
	// DeploymentWindowSourceAutorun is reserved for TaskRunner's internal
	// autorun pass. It is never a user-authenticated manual start and can never
	// carry an override.
	DeploymentWindowSourceAutorun DeploymentWindowSource = "autorun"
)

// DeploymentWindowOrigin distinguishes a transport-triggered request from a
// normal authenticated REST start. This prevents API/webhook source values
// from becoming a generic bypass for manually initiated task or workflow runs.
type DeploymentWindowOrigin string

const (
	DeploymentWindowOriginUser            DeploymentWindowOrigin = "user"
	DeploymentWindowOriginSchedule        DeploymentWindowOrigin = "schedule"
	DeploymentWindowOriginWorkflowTrigger DeploymentWindowOrigin = "workflow_trigger"
	DeploymentWindowOriginWorkflowNode    DeploymentWindowOrigin = "workflow_node"
	DeploymentWindowOriginIntegration     DeploymentWindowOrigin = "integration"
	DeploymentWindowOriginAutorun         DeploymentWindowOrigin = "autorun"
)

type DeploymentWindowOverrideCategory string

const (
	DeploymentWindowOverrideIncident       DeploymentWindowOverrideCategory = "incident"
	DeploymentWindowOverrideSecurity       DeploymentWindowOverrideCategory = "security"
	DeploymentWindowOverrideCustomerImpact DeploymentWindowOverrideCategory = "customer_impact"
)

type DeploymentWindowOverrideRequest struct {
	ActorID       int                              `json:"-"`
	Authenticated bool                             `json:"-"`
	Authorized    bool                             `json:"-"`
	Category      DeploymentWindowOverrideCategory `json:"category"`
	Reference     string                           `json:"reference"`
}

func (request DeploymentWindowOverrideRequest) Validate() error {
	if request.ActorID <= 0 || !request.Authenticated || !request.Authorized ||
		(request.Category != DeploymentWindowOverrideIncident && request.Category != DeploymentWindowOverrideSecurity && request.Category != DeploymentWindowOverrideCustomerImpact) ||
		!validDeploymentWindowOverrideReference(request.Reference) {
		return errors.New("deployment window override is invalid")
	}
	return nil
}

func validDeploymentWindowOverrideReference(value string) bool {
	if len(value) == 0 || len(value) > MaxDeploymentWindowOverrideReferenceBytes || strings.TrimSpace(value) != value {
		return false
	}
	parts := strings.Split(value, "-")
	if len(parts) != 2 || len(parts[0]) < 2 || len(parts[0]) > 16 || len(parts[1]) < 1 || len(parts[1]) > 10 {
		return false
	}
	for _, character := range parts[0] {
		if !(character >= 'A' && character <= 'Z' || character >= '0' && character <= '9') {
			return false
		}
	}
	for _, character := range parts[1] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return parts[1][0] != '0'
}

type DeploymentWindowEvaluationRequest struct {
	ProjectID  int                              `json:"-"`
	TemplateID int                              `json:"-"`
	WorkflowID int                              `json:"-"`
	Source     DeploymentWindowSource           `json:"-"`
	At         time.Time                        `json:"-"`
	Override   *DeploymentWindowOverrideRequest `json:"-"`
}

func (request DeploymentWindowEvaluationRequest) Validate() error {
	if request.ProjectID <= 0 || request.At.IsZero() ||
		(request.TemplateID <= 0 && request.WorkflowID <= 0) ||
		(request.Source != DeploymentWindowSourceManual && request.Source != DeploymentWindowSourceSchedule && request.Source != DeploymentWindowSourceAPI && request.Source != DeploymentWindowSourceWebhook && request.Source != DeploymentWindowSourceWorkflowNode && request.Source != DeploymentWindowSourceIntegration && request.Source != DeploymentWindowSourceAutorun) {
		return errors.New("deployment window evaluation request is invalid")
	}
	// A cross-project workflow node is consumer-scoped. It deliberately carries
	// no owner-template ID, so its admission can only be evaluated against the
	// consumer workflow policy.
	if request.Source == DeploymentWindowSourceWorkflowNode && request.WorkflowID <= 0 {
		return errors.New("deployment window evaluation request is invalid")
	}
	if request.Override != nil {
		if request.Source != DeploymentWindowSourceManual || request.Override.Validate() != nil {
			return errors.New("deployment window evaluation request is invalid")
		}
	}
	return nil
}

// DeploymentWindowAdmissionRequest has no caller-supplied timestamp. The
// repository obtains its evaluation instant from the database in the same
// transaction that inserts the immutable decision.
type DeploymentWindowAdmissionRequest struct {
	ProjectID         int                              `json:"-"`
	DecisionKey       string                           `json:"-"`
	Source            DeploymentWindowSource           `json:"-"`
	Origin            DeploymentWindowOrigin           `json:"-"`
	TemplateID        *int                             `json:"-"`
	WorkflowID        *int                             `json:"-"`
	ScheduleID        *int                             `json:"-"`
	TaskID            *int                             `json:"-"`
	WorkflowRunID     *int                             `json:"-"`
	WorkflowRunNodeID *int                             `json:"-"`
	ActorUserID       *int                             `json:"-"`
	Override          *DeploymentWindowOverrideRequest `json:"-"`
}

func (request DeploymentWindowAdmissionRequest) Validate() error {
	if request.ProjectID <= 0 || len(request.DecisionKey) == 0 || len(request.DecisionKey) > db.MaxDeploymentWindowDecisionKeyBytes || strings.TrimSpace(request.DecisionKey) != request.DecisionKey ||
		(request.TemplateID == nil && request.WorkflowID == nil) ||
		(request.TemplateID != nil && *request.TemplateID <= 0) || (request.WorkflowID != nil && *request.WorkflowID <= 0) ||
		(request.ScheduleID != nil && *request.ScheduleID <= 0) || (request.TaskID != nil && *request.TaskID <= 0) ||
		(request.WorkflowRunID != nil && *request.WorkflowRunID <= 0) || (request.WorkflowRunNodeID != nil && *request.WorkflowRunNodeID <= 0) ||
		(request.ActorUserID != nil && *request.ActorUserID <= 0) || !validDeploymentWindowSourceOrigin(request.Source, request.Origin) {
		return errors.New("deployment window admission request is invalid")
	}
	if request.Source == DeploymentWindowSourceWorkflowNode && request.WorkflowID == nil {
		return errors.New("deployment window admission request is invalid")
	}
	if (request.Source == DeploymentWindowSourceAPI || request.Source == DeploymentWindowSourceWebhook) && (request.WorkflowID == nil || request.TemplateID != nil) {
		return errors.New("deployment window admission request is invalid")
	}
	if request.Source == DeploymentWindowSourceManual && request.TemplateID != nil && request.WorkflowID != nil {
		return errors.New("deployment window admission request is invalid")
	}
	if request.Source == DeploymentWindowSourceAutorun && (request.TemplateID == nil || request.WorkflowID != nil) {
		return errors.New("deployment window admission request is invalid")
	}
	if request.Override != nil {
		if request.Source != DeploymentWindowSourceManual || request.Origin != DeploymentWindowOriginUser || request.ActorUserID == nil || request.Override.ActorID != *request.ActorUserID {
			return errors.New("deployment window admission request is invalid")
		}
		// Authorization is intentionally derived from the current membership by
		// the transactional repository, never trusted from this request.
		if request.Override.Category != DeploymentWindowOverrideIncident && request.Override.Category != DeploymentWindowOverrideSecurity && request.Override.Category != DeploymentWindowOverrideCustomerImpact || !validDeploymentWindowOverrideReference(request.Override.Reference) {
			return errors.New("deployment window admission request is invalid")
		}
	}
	return nil
}

func validDeploymentWindowSourceOrigin(source DeploymentWindowSource, origin DeploymentWindowOrigin) bool {
	switch source {
	case DeploymentWindowSourceManual:
		return origin == DeploymentWindowOriginUser
	case DeploymentWindowSourceSchedule:
		return origin == DeploymentWindowOriginSchedule
	case DeploymentWindowSourceAPI, DeploymentWindowSourceWebhook:
		return origin == DeploymentWindowOriginWorkflowTrigger
	case DeploymentWindowSourceWorkflowNode:
		return origin == DeploymentWindowOriginWorkflowNode
	case DeploymentWindowSourceIntegration:
		return origin == DeploymentWindowOriginIntegration
	case DeploymentWindowSourceAutorun:
		return origin == DeploymentWindowOriginAutorun
	default:
		return false
	}
}

// DeploymentWindowAdmissionClaim is stable after insertion. Inserted is false
// only when the project/source/origin/key tuple already has its immutable
// decision, making retried deliveries safe.
type DeploymentWindowAdmissionClaim struct {
	Decision db.DeploymentWindowDecisionRecord `json:"decision"`
	Inserted bool                              `json:"inserted"`
}

// DeploymentWindowPolicyRepository is the persistence boundary used by the
// admission service. Claim keeps policy loading, DB time, authorization and
// immutable decision insertion in one transaction.
type DeploymentWindowPolicyRepository interface {
	GetDeploymentWindowPolicy(projectID int) (db.DeploymentWindowPolicy, error)
	SaveDeploymentWindowPolicy(policy db.DeploymentWindowPolicy, expectedRevision int) (db.DeploymentWindowPolicy, error)
	DeleteDeploymentWindowPolicy(projectID int, expectedRevision int) error
	ClaimDeploymentWindowAdmission(request DeploymentWindowAdmissionRequest, evaluate func(db.DeploymentWindowPolicy, DeploymentWindowEvaluationRequest) (DeploymentWindowDecision, error)) (DeploymentWindowAdmissionClaim, error)
}

// DeploymentWindowAdmissionService is the sole use-case surface that later
// manual, scheduled and trigger start paths may call. Its implementation does
// not enqueue work; it returns the durable decision that an enqueue boundary
// must bind atomically to its own task or workflow-run mutation.
type DeploymentWindowAdmissionService interface {
	Claim(DeploymentWindowAdmissionRequest) (DeploymentWindowAdmissionClaim, error)
}

// DeploymentWindowAdmissionConfigurer attaches the optional Enhanced boundary
// without widening Community constructors or db.Store.
type DeploymentWindowAdmissionConfigurer interface {
	ConfigureDeploymentWindowAdmission(DeploymentWindowAdmissionService)
}

// DeploymentWindowBlockedError is deliberately coarse for start callers. Rule
// provenance remains private to the decision/audit layer.
type DeploymentWindowBlockedError struct {
	NextEligibleAt    *time.Time
	NextEligibleKnown bool
}

func (e *DeploymentWindowBlockedError) Error() string {
	return "deployment is blocked by the current deployment window"
}

type DeploymentWindowDecisionProvenance struct {
	PolicyRevision    int                           `json:"policy_revision,omitempty"`
	EffectiveTimezone string                        `json:"effective_timezone,omitempty"`
	EvaluatedAt       time.Time                     `json:"evaluated_at,omitempty"`
	MatchedRuleIDs    []int                         `json:"matched_rule_ids,omitempty"`
	MatchedRules      []DeploymentWindowMatchedRule `json:"matched_rules,omitempty"`
	OverrideActorID   int                           `json:"override_actor_id,omitempty"`
}

// DeploymentWindowMatchedRule is admin-only provenance. Public decisions
// intentionally omit it because names and scopes disclose deployment layout.
type DeploymentWindowMatchedRule struct {
	ID       int                     `json:"id"`
	Revision int                     `json:"revision"`
	Kind     db.DeploymentWindowKind `json:"kind"`
}

type DeploymentWindowDecision struct {
	State             DeploymentWindowDecisionState      `json:"state"`
	Reason            DeploymentWindowReason             `json:"reason"`
	NextEligibleAt    *time.Time                         `json:"next_eligible_at,omitempty"`
	NextEligibleKnown bool                               `json:"next_eligible_known"`
	Provenance        DeploymentWindowDecisionProvenance `json:"-"`
}

// DeploymentWindowPublicDecision is intentionally coarse: callers that may
// start one target do not learn policy revision, rule scopes, timezone, or an
// override actor.
type DeploymentWindowPublicDecision struct {
	State             DeploymentWindowDecisionState      `json:"state"`
	Reason            DeploymentWindowReason             `json:"reason"`
	NextEligibleAt    *time.Time                         `json:"next_eligible_at,omitempty"`
	NextEligibleKnown bool                               `json:"next_eligible_known"`
	Provenance        DeploymentWindowDecisionProvenance `json:"-"`
}

type DeploymentWindowAdminDecision struct {
	State             DeploymentWindowDecisionState      `json:"state"`
	Reason            DeploymentWindowReason             `json:"reason"`
	NextEligibleAt    *time.Time                         `json:"next_eligible_at,omitempty"`
	NextEligibleKnown bool                               `json:"next_eligible_known"`
	Provenance        DeploymentWindowDecisionProvenance `json:"provenance"`
}

func (decision DeploymentWindowDecision) PublicView() DeploymentWindowPublicDecision {
	return DeploymentWindowPublicDecision{State: decision.State, Reason: decision.Reason, NextEligibleAt: decision.NextEligibleAt, NextEligibleKnown: decision.NextEligibleKnown}
}

func (decision DeploymentWindowDecision) AdminView() DeploymentWindowAdminDecision {
	return DeploymentWindowAdminDecision{State: decision.State, Reason: decision.Reason, NextEligibleAt: decision.NextEligibleAt, NextEligibleKnown: decision.NextEligibleKnown, Provenance: decision.Provenance}
}
