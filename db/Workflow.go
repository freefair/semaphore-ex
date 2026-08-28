package db

import (
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

type WorkflowConvergenceMode string

const (
	WorkflowConvergenceAll WorkflowConvergenceMode = "all"
	WorkflowConvergenceAny WorkflowConvergenceMode = "any"
)

type WorkflowJoinMode string

const (
	WorkflowJoinAllSuccessful WorkflowJoinMode = "all-successful"
	WorkflowJoinAllComplete   WorkflowJoinMode = "all-complete"
	WorkflowJoinAnySuccessful WorkflowJoinMode = "any-successful"
)

type WorkflowConditionValueType string

const (
	WorkflowConditionBoolean WorkflowConditionValueType = "boolean"
	WorkflowConditionInteger WorkflowConditionValueType = "integer"
	WorkflowConditionString  WorkflowConditionValueType = "string"
)

// WorkflowConditionInstruction is one typed operation in the persisted
// condition stack program. It contains values only; it cannot call code or
// address resources outside the allow-listed workflow result fields.
type WorkflowConditionInstruction struct {
	Operation    string                     `json:"operation"`
	ValueType    WorkflowConditionValueType `json:"value_type,omitempty"`
	Field        string                     `json:"field,omitempty"`
	BooleanValue *bool                      `json:"boolean_value,omitempty"`
	IntegerValue *int64                     `json:"integer_value,omitempty"`
	StringValue  *string                    `json:"string_value,omitempty"`
}

type WorkflowConditionProgram struct {
	Version      int                            `json:"version"`
	Instructions []WorkflowConditionInstruction `json:"instructions"`
}

type WorkflowNodeResultSummary struct {
	State         TaskSummaryState `json:"state"`
	ExpectedHosts int              `json:"expected_hosts"`
	TotalHosts    int              `json:"total_hosts"`
	OkHosts       int              `json:"ok_hosts"`
	FailedHosts   int              `json:"failed_hosts"`
}

// WorkflowNodeResult is the immutable, deliberately narrow condition input.
// Diagnostics, host names, task arguments, secrets, and arbitrary artifacts
// are excluded from this contract.
type WorkflowNodeResult struct {
	Status     WorkflowRunNodeStatus      `json:"status"`
	Successful bool                       `json:"successful"`
	Summary    *WorkflowNodeResultSummary `json:"summary,omitempty"`
}

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

	Nodes []WorkflowNode `db:"-" bolt:"include" json:"nodes" backup:"-"`
	Edges []WorkflowEdge `db:"-" bolt:"include" json:"edges" backup:"edges"`

	LastRun *WorkflowRun `db:"-" json:"last_run,omitempty" backup:"-"`
}

type WorkflowNode struct {
	ID int `db:"id" json:"id" backup:"id"`

	WorkflowTemplateID int `db:"workflow_template_id" json:"workflow_template_id" backup:"-"`

	TemplateID      int                     `db:"template_id" json:"template_id,omitempty" backup:"-"`
	DisplayName     string                  `db:"display_name" json:"display_name,omitempty" backup:"display_name"`
	Kind            WorkflowNodeKind        `db:"kind" json:"kind,omitempty" backup:"kind"`
	ConvergenceMode WorkflowConvergenceMode `db:"convergence_mode" json:"convergence_mode,omitempty" backup:"convergence_mode"`
	JoinMode        WorkflowJoinMode        `db:"join_mode" json:"join_mode,omitempty" backup:"join_mode"`
	ApprovalTimeout *int                    `db:"approval_timeout" json:"approval_timeout,omitempty" backup:"approval_timeout"`
	ApprovalMessage *string                 `db:"approval_message" json:"approval_message,omitempty" backup:"approval_message"`

	TaskParamsID *int        `db:"task_params_id" json:"-" backup:"-"`
	TaskParams   *TaskParams `db:"-" json:"task_params,omitempty" backup:"task_params"`

	ArtifactOutputsJSON string                        `db:"artifact_outputs" json:"-" backup:"artifact_outputs"`
	ArtifactInputsJSON  string                        `db:"artifact_inputs" json:"-" backup:"artifact_inputs"`
	ArtifactOutputs     []WorkflowArtifactDeclaration `db:"-" json:"artifact_outputs,omitempty" backup:"-"`
	ArtifactInputs      []WorkflowArtifactReference   `db:"-" json:"artifact_inputs,omitempty" backup:"-"`

	Note         *string `db:"note" json:"note,omitempty" backup:"note"`
	DelaySeconds *int    `db:"delay_seconds" json:"delay_seconds,omitempty" backup:"delay_seconds"`

	PositionX int `db:"position_x" json:"position_x" backup:"position_x"`
	PositionY int `db:"position_y" json:"position_y" backup:"position_y"`
}

