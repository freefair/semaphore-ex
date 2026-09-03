package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"gopkg.in/yaml.v3"
)

// PolicyGuardrailCompiledPolicy is an immutable, typed policy revision. It is
// deliberately data-only: executing a policy cannot invoke user-provided code.
type PolicyGuardrailCompiledPolicy struct {
	revision pro_interfaces.PolicyGuardrailRevisionRef
	document pro_interfaces.PolicyGuardrailDocument
}

// RevisionRef returns a copy so callers cannot mutate the active binding.
func (p *PolicyGuardrailCompiledPolicy) RevisionRef() pro_interfaces.PolicyGuardrailRevisionRef {
	if p == nil {
		return pro_interfaces.PolicyGuardrailRevisionRef{}
	}
	copy := p.revision
	if copy.ProjectID != nil {
		projectID := *copy.ProjectID
		copy.ProjectID = &projectID
	}
	return copy
}

// Document returns a copy of the normalized AST for inspection only.
func (p *PolicyGuardrailCompiledPolicy) Document() pro_interfaces.PolicyGuardrailDocument {
	if p == nil {
		return pro_interfaces.PolicyGuardrailDocument{}
	}
	return canonicalPolicyGuardrailDocument(p.document)
}

// CompilePolicyGuardrailYAML accepts only one small, unambiguous YAML document
// and returns a normalized typed AST and its content fingerprint.
func CompilePolicyGuardrailYAML(source []byte) (pro_interfaces.PolicyGuardrailDocument, string, error) {
	if len(source) == 0 || len(source) > pro_interfaces.MaxPolicyGuardrailYAMLBytes {
		return pro_interfaces.PolicyGuardrailDocument{}, "", errors.New("policy guardrail YAML exceeds the permitted size")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, "", fmt.Errorf("decode policy guardrail YAML: %w", err)
	}
	if err := validatePolicyGuardrailYAMLNode(&node); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, "", err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return pro_interfaces.PolicyGuardrailDocument{}, "", errors.New("policy guardrail YAML must contain exactly one document")
		}
		return pro_interfaces.PolicyGuardrailDocument{}, "", fmt.Errorf("decode trailing policy guardrail YAML: %w", err)
	}

	typedDecoder := yaml.NewDecoder(bytes.NewReader(source))
	typedDecoder.KnownFields(true)
	var document pro_interfaces.PolicyGuardrailDocument
	if err := typedDecoder.Decode(&document); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, "", fmt.Errorf("decode typed policy guardrail YAML: %w", err)
	}
	if err := document.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, "", err
	}
	document = canonicalPolicyGuardrailDocument(document)
	fingerprint, err := fingerprintPolicyGuardrailDocument(document)
	if err != nil {
		return pro_interfaces.PolicyGuardrailDocument{}, "", err
	}
	return document, fingerprint, nil
}

// CompilePolicyGuardrailPolicyYAML binds compiled content to one active scope.
func CompilePolicyGuardrailPolicyYAML(scope pro_interfaces.PolicyGuardrailScope, projectID *int, revision int, source []byte) (*PolicyGuardrailCompiledPolicy, error) {
	document, _, err := CompilePolicyGuardrailYAML(source)
	if err != nil {
		return nil, err
	}
	return NewCompiledPolicyGuardrailPolicy(scope, projectID, revision, document)
}

// NewCompiledPolicyGuardrailPolicy is useful for already-typed, server-created
// revisions; it applies the same validation and canonical fingerprint as YAML.
func NewCompiledPolicyGuardrailPolicy(scope pro_interfaces.PolicyGuardrailScope, projectID *int, revision int, document pro_interfaces.PolicyGuardrailDocument) (*PolicyGuardrailCompiledPolicy, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	if revision <= 0 {
		return nil, errors.New("policy guardrail revision is required")
	}
	if scope == pro_interfaces.PolicyGuardrailScopeGlobal {
		if projectID != nil {
			return nil, errors.New("global policy guardrails cannot have a project")
		}
	} else if scope == pro_interfaces.PolicyGuardrailScopeProject {
		if projectID == nil || *projectID <= 0 {
			return nil, errors.New("project policy guardrails require a project")
		}
	} else {
		return nil, errors.New("invalid policy guardrail scope")
	}
	document = canonicalPolicyGuardrailDocument(document)
	fingerprint, err := fingerprintPolicyGuardrailDocument(document)
	if err != nil {
		return nil, err
	}
	var boundProjectID *int
	if projectID != nil {
		value := *projectID
		boundProjectID = &value
	}
	return &PolicyGuardrailCompiledPolicy{
		revision: pro_interfaces.PolicyGuardrailRevisionRef{Scope: scope, ProjectID: boundProjectID, Revision: revision, Fingerprint: fingerprint},
		document: document,
	}, nil
}

func validatePolicyGuardrailYAMLNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.DocumentNode || len(node.Content) != 1 {
		return errors.New("policy guardrail YAML requires one mapping document")
	}
	return validatePolicyGuardrailYAMLContent(node.Content[0])
}

func validatePolicyGuardrailYAMLContent(node *yaml.Node) error {
	if node == nil || node.Anchor != "" || node.Alias != nil || node.Kind == yaml.AliasNode {
		return errors.New("policy guardrail YAML does not permit anchors or aliases")
	}
	if !policyGuardrailYAMLCoreTag(node) {
		return errors.New("policy guardrail YAML does not permit non-core tags")
	}
	switch node.Kind {
	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return errors.New("invalid policy guardrail YAML mapping")
		}
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode || !policyGuardrailYAMLCoreTag(key) {
				return errors.New("policy guardrail YAML mapping keys must be strings")
			}
			if key.Value == "<<" || key.Tag == "!!merge" {
				return errors.New("policy guardrail YAML does not permit merge keys")
			}
			if _, duplicate := seen[key.Value]; duplicate {
				return errors.New("policy guardrail YAML does not permit duplicate mapping keys")
			}
			seen[key.Value] = struct{}{}
			if err := validatePolicyGuardrailYAMLContent(key); err != nil {
				return err
			}
			if err := validatePolicyGuardrailYAMLContent(value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := validatePolicyGuardrailYAMLContent(child); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		return nil
	default:
		return errors.New("invalid policy guardrail YAML node")
	}
	return nil
}

