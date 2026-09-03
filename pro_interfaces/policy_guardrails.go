package pro_interfaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

const (
	PolicyGuardrailSchemaVersion          = 1
	PolicyGuardrailCompilerVersion        = 1
	MaxPolicyGuardrailYAMLBytes           = 64 * 1024
	MaxPolicyGuardrailRules               = 32
	MaxPolicyGuardrailPredicates          = 256
	MaxPolicyGuardrailPredicatesPerRule   = 32
	MaxPolicyGuardrailValues              = 32
	MaxPolicyGuardrailValueBytes          = 256
	MaxPolicyGuardrailMessageBytes        = 512
	MaxPolicyGuardrailRemediationBytes    = 2048
	MaxPolicyGuardrailFindings            = 64
	MaxPolicyGuardrailMetadataItems       = 256
	MaxPolicyGuardrailRollbackReasonBytes = 512
)

var policyGuardrailRuleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type PolicyGuardrailScope string

const (
	PolicyGuardrailScopeGlobal  PolicyGuardrailScope = "global"
	PolicyGuardrailScopeProject PolicyGuardrailScope = "project"
)

type PolicyGuardrailEffect string

const (
	PolicyGuardrailEffectAllow PolicyGuardrailEffect = "allow"
	PolicyGuardrailEffectWarn  PolicyGuardrailEffect = "warn"
	PolicyGuardrailEffectDeny  PolicyGuardrailEffect = "deny"
)

type PolicyGuardrailSeverity string

const (
	PolicyGuardrailSeverityInfo     PolicyGuardrailSeverity = "info"
	PolicyGuardrailSeverityLow      PolicyGuardrailSeverity = "low"
	PolicyGuardrailSeverityMedium   PolicyGuardrailSeverity = "medium"
	PolicyGuardrailSeverityHigh     PolicyGuardrailSeverity = "high"
	PolicyGuardrailSeverityCritical PolicyGuardrailSeverity = "critical"
)

type PolicyGuardrailMatch string

const (
	PolicyGuardrailMatchAll PolicyGuardrailMatch = "all"
	PolicyGuardrailMatchAny PolicyGuardrailMatch = "any"
)

type PolicyGuardrailOperator string

const (
	PolicyGuardrailOperatorEquals      PolicyGuardrailOperator = "equals"
	PolicyGuardrailOperatorOneOf       PolicyGuardrailOperator = "one_of"
	PolicyGuardrailOperatorPresent     PolicyGuardrailOperator = "present"
	PolicyGuardrailOperatorAbsent      PolicyGuardrailOperator = "absent"
	PolicyGuardrailOperatorContainsAny PolicyGuardrailOperator = "contains_any"
	PolicyGuardrailOperatorContainsAll PolicyGuardrailOperator = "contains_all"
	PolicyGuardrailOperatorLessThan    PolicyGuardrailOperator = "lt"
	PolicyGuardrailOperatorAtMost      PolicyGuardrailOperator = "lte"
	PolicyGuardrailOperatorGreaterThan PolicyGuardrailOperator = "gt"
	PolicyGuardrailOperatorAtLeast     PolicyGuardrailOperator = "gte"
)

type PolicyGuardrailField string

const (
	PolicyFieldTaskTemplateID           PolicyGuardrailField = "task.template_id"
	PolicyFieldTaskApplication          PolicyGuardrailField = "task.application"
	PolicyFieldTaskSource               PolicyGuardrailField = "task.source"
	PolicyFieldTaskInventoryOverride    PolicyGuardrailField = "task.inventory_override"
	PolicyFieldTaskBranchOverride       PolicyGuardrailField = "task.branch_override"
	PolicyFieldTaskCommitOverride       PolicyGuardrailField = "task.commit_override"
	PolicyFieldTaskArgumentKeyCount     PolicyGuardrailField = "task.argument_key_count"
	PolicyFieldTaskInputKeyCount        PolicyGuardrailField = "task.input_key_count"
	PolicyFieldInventoryPresent         PolicyGuardrailField = "inventory.present"
	PolicyFieldInventoryID              PolicyGuardrailField = "inventory.id"
	PolicyFieldInventoryType            PolicyGuardrailField = "inventory.type"
	PolicyFieldInventoryRunnerTagCount  PolicyGuardrailField = "inventory.runner_tag_count"
	PolicyFieldEnvironmentIDs           PolicyGuardrailField = "environment.ids"
	PolicyFieldEnvironmentCount         PolicyGuardrailField = "environment.count"
	PolicyFieldWorkflowPresent          PolicyGuardrailField = "workflow.present"
	PolicyFieldWorkflowID               PolicyGuardrailField = "workflow.id"
	PolicyFieldWorkflowRevision         PolicyGuardrailField = "workflow.revision"
	PolicyFieldWorkflowNodeID           PolicyGuardrailField = "workflow.node_id"
	PolicyFieldWorkflowNodeKind         PolicyGuardrailField = "workflow.node_kind"
	PolicyFieldWorkflowTriggerSource    PolicyGuardrailField = "workflow.trigger_source"
	PolicyFieldWorkflowCrossProject     PolicyGuardrailField = "workflow.cross_project"
	PolicyFieldRunnerSelectedID         PolicyGuardrailField = "runner.selected_id"
	PolicyFieldRunnerSelectedScope      PolicyGuardrailField = "runner.selected_scope"
	PolicyFieldRunnerSelectedExecutor   PolicyGuardrailField = "runner.selected_executor"
	PolicyFieldRunnerRequestedTags      PolicyGuardrailField = "runner.requested_tags"
	PolicyFieldRunnerCandidateCount     PolicyGuardrailField = "runner.candidate_count"
	PolicyFieldExecutorType             PolicyGuardrailField = "executor.type"
	PolicyFieldImagePresent             PolicyGuardrailField = "image.present"
	PolicyFieldImageReferenceKind       PolicyGuardrailField = "image.reference_kind"
	PolicyFieldImageDigest              PolicyGuardrailField = "image.digest"
	PolicyFieldCredentialIDs            PolicyGuardrailField = "credential.ids"
	PolicyFieldCredentialScopes         PolicyGuardrailField = "credential.scopes"
	PolicyFieldCredentialBindingTargets PolicyGuardrailField = "credential.binding_targets"
	PolicyFieldCredentialCount          PolicyGuardrailField = "credential.count"
	PolicyFieldTimeWeekday              PolicyGuardrailField = "time.weekday"
	PolicyFieldTimeMinuteOfDay          PolicyGuardrailField = "time.minute_of_day"
)