type WorkflowEdge struct {
	ID int `db:"id" json:"id" backup:"-"`

	WorkflowTemplateID int `db:"workflow_template_id" json:"workflow_template_id" backup:"-"`
	SourceNodeID       int `db:"source_node_id" json:"source_node_id" backup:"source_node_id"`
	DestinationNodeID  int `db:"destination_node_id" json:"destination_node_id" backup:"destination_node_id"`

	Condition            WorkflowEdgeCondition    `db:"condition" json:"condition" backup:"condition"`
	Label                string                   `db:"label" json:"label,omitempty" backup:"label"`
	Expression           string                   `db:"condition_expression" json:"condition_expression,omitempty" backup:"condition_expression"`
	ConditionProgramJSON string                   `db:"condition_program" json:"-" backup:"condition_program"`
	ConditionProgram     WorkflowConditionProgram `db:"-" json:"condition_program,omitempty" backup:"-"`
}

const WorkflowDefinitionVersion = 1

// WorkflowValidationIssue is stable API data. Path points to the affected
// field while node_id and edge_id locate the graph element when applicable.
type WorkflowValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	NodeID  *int   `json:"node_id,omitempty"`
	EdgeID  *int   `json:"edge_id,omitempty"`
}

type WorkflowValidationResult struct {
	Valid  bool                      `json:"valid"`
	Issues []WorkflowValidationIssue `json:"issues"`
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

	ProjectID          int `db:"project_id" json:"project_id" backup:"-"`
	WorkflowTemplateID int `db:"workflow_template_id" json:"workflow_template_id" backup:"workflow_template_id"`

	Status WorkflowRunStatus `db:"status" json:"status" backup:"status"`
	Reason string            `db:"reason" json:"reason,omitempty" backup:"reason"`

	Version *string `db:"version" json:"version,omitempty" backup:"version"`

	ActorUserID        int    `db:"actor_user_id" json:"actor_user_id" backup:"actor_user_id"`
	DefinitionVersion  int    `db:"definition_version" json:"definition_version" backup:"definition_version"`
	DefinitionRevision int    `db:"definition_revision" json:"definition_revision" backup:"definition_revision"`
	CorrelationID      string `db:"correlation_id" json:"correlation_id" backup:"correlation_id"`

	DefinitionSnapshotJSON string            `db:"definition_snapshot" json:"-" backup:"definition_snapshot"`
	DefinitionSnapshot     WorkflowTemplate  `db:"-" json:"definition" backup:"-"`
	Nodes                  []WorkflowRunNode `db:"-" json:"nodes" backup:"-"`

	Created time.Time  `db:"created" json:"created" backup:"created"`
	Start   *time.Time `db:"start" json:"start,omitempty" backup:"start"`
	End     *time.Time `db:"end" json:"end,omitempty" backup:"end"`

	RootTaskID *int `db:"root_task_id" json:"root_task_id,omitempty" backup:"root_task_id"`
}

type WorkflowRunNodeStatus string

const (
	WorkflowRunNodePending   WorkflowRunNodeStatus = "pending"
	WorkflowRunNodeQueued    WorkflowRunNodeStatus = "queued"
	WorkflowRunNodeRunning   WorkflowRunNodeStatus = "running"
	WorkflowRunNodeSucceeded WorkflowRunNodeStatus = "succeeded"
	WorkflowRunNodeFailed    WorkflowRunNodeStatus = "failed"
	WorkflowRunNodeStopped   WorkflowRunNodeStatus = "stopped"
	WorkflowRunNodeCanceled  WorkflowRunNodeStatus = "canceled"
	WorkflowRunNodeBlocked   WorkflowRunNodeStatus = "blocked"
	WorkflowRunNodeSkipped   WorkflowRunNodeStatus = "skipped"
)

func (status WorkflowRunNodeStatus) IsFinished() bool {
	return status == WorkflowRunNodeSucceeded || status == WorkflowRunNodeFailed || status == WorkflowRunNodeStopped || status == WorkflowRunNodeCanceled || status == WorkflowRunNodeBlocked || status == WorkflowRunNodeSkipped
}

