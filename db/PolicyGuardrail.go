package db

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxPolicyGuardrailSourceBytes         = 64 * 1024
	MaxPolicyGuardrailCompiledBytes       = 256 * 1024
	MaxPolicyGuardrailEvaluationJSONBytes = 128 * 1024
	MaxPolicyGuardrailHistoryPage         = 100
	MaxPolicyGuardrailDecisionKeyBytes    = 128
	MaxPolicyGuardrailRollbackReasonBytes = 512
)

var (
	ErrPolicyGuardrailDraftRevisionConflict = errors.New("policy guardrail draft revision conflict")
	ErrPolicyGuardrailPublishConflict       = errors.New("policy guardrail publish conflict")
	ErrPolicyGuardrailTenantMismatch        = errors.New("policy guardrail scope does not belong to project")
	policyGuardrailFingerprintPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type PolicyGuardrailScope string

const (
	PolicyGuardrailScopeGlobal  PolicyGuardrailScope = "global"
	PolicyGuardrailScopeProject PolicyGuardrailScope = "project"
)

func ValidatePolicyGuardrailScope(scope PolicyGuardrailScope, projectID *int) error {
	switch scope {
	case PolicyGuardrailScopeGlobal:
		if projectID != nil {
			return errors.New("global policy guardrail scope cannot have a project")
		}
	case PolicyGuardrailScopeProject:
		if projectID == nil || *projectID <= 0 {
			return errors.New("project policy guardrail scope requires a project")
		}
	default:
		return errors.New("invalid policy guardrail scope")
	}
	return nil
}

func PolicyGuardrailScopeKey(scope PolicyGuardrailScope, projectID *int) (string, error) {
	if err := ValidatePolicyGuardrailScope(scope, projectID); err != nil {
		return "", err
	}
	if scope == PolicyGuardrailScopeGlobal {
		return "global", nil
	}
	return fmt.Sprintf("project:%d", *projectID), nil
}

type PolicyGuardrailDraft struct {
	ScopeKey       string               `db:"scope_key" json:"-"`
	Scope          PolicyGuardrailScope `db:"scope" json:"scope"`
	ProjectID      *int                 `db:"project_id" json:"project_id,omitempty"`
	SourceYAML     string               `db:"source_yaml" json:"source_yaml"`
	Revision       int                  `db:"revision" json:"revision"`
	ActiveRevision *int                 `db:"active_revision" json:"active_revision,omitempty"`
	UpdatedBy      int                  `db:"updated_by" json:"updated_by"`
	Created        time.Time            `db:"created" json:"created"`
	Updated        time.Time            `db:"updated" json:"updated"`
}

func (d PolicyGuardrailDraft) Validate() error {
	key, err := PolicyGuardrailScopeKey(d.Scope, d.ProjectID)
	if err != nil || d.ScopeKey != key || d.Revision <= 0 || d.UpdatedBy <= 0 ||
		len(d.SourceYAML) > MaxPolicyGuardrailSourceBytes || d.Created.IsZero() || d.Updated.IsZero() || d.Updated.Before(d.Created) {
		return errors.New("invalid policy guardrail draft")
	}
	if d.ActiveRevision != nil && *d.ActiveRevision <= 0 {
		return errors.New("invalid policy guardrail active revision")
	}
	return nil
}

type PolicyGuardrailRevision struct {
	ID                 int                  `db:"id" json:"id"`
	ScopeKey           string               `db:"scope_key" json:"-"`
	Scope              PolicyGuardrailScope `db:"scope" json:"scope"`
	ProjectID          *int                 `db:"project_id" json:"project_id,omitempty"`
	Revision           int                  `db:"revision" json:"revision"`
	ParentRevision     *int                 `db:"parent_revision" json:"parent_revision,omitempty"`
	RollbackOfRevision *int                 `db:"rollback_of_revision" json:"rollback_of_revision,omitempty"`
	RollbackReason     string               `db:"rollback_reason" json:"rollback_reason,omitempty"`
	SourceYAML         string               `db:"source_yaml" json:"source_yaml"`
	CompiledJSON       string               `db:"compiled_json" json:"-"`
	Fingerprint        string               `db:"fingerprint" json:"fingerprint"`
	CompilerVersion    int                  `db:"compiler_version" json:"compiler_version"`
	PublishedBy        int                  `db:"published_by" json:"published_by"`
	Created            time.Time            `db:"created" json:"created"`
}

func (r PolicyGuardrailRevision) Validate() error {
	key, err := PolicyGuardrailScopeKey(r.Scope, r.ProjectID)
	if err != nil || r.ID <= 0 || r.ScopeKey != key || r.Revision <= 0 || r.CompilerVersion <= 0 || r.PublishedBy <= 0 || r.Created.IsZero() ||
		len(r.SourceYAML) == 0 || len(r.SourceYAML) > MaxPolicyGuardrailSourceBytes || len(r.CompiledJSON) == 0 || len(r.CompiledJSON) > MaxPolicyGuardrailCompiledBytes ||
		!policyGuardrailFingerprintPattern.MatchString(r.Fingerprint) {
		return errors.New("invalid policy guardrail revision")
	}
	if r.Revision == 1 && r.ParentRevision != nil || r.Revision > 1 && (r.ParentRevision == nil || *r.ParentRevision != r.Revision-1) {
		return errors.New("invalid policy guardrail revision parent")
	}
	if r.RollbackOfRevision == nil {
		if r.RollbackReason != "" {
			return errors.New("policy guardrail rollback reason requires a rollback")
		}
		return nil
	}
	if *r.RollbackOfRevision <= 0 || *r.RollbackOfRevision >= r.Revision || strings.TrimSpace(r.RollbackReason) == "" ||
		strings.TrimSpace(r.RollbackReason) != r.RollbackReason || len(r.RollbackReason) > MaxPolicyGuardrailRollbackReasonBytes || strings.ContainsAny(r.RollbackReason, "\x00\r\n") {
		return errors.New("invalid policy guardrail rollback provenance")
	}
	return nil
}

type PolicyGuardrailDecision string

const (
	PolicyGuardrailDecisionAllow PolicyGuardrailDecision = "allow"
	PolicyGuardrailDecisionDeny  PolicyGuardrailDecision = "deny"
)

type PolicyGuardrailEvaluationRecord struct {
	ID                 int                     `db:"id" json:"id"`
	ProjectID          int                     `db:"project_id" json:"project_id"`
	DecisionKey        string                  `db:"decision_key" json:"-"`
	Intent             string                  `db:"intent" json:"intent"`
	Source             string                  `db:"source" json:"source"`
	TemplateID         *int                    `db:"template_id" json:"template_id,omitempty"`
	WorkflowTemplateID *int                    `db:"workflow_template_id" json:"workflow_template_id,omitempty"`
	WorkflowRunID      *int                    `db:"workflow_run_id" json:"workflow_run_id,omitempty"`
	WorkflowRunNodeID  *int                    `db:"workflow_run_node_id" json:"workflow_run_node_id,omitempty"`
	TaskID             *int                    `db:"task_id" json:"task_id,omitempty"`
	ActorUserID        *int                    `db:"actor_user_id" json:"-"`
	InputFingerprint   string                  `db:"input_fingerprint" json:"input_fingerprint"`
	RevisionsJSON      string                  `db:"revisions_json" json:"-"`
	FindingsJSON       string                  `db:"findings_json" json:"-"`
	Decision           PolicyGuardrailDecision `db:"decision" json:"decision"`
	EvaluatedAt        time.Time               `db:"evaluated_at" json:"evaluated_at"`
	Created            time.Time               `db:"created" json:"created"`
}

func (r PolicyGuardrailEvaluationRecord) Validate() error {
	if r.ID <= 0 || r.ProjectID <= 0 || strings.TrimSpace(r.DecisionKey) == "" || len(r.DecisionKey) > MaxPolicyGuardrailDecisionKeyBytes ||
		(r.Intent != "task" && r.Intent != "workflow") || strings.TrimSpace(r.Source) == "" ||
		!policyGuardrailFingerprintPattern.MatchString(r.InputFingerprint) || len(r.RevisionsJSON) == 0 || len(r.RevisionsJSON) > MaxPolicyGuardrailEvaluationJSONBytes ||
		len(r.FindingsJSON) == 0 || len(r.FindingsJSON) > MaxPolicyGuardrailEvaluationJSONBytes ||
		(r.Decision != PolicyGuardrailDecisionAllow && r.Decision != PolicyGuardrailDecisionDeny) || r.EvaluatedAt.IsZero() || r.Created.IsZero() {
		return errors.New("invalid policy guardrail evaluation")
	}
	if r.Intent == "task" && (r.TemplateID == nil || *r.TemplateID <= 0) || r.Intent == "workflow" && (r.WorkflowTemplateID == nil || *r.WorkflowTemplateID <= 0) {
		return errors.New("invalid policy guardrail evaluation target")
	}
	if r.ActorUserID != nil && *r.ActorUserID <= 0 || r.TaskID != nil && *r.TaskID <= 0 || r.WorkflowRunID != nil && *r.WorkflowRunID <= 0 || r.WorkflowRunNodeID != nil && *r.WorkflowRunNodeID <= 0 {
		return errors.New("invalid policy guardrail evaluation binding")
	}
	return nil
}
