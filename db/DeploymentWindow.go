package db

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/robfig/cron/v3"
)

const (
	MaxDeploymentWindowRules            = 64
	MaxDeploymentWindowDurationMinutes  = 24 * 60
	MaxDeploymentWindowRuleNameBytes    = 255
	MaxDeploymentWindowRecurrenceBytes  = 255
	MaxDeploymentWindowDecisionKeyBytes = 128
	// MaxDeploymentWindowHistoryPage bounds the immutable admission history
	// returned to governance callers. Cursor pagination remains available for
	// older records without widening a project-scoped query.
	MaxDeploymentWindowHistoryPage = 100
)

var (
	// ErrDeploymentWindowRevisionConflict reports an optimistic-concurrency
	// conflict. It is deliberately stable so callers can require a refresh
	// rather than retrying a stale policy write.
	ErrDeploymentWindowRevisionConflict = errors.New("deployment window policy revision conflict")
	// ErrDeploymentWindowTenantMismatch reports a referenced target that is not
	// owned by the policy project. It must not be translated into a permissive
	// project lookup.
	ErrDeploymentWindowTenantMismatch = errors.New("deployment window target does not belong to project")
)

type DeploymentWindowKind string

const (
	DeploymentWindowAllow  DeploymentWindowKind = "allow"
	DeploymentWindowFreeze DeploymentWindowKind = "freeze"
)

type DeploymentWindowScope string

const (
	DeploymentWindowProjectScope  DeploymentWindowScope = "project"
	DeploymentWindowTemplateScope DeploymentWindowScope = "template"
	DeploymentWindowWorkflowScope DeploymentWindowScope = "workflow"
)

type DeploymentWindowDefault string

const (
	DeploymentWindowDefaultAllow DeploymentWindowDefault = "allow"
	DeploymentWindowDefaultDeny  DeploymentWindowDefault = "deny"
)

// DeploymentWindowPolicy is the versioned, project-owned definition used at
// the final execution-admission boundary. Its timezone is intentionally
// policy-wide so overlapping rules cannot be evaluated under mixed zones.
type DeploymentWindowPolicy struct {
	ProjectID int                     `db:"project_id" json:"project_id"`
	Revision  int                     `db:"revision" json:"revision"`
	Timezone  string                  `db:"timezone" json:"timezone"`
	Default   DeploymentWindowDefault `db:"default_decision" json:"default"`
	Rules     []DeploymentWindowRule  `db:"-" json:"rules"`
}

type DeploymentWindowRule struct {
	ID              int                   `db:"id" json:"id"`
	Revision        int                   `db:"policy_revision" json:"revision"`
	Name            string                `db:"name" json:"name"`
	Active          bool                  `db:"active" json:"active"`
	Kind            DeploymentWindowKind  `db:"kind" json:"kind"`
	Scope           DeploymentWindowScope `db:"scope" json:"scope"`
	TemplateID      *int                  `db:"template_id" json:"template_id,omitempty"`
	WorkflowID      *int                  `db:"workflow_template_id" json:"workflow_id,omitempty"`
	Recurrence      string                `db:"recurrence" json:"recurrence"`
	DurationMinutes int                   `db:"duration_minutes" json:"duration_minutes"`
	EffectiveFrom   *string               `db:"effective_from" json:"effective_from,omitempty"`
	EffectiveUntil  *string               `db:"effective_until" json:"effective_until,omitempty"`
}

// DeploymentWindowDecisionRecord is the immutable persistence shape for an
// admission result. Target and execution-link IDs are pointers because the
// decision is created before an eventual task or workflow run exists.
// Provenance stays serialized privately by the Enhanced repository.
type DeploymentWindowDecisionRecord struct {
	ID                 int        `db:"id" json:"id"`
	ProjectID          int        `db:"project_id" json:"project_id"`
	DecisionKey        string     `db:"decision_key" json:"-"`
	Source             string     `db:"source" json:"source"`
	Origin             string     `db:"origin" json:"origin"`
	TemplateID         *int       `db:"template_id" json:"template_id,omitempty"`
	WorkflowTemplateID *int       `db:"workflow_template_id" json:"workflow_template_id,omitempty"`
	ScheduleID         *int       `db:"schedule_id" json:"schedule_id,omitempty"`
	TaskID             *int       `db:"task_id" json:"task_id,omitempty"`
	WorkflowRunID      *int       `db:"workflow_run_id" json:"workflow_run_id,omitempty"`
	WorkflowRunNodeID  *int       `db:"workflow_run_node_id" json:"workflow_run_node_id,omitempty"`
	ActorUserID        *int       `db:"actor_user_id" json:"-"`
	PolicyRevision     int        `db:"policy_revision" json:"-"`
	EffectiveTimezone  string     `db:"effective_timezone" json:"-"`
	EvaluatedAt        time.Time  `db:"evaluated_at" json:"-"`
	State              string     `db:"state" json:"state"`
	Reason             string     `db:"reason" json:"reason"`
	NextEligibleAt     *time.Time `db:"next_eligible_at" json:"next_eligible_at,omitempty"`
	NextEligibleKnown  bool       `db:"next_eligible_known" json:"next_eligible_known"`
	OverrideActorID    *int       `db:"override_actor_user_id" json:"-"`
	OverrideCategory   *string    `db:"override_category" json:"-"`
	OverrideReference  *string    `db:"override_reference" json:"-"`
	MatchedRulesJSON   string     `db:"matched_rules" json:"-"`
	Created            time.Time  `db:"created" json:"-"`
}

// ValidateTimezoneFunc is injected by a caller that owns the installed IANA
// database. This avoids a db-to-services import cycle while keeping all
// persisted policy validation backend-authoritative.
type ValidateTimezoneFunc func(string) error