// WorkflowRunNode is the durable execution state for one immutable workflow
// node snapshot. workflow_node_id is the stable definition ID; the serialized
// template keeps execution independent from later template edits.
type WorkflowRunNode struct {
	ID             int `db:"id" json:"id" backup:"-"`
	ProjectID      int `db:"project_id" json:"project_id" backup:"-"`
	WorkflowRunID  int `db:"workflow_run_id" json:"workflow_run_id" backup:"-"`
	WorkflowNodeID int `db:"workflow_node_id" json:"workflow_node_id" backup:"workflow_node_id"`
	TemplateID     int `db:"template_id" json:"template_id" backup:"template_id"`

	Status WorkflowRunNodeStatus `db:"status" json:"status" backup:"status"`
	TaskID *int                  `db:"task_id" json:"task_id,omitempty" backup:"task_id"`
	Reason string                `db:"reason" json:"reason,omitempty" backup:"reason"`

	TemplateSnapshotJSON string                          `db:"template_snapshot" json:"-" backup:"template_snapshot"`
	TemplateSnapshot     Template                        `db:"-" json:"template" backup:"-"`
	ResultJSON           string                          `db:"result" json:"-" backup:"result"`
	Result               WorkflowNodeResult              `db:"-" json:"result,omitempty" backup:"-"`
	ArtifactInputsJSON   string                          `db:"artifact_inputs" json:"-" backup:"artifact_inputs"`
	ArtifactInputs       []WorkflowArtifactInputSnapshot `db:"-" json:"artifact_inputs,omitempty" backup:"-"`

	Created time.Time  `db:"created" json:"created" backup:"created"`
	Queued  *time.Time `db:"queued" json:"queued,omitempty" backup:"queued"`
	Start   *time.Time `db:"start" json:"start,omitempty" backup:"start"`
	End     *time.Time `db:"end" json:"end,omitempty" backup:"end"`
}

type WorkflowApprovalStatus string

const (
	WorkflowApprovalPending  WorkflowApprovalStatus = "pending"
	WorkflowApprovalApproved WorkflowApprovalStatus = "approved"
	WorkflowApprovalRejected WorkflowApprovalStatus = "rejected"
)

type WorkflowApproval struct {
	ID int `db:"id" json:"id" backup:"-"`

	ProjectID        int                    `db:"project_id" json:"project_id" backup:"-"`
	WorkflowRunID    int                    `db:"workflow_run_id" json:"workflow_run_id" backup:"workflow_run_id"`
	WorkflowNodeID   int                    `db:"workflow_node_id" json:"workflow_node_id" backup:"workflow_node_id"`
	Status           WorkflowApprovalStatus `db:"status" json:"status" backup:"status"`
	Created          time.Time              `db:"created" json:"created" backup:"created"`
	Resolved         *time.Time             `db:"resolved" json:"resolved,omitempty" backup:"resolved"`
	ResolvedByUserID *int                   `db:"resolved_by_user_id" json:"resolved_by_user_id,omitempty" backup:"resolved_by_user_id"`
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

func (mode WorkflowJoinMode) Validate() error {
	switch mode {
	case WorkflowJoinAllSuccessful, WorkflowJoinAllComplete, WorkflowJoinAnySuccessful:
		return nil
	default:
		return common_errors.NewValidationError("workflow node join mode is invalid")
	}
}

func (node WorkflowNode) EffectiveJoinMode() WorkflowJoinMode {
	if node.JoinMode != "" {
		return node.JoinMode
	}
	if node.EffectiveConvergenceMode() == WorkflowConvergenceAny {
		return WorkflowJoinAnySuccessful
	}
	return WorkflowJoinAllSuccessful
}

func (node WorkflowNode) EffectiveConvergenceMode() WorkflowConvergenceMode {
	if node.ConvergenceMode == "" {
		return WorkflowConvergenceAll
	}
	return node.ConvergenceMode
}

func (status WorkflowApprovalStatus) Validate() error {
	switch status {
	case WorkflowApprovalPending, WorkflowApprovalApproved, WorkflowApprovalRejected:
		return nil
	default:
		return common_errors.NewValidationError("workflow approval status is invalid")
	}
}

// WorkflowTemplateValidationStore is the slice of Store workflow template
// validation depends on. Narrowing the dependency lets callers (and tests)
// validate against a minimal mock instead of a full store.
type WorkflowTemplateValidationStore interface {
	GetTemplate(projectID int, templateID int) (Template, error)
}

// WorkflowNodeResultStore exposes only the sanitized, persisted summary used
// to freeze allow-listed condition inputs when a workflow task finishes.
type WorkflowNodeResultStore interface {
	GetTaskSummary(projectID int, taskID int) (TaskSummary, error)
}
