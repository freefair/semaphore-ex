package db

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"time"
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

type WorkflowRunNodeStatus string

const (
	WorkflowRunNodePending   WorkflowRunNodeStatus = "pending"
	WorkflowRunNodeQueued    WorkflowRunNodeStatus = "queued"
	WorkflowRunNodeRunning   WorkflowRunNodeStatus = "running"
	WorkflowRunNodeApproval  WorkflowRunNodeStatus = "approval"
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
	OverrideSnapshotJSON string                          `db:"override_snapshot" json:"-" backup:"override_snapshot"`
	OverrideSnapshot     WorkflowNodeOverride            `db:"-" json:"overrides,omitempty" backup:"-"`

	Created time.Time  `db:"created" json:"created" backup:"created"`
	Queued  *time.Time `db:"queued" json:"queued,omitempty" backup:"queued"`
	Start   *time.Time `db:"start" json:"start,omitempty" backup:"start"`
	End     *time.Time `db:"end" json:"end,omitempty" backup:"end"`
}

// WorkflowRunInput contains the two value sources used by the start service.
// TriggerValues is internal-only; direct run API callers supply UserValues.
type WorkflowRunInput struct {
	TriggerValues   map[string]json.RawMessage   `json:"-"`
	UserValues      map[string]json.RawMessage   `json:"parameters,omitempty"`
	NodeOverrides   map[int]WorkflowNodeOverride `json:"node_overrides,omitempty"`
	TriggerSnapshot *WorkflowTriggerSnapshot     `json:"-"`
}

type WorkflowApprovalTimeoutOutcome string

const (
	WorkflowApprovalTimeoutReject  WorkflowApprovalTimeoutOutcome = "reject"
	WorkflowApprovalTimeoutApprove WorkflowApprovalTimeoutOutcome = "approve"
)

type WorkflowApprovalDecisionSource string

const (
	WorkflowApprovalDecisionSourceUser    WorkflowApprovalDecisionSource = "user"
	WorkflowApprovalDecisionSourceTimeout WorkflowApprovalDecisionSource = "timeout"
	WorkflowApprovalDecisionSourceCancel  WorkflowApprovalDecisionSource = "cancel"
)

const MaxWorkflowApprovalPromptBytes = 2048

const MaxWorkflowApprovalCommentBytes = 1024

type WorkflowApprovalDecision struct {
	Status  WorkflowApprovalStatus         `json:"status"`
	Comment string                         `json:"comment,omitempty"`
	Source  WorkflowApprovalDecisionSource `json:"source"`
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

func (node WorkflowNode) EffectiveApprovalPermission() ProjectUserPermission {
	if node.ApprovalPermission == 0 {
		return CanRunProjectTasks
	}
	return node.ApprovalPermission
}

func (node WorkflowNode) EffectiveApprovalTimeoutOutcome() WorkflowApprovalTimeoutOutcome {
	if node.ApprovalTimeoutOutcome == "" {
		return WorkflowApprovalTimeoutReject
	}
	return node.ApprovalTimeoutOutcome
}

func (outcome WorkflowApprovalTimeoutOutcome) Validate() error {
	switch outcome {
	case WorkflowApprovalTimeoutReject, WorkflowApprovalTimeoutApprove:
		return nil
	default:
		return common_errors.NewValidationError("workflow approval timeout outcome is invalid")
	}
}

func (decision WorkflowApprovalDecision) Validate() error {
	if decision.Status != WorkflowApprovalApproved && decision.Status != WorkflowApprovalRejected {
		return common_errors.NewValidationError("workflow approval decision status is invalid")
	}
	if decision.Source != WorkflowApprovalDecisionSourceUser {
		return common_errors.NewValidationError("workflow approval decision source is invalid")
	}
	if len(decision.Comment) > MaxWorkflowApprovalCommentBytes {
		return common_errors.NewValidationError("workflow approval comment is too long")
	}
	return nil
}

// WorkflowParameterValidationStore resolves every project-scoped resource a
// workflow parameter or node override is allowed to reference.
type WorkflowParameterValidationStore interface {
	GetInventory(projectID int, inventoryID int) (Inventory, error)
	GetEnvironment(projectID int, environmentID int) (Environment, error)
	GetAccessKey(projectID int, accessKeyID int) (AccessKey, error)
}

// WorkflowNodeResultStore exposes only the sanitized, persisted summary used
// to freeze allow-listed condition inputs when a workflow task finishes.
type WorkflowNodeResultStore interface {
	GetTaskSummary(projectID int, taskID int) (TaskSummary, error)
}
