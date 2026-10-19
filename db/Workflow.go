package db

import (
	"regexp"
	"time"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

type WorkflowEdgeCondition string

const (
	WorkflowEdgeOnSuccess  WorkflowEdgeCondition = "on_success"
	WorkflowEdgeOnFailure  WorkflowEdgeCondition = "on_failure"
	WorkflowEdgeAlways     WorkflowEdgeCondition = "always"
	WorkflowEdgeExpression WorkflowEdgeCondition = "expression"
)

type WorkflowNodeKind string

const (
	WorkflowNodeTaskKind     WorkflowNodeKind = "task"
	WorkflowNodeApprovalKind WorkflowNodeKind = "approval"
	WorkflowNodeNoteKind     WorkflowNodeKind = "note"
	WorkflowNodeDelayKind    WorkflowNodeKind = "delay"
)

// WorkflowEdgeInputMode says how the outputs of the edge's source node feed
// the survey variables of its destination task node.
type WorkflowEdgeInputMode string

const (
	// WorkflowEdgeInputByName delivers every output whose name equals the name
	// of a non-secret survey variable of the destination template. The default;
	// an empty mode means by_name.
	WorkflowEdgeInputByName WorkflowEdgeInputMode = "by_name"
	// WorkflowEdgeInputExplicit delivers only the pairs listed in InputMappings.
	WorkflowEdgeInputExplicit WorkflowEdgeInputMode = "explicit"
)

// WorkflowInputMapping feeds one survey variable of the destination template
// (Var) from one top-level output of the source node (Key).
type WorkflowInputMapping struct {
	Var string `json:"var" backup:"var"`
	Key string `json:"key" backup:"key"`
}

// WorkflowNodeSurveyInput binds a non-secret survey variable of a task node's
// template to a workflow survey variable. Bindings are explicit: equal names
// alone never transfer a workflow value into a task.
type WorkflowNodeSurveyInput struct {
	Var         string `json:"var" backup:"var"`
	WorkflowVar string `json:"workflow_var" backup:"workflow_var"`
}

// WorkflowRunParams carries the answers supplied when a workflow starts.
// Workflow survey values are deliberately separate from EX parameter values:
// the former are persisted in the run environment while the latter remain in
// the immutable parameter snapshot.
type WorkflowRunParams struct {
	Environment MapStringAnyField `json:"environment,omitempty"`
}

// workflowOutputNamePattern is the shape of an output name. The hyphen is
// allowed because Terraform output names may contain it.
var workflowOutputNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// IsValidWorkflowOutputName reports whether name may be the key of an output
// or of an input mapping.
func IsValidWorkflowOutputName(name string) bool {
	return workflowOutputNamePattern.MatchString(name)
}

type WorkflowConvergenceMode string

const (
	WorkflowConvergenceAll WorkflowConvergenceMode = "all"
	WorkflowConvergenceAny WorkflowConvergenceMode = "any"
)

type WorkflowTemplate struct {
	ID int `db:"id" json:"id" backup:"-"`

	ProjectID int    `db:"project_id" json:"project_id" backup:"-"`
	Name      string `db:"name" json:"name" backup:"name"`

	Description *string `db:"description" json:"description,omitempty" backup:"description"`

	StartVersion *string `db:"start_version" json:"start_version,omitempty" backup:"start_version"`

	// DefinitionVersion identifies the workflow definition schema. Revision is
	// incremented after every successful update and is the optimistic-lock token.
	DefinitionVersion int `db:"definition_version" json:"definition_version" backup:"definition_version"`
	Revision          int `db:"revision" json:"revision" backup:"revision"`
	MaxParallelTasks  int `db:"max_parallel_tasks" json:"max_parallel_tasks" backup:"max_parallel_tasks"`

	ParameterDefinitionsJSON string                         `db:"parameter_definitions" json:"-" backup:"parameter_definitions"`
	ParameterDefinitions     []WorkflowParameterDeclaration `db:"-" json:"parameters,omitempty" backup:"-"`

	AccessPolicyJSON     string               `db:"access_policy" json:"-" backup:"access_policy"`
	AccessPolicyRevision int                  `db:"access_policy_revision" json:"-" backup:"access_policy_revision"`
	AccessPolicy         WorkflowAccessPolicy `db:"-" json:"access_policy,omitempty" backup:"-"`
	CurrentVersionID     int                  `db:"-" json:"current_version_id,omitempty" backup:"-"`
	VersionMessage       string               `db:"-" json:"version_message,omitempty" backup:"-"`

	Nodes []WorkflowNode `db:"-" bolt:"include" json:"nodes" backup:"-"`
	Edges []WorkflowEdge `db:"-" bolt:"include" json:"edges" backup:"edges"`

	// SurveyVars belongs to the workflow graph version. It is copied into the
	// immutable EX definition snapshot with the nodes and edges it names.
	SurveyVars []SurveyVar `db:"-" json:"survey_vars" backup:"survey_vars"`

	// RevisionID is the immutable EX version ID exposed by the revision API.
	// Revision remains the definition CAS token; response metadata is ignored on write.
	RevisionID int `db:"-" json:"revision_id,omitempty" backup:"-"`

	// RevisionAuthorID is set by the API layer before Create/Update so the
	// revision records who saved it. Not part of the JSON contract.
	RevisionAuthorID *int `db:"-" json:"-" backup:"-"`

	LastRun *WorkflowRun `db:"-" json:"last_run,omitempty" backup:"-"`
}

// WorkflowRevision is one immutable snapshot of a workflow template's graph.
// Semaphore EX projects its append-only WorkflowVersion snapshots through this
// compatibility contract, retaining revisions even when they have no runs.
type WorkflowRevision struct {
	ID int `db:"id" json:"id"`

	ProjectID          int `db:"project_id" json:"project_id"`
	WorkflowTemplateID int `db:"workflow_template_id" json:"workflow_template_id"`

	// Number is the 1-based ordinal of the revision within its template; the
	// highest number is the current graph.
	Number int `db:"number" json:"number"`

	Created         time.Time `db:"created" json:"created"`
	CreatedByUserID *int      `db:"created_by_user_id" json:"created_by_user_id,omitempty"`

	// SurveyVarsJSON is the database representation of the immutable revision
	// survey; callers use SurveyVars.
	SurveyVarsJSON *string     `db:"survey_vars" json:"-"`
	SurveyVars     []SurveyVar `db:"-" json:"survey_vars,omitempty"`

	// HasRuns is filled by list queries: true when at least one run pins this
	// revision (such a revision survives later saves).
	HasRuns bool `db:"-" json:"has_runs"`
}

type WorkflowNode struct {
	ID int `db:"id" json:"id" backup:"id"`

	WorkflowTemplateID int  `db:"workflow_template_id" json:"workflow_template_id" backup:"-"`
	RevisionID         *int `db:"revision_id" json:"-" backup:"-"`

	TemplateID                        int                            `db:"template_id" json:"template_id,omitempty" backup:"-"`
	CrossProjectTemplateReferenceJSON string                         `db:"cross_project_template_reference" json:"-" backup:"-"`
	CrossProjectTemplateReference     *CrossProjectTemplateReference `db:"-" json:"cross_project_template_reference,omitempty" backup:"-"`
	DisplayName                       string                         `db:"display_name" json:"display_name,omitempty" backup:"display_name"`
	Kind                              WorkflowNodeKind               `db:"kind" json:"kind,omitempty" backup:"kind"`
	ConvergenceMode                   WorkflowConvergenceMode        `db:"convergence_mode" json:"convergence_mode,omitempty" backup:"convergence_mode"`
	JoinMode                          WorkflowJoinMode               `db:"join_mode" json:"join_mode,omitempty" backup:"join_mode"`
	ApprovalTimeout                   *int                           `db:"approval_timeout" json:"approval_timeout,omitempty" backup:"approval_timeout"`
	ApprovalMessage                   *string                        `db:"approval_message" json:"approval_message,omitempty" backup:"approval_message"`
	ApprovalPermission                ProjectUserPermission          `db:"approval_permission" json:"approval_permission,omitempty" backup:"approval_permission"`
	ApprovalTimeoutOutcome            WorkflowApprovalTimeoutOutcome `db:"approval_timeout_outcome" json:"approval_timeout_outcome,omitempty" backup:"approval_timeout_outcome"`
	ApprovalSeparationOfDuties        bool                           `db:"approval_separation_of_duties" json:"approval_separation_of_duties,omitempty" backup:"approval_separation_of_duties"`
	ApprovalRolePolicyJSON            string                         `db:"approval_role_policy" json:"-" backup:"approval_role_policy"`
	ApprovalRolePolicyRevision        int                            `db:"approval_role_policy_revision" json:"-" backup:"approval_role_policy_revision"`
	ApprovalRolePolicy                WorkflowApprovalRolePolicy     `db:"-" json:"approval_role_policy,omitempty" backup:"-"`

	TaskParamsID *int        `db:"task_params_id" json:"-" backup:"-"`
	TaskParams   *TaskParams `db:"-" json:"task_params,omitempty" backup:"task_params"`

	ArtifactOutputsJSON string                        `db:"artifact_outputs" json:"-" backup:"artifact_outputs"`
	ArtifactInputsJSON  string                        `db:"artifact_inputs" json:"-" backup:"artifact_inputs"`
	ArtifactOutputs     []WorkflowArtifactDeclaration `db:"-" json:"artifact_outputs,omitempty" backup:"-"`
	ArtifactInputs      []WorkflowArtifactReference   `db:"-" json:"artifact_inputs,omitempty" backup:"-"`

	OverridePolicyJSON string                     `db:"override_policy" json:"-" backup:"override_policy"`
	OverridePolicy     WorkflowNodeOverridePolicy `db:"-" json:"override_policy,omitempty" backup:"-"`

	Note         *string `db:"note" json:"note,omitempty" backup:"note"`
	DelaySeconds *int    `db:"delay_seconds" json:"delay_seconds,omitempty" backup:"delay_seconds"`

	// SurveyInputsJSON is the database representation of task-node bindings;
	// callers use SurveyInputs.
	SurveyInputsJSON *string                   `db:"survey_inputs" json:"-" backup:"-"`
	SurveyInputs     []WorkflowNodeSurveyInput `db:"-" json:"survey_inputs,omitempty" backup:"survey_inputs"`

	PositionX int `db:"position_x" json:"position_x" backup:"position_x"`
	PositionY int `db:"position_y" json:"position_y" backup:"position_y"`
}

type WorkflowEdge struct {
	ID int `db:"id" json:"id" backup:"-"`

	WorkflowTemplateID int  `db:"workflow_template_id" json:"workflow_template_id" backup:"-"`
	RevisionID         *int `db:"revision_id" json:"-" backup:"-"`
	SourceNodeID       int  `db:"source_node_id" json:"source_node_id" backup:"source_node_id"`
	DestinationNodeID  int  `db:"destination_node_id" json:"destination_node_id" backup:"destination_node_id"`

	Condition            WorkflowEdgeCondition    `db:"condition" json:"condition" backup:"condition"`
	Label                string                   `db:"label" json:"label,omitempty" backup:"label"`
	Expression           string                   `db:"condition_expression" json:"condition_expression,omitempty" backup:"condition_expression"`
	ConditionProgramJSON string                   `db:"condition_program" json:"-" backup:"condition_program"`
	ConditionProgram     WorkflowConditionProgram `db:"-" json:"condition_program,omitempty" backup:"-"`

	// InputMode and InputMappings say how the outputs of the source node feed
	// the survey variables of the destination task node; see
	// AGENTS/work/workflow-editor/artifacts.md § Consumer. Only an edge into a
	// task node may carry them.
	InputMode WorkflowEdgeInputMode `db:"input_mode" json:"input_mode,omitempty" backup:"input_mode"`

	// InputMappingsJSON used internally for read from database.
	// It is not used for store input mappings to database.
	// Do not use it in your code. Use InputMappings instead.
	InputMappingsJSON *string                `db:"input_mappings" json:"-" backup:"-"`
	InputMappings     []WorkflowInputMapping `db:"-" json:"input_mappings,omitempty" backup:"input_mappings"`
}

func (mode WorkflowEdgeInputMode) Validate() error {
	switch mode {
	case WorkflowEdgeInputByName, WorkflowEdgeInputExplicit:
		return nil
	default:
		return common_errors.NewValidationError("workflow edge input mode is invalid")
	}
}

func (edge WorkflowEdge) EffectiveInputMode() WorkflowEdgeInputMode {
	if edge.InputMode == "" {
		return WorkflowEdgeInputByName
	}
	return edge.InputMode
}

type WorkflowDelayStatus string

const (
	WorkflowDelayWaiting WorkflowDelayStatus = "waiting"
	WorkflowDelaySuccess WorkflowDelayStatus = "success"
	WorkflowDelayStopped WorkflowDelayStatus = "stopped"
)

func (status WorkflowDelayStatus) Validate() error {
	switch status {
	case WorkflowDelayWaiting, WorkflowDelaySuccess, WorkflowDelayStopped:
		return nil
	default:
		return common_errors.NewValidationError("workflow delay status is invalid")
	}
}

type WorkflowDelay struct {
	ID int `db:"id" json:"id" backup:"-"`

	ProjectID      int                 `db:"project_id" json:"project_id" backup:"-"`
	WorkflowRunID  int                 `db:"workflow_run_id" json:"workflow_run_id" backup:"workflow_run_id"`
	WorkflowNodeID int                 `db:"workflow_node_id" json:"workflow_node_id" backup:"workflow_node_id"`
	Status         WorkflowDelayStatus `db:"status" json:"status" backup:"status"`
	ResumeAt       time.Time           `db:"resume_at" json:"resume_at" backup:"resume_at"`
	Created        time.Time           `db:"created" json:"created" backup:"created"`
	Resolved       *time.Time          `db:"resolved" json:"resolved,omitempty" backup:"resolved"`
}

type WorkflowRunStatus string

const (
	WorkflowRunPending   WorkflowRunStatus = "pending"
	WorkflowRunQueued    WorkflowRunStatus = "queued"
	WorkflowRunRunning   WorkflowRunStatus = "running"
	WorkflowRunApproval  WorkflowRunStatus = "approval"
	WorkflowRunStopping  WorkflowRunStatus = "stopping"
	WorkflowRunSucceeded WorkflowRunStatus = "succeeded"
	// WorkflowRunSuccess is retained for reading runs created by an older
	// enhanced implementation. New runs use WorkflowRunSucceeded.
	WorkflowRunSuccess  WorkflowRunStatus = "success"
	WorkflowRunStopped  WorkflowRunStatus = "stopped"
	WorkflowRunCanceled WorkflowRunStatus = "canceled"
	WorkflowRunFailed   WorkflowRunStatus = "failed"
	WorkflowRunBlocked  WorkflowRunStatus = "blocked"
)

func (status WorkflowRunStatus) IsFinished() bool {
	return status == WorkflowRunSucceeded || status == WorkflowRunSuccess || status == WorkflowRunStopped || status == WorkflowRunCanceled || status == WorkflowRunFailed || status == WorkflowRunBlocked
}

type WorkflowRun struct {
	ID int `db:"id" json:"id" backup:"-"`

	ProjectID            int `db:"project_id" json:"project_id" backup:"-"`
	WorkflowTemplateID   int `db:"workflow_template_id" json:"workflow_template_id" backup:"workflow_template_id"`
	NotificationRevision int `db:"notification_revision" json:"-" backup:"-"`

	// RevisionID preserves the nullable upstream schema mapping. EX execution
	// uses WorkflowVersionID and DefinitionSnapshot as its immutable authority.
	RevisionID *int `db:"revision_id" json:"-" backup:"-"`

	Status                      WorkflowRunStatus              `db:"status" json:"status" backup:"status"`
	DesiredState                WorkflowRunDesiredState        `db:"desired_state" json:"desired_state" backup:"desired_state"`
	ReconciliationState         WorkflowRunReconciliationState `db:"reconciliation_state" json:"reconciliation_state" backup:"reconciliation_state"`
	ReconciliationAttempts      int                            `db:"reconciliation_attempts" json:"reconciliation_attempts" backup:"reconciliation_attempts"`
	ReconciliationLastError     string                         `db:"reconciliation_last_error" json:"reconciliation_last_error,omitempty" backup:"reconciliation_last_error"`
	ReconciliationNextRetryAt   *time.Time                     `db:"reconciliation_next_retry_at" json:"reconciliation_next_retry_at,omitempty" backup:"reconciliation_next_retry_at"`
	ReconciliationQuarantinedAt *time.Time                     `db:"reconciliation_quarantined_at" json:"reconciliation_quarantined_at,omitempty" backup:"reconciliation_quarantined_at"`
	Reason                      string                         `db:"reason" json:"reason,omitempty" backup:"reason"`

	Version *string `db:"version" json:"version,omitempty" backup:"version"`

	ActorUserID        int    `db:"actor_user_id" json:"actor_user_id" backup:"actor_user_id"`
	DefinitionVersion  int    `db:"definition_version" json:"definition_version" backup:"definition_version"`
	DefinitionRevision int    `db:"definition_revision" json:"definition_revision" backup:"definition_revision"`
	WorkflowVersionID  int    `db:"workflow_version_id" json:"workflow_version_id" backup:"workflow_version_id"`
	CorrelationID      string `db:"correlation_id" json:"correlation_id" backup:"correlation_id"`

	DefinitionSnapshotJSON string                               `db:"definition_snapshot" json:"-" backup:"definition_snapshot"`
	DefinitionSnapshot     WorkflowTemplate                     `db:"-" json:"definition" backup:"-"`
	ParameterSnapshotJSON  string                               `db:"parameter_snapshot" json:"-" backup:"parameter_snapshot"`
	ParameterSnapshot      map[string]WorkflowParameterSnapshot `db:"-" json:"parameters,omitempty" backup:"-"`
	TriggerSnapshotJSON    string                               `db:"trigger_snapshot" json:"-" backup:"trigger_snapshot"`
	TriggerSnapshot        WorkflowTriggerSnapshot              `db:"-" json:"trigger,omitempty" backup:"-"`
	Nodes                  []WorkflowRunNode                    `db:"-" json:"nodes" backup:"-"`

	// Environment is written only at run creation after survey validation. A
	// workflow survey cannot contain secret values, so this persisted object is
	// safe to show in run details and snapshots.
	Environment MapStringAnyField `db:"environment" json:"environment,omitempty" backup:"-"`

	Created time.Time  `db:"created" json:"created" backup:"created"`
	Start   *time.Time `db:"start" json:"start,omitempty" backup:"start"`
	End     *time.Time `db:"end" json:"end,omitempty" backup:"end"`

	RootTaskID *int `db:"root_task_id" json:"root_task_id,omitempty" backup:"root_task_id"`

	// DeploymentWindowDecisionID is an Enhanced-only admission fence. The
	// immutable decision row owns the workflow-run link.
	DeploymentWindowDecisionID *int `db:"-" json:"-" backup:"-"`
	// PolicyGuardrailEvaluationID is an Enhanced-only admission fence. The
	// immutable evaluation row owns the workflow-run link.
	PolicyGuardrailEvaluationID *int `db:"-" json:"-" backup:"-"`

	ReconciliationOwnership *WorkflowReconciliationDiagnostics `db:"-" json:"reconciliation_ownership,omitempty" backup:"-"`
}

type WorkflowApprovalStatus string

const (
	WorkflowApprovalPending  WorkflowApprovalStatus = "pending"
	WorkflowApprovalApproved WorkflowApprovalStatus = "approved"
	WorkflowApprovalRejected WorkflowApprovalStatus = "rejected"
	WorkflowApprovalExpired  WorkflowApprovalStatus = "expired"
	WorkflowApprovalCanceled WorkflowApprovalStatus = "canceled"
)

type WorkflowApproval struct {
	ID                   int `db:"id" json:"id" backup:"-"`
	NotificationRevision int `db:"notification_revision" json:"-" backup:"-"`

	ProjectID          int                    `db:"project_id" json:"project_id" backup:"-"`
	WorkflowTemplateID int                    `db:"workflow_template_id" json:"workflow_template_id,omitempty" backup:"-"`
	WorkflowName       string                 `db:"workflow_name" json:"workflow_name,omitempty" backup:"-"`
	WorkflowRunID      int                    `db:"workflow_run_id" json:"workflow_run_id" backup:"workflow_run_id"`
	WorkflowNodeID     int                    `db:"workflow_node_id" json:"workflow_node_id" backup:"workflow_node_id"`
	Status             WorkflowApprovalStatus `db:"status" json:"status" backup:"status"`
	Created            time.Time              `db:"created" json:"created" backup:"created"`
	Resolved           *time.Time             `db:"resolved" json:"resolved,omitempty" backup:"resolved"`
	ResolvedByUserID   *int                   `db:"resolved_by_user_id" json:"resolved_by_user_id,omitempty" backup:"resolved_by_user_id"`
	Deadline           *time.Time             `db:"deadline" json:"deadline,omitempty" backup:"deadline"`
	Prompt             string                 `db:"prompt" json:"prompt" backup:"prompt"`
	EligiblePermission ProjectUserPermission  `db:"eligible_permission" json:"eligible_permission" backup:"eligible_permission"`
	// EligiblePermission is retained only to read pre-054 approval records. New
	// decisions must use RolePolicySnapshot, never this mutable broad bit.
	RolePolicySnapshotJSON     string                             `db:"role_policy_snapshot" json:"-" backup:"role_policy_snapshot"`
	RolePolicySnapshotRevision int                                `db:"role_policy_revision" json:"-" backup:"role_policy_revision"`
	RolePolicySnapshot         WorkflowApprovalRolePolicySnapshot `db:"-" json:"role_policy_snapshot,omitempty" backup:"-"`
	SeparationOfDuties         bool                               `db:"separation_of_duties" json:"separation_of_duties" backup:"separation_of_duties"`
	RequestActorUserID         int                                `db:"request_actor_user_id" json:"request_actor_user_id" backup:"request_actor_user_id"`
	TimeoutOutcome             WorkflowApprovalTimeoutOutcome     `db:"timeout_outcome" json:"timeout_outcome" backup:"timeout_outcome"`
	DecisionComment            string                             `db:"decision_comment" json:"decision_comment,omitempty" backup:"decision_comment"`
	DecisionSource             WorkflowApprovalDecisionSource     `db:"decision_source" json:"decision_source,omitempty" backup:"decision_source"`
	CorrelationID              string                             `db:"correlation_id" json:"correlation_id" backup:"correlation_id"`
}

func (condition WorkflowEdgeCondition) Validate() error {
	switch condition {
	case WorkflowEdgeOnSuccess, WorkflowEdgeOnFailure, WorkflowEdgeAlways, WorkflowEdgeExpression:
		return nil
	default:
		return common_errors.NewValidationError("workflow edge condition is invalid")
	}
}

func (kind WorkflowNodeKind) Validate() error {
	switch kind {
	case WorkflowNodeTaskKind,
		WorkflowNodeApprovalKind,
		WorkflowNodeNoteKind,
		WorkflowNodeDelayKind:
		return nil
	default:
		return common_errors.NewValidationError("workflow node kind is invalid")
	}
}

func (node WorkflowNode) EffectiveKind() WorkflowNodeKind {
	if node.Kind == "" {
		return WorkflowNodeTaskKind
	}
	return node.Kind
}

func (mode WorkflowConvergenceMode) Validate() error {
	switch mode {
	case WorkflowConvergenceAll, WorkflowConvergenceAny:
		return nil
	default:
		return common_errors.NewValidationError("workflow node convergence mode is invalid")
	}
}

func (node WorkflowNode) EffectiveConvergenceMode() WorkflowConvergenceMode {
	if node.ConvergenceMode == "" {
		return WorkflowConvergenceAll
	}
	return node.ConvergenceMode
}

func (status WorkflowApprovalStatus) Validate() error {
	switch status {
	case WorkflowApprovalPending, WorkflowApprovalApproved, WorkflowApprovalRejected, WorkflowApprovalExpired, WorkflowApprovalCanceled:
		return nil
	default:
		return common_errors.NewValidationError("workflow approval status is invalid")
	}
}

// ValidateWorkflowSurveyVars rejects ambiguous, malformed, and secret
// workflow survey declarations. Runs persist their answers, therefore secrets
// remain exclusively a task-template concern.
func ValidateWorkflowSurveyVars(vars []SurveyVar) error {
	seen := make(map[string]struct{}, len(vars))
	for _, variable := range vars {
		if variable.Name == "" {
			return common_errors.NewValidationError("workflow survey variable name can not be empty")
		}
		if _, duplicate := seen[variable.Name]; duplicate {
			return common_errors.NewValidationError("workflow survey variable \"" + variable.Name + "\" is declared more than once")
		}
		seen[variable.Name] = struct{}{}
		if variable.Type == SurveyVarSecret {
			return common_errors.NewValidationError("workflow survey variable \"" + variable.Name + "\" can not be a secret")
		}
		switch variable.Target {
		case SurveyVarTargetDefault, SurveyVarTargetEnv:
		default:
			return common_errors.NewValidationError("workflow survey variable \"" + variable.Name + "\": invalid target \"" + string(variable.Target) + "\"")
		}
		if err := ValidateSurveyVar(variable); err != nil {
			return err
		}
	}
	return nil
}

// SurveyVarByName resolves a workflow survey declaration for binding
// validation without exposing mutable storage details.
func (workflow WorkflowTemplate) SurveyVarByName(name string) (SurveyVar, bool) {
	for _, variable := range workflow.SurveyVars {
		if variable.Name == name {
			return variable, true
		}
	}
	return SurveyVar{}, false
}

// WorkflowTemplateValidationStore is the slice of Store workflow template
// validation depends on. Narrowing the dependency lets callers (and tests)
// validate against a minimal mock instead of a full store.
type WorkflowTemplateValidationStore interface {
	GetTemplate(projectID int, templateID int) (Template, error)
}
