package pro_interfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

const (
	ExecutionPreflightContractVersion       = 1
	MaxExecutionPreflightInputs             = 128
	MaxExecutionPreflightReferences         = 256
	MaxExecutionPreflightCommands           = 128
	MaxExecutionPreflightPlacements         = 128
	MaxExecutionPreflightCandidates         = 100
	MaxExecutionPreflightFindings           = 128
	MaxExecutionPreflightChanges            = 16
	MaxExecutionPreflightStringBytes        = 2048
	ExecutionPreflightReviewTokenVersion    = 1
	MaxExecutionPreflightReviewTokenBytes   = 2048
	MaxExecutionPreflightReviewPayloadBytes = 1024
)

const MaxExecutionPreflightReviewTTL = 5 * time.Minute

type ExecutionPreflightIntent string

const (
	ExecutionPreflightTask     ExecutionPreflightIntent = "task"
	ExecutionPreflightWorkflow ExecutionPreflightIntent = "workflow"
)

type ExecutionPreflightReferenceKind string

const (
	ExecutionReferenceTemplate    ExecutionPreflightReferenceKind = "template"
	ExecutionReferenceWorkflow    ExecutionPreflightReferenceKind = "workflow"
	ExecutionReferenceInventory   ExecutionPreflightReferenceKind = "inventory"
	ExecutionReferenceRepository  ExecutionPreflightReferenceKind = "repository"
	ExecutionReferenceEnvironment ExecutionPreflightReferenceKind = "environment"
	ExecutionReferenceCredential  ExecutionPreflightReferenceKind = "credential"
	ExecutionReferenceVault       ExecutionPreflightReferenceKind = "vault"
)

type ExecutionPreflightReasonCode string

const (
	ExecutionReasonSelected              ExecutionPreflightReasonCode = "selected"
	ExecutionReasonProvisionalPlacement  ExecutionPreflightReasonCode = "provisional_placement"
	ExecutionReasonDifferentProject      ExecutionPreflightReasonCode = "different_project"
	ExecutionReasonInactive              ExecutionPreflightReasonCode = "inactive"
	ExecutionReasonNotRegistered         ExecutionPreflightReasonCode = "not_registered"
	ExecutionReasonOffline               ExecutionPreflightReasonCode = "offline"
	ExecutionReasonCapacity              ExecutionPreflightReasonCode = "capacity_exhausted"
	ExecutionReasonTagMismatch           ExecutionPreflightReasonCode = "tag_mismatch"
	ExecutionReasonImageUnsupported      ExecutionPreflightReasonCode = "executor_image_unsupported"
	ExecutionReasonNoCandidate           ExecutionPreflightReasonCode = "no_candidate"
	ExecutionReasonHiddenReference       ExecutionPreflightReasonCode = "hidden_reference"
	ExecutionReasonPermissionDenied      ExecutionPreflightReasonCode = "permission_denied"
	ExecutionReasonCapabilityUnavailable ExecutionPreflightReasonCode = "capability_unavailable"
	ExecutionReasonPolicyDenied          ExecutionPreflightReasonCode = "policy_denied"
	ExecutionReasonPlanLimitExceeded     ExecutionPreflightReasonCode = "plan_limit_exceeded"
	ExecutionReasonInvalidInput          ExecutionPreflightReasonCode = "invalid_input"
)

type ExecutionPreflightFindingSeverity string

const (
	ExecutionFindingInfo    ExecutionPreflightFindingSeverity = "info"
	ExecutionFindingWarning ExecutionPreflightFindingSeverity = "warning"
	ExecutionFindingDenial  ExecutionPreflightFindingSeverity = "denial"
)

type ExecutionPreflightChangeCode string