type policyGuardrailValueKind uint8

const (
	policyGuardrailString policyGuardrailValueKind = iota + 1
	policyGuardrailInteger
	policyGuardrailBoolean
	policyGuardrailStrings
	policyGuardrailIntegers
)

var policyGuardrailFieldKinds = map[PolicyGuardrailField]policyGuardrailValueKind{
	PolicyFieldTaskTemplateID:           policyGuardrailInteger,
	PolicyFieldTaskApplication:          policyGuardrailString,
	PolicyFieldTaskSource:               policyGuardrailString,
	PolicyFieldTaskInventoryOverride:    policyGuardrailBoolean,
	PolicyFieldTaskBranchOverride:       policyGuardrailBoolean,
	PolicyFieldTaskCommitOverride:       policyGuardrailBoolean,
	PolicyFieldTaskArgumentKeyCount:     policyGuardrailInteger,
	PolicyFieldTaskInputKeyCount:        policyGuardrailInteger,
	PolicyFieldInventoryPresent:         policyGuardrailBoolean,
	PolicyFieldInventoryID:              policyGuardrailInteger,
	PolicyFieldInventoryType:            policyGuardrailString,
	PolicyFieldInventoryRunnerTagCount:  policyGuardrailInteger,
	PolicyFieldEnvironmentIDs:           policyGuardrailIntegers,
	PolicyFieldEnvironmentCount:         policyGuardrailInteger,
	PolicyFieldWorkflowPresent:          policyGuardrailBoolean,
	PolicyFieldWorkflowID:               policyGuardrailInteger,
	PolicyFieldWorkflowRevision:         policyGuardrailInteger,
	PolicyFieldWorkflowNodeID:           policyGuardrailInteger,
	PolicyFieldWorkflowNodeKind:         policyGuardrailString,
	PolicyFieldWorkflowTriggerSource:    policyGuardrailString,
	PolicyFieldWorkflowCrossProject:     policyGuardrailBoolean,
	PolicyFieldRunnerSelectedID:         policyGuardrailInteger,
	PolicyFieldRunnerSelectedScope:      policyGuardrailString,
	PolicyFieldRunnerSelectedExecutor:   policyGuardrailString,
	PolicyFieldRunnerRequestedTags:      policyGuardrailStrings,
	PolicyFieldRunnerCandidateCount:     policyGuardrailInteger,
	PolicyFieldExecutorType:             policyGuardrailString,
	PolicyFieldImagePresent:             policyGuardrailBoolean,
	PolicyFieldImageReferenceKind:       policyGuardrailString,
	PolicyFieldImageDigest:              policyGuardrailString,
	PolicyFieldCredentialIDs:            policyGuardrailIntegers,
	PolicyFieldCredentialScopes:         policyGuardrailStrings,
	PolicyFieldCredentialBindingTargets: policyGuardrailStrings,
	PolicyFieldCredentialCount:          policyGuardrailInteger,
	PolicyFieldTimeWeekday:              policyGuardrailString,
	PolicyFieldTimeMinuteOfDay:          policyGuardrailInteger,
}

type PolicyGuardrailDocument struct {
	Version int                   `json:"version" yaml:"version"`
	Rules   []PolicyGuardrailRule `json:"rules" yaml:"rules"`
}

type PolicyGuardrailRule struct {
	ID             string                     `json:"id" yaml:"id"`
	Effect         PolicyGuardrailEffect      `json:"effect" yaml:"effect"`
	Severity       PolicyGuardrailSeverity    `json:"severity" yaml:"severity"`
	Message        string                     `json:"message" yaml:"message"`
	RemediationURL string                     `json:"remediation_url,omitempty" yaml:"remediation_url,omitempty"`
	Match          PolicyGuardrailMatch       `json:"match" yaml:"match"`
	Conditions     []PolicyGuardrailCondition `json:"conditions" yaml:"conditions"`
}

type PolicyGuardrailCondition struct {
	Field         PolicyGuardrailField    `json:"field" yaml:"field"`
	Operator      PolicyGuardrailOperator `json:"operator" yaml:"operator"`
	StringValue   *string                 `json:"string_value,omitempty" yaml:"string_value,omitempty"`
	IntegerValue  *int                    `json:"integer_value,omitempty" yaml:"integer_value,omitempty"`
	BooleanValue  *bool                   `json:"boolean_value,omitempty" yaml:"boolean_value,omitempty"`
	StringValues  []string                `json:"string_values,omitempty" yaml:"string_values,omitempty"`
	IntegerValues []int                   `json:"integer_values,omitempty" yaml:"integer_values,omitempty"`
}

func (d PolicyGuardrailDocument) Validate() error {
	if d.Version != PolicyGuardrailSchemaVersion || len(d.Rules) > MaxPolicyGuardrailRules {
		return errors.New("invalid policy guardrail document")
	}
	seen := make(map[string]struct{}, len(d.Rules))
	total := 0
	for _, rule := range d.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[rule.ID]; duplicate {
			return errors.New("policy guardrail rule IDs must be unique")
		}
		seen[rule.ID] = struct{}{}
		total += len(rule.Conditions)
		if total > MaxPolicyGuardrailPredicates {
			return errors.New("policy guardrail predicate limit exceeded")
		}
	}
	return nil
}