func (policy DeploymentWindowPolicy) Validate(validateTimezone ValidateTimezoneFunc) error {
	if policy.ProjectID <= 0 || policy.Revision <= 0 || validateTimezone == nil || !validDeploymentWindowTimezone(policy.Timezone) || validateTimezone(policy.Timezone) != nil ||
		(policy.Default != DeploymentWindowDefaultAllow && policy.Default != DeploymentWindowDefaultDeny) ||
		len(policy.Rules) > MaxDeploymentWindowRules {
		return errors.New("deployment window policy is invalid")
	}
	ids := make(map[int]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if _, found := ids[rule.ID]; found {
			return errors.New("deployment window rule is invalid")
		}
		ids[rule.ID] = struct{}{}
	}
	return nil
}

// ValidateDraft accepts rules before the SQL repository has assigned their
// durable IDs. Persisted policy reads must still use Validate, which requires
// positive immutable rule IDs.
func (policy DeploymentWindowPolicy) ValidateDraft(validateTimezone ValidateTimezoneFunc) error {
	copy := policy
	copy.Rules = append([]DeploymentWindowRule(nil), policy.Rules...)
	seen := make(map[int]struct{}, len(copy.Rules))
	for index := range copy.Rules {
		if copy.Rules[index].ID < 0 {
			return errors.New("deployment window rule is invalid")
		}
		if copy.Rules[index].ID == 0 {
			copy.Rules[index].ID = -(index + 1)
		}
		if copy.Rules[index].Revision == 0 {
			copy.Rules[index].Revision = copy.Revision
		}
		if _, exists := seen[copy.Rules[index].ID]; exists {
			return errors.New("deployment window rule is invalid")
		}
		seen[copy.Rules[index].ID] = struct{}{}
		if err := validateDeploymentWindowRule(copy.Rules[index], true); err != nil {
			return err
		}
	}
	if copy.ProjectID <= 0 || copy.Revision <= 0 || validateTimezone == nil || !validDeploymentWindowTimezone(copy.Timezone) || validateTimezone(copy.Timezone) != nil ||
		(copy.Default != DeploymentWindowDefaultAllow && copy.Default != DeploymentWindowDefaultDeny) || len(copy.Rules) > MaxDeploymentWindowRules {
		return errors.New("deployment window policy is invalid")
	}
	return nil
}

func validDeploymentWindowTimezone(timezone string) bool {
	if timezone == "UTC" {
		return true
	}
	if len(timezone) == 0 || len(timezone) > 128 || strings.TrimSpace(timezone) != timezone || !strings.Contains(timezone, "/") ||
		strings.Contains(timezone, "..") || strings.HasPrefix(timezone, "/") || strings.HasSuffix(timezone, "/") || strings.Contains(timezone, "//") {
		return false
	}
	for _, character := range timezone {
		if unicode.IsControl(character) || !(unicode.IsLetter(character) || unicode.IsDigit(character) || character == '/' || character == '_' || character == '+' || character == '-') {
			return false
		}
	}
	return true
}

func (rule DeploymentWindowRule) Validate() error {
	return validateDeploymentWindowRule(rule, false)
}

func validateDeploymentWindowRule(rule DeploymentWindowRule, draft bool) error {
	if (!draft && rule.ID <= 0) || (draft && rule.ID == 0) || rule.Revision <= 0 || len(rule.Name) == 0 || len(rule.Name) > MaxDeploymentWindowRuleNameBytes || strings.TrimSpace(rule.Name) != rule.Name ||
		(rule.Kind != DeploymentWindowAllow && rule.Kind != DeploymentWindowFreeze) ||
		(rule.Scope != DeploymentWindowProjectScope && rule.Scope != DeploymentWindowTemplateScope && rule.Scope != DeploymentWindowWorkflowScope) ||
		rule.DurationMinutes <= 0 || rule.DurationMinutes > MaxDeploymentWindowDurationMinutes ||
		len(rule.Recurrence) > MaxDeploymentWindowRecurrenceBytes ||
		!validDeploymentWindowRecurrence(rule.Recurrence) {
		return errors.New("deployment window rule is invalid")
	}
	switch rule.Scope {
	case DeploymentWindowProjectScope:
		if rule.TemplateID != nil || rule.WorkflowID != nil {
			return errors.New("deployment window rule is invalid")
		}
	case DeploymentWindowTemplateScope:
		if rule.TemplateID == nil || *rule.TemplateID <= 0 || rule.WorkflowID != nil {
			return errors.New("deployment window rule is invalid")
		}
	case DeploymentWindowWorkflowScope:
		if rule.WorkflowID == nil || *rule.WorkflowID <= 0 || rule.TemplateID != nil {
			return errors.New("deployment window rule is invalid")
		}
	}
	from, err := deploymentWindowDate(rule.EffectiveFrom)
	if err != nil {
		return errors.New("deployment window rule is invalid")
	}
	until, err := deploymentWindowDate(rule.EffectiveUntil)
	if err != nil || from != nil && until != nil && until.Before(*from) {
		return errors.New("deployment window rule is invalid")
	}
	return nil
}

func validDeploymentWindowRecurrence(value string) bool {
	fields := strings.Fields(value)
	if len(fields) != 5 || strings.Join(fields, " ") != value {
		return false
	}
	_, err := cron.ParseStandard(value)
	return err == nil
}

func deploymentWindowDate(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", *value)
	if err != nil || parsed.Format("2006-01-02") != *value {
		return nil, errors.New("invalid deployment window date")
	}
	return &parsed, nil
}