const (
	ExecutionChangeDefinition ExecutionPreflightChangeCode = "definition_changed"
	ExecutionChangeInput      ExecutionPreflightChangeCode = "input_changed"
	ExecutionChangeReference  ExecutionPreflightChangeCode = "reference_changed"
	ExecutionChangePlacement  ExecutionPreflightChangeCode = "placement_changed"
	ExecutionChangePermission ExecutionPreflightChangeCode = "permission_changed"
	ExecutionChangeCapability ExecutionPreflightChangeCode = "capability_changed"
	ExecutionChangePolicy     ExecutionPreflightChangeCode = "policy_changed"
)

// ExecutionPreflightComponentDigest is a value-free comparison input carried
// by a signed review token. The digest allows a start request to explain which
// safe plan component changed without retaining the reviewed plan server-side.
type ExecutionPreflightComponentDigest struct {
	Code   ExecutionPreflightChangeCode `json:"code"`
	Digest string                       `json:"digest"`
}

// ExecutionPreflightReviewBinding is derived from authenticated request
// context and, when available, the freshly recomputed plan. It is never client
// supplied. Fingerprint is optional only while a caller verifies an older
// review in order to calculate a safe stale-preview diff.
type ExecutionPreflightReviewBinding struct {
	ActorID     int                      `json:"actor_id"`
	ProjectID   int                      `json:"project_id"`
	Intent      ExecutionPreflightIntent `json:"intent"`
	TemplateID  int                      `json:"template_id,omitempty"`
	WorkflowID  int                      `json:"workflow_id,omitempty"`
	Fingerprint string                   `json:"fingerprint"`
}

func (b ExecutionPreflightReviewBinding) Validate() error {
	if b.ActorID <= 0 || b.ProjectID <= 0 || !validExecutionPreflightIntent(b.Intent) ||
		(b.Intent == ExecutionPreflightTask && (b.TemplateID <= 0 || b.WorkflowID != 0)) ||
		(b.Intent == ExecutionPreflightWorkflow && (b.WorkflowID <= 0 || b.TemplateID != 0)) ||
		(b.Fingerprint != "" && !validExecutionPreflightFingerprint(b.Fingerprint)) {
		return errors.New("invalid execution preflight review binding")
	}
	return nil
}

// ExecutionPreflightReviewTokenClaims is the minimal, value-free authenticated
// claim set. It intentionally excludes plans, commands, references, and input
// values; those remain in the response DTO and are recomputed before start.
type ExecutionPreflightReviewTokenClaims struct {
	Version          int                                 `json:"v"`
	ActorID          int                                 `json:"actor_id"`
	ProjectID        int                                 `json:"project_id"`
	Intent           ExecutionPreflightIntent            `json:"intent"`
	TemplateID       int                                 `json:"template_id,omitempty"`
	WorkflowID       int                                 `json:"workflow_id,omitempty"`
	Fingerprint      string                              `json:"fingerprint"`
	IssuedAt         int64                               `json:"iat"`
	ExpiresAt        int64                               `json:"exp"`
	Nonce            string                              `json:"nonce"`
	ComponentDigests []ExecutionPreflightComponentDigest `json:"components"`
}

// ExecutionPreflightReviewTokenError is a stable internal classification for
// malformed, expired, and wrongly scoped review tokens. Its text never
// includes untrusted token data or decoded claims.
type ExecutionPreflightReviewTokenError struct {
	Code string
}

func (e ExecutionPreflightReviewTokenError) Error() string {
	return "execution preflight review token " + e.Code
}

func (e ExecutionPreflightReviewTokenError) Is(target error) bool {
	other, ok := target.(ExecutionPreflightReviewTokenError)
	return ok && e.Code == other.Code
}

var (
	ErrExecutionPreflightReviewTokenInvalid  = ExecutionPreflightReviewTokenError{Code: "invalid"}
	ErrExecutionPreflightReviewTokenExpired  = ExecutionPreflightReviewTokenError{Code: "expired"}
	ErrExecutionPreflightReviewScopeMismatch = ExecutionPreflightReviewTokenError{Code: "scope_mismatch"}
)