func (r PolicyGuardrailRule) Validate() error {
	if !policyGuardrailRuleIDPattern.MatchString(r.ID) {
		return errors.New("invalid policy guardrail rule ID")
	}
	if r.Effect != PolicyGuardrailEffectAllow && r.Effect != PolicyGuardrailEffectWarn && r.Effect != PolicyGuardrailEffectDeny {
		return errors.New("invalid policy guardrail effect")
	}
	if r.Severity != PolicyGuardrailSeverityInfo && r.Severity != PolicyGuardrailSeverityLow &&
		r.Severity != PolicyGuardrailSeverityMedium && r.Severity != PolicyGuardrailSeverityHigh &&
		r.Severity != PolicyGuardrailSeverityCritical {
		return errors.New("invalid policy guardrail severity")
	}
	if r.Match != PolicyGuardrailMatchAll && r.Match != PolicyGuardrailMatchAny {
		return errors.New("invalid policy guardrail match mode")
	}
	if len(r.Conditions) == 0 || len(r.Conditions) > MaxPolicyGuardrailPredicatesPerRule {
		return errors.New("invalid policy guardrail condition count")
	}
	if strings.TrimSpace(r.Message) == "" || len(r.Message) > MaxPolicyGuardrailMessageBytes || strings.ContainsAny(r.Message, "\x00\r\n") {
		return errors.New("invalid policy guardrail message")
	}
	if err := validatePolicyGuardrailRemediationURL(r.RemediationURL); err != nil {
		return err
	}
	for _, condition := range r.Conditions {
		if err := condition.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c PolicyGuardrailCondition) Validate() error {
	kind, known := policyGuardrailFieldKinds[c.Field]
	if !known {
		return errors.New("invalid policy guardrail field")
	}
	payloads := 0
	if c.StringValue != nil {
		payloads++
	}
	if c.IntegerValue != nil {
		payloads++
	}
	if c.BooleanValue != nil {
		payloads++
	}
	if c.StringValues != nil {
		payloads++
	}
	if c.IntegerValues != nil {
		payloads++
	}
	if c.Operator == PolicyGuardrailOperatorPresent || c.Operator == PolicyGuardrailOperatorAbsent {
		if payloads != 0 {
			return errors.New("presence predicates do not accept values")
		}
		return nil
	}
	if payloads != 1 {
		return errors.New("policy guardrail predicate requires exactly one typed value")
	}
	if !validPolicyGuardrailOperator(kind, c.Operator) || !c.matchesOperatorPayload(kind) {
		return errors.New("policy guardrail field, operator, and value types do not match")
	}
	if len(c.StringValues) > MaxPolicyGuardrailValues || len(c.IntegerValues) > MaxPolicyGuardrailValues ||
		(c.StringValues != nil && len(c.StringValues) == 0) || (c.IntegerValues != nil && len(c.IntegerValues) == 0) {
		return errors.New("policy guardrail value limit exceeded")
	}
	if c.StringValue != nil && !validPolicyGuardrailValue(*c.StringValue) {
		return errors.New("invalid policy guardrail string value")
	}
	for _, value := range c.StringValues {
		if !validPolicyGuardrailValue(value) {
			return errors.New("invalid policy guardrail string value")
		}
	}
	return nil
}

func (c PolicyGuardrailCondition) matchesOperatorPayload(kind policyGuardrailValueKind) bool {
	switch c.Operator {
	case PolicyGuardrailOperatorEquals:
		return kind == policyGuardrailString && c.StringValue != nil ||
			kind == policyGuardrailInteger && c.IntegerValue != nil ||
			kind == policyGuardrailBoolean && c.BooleanValue != nil
	case PolicyGuardrailOperatorOneOf:
		return kind == policyGuardrailString && c.StringValues != nil ||
			kind == policyGuardrailInteger && c.IntegerValues != nil
	case PolicyGuardrailOperatorContainsAny, PolicyGuardrailOperatorContainsAll:
		return kind == policyGuardrailStrings && c.StringValues != nil ||
			kind == policyGuardrailIntegers && c.IntegerValues != nil
	case PolicyGuardrailOperatorLessThan, PolicyGuardrailOperatorAtMost,
		PolicyGuardrailOperatorGreaterThan, PolicyGuardrailOperatorAtLeast:
		return kind == policyGuardrailInteger && c.IntegerValue != nil
	default:
		return false
	}
}

func validPolicyGuardrailOperator(kind policyGuardrailValueKind, operator PolicyGuardrailOperator) bool {
	switch operator {
	case PolicyGuardrailOperatorEquals:
		return kind == policyGuardrailString || kind == policyGuardrailInteger || kind == policyGuardrailBoolean
	case PolicyGuardrailOperatorOneOf:
		return kind == policyGuardrailString || kind == policyGuardrailInteger
	case PolicyGuardrailOperatorContainsAny, PolicyGuardrailOperatorContainsAll:
		return kind == policyGuardrailStrings || kind == policyGuardrailIntegers
	case PolicyGuardrailOperatorLessThan, PolicyGuardrailOperatorAtMost, PolicyGuardrailOperatorGreaterThan, PolicyGuardrailOperatorAtLeast:
		return kind == policyGuardrailInteger
	default:
		return false
	}
}

func validPolicyGuardrailValue(value string) bool {
	return value != "" && len(value) <= MaxPolicyGuardrailValueBytes && !strings.ContainsAny(value, "\x00\r\n")
}

func validatePolicyGuardrailRemediationURL(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > MaxPolicyGuardrailRemediationBytes || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("invalid policy guardrail remediation URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("policy guardrail remediation URL must be an HTTPS URL without credentials or fragments")
	}
	return nil
}

type PolicyGuardrailTemplateMetadata struct {
	ID                int    `json:"id"`
	Application       string `json:"application"`
	Source            string `json:"source"`
	InventoryOverride bool   `json:"inventory_override"`
	BranchOverride    bool   `json:"branch_override"`
	CommitOverride    bool   `json:"commit_override"`
	ArgumentKeyCount  int    `json:"argument_key_count"`
	InputKeyCount     int    `json:"input_key_count"`
}

type PolicyGuardrailInventoryMetadata struct {
	ID             int    `json:"id"`
	Type           string `json:"type"`
	RunnerTagCount int    `json:"runner_tag_count"`
}

type PolicyGuardrailWorkflowMetadata struct {
	ID            int    `json:"id"`
	Revision      int    `json:"revision"`
	NodeID        int    `json:"node_id,omitempty"`
	NodeKind      string `json:"node_kind,omitempty"`
	TriggerSource string `json:"trigger_source,omitempty"`
	CrossProject  bool   `json:"cross_project"`
}

type PolicyGuardrailRunnerMetadata struct {
	SelectedID       int      `json:"selected_id,omitempty"`
	SelectedScope    string   `json:"selected_scope,omitempty"`
	SelectedExecutor string   `json:"selected_executor,omitempty"`
	RequestedTags    []string `json:"requested_tags"`
	CandidateCount   int      `json:"candidate_count"`
}

type PolicyGuardrailExecutorMetadata struct {
	Type               string `json:"type"`
	ImagePresent       bool   `json:"image_present"`
	ImageReferenceKind string `json:"image_reference_kind"`
	ImageDigest        string `json:"image_digest,omitempty"`
}

type PolicyGuardrailCredentialReferenceMetadata struct {
	ID            int    `json:"id"`
	Scope         string `json:"scope"`
	BindingTarget string `json:"binding_target"`
}

type PolicyGuardrailEvaluationInput struct {
	ProjectID      int                                          `json:"project_id"`
	Intent         ExecutionPreflightIntent                     `json:"intent"`
	EvaluatedAt    time.Time                                    `json:"evaluated_at"`
	Template       *PolicyGuardrailTemplateMetadata             `json:"template,omitempty"`
	Inventory      *PolicyGuardrailInventoryMetadata            `json:"inventory,omitempty"`
	EnvironmentIDs []int                                        `json:"environment_ids"`
	Workflow       *PolicyGuardrailWorkflowMetadata             `json:"workflow,omitempty"`
	Runner         PolicyGuardrailRunnerMetadata                `json:"runner"`
	Executor       PolicyGuardrailExecutorMetadata              `json:"executor"`
	Credentials    []PolicyGuardrailCredentialReferenceMetadata `json:"credentials"`
}

func (i PolicyGuardrailEvaluationInput) Validate() error {
	if i.ProjectID <= 0 || !validExecutionPreflightIntent(i.Intent) {
		return errors.New("invalid policy guardrail evaluation scope")
	}
	if i.EvaluatedAt.IsZero() {
		return errors.New("policy guardrail evaluation time is required")
	}
	if i.Template == nil && i.Workflow == nil {
		return errors.New("policy guardrail evaluation requires a task or workflow")
	}
	if i.Template != nil {
		if i.Template.ID <= 0 || !validPolicyGuardrailValue(i.Template.Application) || !validPolicyGuardrailValue(i.Template.Source) ||
			i.Template.ArgumentKeyCount < 0 || i.Template.ArgumentKeyCount > MaxPolicyGuardrailMetadataItems ||
			i.Template.InputKeyCount < 0 || i.Template.InputKeyCount > MaxPolicyGuardrailMetadataItems {
			return errors.New("invalid policy guardrail task metadata")
		}
	}
	if i.Inventory != nil && (i.Inventory.ID <= 0 || !validPolicyGuardrailValue(i.Inventory.Type) || i.Inventory.RunnerTagCount < 0) {
		return errors.New("invalid policy guardrail inventory metadata")
	}
	if len(i.EnvironmentIDs) > MaxPolicyGuardrailMetadataItems {
		return errors.New("policy guardrail environment limit exceeded")
	}
	for _, id := range i.EnvironmentIDs {
		if id <= 0 {
			return errors.New("invalid policy guardrail environment ID")
		}
	}
	if i.Workflow != nil && (i.Workflow.ID <= 0 || i.Workflow.Revision <= 0 ||
		(i.Workflow.NodeID < 0) || !validOptionalPolicyValue(i.Workflow.NodeKind) ||
		!validOptionalPolicyValue(i.Workflow.TriggerSource)) {
		return errors.New("invalid policy guardrail workflow metadata")
	}
	if i.Runner.SelectedID < 0 || i.Runner.CandidateCount < 0 || len(i.Runner.RequestedTags) > MaxPolicyGuardrailMetadataItems ||
		!validOptionalPolicyValue(i.Runner.SelectedScope) || !validOptionalPolicyValue(i.Runner.SelectedExecutor) ||
		!validBoundedPolicyStrings(i.Runner.RequestedTags) {
		return errors.New("invalid policy guardrail runner metadata")
	}
	if !validPolicyGuardrailValue(i.Executor.Type) || !validPolicyGuardrailValue(i.Executor.ImageReferenceKind) ||
		!validOptionalPolicyValue(i.Executor.ImageDigest) {
		return errors.New("invalid policy guardrail executor metadata")
	}
	if len(i.Credentials) > MaxPolicyGuardrailMetadataItems {
		return errors.New("policy guardrail credential limit exceeded")
	}
	for _, credential := range i.Credentials {
		if credential.ID <= 0 || !validPolicyGuardrailValue(credential.Scope) || !validPolicyGuardrailValue(credential.BindingTarget) {
			return errors.New("invalid policy guardrail credential reference")
		}
	}
	return nil
}

func validBoundedPolicyStrings(values []string) bool {
	for _, value := range values {
		if !validPolicyGuardrailValue(value) {
			return false
		}
	}
	return true
}

func validOptionalPolicyValue(value string) bool {
	return value == "" || validPolicyGuardrailValue(value)
}

type PolicyGuardrailRevisionRef struct {
	Scope       PolicyGuardrailScope `json:"scope"`
	ProjectID   *int                 `json:"project_id,omitempty"`
	Revision    int                  `json:"revision"`
	Fingerprint string               `json:"fingerprint"`
}

type PolicyGuardrailFinding struct {
	Scope          PolicyGuardrailScope    `json:"scope"`
	Revision       int                     `json:"revision"`
	RuleID         string                  `json:"rule_id"`
	Effect         PolicyGuardrailEffect   `json:"effect"`
	Severity       PolicyGuardrailSeverity `json:"severity"`
	Message        string                  `json:"message"`
	RemediationURL string                  `json:"remediation_url,omitempty"`
	NodeID         *int                    `json:"node_id,omitempty"`
}

type PolicyGuardrailEvaluation struct {
	Revisions        []PolicyGuardrailRevisionRef `json:"revisions"`
	Findings         []PolicyGuardrailFinding     `json:"findings"`
	Allowed          bool                         `json:"allowed"`
	InputFingerprint string                       `json:"input_fingerprint"`
	EvaluatedAt      time.Time                    `json:"evaluated_at"`
}

// Validate checks an evaluation before it reaches an execution preflight. The
// policy evaluator is authoritative for matching, but provenance must still be
// closed, ordered, and self-consistent at this contract boundary.
func (e PolicyGuardrailEvaluation) Validate(projectID int) error {
	if projectID <= 0 || !validExecutionPreflightFingerprint(e.InputFingerprint) || e.EvaluatedAt.IsZero() ||
		len(e.Revisions) > 2 || len(e.Findings) > MaxPolicyGuardrailFindings {
		return errors.New("invalid policy guardrail evaluation")
	}
	seenGlobal, seenProject := false, false
	for _, revision := range e.Revisions {
		if revision.Revision <= 0 || !validExecutionPreflightFingerprint(revision.Fingerprint) {
			return errors.New("invalid policy guardrail evaluation revision")
		}
		switch revision.Scope {
		case PolicyGuardrailScopeGlobal:
			if seenGlobal || seenProject || revision.ProjectID != nil {
				return errors.New("policy guardrail revisions must be global before project")
			}
			seenGlobal = true
		case PolicyGuardrailScopeProject:
			if seenProject || revision.ProjectID == nil || *revision.ProjectID != projectID {
				return errors.New("policy guardrail evaluation project revision mismatch")
			}
			seenProject = true
		default:
			return errors.New("invalid policy guardrail evaluation revision scope")
		}
	}

	denied := false
	var previous *PolicyGuardrailFinding
	for index := range e.Findings {
		finding := e.Findings[index]
		if !policyGuardrailFindingValid(finding, e.Revisions) {
			return errors.New("invalid policy guardrail evaluation finding")
		}
		if previous != nil && !policyGuardrailFindingOrder(previous, &finding) {
			return errors.New("policy guardrail evaluation findings are not deterministic")
		}
		previous = &finding
		denied = denied || finding.Effect == PolicyGuardrailEffectDeny
	}
	if e.Allowed == denied {
		return errors.New("policy guardrail evaluation decision does not match findings")
	}
	return nil
}

func policyGuardrailFindingValid(finding PolicyGuardrailFinding, revisions []PolicyGuardrailRevisionRef) bool {
	if !policyGuardrailRuleIDPattern.MatchString(finding.RuleID) ||
		strings.TrimSpace(finding.Message) == "" || len(finding.Message) > MaxPolicyGuardrailMessageBytes || strings.ContainsAny(finding.Message, "\x00\r\n") ||
		validatePolicyGuardrailRemediationURL(finding.RemediationURL) != nil ||
		(finding.Effect != PolicyGuardrailEffectAllow && finding.Effect != PolicyGuardrailEffectWarn && finding.Effect != PolicyGuardrailEffectDeny) ||
		(finding.Severity != PolicyGuardrailSeverityInfo && finding.Severity != PolicyGuardrailSeverityLow && finding.Severity != PolicyGuardrailSeverityMedium && finding.Severity != PolicyGuardrailSeverityHigh && finding.Severity != PolicyGuardrailSeverityCritical) ||
		(finding.NodeID != nil && *finding.NodeID <= 0) {
		return false
	}
	for _, revision := range revisions {
		if finding.Scope == revision.Scope && finding.Revision == revision.Revision {
			return true
		}
	}
	return false
}

func policyGuardrailFindingOrder(left, right *PolicyGuardrailFinding) bool {
	leftScope, rightScope := policyGuardrailScopeOrder(left.Scope), policyGuardrailScopeOrder(right.Scope)
	if leftScope != rightScope {
		return leftScope < rightScope
	}
	if left.RuleID != right.RuleID {
		return left.RuleID < right.RuleID
	}
	// A rule may only produce one finding in one immutable revision. Rejecting
	// equal ordering keys avoids ambiguous persistence and fingerprinting.
	return false
}

func policyGuardrailScopeOrder(scope PolicyGuardrailScope) int {
	if scope == PolicyGuardrailScopeGlobal {
		return 0
	}
	return 1
}

// ApplyPolicyGuardrailEvaluation appends typed policy provenance to a valid
// plan. It never replaces findings or revisions that were already present.
func ApplyPolicyGuardrailEvaluation(plan *ExecutionPreflightPlan, evaluation PolicyGuardrailEvaluation) error {
	if plan == nil || plan.Validate() != nil || evaluation.Validate(plan.ProjectID) != nil ||
		len(plan.PolicyRevisions) != 0 || planHasPolicyGuardrailFinding(*plan) ||
		len(plan.Findings)+len(evaluation.Findings) > MaxExecutionPreflightFindings {
		return errors.New("cannot apply policy guardrail evaluation")
	}
	findings := append([]ExecutionPreflightFinding(nil), plan.Findings...)
	for _, finding := range evaluation.Findings {
		mapped, err := policyGuardrailPreflightFinding(finding)
		if err != nil {
			return err
		}
		findings = append(findings, mapped)
	}
	revisions := copyPolicyGuardrailRevisionRefs(evaluation.Revisions)
	candidate := *plan
	candidate.Findings = findings
	candidate.PolicyRevisions = revisions
	if candidate.Validate() != nil {
		return errors.New("policy guardrail evaluation exceeds preflight contract")
	}
	plan.Findings = findings
	plan.PolicyRevisions = revisions
	return nil
}

// ApplyWorkflowPolicyGuardrailEvaluations combines one immutable workflow-root
// evaluation with one evaluation for every executable task node. The workflow
// service owns context construction; this contract boundary verifies that the
// evaluations still describe exactly that one snapshot before it adds their
// value-free provenance to the public preflight plan.
func ApplyWorkflowPolicyGuardrailEvaluations(
	plan *ExecutionPreflightPlan,
	inputs []PolicyGuardrailEvaluationInput,
	evaluations []PolicyGuardrailEvaluation,
) error {
	if plan == nil || plan.Validate() != nil || plan.Intent != ExecutionPreflightWorkflow ||
		len(plan.PolicyRevisions) != 0 || planHasPolicyGuardrailFinding(*plan) ||
		len(inputs) == 0 || len(inputs) != len(evaluations) || len(inputs) > MaxPolicyGuardrailAdmissionBatch {
		return errors.New("cannot apply workflow policy guardrail evaluations")
	}

	var revisions []PolicyGuardrailRevisionRef
	findings := append([]ExecutionPreflightFinding(nil), plan.Findings...)
	var workflowID, workflowRevision int
	var triggerSource string
	var evaluatedAt time.Time
	lastNodeID := 0
	for index, input := range inputs {
		if err := input.Validate(); err != nil || input.ProjectID != plan.ProjectID {
			return errors.New("invalid workflow policy guardrail input")
		}
		metadata := input.Workflow
		if metadata == nil || metadata.ID != plan.WorkflowID {
			return errors.New("workflow policy guardrail provenance mismatch")
		}
		if index == 0 {
			if input.Intent != ExecutionPreflightWorkflow || input.Template != nil || metadata.NodeID != 0 || metadata.NodeKind != "" ||
				!validWorkflowPolicyGuardrailTriggerSource(metadata.TriggerSource) {
				return errors.New("invalid workflow root policy guardrail input")
			}
			workflowID, workflowRevision, triggerSource, evaluatedAt = metadata.ID, metadata.Revision, metadata.TriggerSource, input.EvaluatedAt
		} else if input.Intent != ExecutionPreflightTask || input.Template == nil || metadata.ID != workflowID ||
			metadata.Revision != workflowRevision || metadata.TriggerSource != triggerSource || metadata.NodeID <= lastNodeID ||
			metadata.NodeKind != "task" || !input.EvaluatedAt.Equal(evaluatedAt) {
			return errors.New("invalid workflow task policy guardrail input")
		} else {
			lastNodeID = metadata.NodeID
		}

		fingerprint, err := FingerprintPolicyGuardrailInput(input)
		if err != nil || evaluations[index].InputFingerprint != fingerprint || !evaluations[index].EvaluatedAt.Equal(input.EvaluatedAt) ||
			evaluations[index].Validate(plan.ProjectID) != nil {
			return errors.New("workflow policy guardrail evaluation mismatch")
		}
		if index == 0 {
			revisions = copyPolicyGuardrailRevisionRefs(evaluations[index].Revisions)
		} else if !policyGuardrailRevisionRefsEqual(revisions, evaluations[index].Revisions) {
			return errors.New("workflow policy guardrail revisions changed during evaluation")
		}
		for _, finding := range evaluations[index].Findings {
			if finding.NodeID != nil {
				return errors.New("workflow policy guardrail finding node is evaluator-owned")
			}
			mapped, mapErr := policyGuardrailPreflightFinding(finding)
			if mapErr != nil {
				return mapErr
			}
			if index > 0 {
				mapped.NodeID = intCopyPolicyGuardrail(metadata.NodeID)
			}
			findings = append(findings, mapped)
		}
	}
	if len(findings) > MaxExecutionPreflightFindings {
		return errors.New("workflow policy guardrail findings exceed preflight contract")
	}
	sortWorkflowPolicyGuardrailFindings(findings[len(plan.Findings):])
	candidate := *plan
	candidate.Findings = findings
	candidate.PolicyRevisions = revisions
	if candidate.Validate() != nil {
		return errors.New("workflow policy guardrail evaluation exceeds preflight contract")
	}
	plan.Findings = findings
	plan.PolicyRevisions = revisions
	return nil
}

func validWorkflowPolicyGuardrailTriggerSource(source string) bool {
	switch source {
	case string(DeploymentWindowSourceManual), string(DeploymentWindowSourceSchedule), string(DeploymentWindowSourceAPI), string(DeploymentWindowSourceWebhook):
		return true
	default:
		return false
	}
}

func policyGuardrailRevisionRefsEqual(left, right []PolicyGuardrailRevisionRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Scope != right[index].Scope || left[index].Revision != right[index].Revision ||
			left[index].Fingerprint != right[index].Fingerprint || (left[index].ProjectID == nil) != (right[index].ProjectID == nil) {
			return false
		}
		if left[index].ProjectID != nil && *left[index].ProjectID != *right[index].ProjectID {
			return false
		}
	}
	return true
}

