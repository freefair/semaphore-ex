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

const MaxWorkflowVersionMessageBytes = 512

// WorkflowVersion is one append-only definition snapshot. Version
// numbers follow the live workflow revision while the row ID provides an
// immutable reference for runs, diffs, and restore provenance.
type WorkflowVersion struct {
	ID int `db:"id" json:"id"`

	ProjectID          int `db:"project_id" json:"project_id"`
	WorkflowTemplateID int `db:"workflow_template_id" json:"workflow_template_id"`
	VersionNumber      int `db:"version_number" json:"version_number"`

	ParentVersionID       *int `db:"parent_version_id" json:"parent_version_id,omitempty"`
	RestoredFromVersionID *int `db:"restored_from_version_id" json:"restored_from_version_id,omitempty"`
	AuthorUserID          int  `db:"author_user_id" json:"author_user_id"`

	Message            string `db:"message" json:"message"`
	ContentFingerprint string `db:"content_fingerprint" json:"content_fingerprint"`

	DefinitionSnapshotJSON string           `db:"definition_snapshot" json:"-"`
	DefinitionSnapshot     WorkflowTemplate `db:"-" json:"definition"`

	Created time.Time `db:"created" json:"created"`
}

// WorkflowVersionMutation is server-owned authorship and restore provenance
// supplied to the atomic versioned repository mutation.
type WorkflowVersionMutation struct {
	AuthorUserID          int
	Message               string
	RestoredFromVersionID *int
	Created               time.Time
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

type WorkflowRunDesiredState string

const (
	WorkflowRunDesiredRunning  WorkflowRunDesiredState = "running"
	WorkflowRunDesiredStopping WorkflowRunDesiredState = "stopping"
	WorkflowRunDesiredStopped  WorkflowRunDesiredState = "stopped"
)

type WorkflowRunReconciliationState string

const (
	WorkflowRunReconciliationHealthy     WorkflowRunReconciliationState = "healthy"
	WorkflowRunReconciliationRecovering  WorkflowRunReconciliationState = "recovering"
	WorkflowRunReconciliationQuarantined WorkflowRunReconciliationState = "quarantined"
)

// WorkflowReconciliationDiagnostics is value-free HA ownership context. It
// deliberately contains no workflow inputs, task arguments, or credentials.
type WorkflowReconciliationDiagnostics struct {
	Owned                    bool       `json:"owned"`
	OwnerBootID              string     `json:"owner_boot_id"`
	PreviousOwnerBootID      string     `json:"previous_owner_boot_id,omitempty"`
	FencingToken             int64      `json:"fencing_token"`
	LeaseExpiresAt           time.Time  `json:"lease_expires_at"`
	AcquiredAt               time.Time  `json:"acquired_at"`
	OwnershipTransferredAt   *time.Time `json:"ownership_transferred_at,omitempty"`
	TransferCount            int        `json:"transfer_count"`
	LastReconciledAt         *time.Time `json:"last_reconciled_at,omitempty"`
	LeaseAgeSeconds          int64      `json:"lease_age_seconds"`
	ReconciliationLagSeconds int64      `json:"reconciliation_lag_seconds"`
	Recovered                bool       `json:"recovered"`
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
	// Deployment-window provenance remains private to the Enhanced admission
	// boundary. Public workflow state exposes only the terminal blocked status.
	DeploymentWindowDecisionID *int       `db:"deployment_window_decision_id" json:"-" backup:"-"`
	NextEligibleAt             *time.Time `db:"next_eligible_at" json:"-" backup:"-"`
	NextEligibleKnown          bool       `db:"next_eligible_known" json:"-" backup:"-"`
	BlockedAt                  *time.Time `db:"blocked_at" json:"-" backup:"-"`
	// ProgressionFencingToken binds task and approval attempt creation to the
	// workflow reconciliation owner that claimed this node.
	ProgressionFencingToken int64 `db:"progression_fencing_token" json:"-" backup:"-"`

	TemplateSnapshotJSON string   `db:"template_snapshot" json:"-" backup:"template_snapshot"`
	TemplateSnapshot     Template `db:"-" json:"template" backup:"-"`
	// ExecutionSnapshotJSON freezes the reviewed node's non-secret execution
	// configuration before delayed workflow dispatch. It is never public.
	ExecutionSnapshotJSON              string                          `db:"execution_snapshot" json:"-" backup:"-"`
	CrossProjectTemplateProvenanceJSON string                          `db:"cross_project_template_provenance" json:"-" backup:"cross_project_template_provenance"`
	CrossProjectTemplateProvenance     *CrossProjectTemplateProvenance `db:"-" json:"cross_project_template_provenance,omitempty" backup:"-"`
	ResultJSON                         string                          `db:"result" json:"-" backup:"result"`
	Result                             WorkflowNodeResult              `db:"-" json:"result,omitempty" backup:"-"`
	ArtifactInputsJSON                 string                          `db:"artifact_inputs" json:"-" backup:"artifact_inputs"`
	ArtifactInputs                     []WorkflowArtifactInputSnapshot `db:"-" json:"artifact_inputs,omitempty" backup:"-"`
	OverrideSnapshotJSON               string                          `db:"override_snapshot" json:"-" backup:"override_snapshot"`
	OverrideSnapshot                   WorkflowNodeOverride            `db:"-" json:"overrides,omitempty" backup:"-"`

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

const MaxWorkflowApprovalCorrelationIDBytes = 128

const MaxWorkflowApprovalDirectoryRevisionFingerprintBytes = 64

type WorkflowApprovalDecision struct {
	Status  WorkflowApprovalStatus         `json:"status"`
	Comment string                         `json:"comment,omitempty"`
	Source  WorkflowApprovalDecisionSource `json:"source"`
}

// WorkflowAccessPolicy narrows only workflow view and start permissions. An
// empty role list preserves the workflow behavior that existed before Slice
// 054; edit, stop, and administer remain base-permission decisions.
type WorkflowAccessPolicy struct {
	Revision     int                    `json:"revision"`
	ViewRoleIDs  []ProjectRoleReference `json:"view_role_ids,omitempty"`
	StartRoleIDs []ProjectRoleReference `json:"start_role_ids,omitempty"`
}

// WorkflowApprovalRoleMode controls whether a policy requires a contribution
// from any selected role or coverage of every selected role.
type WorkflowApprovalRoleMode string

const (
	WorkflowApprovalRoleModeAnyOf WorkflowApprovalRoleMode = "any_of"
	WorkflowApprovalRoleModeAllOf WorkflowApprovalRoleMode = "all_of"
)

// WorkflowApprovalRolePolicy is definition-time approval policy. It is copied
// unchanged into WorkflowApprovalRolePolicySnapshot when a run opens it.
type WorkflowApprovalRolePolicy struct {
	Revision                 int                      `json:"revision"`
	Mode                     WorkflowApprovalRoleMode `json:"mode"`
	RoleIDs                  []ProjectRoleReference   `json:"role_ids"`
	MinimumDistinctApprovers int                      `json:"minimum_distinct_approvers"`
	InitiatorSeparation      bool                     `json:"initiator_separation"`
}

// WorkflowApprovalRolePolicySnapshot is immutable run evidence. Current
// eligibility is evaluated separately; this snapshot must never become a
// copied grant after a role is revoked or deleted.
type WorkflowApprovalRolePolicySnapshot struct {
	PolicyRevision int                        `json:"policy_revision"`
	Policy         WorkflowApprovalRolePolicy `json:"policy"`
}

// WorkflowApprovalRoleOrigin records how the contributor's effective role was
// assigned at the exact time of their decision.
type WorkflowApprovalRoleOrigin string

const (
	WorkflowApprovalRoleOriginBuiltIn WorkflowApprovalRoleOrigin = "built_in"
	WorkflowApprovalRoleOriginManual  WorkflowApprovalRoleOrigin = "manual"
	WorkflowApprovalRoleOriginLDAP    WorkflowApprovalRoleOrigin = "ldap"
	WorkflowApprovalRoleOriginOIDC    WorkflowApprovalRoleOrigin = "oidc"
)

// WorkflowApprovalContribution is immutable, durable evidence of one
// distinct user's contribution. DirectoryRevisionFingerprint stores a bounded
// fingerprint of the reconciliation revision, never raw LDAP/OIDC claims.
type WorkflowApprovalContribution struct {
	ApprovalID                   int                        `db:"workflow_approval_id" json:"workflow_approval_id"`
	ActorUserID                  int                        `db:"actor_user_id" json:"actor_user_id"`
	RoleID                       ProjectRoleReference       `db:"role_id" json:"role_id"`
	RoleRevision                 int                        `db:"role_revision" json:"role_revision"`
	RoleOrigin                   WorkflowApprovalRoleOrigin `db:"role_origin" json:"role_origin"`
	DirectoryProviderID          string                     `db:"directory_provider_id" json:"directory_provider_id,omitempty"`
	DirectoryMappingID           string                     `db:"directory_mapping_id" json:"directory_mapping_id,omitempty"`
	DirectoryMappingRevision     int                        `db:"directory_mapping_revision" json:"directory_mapping_revision,omitempty"`
	DirectoryRevisionFingerprint string                     `db:"directory_revision_fingerprint" json:"directory_revision_fingerprint,omitempty"`
	Decision                     WorkflowApprovalStatus     `db:"decision" json:"decision"`
	Comment                      string                     `db:"comment" json:"comment,omitempty"`
	Created                      time.Time                  `db:"created" json:"created"`
	PolicyRevision               int                        `db:"policy_revision" json:"policy_revision"`
	CorrelationID                string                     `db:"correlation_id" json:"correlation_id"`
}

// Validate ensures that persisted contribution evidence cannot represent a
// non-user decision or an unbounded directory value.
func (contribution WorkflowApprovalContribution) Validate() error {
	if contribution.ApprovalID <= 0 || contribution.ActorUserID <= 0 ||
		contribution.RoleRevision < 1 || contribution.PolicyRevision < 1 || contribution.Created.IsZero() {
		return common_errors.NewValidationError("workflow approval contribution is invalid")
	}
	if err := ValidateProjectRoleReference(contribution.RoleID); err != nil {
		return err
	}
	if contribution.Decision != WorkflowApprovalApproved && contribution.Decision != WorkflowApprovalRejected {
		return common_errors.NewValidationError("workflow approval contribution decision is invalid")
	}
	if len(contribution.Comment) > MaxWorkflowApprovalCommentBytes ||
		len(contribution.CorrelationID) == 0 || len(contribution.CorrelationID) > MaxWorkflowApprovalCorrelationIDBytes ||
		len(contribution.DirectoryRevisionFingerprint) > MaxWorkflowApprovalDirectoryRevisionFingerprintBytes {
		return common_errors.NewValidationError("workflow approval contribution evidence is invalid")
	}
	switch contribution.RoleOrigin {
	case WorkflowApprovalRoleOriginBuiltIn, WorkflowApprovalRoleOriginManual:
		if contribution.DirectoryProviderID != "" || contribution.DirectoryMappingID != "" ||
			contribution.DirectoryMappingRevision != 0 || contribution.DirectoryRevisionFingerprint != "" {
			return common_errors.NewValidationError("workflow approval contribution provenance is invalid")
		}
	case WorkflowApprovalRoleOriginLDAP, WorkflowApprovalRoleOriginOIDC:
		if contribution.DirectoryProviderID == "" || contribution.DirectoryMappingID == "" ||
			contribution.DirectoryMappingRevision < 1 ||
			!isCanonicalSHA256Fingerprint(contribution.DirectoryRevisionFingerprint) {
			return common_errors.NewValidationError("workflow approval directory provenance is incomplete")
		}
	default:
		return common_errors.NewValidationError("workflow approval contribution role origin is invalid")
	}
	return nil
}

func isCanonicalSHA256Fingerprint(value string) bool {
	if len(value) != MaxWorkflowApprovalDirectoryRevisionFingerprintBytes {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

// Validate validates a workflow narrowing policy before it is persisted.
// Existence of custom roles is intentionally evaluated against the live store
// by the authorization evaluator rather than being inferred from this record.
func (policy WorkflowAccessPolicy) Validate() error {
	if policy.Revision < 0 {
		return common_errors.NewValidationError("workflow access policy revision is invalid")
	}
	if err := validateWorkflowRoleReferences(policy.ViewRoleIDs); err != nil {
		return err
	}
	return validateWorkflowRoleReferences(policy.StartRoleIDs)
}

// Validate validates a role-based approval policy before it is persisted.
func (policy WorkflowApprovalRolePolicy) Validate() error {
	if policy.Revision < 0 {
		return common_errors.NewValidationError("workflow approval role policy revision is invalid")
	}
	if policy.Mode != WorkflowApprovalRoleModeAnyOf && policy.Mode != WorkflowApprovalRoleModeAllOf {
		return common_errors.NewValidationError("workflow approval role policy mode is invalid")
	}
	if err := validateWorkflowRoleReferences(policy.RoleIDs); err != nil {
		return err
	}
	if len(policy.RoleIDs) == 0 {
		return common_errors.NewValidationError("workflow approval role policy requires roles")
	}
	if policy.MinimumDistinctApprovers < 1 {
		return common_errors.NewValidationError("workflow approval minimum distinct approvers is invalid")
	}
	if policy.Mode == WorkflowApprovalRoleModeAllOf && policy.MinimumDistinctApprovers < len(policy.RoleIDs) {
		return common_errors.NewValidationError("workflow approval all-of policy requires one distinct approver per role")
	}
	return nil
}

func validateWorkflowRoleReferences(references []ProjectRoleReference) error {
	seen := make(map[ProjectRoleReference]struct{}, len(references))
	for _, reference := range references {
		if err := ValidateProjectRoleReference(reference); err != nil {
			return err
		}
		if _, duplicate := seen[reference]; duplicate {
			return common_errors.NewValidationError("workflow role policy contains duplicate role reference")
		}
		seen[reference] = struct{}{}
	}
	return nil
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

// WorkflowGlobalCredentialValidationStore is the value-free subset used while
// validating workflow declarations and snapshots. Material and versions are
// deliberately absent: workflow authoring may approve a reference, not read it.
type WorkflowGlobalCredentialValidationStore interface {
	GetGlobalCredential(credentialID int) (GlobalCredential, error)
	GetGlobalCredentialGrantForProject(credentialID int, projectID int) (GlobalCredentialGrant, error)
}

// WorkflowNodeResultStore exposes only the sanitized, persisted summary used
// to freeze allow-listed condition inputs when a workflow task finishes.
type WorkflowNodeResultStore interface {
	GetTaskSummary(projectID int, taskID int) (TaskSummary, error)
}