type ExecutionPreflightDefinition struct {
	Kind        ExecutionPreflightReferenceKind `json:"kind"`
	ID          int                             `json:"id"`
	Name        string                          `json:"name,omitempty"`
	Revision    string                          `json:"revision"`
	Fingerprint string                          `json:"fingerprint"`
}

type ExecutionPreflightInput struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Source    string `json:"source"`
	Present   bool   `json:"present"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type ExecutionPreflightReference struct {
	Kind          ExecutionPreflightReferenceKind `json:"kind"`
	ID            int                             `json:"id"`
	Name          string                          `json:"name,omitempty"`
	Revision      string                          `json:"revision,omitempty"`
	Fingerprint   string                          `json:"fingerprint,omitempty"`
	BindingTarget string                          `json:"binding_target,omitempty"`
	Visible       bool                            `json:"visible"`
	Reason        ExecutionPreflightReasonCode    `json:"reason,omitempty"`
}

type ExecutionPreflightCommand struct {
	NodeID         *int           `json:"node_id,omitempty"`
	TemplateID     int            `json:"template_id"`
	Application    db.TemplateApp `json:"application"`
	Playbook       string         `json:"playbook,omitempty"`
	ArgumentKeys   []string       `json:"argument_keys,omitempty"`
	InputKeys      []string       `json:"input_keys,omitempty"`
	BranchOverride bool           `json:"branch_override"`
}

type ExecutionPreflightCandidate struct {
	RunnerID        int                            `json:"runner_id"`
	RunnerName      string                         `json:"runner_name"`
	Scope           db.RunnerPlacementScope        `json:"scope"`
	Executor        db.RunnerExecutorType          `json:"executor"`
	Eligible        bool                           `json:"eligible"`
	AcceptedReasons []ExecutionPreflightReasonCode `json:"accepted_reasons,omitempty"`
	RejectedReasons []ExecutionPreflightReasonCode `json:"rejected_reasons,omitempty"`
}

type ExecutionPreflightPlacement struct {
	NodeID           *int                          `json:"node_id,omitempty"`
	RequestedTags    []string                      `json:"requested_tags,omitempty"`
	MatchMode        db.RunnerTagMatchMode         `json:"match_mode"`
	RequestedImage   *string                       `json:"requested_executor_image,omitempty"`
	SelectedRunnerID *int                          `json:"selected_runner_id,omitempty"`
	SelectedName     string                        `json:"selected_runner_name,omitempty"`
	SelectedScope    db.RunnerPlacementScope       `json:"selected_scope,omitempty"`
	Decision         ExecutionPreflightReasonCode  `json:"decision"`
	Provisional      bool                          `json:"provisional"`
	Candidates       []ExecutionPreflightCandidate `json:"candidates"`
}

type ExecutionPreflightFinding struct {
	Severity ExecutionPreflightFindingSeverity `json:"severity"`
	Code     ExecutionPreflightReasonCode      `json:"code"`
	Message  string                            `json:"message"`
	NodeID   *int                              `json:"node_id,omitempty"`
}

type ExecutionPreflightPlan struct {
	ContractVersion int                           `json:"contract_version"`
	Intent          ExecutionPreflightIntent      `json:"intent"`
	ProjectID       int                           `json:"project_id"`
	ActorID         int                           `json:"actor_id"`
	TemplateID      int                           `json:"template_id,omitempty"`
	WorkflowID      int                           `json:"workflow_id,omitempty"`
	Definition      ExecutionPreflightDefinition  `json:"definition"`
	Inputs          []ExecutionPreflightInput     `json:"inputs"`
	References      []ExecutionPreflightReference `json:"references"`
	Commands        []ExecutionPreflightCommand   `json:"commands"`
	Placements      []ExecutionPreflightPlacement `json:"placements"`
	Findings        []ExecutionPreflightFinding   `json:"findings"`
	Fingerprint     string                        `json:"fingerprint"`
	ExpiresAt       time.Time                     `json:"expires_at"`
	ReviewToken     string                        `json:"review_token,omitempty"`
}

// ExecutionPreflightSnapshot keeps private, value-derived comparison digests
// beside the redacted public plan. Components never cross the API boundary.
type ExecutionPreflightSnapshot struct {
	Plan       ExecutionPreflightPlan
	Components map[ExecutionPreflightChangeCode]string
}

// WorkflowExecutionPreflightPlanner is the narrow task-planning seam used by
// the optional Enhanced workflow implementation. Community task enqueue does
// not depend on workflow packages.
type WorkflowExecutionPreflightPlanner interface {
	BuildWorkflowTaskExecutionPreflight(db.Task, db.Template, *db.User, int, time.Time) (ExecutionPreflightSnapshot, error)
	SealExecutionPreflight(ExecutionPreflightSnapshot) (ExecutionPreflightPlan, error)
	VerifyExecutionPreflightReview(ExecutionPreflightSnapshot, ExecutionPreflightReview) ([]ExecutionPreflightChangeCode, error)
}

// WorkflowTaskExecutionSnapshotPlanner materializes the same private,
// non-secret execution envelope used to produce a workflow child plan. The
// optional Enhanced workflow service persists it with the run node so delayed
// dispatch never re-reads mutable resource configuration after review.
type WorkflowTaskExecutionSnapshotPlanner interface {
	BuildWorkflowTaskExecutionPreflightSnapshot(db.Task, db.Template, *db.User, int, time.Time) (ExecutionPreflightSnapshot, string, error)
}

// WorkflowExecutionPreflightService is an optional Enhanced seam. Keeping it
// separate preserves the established Community WorkflowService interface.
type WorkflowExecutionPreflightService interface {
	PreviewWorkflowExecution(db.WorkflowTemplate, *db.User, ...db.WorkflowRunInput) (ExecutionPreflightPlan, error)
	StartWorkflowWithExecutionPreflight(db.WorkflowTemplate, *db.User, string, ExecutionPreflightReview, ...db.WorkflowRunInput) (db.WorkflowRun, error)
}

// WorkflowExecutionPreflightAuditResultService exposes the fresh trusted plan
// used for a start decision without widening the established workflow service
// interface. HTTP controllers use it only for value-free audit provenance.
type WorkflowExecutionPreflightAuditResultService interface {
	StartWorkflowWithExecutionPreflightPlan(db.WorkflowTemplate, *db.User, string, ExecutionPreflightReview, ...db.WorkflowRunInput) (db.WorkflowRun, ExecutionPreflightPlan, error)
}

// WorkflowExecutionPreflightOverrideService is an optional manual-start seam.
// It deliberately accepts only the request-only override envelope; the
// implementation derives actor, source, origin, and authorization at the
// final admission boundary. Trigger, schedule, API, and webhook callers keep
// using WorkflowService and therefore cannot supply an override.
type WorkflowExecutionPreflightOverrideService interface {
	StartWorkflowWithExecutionPreflightAndDeploymentWindowOverride(db.WorkflowTemplate, *db.User, string, ExecutionPreflightReview, *DeploymentWindowOverrideInput, ...db.WorkflowRunInput) (db.WorkflowRun, error)
}

// WorkflowExecutionPreflightOverrideAuditResultService is the audit-capable
// variant of WorkflowExecutionPreflightOverrideService. It covers both
// reviewed and headerless manual starts because the review value may be empty.
type WorkflowExecutionPreflightOverrideAuditResultService interface {
	StartWorkflowWithExecutionPreflightPlanAndDeploymentWindowOverride(db.WorkflowTemplate, *db.User, string, ExecutionPreflightReview, *DeploymentWindowOverrideInput, ...db.WorkflowRunInput) (db.WorkflowRun, ExecutionPreflightPlan, error)
}

type ExecutionPreflightReview struct {
	Fingerprint string    `json:"fingerprint"`
	ReviewToken string    `json:"review_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ExecutionPreflightStale struct {
	Code      string                         `json:"code"`
	Changes   []ExecutionPreflightChangeCode `json:"changes"`
	Preflight *ExecutionPreflightPlan        `json:"preflight,omitempty"`
}