func sortWorkflowPolicyGuardrailFindings(findings []ExecutionPreflightFinding) {
	sort.Slice(findings, func(left, right int) bool {
		leftNodeID, rightNodeID := workflowPolicyFindingNodeID(findings[left]), workflowPolicyFindingNodeID(findings[right])
		if leftNodeID != rightNodeID {
			return leftNodeID < rightNodeID
		}
		leftScope, rightScope := policyGuardrailScopeOrder(findings[left].PolicyScope), policyGuardrailScopeOrder(findings[right].PolicyScope)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		if findings[left].PolicyRuleID != findings[right].PolicyRuleID {
			return findings[left].PolicyRuleID < findings[right].PolicyRuleID
		}
		return findings[left].Message < findings[right].Message
	})
}

func workflowPolicyFindingNodeID(finding ExecutionPreflightFinding) int {
	if finding.NodeID == nil {
		return 0
	}
	return *finding.NodeID
}

func intCopyPolicyGuardrail(value int) *int {
	copy := value
	return &copy
}

func policyGuardrailPreflightFinding(finding PolicyGuardrailFinding) (ExecutionPreflightFinding, error) {
	result := ExecutionPreflightFinding{Message: finding.Message, PolicyScope: finding.Scope, PolicyRevision: finding.Revision, PolicyRuleID: finding.RuleID, PolicyEffect: finding.Effect, RemediationURL: finding.RemediationURL}
	if finding.NodeID != nil {
		nodeID := *finding.NodeID
		result.NodeID = &nodeID
	}
	switch finding.Effect {
	case PolicyGuardrailEffectAllow:
		result.Severity, result.Code = ExecutionFindingInfo, ExecutionReasonPolicyAllowed
	case PolicyGuardrailEffectWarn:
		result.Severity, result.Code = ExecutionFindingWarning, ExecutionReasonPolicyWarning
	case PolicyGuardrailEffectDeny:
		result.Severity, result.Code = ExecutionFindingDenial, ExecutionReasonPolicyDenied
	default:
		return ExecutionPreflightFinding{}, errors.New("invalid policy guardrail effect")
	}
	return result, nil
}