func policyGuardrailYAMLCoreTag(node *yaml.Node) bool {
	switch node.Kind {
	case yaml.MappingNode:
		return node.Tag == "!!map"
	case yaml.SequenceNode:
		return node.Tag == "!!seq"
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str", "!!int", "!!bool":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func canonicalPolicyGuardrailDocument(document pro_interfaces.PolicyGuardrailDocument) pro_interfaces.PolicyGuardrailDocument {
	copy := document
	copy.Rules = append([]pro_interfaces.PolicyGuardrailRule(nil), document.Rules...)
	for ruleIndex := range copy.Rules {
		rule := &copy.Rules[ruleIndex]
		rule.Conditions = append([]pro_interfaces.PolicyGuardrailCondition(nil), rule.Conditions...)
		for conditionIndex := range rule.Conditions {
			condition := &rule.Conditions[conditionIndex]
			if condition.StringValue != nil {
				value := *condition.StringValue
				condition.StringValue = &value
			}
			if condition.IntegerValue != nil {
				value := *condition.IntegerValue
				condition.IntegerValue = &value
			}
			if condition.BooleanValue != nil {
				value := *condition.BooleanValue
				condition.BooleanValue = &value
			}
			condition.StringValues = append([]string(nil), condition.StringValues...)
			condition.IntegerValues = append([]int(nil), condition.IntegerValues...)
			sort.Strings(condition.StringValues)
			sort.Ints(condition.IntegerValues)
		}
		sort.SliceStable(rule.Conditions, func(left, right int) bool {
			return canonicalPolicyGuardrailConditionKey(rule.Conditions[left]) < canonicalPolicyGuardrailConditionKey(rule.Conditions[right])
		})
	}
	sort.SliceStable(copy.Rules, func(left, right int) bool { return copy.Rules[left].ID < copy.Rules[right].ID })
	return copy
}

func canonicalPolicyGuardrailConditionKey(condition pro_interfaces.PolicyGuardrailCondition) string {
	encoded, err := json.Marshal(condition)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func fingerprintPolicyGuardrailDocument(document pro_interfaces.PolicyGuardrailDocument) (string, error) {
	payload := struct {
		CompilerVersion int                                    `json:"compiler_version"`
		Document        pro_interfaces.PolicyGuardrailDocument `json:"document"`
	}{CompilerVersion: pro_interfaces.PolicyGuardrailCompilerVersion, Document: document}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("encode policy guardrail document")
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// PolicyGuardrailEvaluator evaluates up to one active global and project
// policy. It has no callbacks and reads only the explicit public input value.
type PolicyGuardrailEvaluator struct {
	global  *PolicyGuardrailCompiledPolicy
	project *PolicyGuardrailCompiledPolicy
}

var _ pro_interfaces.PolicyGuardrailEvaluator = (*PolicyGuardrailEvaluator)(nil)

func NewPolicyGuardrailEvaluator(global, project *PolicyGuardrailCompiledPolicy) (*PolicyGuardrailEvaluator, error) {
	if err := validateCompiledPolicyGuardrailPolicy(global, pro_interfaces.PolicyGuardrailScopeGlobal, 0); err != nil {
		return nil, err
	}
	if err := validateCompiledPolicyGuardrailPolicy(project, pro_interfaces.PolicyGuardrailScopeProject, 0); err != nil {
		return nil, err
	}
	return &PolicyGuardrailEvaluator{global: global, project: project}, nil
}

func validateCompiledPolicyGuardrailPolicy(policy *PolicyGuardrailCompiledPolicy, expectedScope pro_interfaces.PolicyGuardrailScope, inputProjectID int) error {
	if policy == nil {
		return nil
	}
	if policy.revision.Scope != expectedScope || policy.revision.Revision <= 0 || policy.revision.Fingerprint == "" || policy.document.Validate() != nil {
		return errors.New("invalid compiled policy guardrail policy")
	}
	if expectedScope == pro_interfaces.PolicyGuardrailScopeGlobal && policy.revision.ProjectID != nil {
		return errors.New("invalid global policy guardrail policy")
	}
	if expectedScope == pro_interfaces.PolicyGuardrailScopeProject && (policy.revision.ProjectID == nil || *policy.revision.ProjectID <= 0 || (inputProjectID != 0 && *policy.revision.ProjectID != inputProjectID)) {
		return errors.New("invalid project policy guardrail policy")
	}
	canonical := canonicalPolicyGuardrailDocument(policy.document)
	if !reflect.DeepEqual(policy.document, canonical) {
		return errors.New("policy guardrail compiled content is not canonical")
	}
	fingerprint, err := fingerprintPolicyGuardrailDocument(canonical)
	if err != nil || fingerprint != policy.revision.Fingerprint {
		return errors.New("policy guardrail compiled content fingerprint mismatch")
	}
	return nil
}

func (e *PolicyGuardrailEvaluator) EvaluatePolicyGuardrails(input pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if e == nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, errors.New("policy guardrail evaluator is required")
	}
	if err := input.Validate(); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	if err := validateCompiledPolicyGuardrailPolicy(e.global, pro_interfaces.PolicyGuardrailScopeGlobal, input.ProjectID); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	if err := validateCompiledPolicyGuardrailPolicy(e.project, pro_interfaces.PolicyGuardrailScopeProject, input.ProjectID); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	inputFingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	evaluation := pro_interfaces.PolicyGuardrailEvaluation{Allowed: true, InputFingerprint: inputFingerprint, EvaluatedAt: input.EvaluatedAt.UTC()}
	for _, policy := range []*PolicyGuardrailCompiledPolicy{e.global, e.project} {
		if policy == nil {
			continue
		}
		evaluation.Revisions = append(evaluation.Revisions, policy.RevisionRef())
		for _, rule := range policy.document.Rules {
			matched, err := policyGuardrailRuleMatches(rule, input)
			if err != nil {
				return pro_interfaces.PolicyGuardrailEvaluation{}, err
			}
			if !matched {
				continue
			}
			if len(evaluation.Findings) >= pro_interfaces.MaxPolicyGuardrailFindings {
				return pro_interfaces.PolicyGuardrailEvaluation{}, errors.New("policy guardrail finding limit exceeded")
			}
			finding := pro_interfaces.PolicyGuardrailFinding{Scope: policy.revision.Scope, Revision: policy.revision.Revision, RuleID: rule.ID, Effect: rule.Effect, Severity: rule.Severity, Message: rule.Message, RemediationURL: rule.RemediationURL}
			if input.Workflow != nil && input.Workflow.NodeID > 0 {
				nodeID := input.Workflow.NodeID
				finding.NodeID = &nodeID
			}
			evaluation.Findings = append(evaluation.Findings, finding)
			if rule.Effect == pro_interfaces.PolicyGuardrailEffectDeny {
				evaluation.Allowed = false
			}
		}
	}
	return evaluation, nil
}

func policyGuardrailRuleMatches(rule pro_interfaces.PolicyGuardrailRule, input pro_interfaces.PolicyGuardrailEvaluationInput) (bool, error) {
	matchedAny := false
	for _, condition := range rule.Conditions {
		matched, err := policyGuardrailConditionMatches(condition, input)
		if err != nil {
			return false, err
		}
		if rule.Match == pro_interfaces.PolicyGuardrailMatchAll && !matched {
			return false, nil
		}
		if rule.Match == pro_interfaces.PolicyGuardrailMatchAny && matched {
			return true, nil
		}
		matchedAny = matchedAny || matched
	}
	return rule.Match == pro_interfaces.PolicyGuardrailMatchAll || matchedAny, nil
}

type policyGuardrailFieldValue struct {
	present       bool
	stringValue   string
	integerValue  int
	booleanValue  bool
	stringValues  []string
	integerValues []int
}

func policyGuardrailConditionMatches(condition pro_interfaces.PolicyGuardrailCondition, input pro_interfaces.PolicyGuardrailEvaluationInput) (bool, error) {
	value, known := policyGuardrailFieldValueFor(condition.Field, input)
	if !known {
		return false, errors.New("unknown policy guardrail field")
	}
	switch condition.Operator {
	case pro_interfaces.PolicyGuardrailOperatorPresent:
		return value.present, nil
	case pro_interfaces.PolicyGuardrailOperatorAbsent:
		return !value.present, nil
	}
	if !value.present {
		return false, nil
	}
	switch condition.Operator {
	case pro_interfaces.PolicyGuardrailOperatorEquals:
		if condition.StringValue != nil {
			return value.stringValue == *condition.StringValue, nil
		}
		if condition.IntegerValue != nil {
			return value.integerValue == *condition.IntegerValue, nil
		}
		return value.booleanValue == *condition.BooleanValue, nil
	case pro_interfaces.PolicyGuardrailOperatorOneOf:
		if condition.StringValues != nil {
			return containsPolicyGuardrailString(condition.StringValues, value.stringValue), nil
		}
		return containsPolicyGuardrailInteger(condition.IntegerValues, value.integerValue), nil
	case pro_interfaces.PolicyGuardrailOperatorContainsAny:
		if condition.StringValues != nil {
			return policyGuardrailContainsAnyStrings(value.stringValues, condition.StringValues), nil
		}
		return policyGuardrailContainsAnyIntegers(value.integerValues, condition.IntegerValues), nil
	case pro_interfaces.PolicyGuardrailOperatorContainsAll:
		if condition.StringValues != nil {
			return policyGuardrailContainsAllStrings(value.stringValues, condition.StringValues), nil
		}
		return policyGuardrailContainsAllIntegers(value.integerValues, condition.IntegerValues), nil
	case pro_interfaces.PolicyGuardrailOperatorLessThan:
		return value.integerValue < *condition.IntegerValue, nil
	case pro_interfaces.PolicyGuardrailOperatorAtMost:
		return value.integerValue <= *condition.IntegerValue, nil
	case pro_interfaces.PolicyGuardrailOperatorGreaterThan:
		return value.integerValue > *condition.IntegerValue, nil
	case pro_interfaces.PolicyGuardrailOperatorAtLeast:
		return value.integerValue >= *condition.IntegerValue, nil
	default:
		return false, errors.New("unsupported policy guardrail operator")
	}
}

// policyGuardrailFieldValueFor is intentionally exhaustive. It is the only
// path from execution metadata to policy values, so secrets cannot enter it.
func policyGuardrailFieldValueFor(field pro_interfaces.PolicyGuardrailField, input pro_interfaces.PolicyGuardrailEvaluationInput) (policyGuardrailFieldValue, bool) {
	template := input.Template
	inventory := input.Inventory
	workflow := input.Workflow
	switch field {
	case pro_interfaces.PolicyFieldTaskTemplateID:
		return policyGuardrailIntegerField(template != nil, policyGuardrailTemplateID(template)), true
	case pro_interfaces.PolicyFieldTaskApplication:
		return policyGuardrailStringField(template != nil, policyGuardrailTemplateApplication(template)), true
	case pro_interfaces.PolicyFieldTaskSource:
		return policyGuardrailStringField(template != nil, policyGuardrailTemplateSource(template)), true
	case pro_interfaces.PolicyFieldTaskInventoryOverride:
		return policyGuardrailBooleanField(template != nil, policyGuardrailTemplateInventoryOverride(template)), true
	case pro_interfaces.PolicyFieldTaskBranchOverride:
		return policyGuardrailBooleanField(template != nil, policyGuardrailTemplateBranchOverride(template)), true
	case pro_interfaces.PolicyFieldTaskCommitOverride:
		return policyGuardrailBooleanField(template != nil, policyGuardrailTemplateCommitOverride(template)), true
	case pro_interfaces.PolicyFieldTaskArgumentKeyCount:
		return policyGuardrailIntegerField(template != nil, policyGuardrailTemplateArgumentKeyCount(template)), true
	case pro_interfaces.PolicyFieldTaskInputKeyCount:
		return policyGuardrailIntegerField(template != nil, policyGuardrailTemplateInputKeyCount(template)), true
	case pro_interfaces.PolicyFieldInventoryPresent:
		return policyGuardrailBooleanField(true, inventory != nil), true
	case pro_interfaces.PolicyFieldInventoryID:
		return policyGuardrailIntegerField(inventory != nil, policyGuardrailInventoryID(inventory)), true
	case pro_interfaces.PolicyFieldInventoryType:
		return policyGuardrailStringField(inventory != nil, policyGuardrailInventoryType(inventory)), true
	case pro_interfaces.PolicyFieldInventoryRunnerTagCount:
		return policyGuardrailIntegerField(inventory != nil, policyGuardrailInventoryRunnerTagCount(inventory)), true
	case pro_interfaces.PolicyFieldEnvironmentIDs:
		return policyGuardrailIntegersField(len(input.EnvironmentIDs) > 0, input.EnvironmentIDs), true
	case pro_interfaces.PolicyFieldEnvironmentCount:
		return policyGuardrailIntegerField(true, len(input.EnvironmentIDs)), true
	case pro_interfaces.PolicyFieldWorkflowPresent:
		return policyGuardrailBooleanField(true, workflow != nil), true
	case pro_interfaces.PolicyFieldWorkflowID:
		return policyGuardrailIntegerField(workflow != nil, policyGuardrailWorkflowID(workflow)), true
	case pro_interfaces.PolicyFieldWorkflowRevision:
		return policyGuardrailIntegerField(workflow != nil, policyGuardrailWorkflowRevision(workflow)), true
	case pro_interfaces.PolicyFieldWorkflowNodeID:
		return policyGuardrailIntegerField(workflow != nil && workflow.NodeID > 0, policyGuardrailWorkflowNodeID(workflow)), true
	case pro_interfaces.PolicyFieldWorkflowNodeKind:
		return policyGuardrailStringField(workflow != nil && workflow.NodeKind != "", policyGuardrailWorkflowNodeKind(workflow)), true
	case pro_interfaces.PolicyFieldWorkflowTriggerSource:
		return policyGuardrailStringField(workflow != nil && workflow.TriggerSource != "", policyGuardrailWorkflowTriggerSource(workflow)), true
	case pro_interfaces.PolicyFieldWorkflowCrossProject:
		return policyGuardrailBooleanField(workflow != nil, policyGuardrailWorkflowCrossProject(workflow)), true
	case pro_interfaces.PolicyFieldRunnerSelectedID:
		return policyGuardrailIntegerField(input.Runner.SelectedID > 0, input.Runner.SelectedID), true
	case pro_interfaces.PolicyFieldRunnerSelectedScope:
		return policyGuardrailStringField(input.Runner.SelectedScope != "", input.Runner.SelectedScope), true
	case pro_interfaces.PolicyFieldRunnerSelectedExecutor:
		return policyGuardrailStringField(input.Runner.SelectedExecutor != "", input.Runner.SelectedExecutor), true
	case pro_interfaces.PolicyFieldRunnerRequestedTags:
		return policyGuardrailStringsField(len(input.Runner.RequestedTags) > 0, input.Runner.RequestedTags), true
	case pro_interfaces.PolicyFieldRunnerCandidateCount:
		return policyGuardrailIntegerField(true, input.Runner.CandidateCount), true
	case pro_interfaces.PolicyFieldExecutorType:
		return policyGuardrailStringField(true, input.Executor.Type), true
	case pro_interfaces.PolicyFieldImagePresent:
		return policyGuardrailBooleanField(true, input.Executor.ImagePresent), true
	case pro_interfaces.PolicyFieldImageReferenceKind:
		return policyGuardrailStringField(true, input.Executor.ImageReferenceKind), true
	case pro_interfaces.PolicyFieldImageDigest:
		return policyGuardrailStringField(input.Executor.ImageDigest != "", input.Executor.ImageDigest), true
	case pro_interfaces.PolicyFieldCredentialIDs:
		return policyGuardrailIntegersField(len(input.Credentials) > 0, policyGuardrailCredentialIDs(input.Credentials)), true
	case pro_interfaces.PolicyFieldCredentialScopes:
		return policyGuardrailStringsField(len(input.Credentials) > 0, policyGuardrailCredentialScopes(input.Credentials)), true
	case pro_interfaces.PolicyFieldCredentialBindingTargets:
		return policyGuardrailStringsField(len(input.Credentials) > 0, policyGuardrailCredentialBindingTargets(input.Credentials)), true
	case pro_interfaces.PolicyFieldCredentialCount:
		return policyGuardrailIntegerField(true, len(input.Credentials)), true
	case pro_interfaces.PolicyFieldTimeWeekday:
		return policyGuardrailStringField(true, strings.ToLower(input.EvaluatedAt.UTC().Weekday().String())), true
	case pro_interfaces.PolicyFieldTimeMinuteOfDay:
		return policyGuardrailIntegerField(true, input.EvaluatedAt.UTC().Hour()*60+input.EvaluatedAt.UTC().Minute()), true
	default:
		return policyGuardrailFieldValue{}, false
	}
}

func policyGuardrailTemplateID(value *pro_interfaces.PolicyGuardrailTemplateMetadata) int {
	if value == nil {
		return 0
	}
	return value.ID
}
func policyGuardrailTemplateApplication(value *pro_interfaces.PolicyGuardrailTemplateMetadata) string {
	if value == nil {
		return ""
	}
	return value.Application
}
func policyGuardrailTemplateSource(value *pro_interfaces.PolicyGuardrailTemplateMetadata) string {
	if value == nil {
		return ""
	}
	return value.Source
}
func policyGuardrailTemplateInventoryOverride(value *pro_interfaces.PolicyGuardrailTemplateMetadata) bool {
	return value != nil && value.InventoryOverride
}
func policyGuardrailTemplateBranchOverride(value *pro_interfaces.PolicyGuardrailTemplateMetadata) bool {
	return value != nil && value.BranchOverride
}
func policyGuardrailTemplateCommitOverride(value *pro_interfaces.PolicyGuardrailTemplateMetadata) bool {
	return value != nil && value.CommitOverride
}
func policyGuardrailTemplateArgumentKeyCount(value *pro_interfaces.PolicyGuardrailTemplateMetadata) int {
	if value == nil {
		return 0
	}
	return len(value.ArgumentKeys)
}
func policyGuardrailTemplateInputKeyCount(value *pro_interfaces.PolicyGuardrailTemplateMetadata) int {
	if value == nil {
		return 0
	}
	return len(value.InputKeys)
}
func policyGuardrailInventoryID(value *pro_interfaces.PolicyGuardrailInventoryMetadata) int {
	if value == nil {
		return 0
	}
	return value.ID
}
func policyGuardrailInventoryType(value *pro_interfaces.PolicyGuardrailInventoryMetadata) string {
	if value == nil {
		return ""
	}
	return value.Type
}
func policyGuardrailInventoryRunnerTagCount(value *pro_interfaces.PolicyGuardrailInventoryMetadata) int {
	if value == nil {
		return 0
	}
	return value.RunnerTagCount
}
func policyGuardrailWorkflowID(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) int {
	if value == nil {
		return 0
	}
	return value.ID
}
func policyGuardrailWorkflowRevision(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) int {
	if value == nil {
		return 0
	}
	return value.Revision
}
func policyGuardrailWorkflowNodeID(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) int {
	if value == nil {
		return 0
	}
	return value.NodeID
}
func policyGuardrailWorkflowNodeKind(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) string {
	if value == nil {
		return ""
	}
	return value.NodeKind
}
func policyGuardrailWorkflowTriggerSource(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) string {
	if value == nil {
		return ""
	}
	return value.TriggerSource
}
func policyGuardrailWorkflowCrossProject(value *pro_interfaces.PolicyGuardrailWorkflowMetadata) bool {
	return value != nil && value.CrossProject
}
func policyGuardrailCredentialIDs(values []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata) []int {
	result := make([]int, len(values))
	for index := range values {
		result[index] = values[index].ID
	}
	return result
}
func policyGuardrailCredentialScopes(values []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].Scope
	}
	return result
}
func policyGuardrailCredentialBindingTargets(values []pro_interfaces.PolicyGuardrailCredentialReferenceMetadata) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].BindingTarget
	}
	return result
}
func policyGuardrailStringField(present bool, value string) policyGuardrailFieldValue {
	return policyGuardrailFieldValue{present: present, stringValue: value}
}
func policyGuardrailIntegerField(present bool, value int) policyGuardrailFieldValue {
	return policyGuardrailFieldValue{present: present, integerValue: value}
}
func policyGuardrailBooleanField(present bool, value bool) policyGuardrailFieldValue {
	return policyGuardrailFieldValue{present: present, booleanValue: value}
}
func policyGuardrailStringsField(present bool, values []string) policyGuardrailFieldValue {
	return policyGuardrailFieldValue{present: present, stringValues: values}
}
func policyGuardrailIntegersField(present bool, values []int) policyGuardrailFieldValue {
	return policyGuardrailFieldValue{present: present, integerValues: values}
}
func containsPolicyGuardrailString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func containsPolicyGuardrailInteger(values []int, expected int) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func policyGuardrailContainsAnyStrings(values, expected []string) bool {
	for _, value := range expected {
		if containsPolicyGuardrailString(values, value) {
			return true
		}
	}
	return false
}
func policyGuardrailContainsAnyIntegers(values, expected []int) bool {
	for _, value := range expected {
		if containsPolicyGuardrailInteger(values, value) {
			return true
		}
	}
	return false
}
func policyGuardrailContainsAllStrings(values, expected []string) bool {
	for _, value := range expected {
		if !containsPolicyGuardrailString(values, value) {
			return false
		}
	}
	return true
}
func policyGuardrailContainsAllIntegers(values, expected []int) bool {
	for _, value := range expected {
		if !containsPolicyGuardrailInteger(values, value) {
			return false
		}
	}
	return true
}