type ExecutionPreflightStaleError struct {
	Changes   []ExecutionPreflightChangeCode
	Preflight ExecutionPreflightPlan
}

func (e *ExecutionPreflightStaleError) Error() string { return "execution preflight is stale" }

type ExecutionPreflightDeniedError struct {
	Preflight ExecutionPreflightPlan
}

func (e *ExecutionPreflightDeniedError) Error() string { return "execution preflight denied" }

func (p ExecutionPreflightPlan) Validate() error {
	if p.ContractVersion != ExecutionPreflightContractVersion {
		return errors.New("unsupported execution preflight contract version")
	}
	if p.Intent != ExecutionPreflightTask && p.Intent != ExecutionPreflightWorkflow {
		return errors.New("invalid execution preflight intent")
	}
	if p.ProjectID <= 0 || p.ActorID <= 0 {
		return errors.New("execution preflight project and actor are required")
	}
	if p.Intent == ExecutionPreflightTask && (p.TemplateID <= 0 || p.WorkflowID != 0) {
		return errors.New("task preflight requires exactly one template")
	}
	if p.Intent == ExecutionPreflightWorkflow && (p.WorkflowID <= 0 || p.TemplateID != 0) {
		return errors.New("workflow preflight requires exactly one workflow")
	}
	if len(p.Inputs) > MaxExecutionPreflightInputs || len(p.References) > MaxExecutionPreflightReferences ||
		len(p.Commands) > MaxExecutionPreflightCommands || len(p.Placements) > MaxExecutionPreflightPlacements ||
		len(p.Findings) > MaxExecutionPreflightFindings {
		return errors.New("execution preflight plan limit exceeded")
	}
	if err := validatePreflightString(p.Definition.Name); err != nil {
		return fmt.Errorf("definition name: %w", err)
	}
	for _, placement := range p.Placements {
		if !placement.Provisional {
			return errors.New("execution preflight placement must be provisional")
		}
		if len(placement.Candidates) > MaxExecutionPreflightCandidates {
			return errors.New("execution preflight candidate limit exceeded")
		}
	}
	for _, input := range p.Inputs {
		if input.Name == "" || validatePreflightString(input.Name) != nil || validatePreflightString(input.Type) != nil || validatePreflightString(input.Source) != nil {
			return errors.New("invalid execution preflight input")
		}
	}
	for _, reference := range p.References {
		if err := validatePreflightString(reference.Name); err != nil {
			return errors.New("invalid execution preflight reference")
		}
	}
	for _, finding := range p.Findings {
		if finding.Severity != ExecutionFindingInfo && finding.Severity != ExecutionFindingWarning && finding.Severity != ExecutionFindingDenial {
			return errors.New("invalid execution preflight finding severity")
		}
		if err := validatePreflightString(finding.Message); err != nil {
			return errors.New("invalid execution preflight finding")
		}
	}
	return nil
}