func copyPolicyGuardrailRevisionRefs(revisions []PolicyGuardrailRevisionRef) []PolicyGuardrailRevisionRef {
	result := make([]PolicyGuardrailRevisionRef, len(revisions))
	for index, revision := range revisions {
		result[index] = revision
		if revision.ProjectID != nil {
			projectID := *revision.ProjectID
			result[index].ProjectID = &projectID
		}
	}
	return result
}

func planHasPolicyGuardrailFinding(plan ExecutionPreflightPlan) bool {
	if len(plan.PolicyRevisions) != 0 {
		return true
	}
	for _, finding := range plan.Findings {
		if finding.PolicyScope != "" || finding.PolicyRevision != 0 || finding.PolicyRuleID != "" || finding.PolicyEffect != "" || finding.RemediationURL != "" ||
			finding.Code == ExecutionReasonPolicyAllowed || finding.Code == ExecutionReasonPolicyWarning || finding.Code == ExecutionReasonPolicyDenied {
			return true
		}
	}
	return false
}

func planHasPolicyGuardrailDenial(plan ExecutionPreflightPlan) bool {
	for _, finding := range plan.Findings {
		if finding.Code == ExecutionReasonPolicyDenied && finding.PolicyEffect == PolicyGuardrailEffectDeny {
			return true
		}
	}
	return false
}