func validatePreflightString(value string) error {
	if len(value) > MaxExecutionPreflightStringBytes || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("value exceeds execution preflight bounds")
	}
	return nil
}

func FingerprintExecutionPreflight(plan ExecutionPreflightPlan) (string, error) {
	canonical := canonicalExecutionPreflight(plan)
	canonical.Fingerprint = ""
	canonical.ExpiresAt = time.Time{}
	canonical.ReviewToken = ""
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode execution preflight fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ExecutionPreflightReviewBindingFromPlan returns the authenticated binding
// expected when the reviewed plan is started. Callers must first compute and
// assign the canonical fingerprint to the plan.
func ExecutionPreflightReviewBindingFromPlan(plan ExecutionPreflightPlan) ExecutionPreflightReviewBinding {
	return ExecutionPreflightReviewBinding{
		ActorID: plan.ActorID, ProjectID: plan.ProjectID, Intent: plan.Intent,
		TemplateID: plan.TemplateID, WorkflowID: plan.WorkflowID, Fingerprint: plan.Fingerprint,
	}
}

// ExecutionPreflightComponentDigests derives a fixed, value-free digest set
// for the bounded stale-preview categories. It is deterministic even when the
// planner assembled semantically unordered slices in different orders.
func ExecutionPreflightComponentDigests(plan ExecutionPreflightPlan) ([]ExecutionPreflightComponentDigest, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	canonical := canonicalExecutionPreflight(plan)
	components := []struct {
		code  ExecutionPreflightChangeCode
		value any
	}{
		{ExecutionChangeDefinition, struct {
			Definition ExecutionPreflightDefinition
			Commands   []ExecutionPreflightCommand
		}{Definition: canonical.Definition, Commands: canonical.Commands}},
		{ExecutionChangeInput, canonical.Inputs},
		{ExecutionChangeReference, canonical.References},
		{ExecutionChangePlacement, canonical.Placements},
		{ExecutionChangePermission, preflightFindingsForCode(canonical.Findings, ExecutionReasonPermissionDenied)},
		{ExecutionChangeCapability, preflightFindingsForCode(canonical.Findings, ExecutionReasonCapabilityUnavailable)},
		{ExecutionChangePolicy, preflightPolicyFindings(canonical.Findings)},
	}
	result := make([]ExecutionPreflightComponentDigest, 0, len(components))
	for _, component := range components {
		encoded, err := json.Marshal(component.value)
		if err != nil {
			return nil, errors.New("encode execution preflight component")
		}
		digest := sha256.Sum256(encoded)
		result = append(result, ExecutionPreflightComponentDigest{
			Code: component.code, Digest: "sha256:" + hex.EncodeToString(digest[:]),
		})
	}
	return result, nil
}

// DiffExecutionPreflightComponentDigests returns bounded stable change codes
// without re-exposing the previous plan or any reviewed input values.
func DiffExecutionPreflightComponentDigests(previous, current []ExecutionPreflightComponentDigest) ([]ExecutionPreflightChangeCode, error) {
	if err := validateExecutionPreflightComponentDigests(previous); err != nil {
		return nil, err
	}
	if err := validateExecutionPreflightComponentDigests(current); err != nil {
		return nil, err
	}
	before := make(map[ExecutionPreflightChangeCode]string, len(previous))
	for _, component := range previous {
		before[component.Code] = component.Digest
	}
	after := make(map[ExecutionPreflightChangeCode]string, len(current))
	for _, component := range current {
		after[component.Code] = component.Digest
	}
	changes := make([]ExecutionPreflightChangeCode, 0, MaxExecutionPreflightChanges)
	for _, code := range executionPreflightChangeCodes() {
		if before[code] != after[code] {
			changes = append(changes, code)
		}
	}
	return changes, nil
}

func DiffExecutionPreflight(previous, current ExecutionPreflightPlan) []ExecutionPreflightChangeCode {
	changes := make([]ExecutionPreflightChangeCode, 0, 7)
	appendChange := func(changed bool, code ExecutionPreflightChangeCode) {
		if changed && len(changes) < MaxExecutionPreflightChanges {
			changes = append(changes, code)
		}
	}
	appendChange(previous.Definition.Fingerprint != current.Definition.Fingerprint || previous.Definition.Revision != current.Definition.Revision, ExecutionChangeDefinition)
	appendChange(!preflightJSONEqual(canonicalExecutionPreflight(previous).Inputs, canonicalExecutionPreflight(current).Inputs), ExecutionChangeInput)
	appendChange(!preflightJSONEqual(canonicalExecutionPreflight(previous).References, canonicalExecutionPreflight(current).References), ExecutionChangeReference)
	appendChange(!preflightJSONEqual(canonicalExecutionPreflight(previous).Placements, canonicalExecutionPreflight(current).Placements), ExecutionChangePlacement)
	appendChange(preflightFindingsChanged(previous, current, ExecutionReasonPermissionDenied), ExecutionChangePermission)
	appendChange(preflightFindingsChanged(previous, current, ExecutionReasonCapabilityUnavailable), ExecutionChangeCapability)
	appendChange(preflightPolicyChanged(previous, current), ExecutionChangePolicy)
	return changes
}

func canonicalExecutionPreflight(plan ExecutionPreflightPlan) ExecutionPreflightPlan {
	result := plan
	result.Inputs = append([]ExecutionPreflightInput(nil), plan.Inputs...)
	result.References = append([]ExecutionPreflightReference(nil), plan.References...)
	result.Commands = append([]ExecutionPreflightCommand(nil), plan.Commands...)
	result.Placements = append([]ExecutionPreflightPlacement(nil), plan.Placements...)
	result.Findings = append([]ExecutionPreflightFinding(nil), plan.Findings...)
	sort.Slice(result.Inputs, func(i, j int) bool {
		if result.Inputs[i].Name != result.Inputs[j].Name {
			return result.Inputs[i].Name < result.Inputs[j].Name
		}
		if result.Inputs[i].Source != result.Inputs[j].Source {
			return result.Inputs[i].Source < result.Inputs[j].Source
		}
		return result.Inputs[i].Type < result.Inputs[j].Type
	})
	sort.Slice(result.References, func(i, j int) bool {
		if result.References[i].Kind != result.References[j].Kind {
			return result.References[i].Kind < result.References[j].Kind
		}
		return result.References[i].ID < result.References[j].ID
	})
	for i := range result.Commands {
		result.Commands[i].ArgumentKeys = append([]string(nil), result.Commands[i].ArgumentKeys...)
		result.Commands[i].InputKeys = append([]string(nil), result.Commands[i].InputKeys...)
		sort.Strings(result.Commands[i].ArgumentKeys)
		sort.Strings(result.Commands[i].InputKeys)
	}
	sort.Slice(result.Commands, func(i, j int) bool {
		if preflightNodeID(result.Commands[i].NodeID) != preflightNodeID(result.Commands[j].NodeID) {
			return preflightNodeID(result.Commands[i].NodeID) < preflightNodeID(result.Commands[j].NodeID)
		}
		return result.Commands[i].TemplateID < result.Commands[j].TemplateID
	})
	for i := range result.Placements {
		result.Placements[i].RequestedTags = append([]string(nil), result.Placements[i].RequestedTags...)
		sort.Strings(result.Placements[i].RequestedTags)
		result.Placements[i].Candidates = append([]ExecutionPreflightCandidate(nil), result.Placements[i].Candidates...)
		for candidate := range result.Placements[i].Candidates {
			result.Placements[i].Candidates[candidate].AcceptedReasons = append([]ExecutionPreflightReasonCode(nil), result.Placements[i].Candidates[candidate].AcceptedReasons...)
			result.Placements[i].Candidates[candidate].RejectedReasons = append([]ExecutionPreflightReasonCode(nil), result.Placements[i].Candidates[candidate].RejectedReasons...)
			sort.Slice(result.Placements[i].Candidates[candidate].AcceptedReasons, func(a, b int) bool {
				return result.Placements[i].Candidates[candidate].AcceptedReasons[a] < result.Placements[i].Candidates[candidate].AcceptedReasons[b]
			})
			sort.Slice(result.Placements[i].Candidates[candidate].RejectedReasons, func(a, b int) bool {
				return result.Placements[i].Candidates[candidate].RejectedReasons[a] < result.Placements[i].Candidates[candidate].RejectedReasons[b]
			})
		}
		sort.Slice(result.Placements[i].Candidates, func(a, b int) bool {
			return result.Placements[i].Candidates[a].RunnerID < result.Placements[i].Candidates[b].RunnerID
		})
	}
	sort.Slice(result.Placements, func(i, j int) bool {
		return preflightNodeID(result.Placements[i].NodeID) < preflightNodeID(result.Placements[j].NodeID)
	})
	sort.Slice(result.Findings, func(i, j int) bool {
		if result.Findings[i].Severity != result.Findings[j].Severity {
			return result.Findings[i].Severity < result.Findings[j].Severity
		}
		if result.Findings[i].Code != result.Findings[j].Code {
			return result.Findings[i].Code < result.Findings[j].Code
		}
		return result.Findings[i].Message < result.Findings[j].Message
	})
	return result
}

func preflightNodeID(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func preflightJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func preflightFindingsChanged(previous, current ExecutionPreflightPlan, code ExecutionPreflightReasonCode) bool {
	filter := func(findings []ExecutionPreflightFinding) []ExecutionPreflightFinding {
		result := make([]ExecutionPreflightFinding, 0)
		for _, finding := range findings {
			if finding.Code == code {
				result = append(result, finding)
			}
		}
		return result
	}
	return !preflightJSONEqual(filter(previous.Findings), filter(current.Findings))
}

func preflightPolicyChanged(previous, current ExecutionPreflightPlan) bool {
	policyCodes := map[ExecutionPreflightReasonCode]bool{
		ExecutionReasonPolicyDenied:      true,
		ExecutionReasonPlanLimitExceeded: true,
		ExecutionReasonInvalidInput:      true,
	}
	filter := func(findings []ExecutionPreflightFinding) []ExecutionPreflightFinding {
		result := make([]ExecutionPreflightFinding, 0)
		for _, finding := range findings {
			if policyCodes[finding.Code] {
				result = append(result, finding)
			}
		}
		return result
	}
	return !preflightJSONEqual(filter(previous.Findings), filter(current.Findings))
}

func validExecutionPreflightIntent(intent ExecutionPreflightIntent) bool {
	return intent == ExecutionPreflightTask || intent == ExecutionPreflightWorkflow
}

func validExecutionPreflightFingerprint(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func executionPreflightChangeCodes() []ExecutionPreflightChangeCode {
	return []ExecutionPreflightChangeCode{
		ExecutionChangeDefinition,
		ExecutionChangeInput,
		ExecutionChangeReference,
		ExecutionChangePlacement,
		ExecutionChangePermission,
		ExecutionChangeCapability,
		ExecutionChangePolicy,
	}
}

func validateExecutionPreflightComponentDigests(components []ExecutionPreflightComponentDigest) error {
	codes := executionPreflightChangeCodes()
	if len(components) != len(codes) {
		return errors.New("execution preflight component digests are invalid")
	}
	seen := make(map[ExecutionPreflightChangeCode]struct{}, len(components))
	for _, component := range components {
		if !validExecutionPreflightFingerprint(component.Digest) {
			return errors.New("execution preflight component digests are invalid")
		}
		if _, duplicate := seen[component.Code]; duplicate {
			return errors.New("execution preflight component digests are invalid")
		}
		seen[component.Code] = struct{}{}
	}
	for _, code := range codes {
		if _, present := seen[code]; !present {
			return errors.New("execution preflight component digests are invalid")
		}
	}
	return nil
}

func preflightFindingsForCode(findings []ExecutionPreflightFinding, code ExecutionPreflightReasonCode) []ExecutionPreflightFinding {
	result := make([]ExecutionPreflightFinding, 0)
	for _, finding := range findings {
		if finding.Code == code {
			result = append(result, finding)
		}
	}
	return result
}

func preflightPolicyFindings(findings []ExecutionPreflightFinding) []ExecutionPreflightFinding {
	policyCodes := map[ExecutionPreflightReasonCode]bool{
		ExecutionReasonPolicyDenied:      true,
		ExecutionReasonPlanLimitExceeded: true,
		ExecutionReasonInvalidInput:      true,
	}
	result := make([]ExecutionPreflightFinding, 0)
	for _, finding := range findings {
		if policyCodes[finding.Code] {
			result = append(result, finding)
		}
	}
	return result
}