func FingerprintPolicyGuardrailInput(input PolicyGuardrailEvaluationInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	copy := input
	copy.EvaluatedAt = time.Time{}
	copy.EnvironmentIDs = append([]int(nil), input.EnvironmentIDs...)
	sort.Ints(copy.EnvironmentIDs)
	copy.Runner.RequestedTags = append([]string(nil), input.Runner.RequestedTags...)
	sort.Strings(copy.Runner.RequestedTags)
	copy.Credentials = append([]PolicyGuardrailCredentialReferenceMetadata(nil), input.Credentials...)
	sort.Slice(copy.Credentials, func(a, b int) bool {
		if copy.Credentials[a].ID != copy.Credentials[b].ID {
			return copy.Credentials[a].ID < copy.Credentials[b].ID
		}
		return copy.Credentials[a].BindingTarget < copy.Credentials[b].BindingTarget
	})
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", errors.New("encode policy guardrail input")
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type PolicyGuardrailEvaluator interface {
	EvaluatePolicyGuardrails(PolicyGuardrailEvaluationInput) (PolicyGuardrailEvaluation, error)
}

type PolicyGuardrailValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type PolicyGuardrailValidationResult struct {
	Valid       bool                             `json:"valid"`
	Fingerprint string                           `json:"fingerprint,omitempty"`
	RuleCount   int                              `json:"rule_count"`
	Issues      []PolicyGuardrailValidationIssue `json:"issues"`
}

type PolicyGuardrailDraftState struct {
	Draft  db.PolicyGuardrailDraft     `json:"draft"`
	Active *db.PolicyGuardrailRevision `json:"active,omitempty"`
}

type PolicyGuardrailDiff struct {
	FromRevision int      `json:"from_revision"`
	ToRevision   int      `json:"to_revision"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	Changed      []string `json:"changed"`
}

type PolicyGuardrailFixtureRequest struct {
	SourceYAML string                         `json:"source_yaml,omitempty"`
	Input      PolicyGuardrailEvaluationInput `json:"input"`
}

type PolicyGuardrailImpactRequest struct {
	Inputs []PolicyGuardrailEvaluationInput `json:"inputs"`
}

type PolicyGuardrailImpactResult struct {
	Evaluations []PolicyGuardrailEvaluation `json:"evaluations"`
	Allowed     int                         `json:"allowed"`
	Denied      int                         `json:"denied"`
}

type PolicyGuardrailPublishRequest struct {
	ExpectedDraftRevision int `json:"expected_draft_revision"`
}

type PolicyGuardrailRollbackRequest struct {
	Revision              int    `json:"revision"`
	ExpectedDraftRevision int    `json:"expected_draft_revision"`
	Reason                string `json:"reason"`
}

type PolicyGuardrailGovernanceRepository interface {
	GetPolicyGuardrailDraft(PolicyGuardrailScope, *int) (db.PolicyGuardrailDraft, error)
	SavePolicyGuardrailDraft(PolicyGuardrailScope, *int, string, int, int) (db.PolicyGuardrailDraft, error)
	PublishPolicyGuardrailRevision(PolicyGuardrailScope, *int, int, int, string, string, string, int) (db.PolicyGuardrailRevision, error)
	RollbackPolicyGuardrailRevision(PolicyGuardrailScope, *int, int, int, int, string) (db.PolicyGuardrailRevision, error)
	GetPolicyGuardrailRevision(PolicyGuardrailScope, *int, int) (db.PolicyGuardrailRevision, error)
	GetPolicyGuardrailRevisions(PolicyGuardrailScope, *int, db.RetrieveQueryParams) ([]db.PolicyGuardrailRevision, error)
	GetPolicyGuardrailEvaluationHistory(*int, db.RetrieveQueryParams) ([]db.PolicyGuardrailEvaluationRecord, error)
}

type PolicyGuardrailGovernanceServiceFacade interface {
	Get(context.Context, PolicyGuardrailScope, *int) (PolicyGuardrailDraftState, error)
	SaveDraft(context.Context, PolicyGuardrailScope, *int, string, int, int) (db.PolicyGuardrailDraft, error)
	Validate(context.Context, PolicyGuardrailScope, *int, string) PolicyGuardrailValidationResult
	TestFixture(context.Context, PolicyGuardrailScope, *int, PolicyGuardrailFixtureRequest) (PolicyGuardrailEvaluation, error)
	Diff(context.Context, PolicyGuardrailScope, *int, int, int) (PolicyGuardrailDiff, error)
	Publish(context.Context, PolicyGuardrailScope, *int, PolicyGuardrailPublishRequest, int) (db.PolicyGuardrailRevision, error)
	Rollback(context.Context, PolicyGuardrailScope, *int, PolicyGuardrailRollbackRequest, int) (db.PolicyGuardrailRevision, error)
	Impact(context.Context, PolicyGuardrailScope, *int, PolicyGuardrailImpactRequest) (PolicyGuardrailImpactResult, error)
	Revisions(context.Context, PolicyGuardrailScope, *int, db.RetrieveQueryParams) ([]db.PolicyGuardrailRevision, error)
	Evaluations(context.Context, *int, db.RetrieveQueryParams) ([]db.PolicyGuardrailEvaluationRecord, error)
}

type PolicyGuardrailAdmissionRequest struct {
	DecisionKey string                         `json:"-"`
	Source      string                         `json:"source"`
	ActorUserID *int                           `json:"-"`
	Input       PolicyGuardrailEvaluationInput `json:"-"`
}

type PolicyGuardrailEvaluationClaim struct {
	Record     db.PolicyGuardrailEvaluationRecord
	Evaluation PolicyGuardrailEvaluation
	Inserted   bool
}

// MaxPolicyGuardrailAdmissionBatch bounds one atomic admission snapshot to a
// workflow root plus its maximum number of nodes.
const MaxPolicyGuardrailAdmissionBatch = 201

type PolicyGuardrailAdmissionRepository interface {
	PreviewPolicyGuardrails(PolicyGuardrailEvaluationInput, func([]db.PolicyGuardrailRevision, PolicyGuardrailEvaluationInput) (PolicyGuardrailEvaluation, error)) (PolicyGuardrailEvaluation, error)
	PreviewPolicyGuardrailEvaluations([]PolicyGuardrailEvaluationInput, func([]db.PolicyGuardrailRevision, PolicyGuardrailEvaluationInput) (PolicyGuardrailEvaluation, error)) ([]PolicyGuardrailEvaluation, error)
	ClaimPolicyGuardrailEvaluation(PolicyGuardrailAdmissionRequest, func([]db.PolicyGuardrailRevision, PolicyGuardrailEvaluationInput) (PolicyGuardrailEvaluation, error)) (PolicyGuardrailEvaluationClaim, error)
	ClaimPolicyGuardrailEvaluations([]PolicyGuardrailAdmissionRequest, func([]db.PolicyGuardrailRevision, PolicyGuardrailEvaluationInput) (PolicyGuardrailEvaluation, error)) ([]PolicyGuardrailEvaluationClaim, error)
}

// PolicyGuardrailRepository joins the narrow start-admission and governance
// storage contracts for the edition factory. Start paths still receive only
// PolicyGuardrailAdmissionRepository; this composite type merely ensures both
// services share one SQL-backed policy snapshot store at process wiring time.
type PolicyGuardrailRepository interface {
	PolicyGuardrailGovernanceRepository
	PolicyGuardrailAdmissionRepository
}

type PolicyGuardrailAdmissionService interface {
	PolicyGuardrailEvaluator
	PreviewPolicyGuardrailEvaluations([]PolicyGuardrailEvaluationInput) ([]PolicyGuardrailEvaluation, error)
	ClaimPolicyGuardrailEvaluation(PolicyGuardrailAdmissionRequest) (PolicyGuardrailEvaluationClaim, error)
	ClaimPolicyGuardrailEvaluations([]PolicyGuardrailAdmissionRequest) ([]PolicyGuardrailEvaluationClaim, error)
}

type PolicyGuardrailAdmissionConfigurer interface {
	ConfigurePolicyGuardrailAdmission(PolicyGuardrailAdmissionService)
}

type PolicyGuardrailDeniedError struct {
	Evaluation PolicyGuardrailEvaluation
}

func (e *PolicyGuardrailDeniedError) Error() string { return "policy guardrail denied execution" }
