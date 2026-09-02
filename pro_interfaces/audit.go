package pro_interfaces

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/db"
)

type AuditAction string

const (
	AuditActionCapabilityResolve      AuditAction = "capability_resolve"
	AuditActionCapabilityRead         AuditAction = "capability_read"
	AuditActionCapabilityWrite        AuditAction = "capability_write"
	AuditActionCapabilityExecute      AuditAction = "capability_execute"
	AuditActionCapabilityConfigure    AuditAction = "capability_configure"
	AuditActionProjectRunnerList      AuditAction = "project_runner_list"
	AuditActionProjectRunnerRead      AuditAction = "project_runner_read"
	AuditActionProjectRunnerHealth    AuditAction = "project_runner_health"
	AuditActionProjectRunnerHistory   AuditAction = "project_runner_history"
	AuditActionProjectRunnerCreate    AuditAction = "project_runner_create"
	AuditActionProjectRunnerIssue     AuditAction = "project_runner_registration_issue"
	AuditActionProjectRunnerUpdate    AuditAction = "project_runner_update"
	AuditActionProjectRunnerActive    AuditAction = "project_runner_set_active"
	AuditActionProjectRunnerDelete    AuditAction = "project_runner_delete"
	AuditActionProjectRunnerCache     AuditAction = "project_runner_cache_clear"
	AuditActionProjectRoleCreate      AuditAction = "project_role_create"
	AuditActionProjectRoleUpdate      AuditAction = "project_role_update"
	AuditActionProjectRoleDelete      AuditAction = "project_role_delete"
	AuditActionProjectRoleAssign      AuditAction = "project_role_assign"
	AuditActionProjectMemberAdd       AuditAction = "project_member_add"
	AuditActionProjectMemberRemove    AuditAction = "project_member_remove"
	AuditActionGlobalRoleCreate       AuditAction = "global_role_create"
	AuditActionGlobalRoleUpdate       AuditAction = "global_role_update"
	AuditActionGlobalRoleDelete       AuditAction = "global_role_delete"
	AuditActionGlobalRoleAssign       AuditAction = "global_role_assign"
	AuditActionGlobalRoleUnassign     AuditAction = "global_role_unassign"
	AuditActionGlobalRoleRead         AuditAction = "global_role_read"
	AuditActionGlobalUserRead         AuditAction = "global_user_read"
	AuditActionGlobalUserCreate       AuditAction = "global_user_create"
	AuditActionGlobalUserUpdate       AuditAction = "global_user_update"
	AuditActionGlobalUserDelete       AuditAction = "global_user_delete"
	AuditActionGlobalUserPassword     AuditAction = "global_user_password_reset"
	AuditActionGlobalSystemRead       AuditAction = "global_system_read"
	AuditActionGlobalSystemWrite      AuditAction = "global_system_write"
	AuditActionGlobalAuditRead        AuditAction = "global_audit_read"
	AuditActionTemplateRoleCreate     AuditAction = "template_role_create"
	AuditActionTemplateRoleUpdate     AuditAction = "template_role_update"
	AuditActionTemplateRoleDelete     AuditAction = "template_role_delete"
	AuditActionWebhookRead            AuditAction = "audit_webhook_read"
	AuditActionWebhookConfigure       AuditAction = "audit_webhook_configure"
	AuditActionWebhookTest            AuditAction = "audit_webhook_test"
	AuditActionWebhookPause           AuditAction = "audit_webhook_pause"
	AuditActionWebhookResume          AuditAction = "audit_webhook_resume"
	AuditActionTOTPEnrollBegin        AuditAction = "totp_enroll_begin"
	AuditActionTOTPEnrollConfirm      AuditAction = "totp_enroll_confirm"
	AuditActionTOTPRecoveryAck        AuditAction = "totp_recovery_acknowledge"
	AuditActionTOTPChallenge          AuditAction = "totp_challenge"
	AuditActionTOTPRecover            AuditAction = "totp_recover"
	AuditActionTOTPReset              AuditAction = "totp_reset"
	AuditActionTOTPRollout            AuditAction = "totp_rollout"
	AuditActionLDAPConfigure          AuditAction = "ldap_configure"
	AuditActionLDAPTest               AuditAction = "ldap_test"
	AuditActionLDAPLogin              AuditAction = "ldap_login"
	AuditActionLDAPLink               AuditAction = "ldap_link"
	AuditActionLDAPGroupMappingRead   AuditAction = "ldap_group_mapping_read"
	AuditActionLDAPGroupMappingWrite  AuditAction = "ldap_group_mapping_write"
	AuditActionLDAPGroupMappingDelete AuditAction = "ldap_group_mapping_delete"
	AuditActionLDAPGroupPreview       AuditAction = "ldap_group_preview"
	AuditActionLDAPGroupApply         AuditAction = "ldap_group_apply"
	AuditActionLDAPGroupReconcile     AuditAction = "ldap_group_reconcile"
	AuditActionOIDCGroupMappingRead   AuditAction = "oidc_group_mapping_read"
	AuditActionOIDCGroupMappingWrite  AuditAction = "oidc_group_mapping_write"
	AuditActionOIDCGroupMappingDelete AuditAction = "oidc_group_mapping_delete"
	AuditActionOIDCGroupPreview       AuditAction = "oidc_group_preview"
)

const (
	AuditActionGlobalCredentialCreate        AuditAction = "global_credential_create"
	AuditActionGlobalCredentialRead          AuditAction = "global_credential_read"
	AuditActionGrantedCredentialRead         AuditAction = "granted_credential_read"
	AuditActionGlobalCredentialUpdate        AuditAction = "global_credential_update"
	AuditActionGlobalCredentialRotate        AuditAction = "global_credential_rotate"
	AuditActionGlobalCredentialGrant         AuditAction = "global_credential_grant"
	AuditActionGlobalCredentialGrantRevoke   AuditAction = "global_credential_grant_revoke"
	AuditActionGlobalCredentialGrantRestore  AuditAction = "global_credential_grant_restore"
	AuditActionGlobalCredentialGrantDelete   AuditAction = "global_credential_grant_delete"
	AuditActionGlobalCredentialDisable       AuditAction = "global_credential_disable"
	AuditActionGlobalCredentialEnable        AuditAction = "global_credential_enable"
	AuditActionGlobalCredentialDeleteAttempt AuditAction = "global_credential_delete_attempt"
)

const (
	AuditActionWorkflowList                         AuditAction = "workflow_list"
	AuditActionWorkflowRead                         AuditAction = "workflow_read"
	AuditActionWorkflowCreate                       AuditAction = "workflow_create"
	AuditActionWorkflowUpdate                       AuditAction = "workflow_update"
	AuditActionWorkflowDelete                       AuditAction = "workflow_delete"
	AuditActionWorkflowStart                        AuditAction = "workflow_start"
	AuditActionWorkflowStop                         AuditAction = "workflow_stop"
	AuditActionWorkflowRunRead                      AuditAction = "workflow_run_read"
	AuditActionWorkflowRunLogsRead                  AuditAction = "workflow_run_logs_read"
	AuditActionWorkflowApprovalInbox                AuditAction = "workflow_approval_inbox_read"
	AuditActionWorkflowApprovalContribute           AuditAction = "workflow_approval_contribute"
	AuditActionCrossProjectTemplateVersionPublish   AuditAction = "cross_project_template_version_publish"
	AuditActionCrossProjectTemplateGrantCreate      AuditAction = "cross_project_template_grant_create"
	AuditActionCrossProjectTemplateGrantUpdate      AuditAction = "cross_project_template_grant_update"
	AuditActionCrossProjectTemplateGrantAccept      AuditAction = "cross_project_template_grant_accept"
	AuditActionCrossProjectTemplateGrantRevoke      AuditAction = "cross_project_template_grant_revoke"
	AuditActionCrossProjectTemplateGrantDelete      AuditAction = "cross_project_template_grant_delete"
	AuditActionCrossProjectTemplateReferenceResolve AuditAction = "cross_project_template_reference_resolve"
	AuditActionExecutionPreflightPreview            AuditAction = "execution_preflight_preview"
	AuditActionExecutionPreflightStart              AuditAction = "execution_preflight_start"
)

type AuditTargetType string

const (
	AuditTargetCapability           AuditTargetType = "capability"
	AuditTargetProjectRunner        AuditTargetType = "project_runner"
	AuditTargetProjectRole          AuditTargetType = "project_role"
	AuditTargetProjectMembership    AuditTargetType = "project_membership"
	AuditTargetGlobalRole           AuditTargetType = "global_role"
	AuditTargetGlobalRoleAssignment AuditTargetType = "global_role_assignment"
	AuditTargetGlobalUser           AuditTargetType = "global_user"
	AuditTargetGlobalSystem         AuditTargetType = "global_system"
	AuditTargetGlobalAudit          AuditTargetType = "global_audit"
	AuditTargetTemplateRole         AuditTargetType = "template_role"
	AuditTargetWebhook              AuditTargetType = "audit_webhook"
	AuditTargetLDAPGroupMapping     AuditTargetType = "ldap_group_mapping"
	AuditTargetOIDCGroupMapping     AuditTargetType = "oidc_group_mapping"
	AuditTargetNotification         AuditTargetType = "notification_governance"
)

const (
	AuditTargetGlobalCredential      AuditTargetType = "global_credential"
	AuditTargetGlobalCredentialGrant AuditTargetType = "global_credential_grant"
)

const (
	AuditTargetWorkflow                    AuditTargetType = "workflow"
	AuditTargetWorkflowRun                 AuditTargetType = "workflow_run"
	AuditTargetWorkflowApproval            AuditTargetType = "workflow_approval"
	AuditTargetWorkflowApprovalInbox       AuditTargetType = "workflow_approval_inbox"
	AuditTargetCrossProjectTemplateGrant   AuditTargetType = "cross_project_template_grant"
	AuditTargetCrossProjectTemplateVersion AuditTargetType = "cross_project_template_version"
	AuditTargetExecutionPreflight          AuditTargetType = "execution_preflight"
)

type AuditOutcome string

const (
	AuditOutcomeAllowed AuditOutcome = "allowed"
	AuditOutcomeDenied  AuditOutcome = "denied"
	AuditOutcomeFailure AuditOutcome = "failure"
)

type AuditSource string

const (
	AuditSourceAPI    AuditSource = "api"
	AuditSourceWorker AuditSource = "worker"
)

const (
	AuditReasonUnauthenticated        = "unauthenticated"
	AuditReasonCrossOrigin            = "cross_origin"
	AuditReasonProviderError          = "provider_error"
	AuditReasonInvalidInput           = "invalid_input"
	AuditReasonOperationError         = "operation_error"
	AuditReasonActiveAssignments      = "active_assignments"
	AuditReasonEnrollmentPending      = "enrollment_pending"
	AuditReasonEnrollmentActive       = "enrollment_active"
	AuditReasonInvalidCode            = "invalid_code"
	AuditReasonReplay                 = "replay"
	AuditReasonThrottled              = "throttled"
	AuditReasonRecoveryUsed           = "recovery_used"
	AuditReasonReset                  = "reset"
	AuditReasonReadiness              = "readiness"
	AuditReasonLDAPInvalidCredentials = "ldap_invalid_credentials"
	AuditReasonLDAPPolicy             = "ldap_policy"
	AuditReasonOIDCPolicy             = "oidc_policy"
)

const (
	AuditReasonWorkflowPolicyAllowed              = "workflow_policy_allowed"
	AuditReasonWorkflowPolicyDenied               = "workflow_policy_denied"
	AuditReasonWorkflowApprovalApproved           = "workflow_approval_approved"
	AuditReasonWorkflowApprovalRejected           = "workflow_approval_rejected"
	AuditReasonWorkflowApprovalIneligible         = "workflow_approval_ineligible"
	AuditReasonWorkflowApprovalInitiatorSeparated = "workflow_approval_initiator_separated"
	AuditReasonWorkflowApprovalRoleRevoked        = "workflow_approval_role_revoked"
	AuditReasonWorkflowApprovalAlreadyResolved    = "workflow_approval_already_resolved"
	AuditReasonWorkflowApprovalTimedOut           = "workflow_approval_timed_out"
	AuditReasonCrossProjectTemplateGrantActive    = "cross_project_template_grant_active"
	AuditReasonCrossProjectTemplateGrantDenied    = "cross_project_template_grant_denied"
	AuditReasonExecutionPreflightPreviewed        = "execution_preflight_previewed"
	AuditReasonExecutionPreflightStarted          = "execution_preflight_started"
	AuditReasonExecutionPreflightDenied           = "execution_preflight_denied"
	AuditReasonExecutionPreflightStale            = "execution_preflight_stale"
)

// AuditRoleOrigin records how a role was effective when a workflow decision
// was made. It intentionally excludes directory claims and display names.
type AuditRoleOrigin string

const (
	AuditRoleOriginBuiltin AuditRoleOrigin = "builtin"
	AuditRoleOriginManual  AuditRoleOrigin = "manual"
	AuditRoleOriginLDAP    AuditRoleOrigin = "ldap"
	AuditRoleOriginOIDC    AuditRoleOrigin = "oidc"
)

type DependencyID string

const (
	DependencyAuditDatabase DependencyID = "audit_database"
	DependencyAuditFile     DependencyID = "audit_file"
	DependencyAuditWebhook  DependencyID = "audit_webhook"
)

type AuditSink string

const (
	AuditSinkDatabase AuditSink = "database"
	AuditSinkFile     AuditSink = "file"
)

type DroppedRecordReason string

const (
	DroppedRecordWriteFailure DroppedRecordReason = "write_failure"
	DroppedRecordInvalid      DroppedRecordReason = "invalid_record"
)

type QueueID string

const (
	QueueEnhancedAudit QueueID = "enhanced_audit"
	QueueAuditWebhook  QueueID = "audit_webhook"
)

const (
	AuditWebhookSchemaVersion = "semaphore.audit.v1"
	AuditEventIDBytes         = 16
	AuditUserAgentMaxLength   = 256
)

const (
	AuditWorkflowPolicyRevisionMax = 1_000_000_000
	AuditRoleProvenanceMaxEntries  = 16
)

var (
	correlationPattern              = regexp.MustCompile(`^(?:[a-f0-9]{32}|internal)$`)
	eventIDPattern                  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	identifierPattern               = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
	projectRunnerTargetPattern      = regexp.MustCompile(`^(?:project|runner):[1-9][0-9]*$`)
	projectRoleTargetPattern        = regexp.MustCompile(`^(?:project:[1-9][0-9]*|role:[a-z0-9][a-z0-9_.-]{0,49})$`)
	projectMemberTargetPattern      = regexp.MustCompile(`^(?:project:[1-9][0-9]*|member:[1-9][0-9]*)$`)
	globalRoleTargetPattern         = regexp.MustCompile(`^(?:roles|role:[a-z0-9][a-z0-9_.-]{0,49})$`)
	globalAssignmentTargetPattern   = regexp.MustCompile(`^(?:user|assignment):[1-9][0-9]*$`)
	globalUserTargetPattern         = regexp.MustCompile(`^(?:users|user:[1-9][0-9]*)$`)
	globalSystemTargetPattern       = regexp.MustCompile(`^(?:subscription|options|cache)$`)
	templateRoleTargetPattern       = regexp.MustCompile(`^(?:template|template-role):[1-9][0-9]*$`)
	ldapGroupTargetPattern          = regexp.MustCompile(`^(?:(?:entryuuid|objectguid|nsuniqueid|ipauniqueid):[0-9a-f-]{36}|provider:[a-z][a-z0-9_-]{0,63})$`)
	oidcGroupTargetPattern          = regexp.MustCompile(`^provider:[a-z][a-z0-9_-]{0,63}$`)
	workflowTargetPattern           = regexp.MustCompile(`^(?:project|workflow):[1-9][0-9]*$`)
	workflowRunTargetPattern        = regexp.MustCompile(`^run:[1-9][0-9]*$`)
	workflowApprovalTargetPattern   = regexp.MustCompile(`^approval:[1-9][0-9]*$`)
	workflowInboxTargetPattern      = regexp.MustCompile(`^project:[1-9][0-9]*$`)
	executionPreflightTargetPattern = regexp.MustCompile(`^(?:task-template|workflow):[1-9][0-9]*$`)
	notificationTargetPattern       = regexp.MustCompile(`^(?:global|project:[1-9][0-9]*)$`)
	workflowRoleIDPattern           = regexp.MustCompile(`^(?:builtin:(?:owner|manager|task_runner|guest)|role:[a-z0-9][a-z0-9_-]{0,63})$`)
)

var (
	globalCredentialTargetPattern      = regexp.MustCompile(`^(?:credentials|credential:[1-9][0-9]*)$`)
	globalCredentialGrantTargetPattern = regexp.MustCompile(`^credential-grant:[1-9][0-9]*$`)
)

// AuditRoleProvenance is immutable, bounded evidence for an effective role at
// one workflow authorization decision. Directory fields contain only stable
// identifiers and revisions; raw claims, group names, and credentials are not
// part of the audit protocol.
type AuditRoleProvenance struct {
	RoleID                       string          `json:"role_id"`
	RoleRevision                 int             `json:"role_revision"`
	Origin                       AuditRoleOrigin `json:"origin"`
	DirectoryProviderID          string          `json:"directory_provider_id,omitempty"`
	DirectoryMappingID           string          `json:"directory_mapping_id,omitempty"`
	DirectoryMappingRevision     int             `json:"directory_mapping_revision,omitempty"`
	DirectoryRevisionFingerprint string          `json:"directory_revision_fingerprint,omitempty"`
}

// AuditEvent is the allowlisted payload shared by enhanced features. It has no
// field for request bodies, credentials, raw errors, or arbitrary log values.
type AuditEvent struct {
	EventID       string          `json:"event_id,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at,omitempty"`
	CorrelationID string          `json:"correlation_id"`
	ActorID       *int            `json:"actor_id,omitempty"`
	ProjectID     *int            `json:"project_id,omitempty"`
	Action        AuditAction     `json:"action"`
	TargetType    AuditTargetType `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Outcome       AuditOutcome    `json:"outcome"`
	Source        AuditSource     `json:"source"`
	SourceIP      string          `json:"source_ip,omitempty"`
	UserAgent     string          `json:"user_agent,omitempty"`
	Reason        string          `json:"reason"`
	// WorkflowPolicyRevision and RoleProvenance are retained in the database
	// event JSON. They are intentionally not added to the v1 webhook envelope.
	WorkflowPolicyRevision         int                                  `json:"workflow_policy_revision,omitempty"`
	RoleProvenance                 []AuditRoleProvenance                `json:"role_provenance,omitempty"`
	CrossProjectTemplateProvenance *AuditCrossProjectTemplateProvenance `json:"cross_project_template_provenance,omitempty"`
	ExecutionPreflightProvenance   *AuditExecutionPreflightProvenance   `json:"execution_preflight_provenance,omitempty"`
}

// AuditExecutionPreflightProvenance is the complete allowlist for execution
// preview/start context. It deliberately has no token, resource names, input,
// command, endpoint, path, webhook, or error-text field.
type AuditExecutionPreflightProvenance struct {
	Intent           ExecutionPreflightIntent       `json:"intent"`
	Fingerprint      string                         `json:"fingerprint"`
	Changes          []ExecutionPreflightChangeCode `json:"changes,omitempty"`
	ReasonCodes      []ExecutionPreflightReasonCode `json:"reason_codes,omitempty"`
	CandidateCount   int                            `json:"candidate_count,omitempty"`
	SelectedRunnerID int                            `json:"selected_runner_id,omitempty"`
}

// NewExecutionPreflightAuditProvenance projects a plan into the only
// execution-preflight metadata permitted in an audit record.
func NewExecutionPreflightAuditProvenance(
	plan ExecutionPreflightPlan,
	changes []ExecutionPreflightChangeCode,
) *AuditExecutionPreflightProvenance {
	provenance := &AuditExecutionPreflightProvenance{
		Intent: plan.Intent, Fingerprint: plan.Fingerprint,
	}
	changeSet := make(map[ExecutionPreflightChangeCode]struct{}, len(changes))
	for _, change := range changes {
		changeSet[change] = struct{}{}
	}
	for change := range changeSet {
		provenance.Changes = append(provenance.Changes, change)
	}
	reasons := make(map[ExecutionPreflightReasonCode]struct{})
	selected := 0
	selectedCount := 0
	for _, placement := range plan.Placements {
		provenance.CandidateCount += len(placement.Candidates)
		if placement.Decision != "" {
			reasons[placement.Decision] = struct{}{}
		}
		if placement.SelectedRunnerID != nil {
			selected = *placement.SelectedRunnerID
			selectedCount++
		}
	}
	if selectedCount == 1 {
		provenance.SelectedRunnerID = selected
	}
	for _, finding := range plan.Findings {
		if finding.Code != "" {
			reasons[finding.Code] = struct{}{}
		}
	}
	for reason := range reasons {
		provenance.ReasonCodes = append(provenance.ReasonCodes, reason)
	}
	sort.Slice(provenance.Changes, func(i, j int) bool { return provenance.Changes[i] < provenance.Changes[j] })
	sort.Slice(provenance.ReasonCodes, func(i, j int) bool { return provenance.ReasonCodes[i] < provenance.ReasonCodes[j] })
	return provenance
}

// ExecutionPreflightAuditDenialReason returns the bounded, server-derived
// reason used for a denied preview/start audit event. It never returns a
// finding message or any other plan content.
func ExecutionPreflightAuditDenialReason(plan ExecutionPreflightPlan) (string, bool) {
	for _, expected := range []ExecutionPreflightReasonCode{
		ExecutionReasonHiddenReference,
		ExecutionReasonPermissionDenied,
		ExecutionReasonCapabilityUnavailable,
		ExecutionReasonPolicyDenied,
		ExecutionReasonPlanLimitExceeded,
		ExecutionReasonInvalidInput,
		ExecutionReasonNoCandidate,
	} {
		for _, finding := range plan.Findings {
			if finding.Severity == ExecutionFindingDenial && finding.Code == expected {
				return string(expected), true
			}
		}
	}
	return "", false
}

// NewExecutionPreflightAuditEvent creates a scoped API audit event without an
// extension point for arbitrary request data or error text.
func NewExecutionPreflightAuditEvent(
	actorID, projectID int,
	correlationID string,
	sourceIP, userAgent string,
	action AuditAction,
	outcome AuditOutcome,
	reason string,
	intent ExecutionPreflightIntent,
	resourceID int,
	provenance *AuditExecutionPreflightProvenance,
) AuditEvent {
	targetID := ""
	if intent == ExecutionPreflightTask {
		targetID = "task-template:" + strconv.Itoa(resourceID)
	} else if intent == ExecutionPreflightWorkflow {
		targetID = "workflow:" + strconv.Itoa(resourceID)
	}
	return AuditEvent{
		CorrelationID:                correlationID,
		ActorID:                      &actorID,
		ProjectID:                    &projectID,
		Action:                       action,
		TargetType:                   AuditTargetExecutionPreflight,
		TargetID:                     targetID,
		Outcome:                      outcome,
		Source:                       AuditSourceAPI,
		SourceIP:                     NormalizeAuditSourceIP(sourceIP),
		UserAgent:                    SanitizeAuditUserAgent(userAgent),
		Reason:                       reason,
		ExecutionPreflightProvenance: provenance,
	}
}

// AuditCrossProjectTemplateProvenance is the only permitted cross-project
// audit context. It carries immutable identifiers, never names or values.
type AuditCrossProjectTemplateProvenance struct {
	OwnerProjectID             int    `json:"owner_project_id"`
	ConsumerProjectID          int    `json:"consumer_project_id"`
	TemplateID                 int    `json:"template_id"`
	TemplateVersionID          int    `json:"template_version_id"`
	TemplateVersionNumber      int    `json:"template_version_number"`
	TemplateVersionFingerprint string `json:"template_version_fingerprint"`
	MinTemplateVersion         int    `json:"min_template_version,omitempty"`
	MaxTemplateVersion         int    `json:"max_template_version,omitempty"`
	GrantID                    int    `json:"grant_id"`
	GrantRevision              int    `json:"grant_revision"`
	Operation                  int    `json:"operation"`
}

func (e AuditEvent) Validate() error {
	if !correlationPattern.MatchString(e.CorrelationID) {
		return fmt.Errorf("invalid audit correlation ID")
	}
	if e.EventID != "" && !eventIDPattern.MatchString(e.EventID) {
		return fmt.Errorf("invalid audit event ID")
	}
	if !e.OccurredAt.IsZero() && e.OccurredAt.Location() != time.UTC {
		return fmt.Errorf("audit occurrence time must be UTC")
	}
	if e.SourceIP != "" && net.ParseIP(e.SourceIP) == nil {
		return fmt.Errorf("invalid audit source IP")
	}
	if !validAuditUserAgent(e.UserAgent) {
		return fmt.Errorf("invalid audit user agent")
	}
	if !identifierPattern.MatchString(e.TargetID) || !identifierPattern.MatchString(e.Reason) {
		return fmt.Errorf("invalid audit identifier")
	}
	if !validAuditAction(e.Action) || !validAuditTarget(e) || !validAuditActionTarget(e) ||
		!validAuditOutcome(e.Outcome) || !validAuditSource(e.Source) || !validAuditReason(e.Reason) {
		return fmt.Errorf("unsupported audit context")
	}
	if !validWorkflowAuditReason(e) {
		return fmt.Errorf("invalid workflow audit reason")
	}
	if !validWorkflowAuditProvenance(e) {
		return fmt.Errorf("invalid workflow audit provenance")
	}
	if !validCrossProjectTemplateAuditProvenance(e) {
		return fmt.Errorf("invalid cross-project template audit provenance")
	}
	if !validExecutionPreflightAuditProvenance(e) {
		return fmt.Errorf("invalid execution preflight audit provenance")
	}
	return nil
}

func validAuditTarget(event AuditEvent) bool {
	switch event.TargetType {
	case AuditTargetCapability:
		return event.ProjectID == nil && validCapabilityAuditTarget(event.TargetID)
	case AuditTargetProjectRunner:
		if !projectRunnerTargetPattern.MatchString(event.TargetID) {
			return false
		}
		if event.ProjectID == nil {
			return event.Outcome == AuditOutcomeDenied &&
				(event.Reason == AuditReasonUnauthenticated || event.Reason == AuditReasonCrossOrigin)
		}
		if *event.ProjectID <= 0 {
			return false
		}
		if !strings.HasPrefix(event.TargetID, "project:") {
			return true
		}
		targetProjectID, err := strconv.Atoi(strings.TrimPrefix(event.TargetID, "project:"))
		return err == nil && targetProjectID == *event.ProjectID
	case AuditTargetProjectRole:
		return validScopedProjectAuditTarget(event, projectRoleTargetPattern)
	case AuditTargetProjectMembership:
		return validScopedProjectAuditTarget(event, projectMemberTargetPattern)
	case AuditTargetGlobalRole:
		return event.ProjectID == nil && globalRoleTargetPattern.MatchString(event.TargetID)
	case AuditTargetGlobalRoleAssignment:
		return event.ProjectID == nil && globalAssignmentTargetPattern.MatchString(event.TargetID)
	case AuditTargetGlobalUser:
		return event.ProjectID == nil && globalUserTargetPattern.MatchString(event.TargetID)
	case AuditTargetGlobalSystem:
		return event.ProjectID == nil && globalSystemTargetPattern.MatchString(event.TargetID)
	case AuditTargetGlobalAudit:
		return event.ProjectID == nil && event.TargetID == "events"
	case AuditTargetTemplateRole:
		return validScopedProjectAuditTarget(event, templateRoleTargetPattern)
	case AuditTargetWebhook:
		return event.ProjectID == nil && event.TargetID == "audit_webhook"
	case AuditTargetLDAPGroupMapping:
		return event.ProjectID == nil && ldapGroupTargetPattern.MatchString(event.TargetID)
	case AuditTargetOIDCGroupMapping:
		return event.ProjectID == nil && oidcGroupTargetPattern.MatchString(event.TargetID)
	case AuditTargetNotification:
		if !notificationTargetPattern.MatchString(event.TargetID) {
			return false
		}
		if event.ProjectID == nil {
			return event.TargetID == "global"
		}
		return event.TargetID == "project:"+strconv.Itoa(*event.ProjectID)
	case AuditTargetGlobalCredential:
		return event.ProjectID == nil && globalCredentialTargetPattern.MatchString(event.TargetID)
	case AuditTargetGlobalCredentialGrant:
		if event.ProjectID != nil {
			return event.TargetID == "project:"+strconv.Itoa(*event.ProjectID)
		}
		return globalCredentialGrantTargetPattern.MatchString(event.TargetID)
	case AuditTargetWorkflow:
		return validScopedProjectAuditTarget(event, workflowTargetPattern)
	case AuditTargetWorkflowRun:
		return validScopedProjectAuditTarget(event, workflowRunTargetPattern)
	case AuditTargetWorkflowApproval:
		return validScopedProjectAuditTarget(event, workflowApprovalTargetPattern)
	case AuditTargetWorkflowApprovalInbox:
		return validScopedProjectAuditTarget(event, workflowInboxTargetPattern)
	case AuditTargetCrossProjectTemplateGrant:
		return validScopedProjectAuditTarget(event, regexp.MustCompile(`^grant:[1-9][0-9]*$`))
	case AuditTargetCrossProjectTemplateVersion:
		return validScopedProjectAuditTarget(event, regexp.MustCompile(`^template-version:[1-9][0-9]*$`))
	case AuditTargetExecutionPreflight:
		if event.ProjectID == nil || *event.ProjectID <= 0 || !executionPreflightTargetPattern.MatchString(event.TargetID) {
			return false
		}
		return true
	default:
		return false
	}
}

func validCrossProjectTemplateAuditProvenance(event AuditEvent) bool {
	if !isCrossProjectTemplateAuditAction(event.Action) {
		return event.CrossProjectTemplateProvenance == nil
	}
	p := event.CrossProjectTemplateProvenance
	if p == nil || p.OwnerProjectID <= 0 || p.TemplateID <= 0 {
		return false
	}
	exact := event.Action == AuditActionCrossProjectTemplateVersionPublish || event.Action == AuditActionCrossProjectTemplateReferenceResolve
	if exact && (p.TemplateVersionID <= 0 || p.TemplateVersionNumber <= 0 || len(p.TemplateVersionFingerprint) != len("sha256:")+64 || !strings.HasPrefix(p.TemplateVersionFingerprint, "sha256:")) {
		return false
	}
	if exact {
		if _, err := hex.DecodeString(strings.TrimPrefix(p.TemplateVersionFingerprint, "sha256:")); err != nil {
			return false
		}
	}
	if event.Action == AuditActionCrossProjectTemplateVersionPublish {
		return event.TargetType == AuditTargetCrossProjectTemplateVersion && event.TargetID == "template-version:"+strconv.Itoa(p.TemplateVersionID) && event.ProjectID != nil && *event.ProjectID == p.OwnerProjectID && p.ConsumerProjectID == 0 && p.GrantID == 0 && p.GrantRevision == 0 && p.Operation == 0
	}
	if p.ConsumerProjectID <= 0 || p.OwnerProjectID == p.ConsumerProjectID || p.GrantID <= 0 || p.GrantRevision <= 0 || !db.CrossProjectTemplateGrantOperation(p.Operation).IsValid() || p.MinTemplateVersion <= 0 || p.MaxTemplateVersion < p.MinTemplateVersion {
		return false
	}
	if event.TargetType != AuditTargetCrossProjectTemplateGrant || event.TargetID != "grant:"+strconv.Itoa(p.GrantID) || event.ProjectID == nil {
		return false
	}
	switch event.Action {
	case AuditActionCrossProjectTemplateGrantCreate, AuditActionCrossProjectTemplateGrantUpdate, AuditActionCrossProjectTemplateGrantDelete:
		return *event.ProjectID == p.OwnerProjectID
	case AuditActionCrossProjectTemplateGrantAccept:
		return *event.ProjectID == p.ConsumerProjectID
	case AuditActionCrossProjectTemplateGrantRevoke:
		return *event.ProjectID == p.OwnerProjectID || *event.ProjectID == p.ConsumerProjectID
	case AuditActionCrossProjectTemplateReferenceResolve:
		return *event.ProjectID == p.ConsumerProjectID && db.CrossProjectTemplateGrantOperation(p.Operation) == db.CrossProjectTemplateGrantReference
	default:
		return false
	}
}

func validExecutionPreflightAuditProvenance(event AuditEvent) bool {
	if !isExecutionPreflightAuditAction(event.Action) {
		return event.ExecutionPreflightProvenance == nil
	}
	p := event.ExecutionPreflightProvenance
	if p == nil {
		return (event.Outcome == AuditOutcomeDenied && event.Reason == AuditReasonExecutionPreflightDenied) ||
			(event.Outcome == AuditOutcomeFailure && event.Reason == AuditReasonOperationError)
	}
	if !validExecutionPreflightAuditFingerprint(p.Fingerprint) ||
		len(p.Changes) > MaxExecutionPreflightChanges || len(p.ReasonCodes) > MaxExecutionPreflightFindings ||
		p.CandidateCount < 0 || p.CandidateCount > MaxExecutionPreflightPlacements*MaxExecutionPreflightCandidates ||
		p.SelectedRunnerID < 0 {
		return false
	}
	if p.Intent == ExecutionPreflightTask {
		if !strings.HasPrefix(event.TargetID, "task-template:") {
			return false
		}
	} else if p.Intent == ExecutionPreflightWorkflow {
		if !strings.HasPrefix(event.TargetID, "workflow:") {
			return false
		}
	} else {
		return false
	}
	if !validDistinctExecutionPreflightChanges(p.Changes) || !validDistinctExecutionPreflightReasons(p.ReasonCodes) {
		return false
	}
	switch event.Action {
	case AuditActionExecutionPreflightPreview:
		switch event.Outcome {
		case AuditOutcomeAllowed:
			return event.Reason == AuditReasonExecutionPreflightPreviewed
		case AuditOutcomeDenied:
			return validExecutionPreflightAuditDenialReason(event.Reason)
		case AuditOutcomeFailure:
			return event.Reason == AuditReasonOperationError
		}
	case AuditActionExecutionPreflightStart:
		switch event.Outcome {
		case AuditOutcomeAllowed:
			return event.Reason == AuditReasonExecutionPreflightStarted
		case AuditOutcomeDenied:
			return validExecutionPreflightAuditDenialReason(event.Reason) || event.Reason == AuditReasonExecutionPreflightStale
		case AuditOutcomeFailure:
			return event.Reason == AuditReasonOperationError
		}
	}
	return false
}

func validExecutionPreflightAuditFingerprint(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validExecutionPreflightAuditDenialReason(reason string) bool {
	switch ExecutionPreflightReasonCode(reason) {
	case ExecutionReasonHiddenReference, ExecutionReasonPermissionDenied, ExecutionReasonCapabilityUnavailable,
		ExecutionReasonPolicyDenied, ExecutionReasonPlanLimitExceeded, ExecutionReasonInvalidInput,
		ExecutionReasonNoCandidate:
		return true
	default:
		return false
	}
}

func validDistinctExecutionPreflightChanges(values []ExecutionPreflightChangeCode) bool {
	seen := make(map[ExecutionPreflightChangeCode]struct{}, len(values))
	for _, value := range values {
		switch value {
		case ExecutionChangeDefinition, ExecutionChangeInput, ExecutionChangeReference, ExecutionChangePlacement,
			ExecutionChangePermission, ExecutionChangeCapability, ExecutionChangePolicy:
		default:
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validDistinctExecutionPreflightReasons(values []ExecutionPreflightReasonCode) bool {
	seen := make(map[ExecutionPreflightReasonCode]struct{}, len(values))
	for _, value := range values {
		switch value {
		case ExecutionReasonSelected, ExecutionReasonProvisionalPlacement, ExecutionReasonDifferentProject, ExecutionReasonInactive,
			ExecutionReasonNotRegistered, ExecutionReasonOffline, ExecutionReasonCapacity,
			ExecutionReasonTagMismatch, ExecutionReasonImageUnsupported, ExecutionReasonNoCandidate,
			ExecutionReasonHiddenReference, ExecutionReasonPermissionDenied, ExecutionReasonCapabilityUnavailable,
			ExecutionReasonPolicyDenied, ExecutionReasonPlanLimitExceeded, ExecutionReasonInvalidInput:
		default:
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validWorkflowAuditProvenance(event AuditEvent) bool {
	workflowTarget := event.TargetType == AuditTargetWorkflow ||
		event.TargetType == AuditTargetWorkflowRun ||
		event.TargetType == AuditTargetWorkflowApproval ||
		event.TargetType == AuditTargetWorkflowApprovalInbox
	if !workflowTarget {
		return event.WorkflowPolicyRevision == 0 && len(event.RoleProvenance) == 0
	}
	if event.WorkflowPolicyRevision <= 0 || event.WorkflowPolicyRevision > AuditWorkflowPolicyRevisionMax {
		return false
	}
	if len(event.RoleProvenance) > AuditRoleProvenanceMaxEntries {
		return false
	}
	if event.Action == AuditActionWorkflowApprovalContribute {
		if event.Outcome == AuditOutcomeAllowed && len(event.RoleProvenance) == 0 {
			return false
		}
		if event.Outcome != AuditOutcomeAllowed && len(event.RoleProvenance) != 0 {
			return false
		}
	}
	for _, provenance := range event.RoleProvenance {
		if !validAuditRoleProvenance(provenance) {
			return false
		}
	}
	return true
}

func validWorkflowAuditReason(event AuditEvent) bool {
	if !isWorkflowAuditAction(event.Action) {
		return true
	}
	if event.Action == AuditActionWorkflowApprovalContribute {
		switch event.Outcome {
		case AuditOutcomeAllowed:
			return event.Reason == AuditReasonWorkflowApprovalApproved ||
				event.Reason == AuditReasonWorkflowApprovalRejected
		case AuditOutcomeDenied:
			return event.Reason == AuditReasonWorkflowApprovalIneligible ||
				event.Reason == AuditReasonWorkflowApprovalInitiatorSeparated ||
				event.Reason == AuditReasonWorkflowApprovalRoleRevoked ||
				event.Reason == AuditReasonWorkflowApprovalAlreadyResolved ||
				event.Reason == AuditReasonWorkflowApprovalTimedOut
		case AuditOutcomeFailure:
			return event.Reason == AuditReasonOperationError
		default:
			return false
		}
	}
	switch event.Outcome {
	case AuditOutcomeAllowed:
		return event.Reason == AuditReasonWorkflowPolicyAllowed
	case AuditOutcomeDenied:
		return event.Reason == AuditReasonWorkflowPolicyDenied ||
			event.Reason == AuditReasonUnauthenticated || event.Reason == AuditReasonCrossOrigin
	case AuditOutcomeFailure:
		return event.Reason == AuditReasonOperationError
	default:
		return false
	}
}

func validAuditActionTarget(event AuditEvent) bool {
	if isExecutionPreflightAuditAction(event.Action) {
		if event.TargetType != AuditTargetExecutionPreflight || event.ProjectID == nil {
			return false
		}
		if event.Action == AuditActionExecutionPreflightPreview || event.Action == AuditActionExecutionPreflightStart {
			return executionPreflightTargetPattern.MatchString(event.TargetID)
		}
		return false
	}
	if event.TargetType == AuditTargetExecutionPreflight {
		return false
	}
	if isGlobalCredentialAuditAction(event.Action) {
		switch event.Action {
		case AuditActionGrantedCredentialRead:
			return event.ProjectID != nil && event.TargetType == AuditTargetGlobalCredentialGrant &&
				event.TargetID == "project:"+strconv.Itoa(*event.ProjectID)
		case AuditActionGlobalCredentialRead:
			return event.ProjectID == nil && event.TargetType == AuditTargetGlobalCredential
		case AuditActionGlobalCredentialCreate:
			return event.ProjectID == nil && event.TargetType == AuditTargetGlobalCredential && event.TargetID == "credentials"
		case AuditActionGlobalCredentialGrant:
			return event.ProjectID == nil && ((event.TargetType == AuditTargetGlobalCredential && event.TargetID != "credentials") ||
				event.TargetType == AuditTargetGlobalCredentialGrant)
		case AuditActionGlobalCredentialGrantRevoke, AuditActionGlobalCredentialGrantRestore,
			AuditActionGlobalCredentialGrantDelete:
			return event.ProjectID == nil && event.TargetType == AuditTargetGlobalCredentialGrant
		default:
			return event.ProjectID == nil && event.TargetType == AuditTargetGlobalCredential && event.TargetID != "credentials"
		}
	}
	if isCrossProjectTemplateAuditAction(event.Action) {
		if event.Action == AuditActionCrossProjectTemplateVersionPublish {
			return event.TargetType == AuditTargetCrossProjectTemplateVersion
		}
		return event.TargetType == AuditTargetCrossProjectTemplateGrant
	}
	workflowTarget := event.TargetType == AuditTargetWorkflow ||
		event.TargetType == AuditTargetWorkflowRun ||
		event.TargetType == AuditTargetWorkflowApproval ||
		event.TargetType == AuditTargetWorkflowApprovalInbox
	if !workflowTarget {
		return !isWorkflowAuditAction(event.Action)
	}
	switch event.Action {
	case AuditActionWorkflowList, AuditActionWorkflowCreate:
		return event.TargetType == AuditTargetWorkflow && strings.HasPrefix(event.TargetID, "project:")
	case AuditActionWorkflowRead, AuditActionWorkflowUpdate, AuditActionWorkflowDelete, AuditActionWorkflowStart:
		return event.TargetType == AuditTargetWorkflow && strings.HasPrefix(event.TargetID, "workflow:")
	case AuditActionWorkflowStop, AuditActionWorkflowRunRead, AuditActionWorkflowRunLogsRead:
		return event.TargetType == AuditTargetWorkflowRun
	case AuditActionWorkflowApprovalInbox:
		return event.TargetType == AuditTargetWorkflowApprovalInbox
	case AuditActionWorkflowApprovalContribute:
		return event.TargetType == AuditTargetWorkflowApproval
	default:
		return false
	}
}

func isWorkflowAuditAction(action AuditAction) bool {
	switch action {
	case AuditActionWorkflowList, AuditActionWorkflowRead, AuditActionWorkflowCreate,
		AuditActionWorkflowUpdate, AuditActionWorkflowDelete, AuditActionWorkflowStart,
		AuditActionWorkflowStop, AuditActionWorkflowRunRead, AuditActionWorkflowRunLogsRead,
		AuditActionWorkflowApprovalInbox, AuditActionWorkflowApprovalContribute:
		return true
	default:
		return false
	}
}

func isExecutionPreflightAuditAction(action AuditAction) bool {
	return action == AuditActionExecutionPreflightPreview || action == AuditActionExecutionPreflightStart
}

func isCrossProjectTemplateAuditAction(action AuditAction) bool {
	switch action {
	case AuditActionCrossProjectTemplateVersionPublish, AuditActionCrossProjectTemplateGrantCreate, AuditActionCrossProjectTemplateGrantUpdate, AuditActionCrossProjectTemplateGrantAccept, AuditActionCrossProjectTemplateGrantRevoke, AuditActionCrossProjectTemplateGrantDelete, AuditActionCrossProjectTemplateReferenceResolve:
		return true
	}
	return false
}

func isGlobalCredentialAuditAction(action AuditAction) bool {
	switch action {
	case AuditActionGlobalCredentialCreate, AuditActionGlobalCredentialRead,
		AuditActionGrantedCredentialRead, AuditActionGlobalCredentialUpdate,
		AuditActionGlobalCredentialRotate, AuditActionGlobalCredentialGrant,
		AuditActionGlobalCredentialGrantRevoke, AuditActionGlobalCredentialGrantRestore,
		AuditActionGlobalCredentialGrantDelete,
		AuditActionGlobalCredentialDisable, AuditActionGlobalCredentialEnable,
		AuditActionGlobalCredentialDeleteAttempt:
		return true
	default:
		return false
	}
}

func validAuditRoleProvenance(provenance AuditRoleProvenance) bool {
	if !workflowRoleIDPattern.MatchString(provenance.RoleID) ||
		provenance.RoleRevision <= 0 || provenance.RoleRevision > AuditWorkflowPolicyRevisionMax {
		return false
	}
	directoryFieldsPresent := provenance.DirectoryProviderID != "" ||
		provenance.DirectoryMappingID != "" ||
		provenance.DirectoryMappingRevision != 0 || provenance.DirectoryRevisionFingerprint != ""
	switch provenance.Origin {
	case AuditRoleOriginBuiltin, AuditRoleOriginManual:
		return !directoryFieldsPresent
	case AuditRoleOriginLDAP, AuditRoleOriginOIDC:
		return identifierPattern.MatchString(provenance.DirectoryProviderID) &&
			identifierPattern.MatchString(provenance.DirectoryMappingID) &&
			provenance.DirectoryMappingRevision > 0 &&
			provenance.DirectoryMappingRevision <= AuditWorkflowPolicyRevisionMax &&
			validAuditDirectoryRevisionFingerprint(provenance.DirectoryRevisionFingerprint)
	default:
		return false
	}
}

func validAuditDirectoryRevisionFingerprint(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validScopedProjectAuditTarget(event AuditEvent, pattern *regexp.Regexp) bool {
	if !pattern.MatchString(event.TargetID) {
		return false
	}
	if event.ProjectID == nil {
		return event.Outcome == AuditOutcomeDenied &&
			(event.Reason == AuditReasonUnauthenticated || event.Reason == AuditReasonCrossOrigin)
	}
	if *event.ProjectID <= 0 {
		return false
	}
	if !strings.HasPrefix(event.TargetID, "project:") {
		return true
	}
	targetProjectID, err := strconv.Atoi(strings.TrimPrefix(event.TargetID, "project:"))
	return err == nil && targetProjectID == *event.ProjectID
}

func validCapabilityAuditTarget(targetID string) bool {
	switch CapabilityID(targetID) {
	case CapabilityLifecycleTest, CapabilityRuntimeSecrets, CapabilityTOTP, CapabilityLDAP, CapabilityProjectRoles:
		return true
	default:
		return false
	}
}

func validAuditReason(reason string) bool {
	switch reason {
	case AuditReasonUnauthenticated, AuditReasonCrossOrigin, AuditReasonProviderError, AuditReasonInvalidInput, AuditReasonOperationError,
		AuditReasonActiveAssignments, AuditReasonEnrollmentPending, AuditReasonEnrollmentActive,
		AuditReasonInvalidCode, AuditReasonReplay, AuditReasonThrottled,
		AuditReasonRecoveryUsed, AuditReasonReset, AuditReasonReadiness,
		AuditReasonLDAPInvalidCredentials, AuditReasonLDAPPolicy, AuditReasonOIDCPolicy,
		AuditReasonWorkflowPolicyAllowed, AuditReasonWorkflowPolicyDenied,
		AuditReasonWorkflowApprovalApproved, AuditReasonWorkflowApprovalRejected,
		AuditReasonWorkflowApprovalIneligible, AuditReasonWorkflowApprovalInitiatorSeparated,
		AuditReasonWorkflowApprovalRoleRevoked, AuditReasonWorkflowApprovalAlreadyResolved,
		AuditReasonWorkflowApprovalTimedOut, AuditReasonCrossProjectTemplateGrantActive,
		AuditReasonCrossProjectTemplateGrantDenied,
		AuditReasonExecutionPreflightPreviewed, AuditReasonExecutionPreflightStarted,
		AuditReasonExecutionPreflightDenied, AuditReasonExecutionPreflightStale,
		string(ExecutionReasonHiddenReference), string(ExecutionReasonPermissionDenied),
		string(ExecutionReasonCapabilityUnavailable), string(ExecutionReasonPolicyDenied),
		string(ExecutionReasonPlanLimitExceeded), string(ExecutionReasonNoCandidate),
		string(CapabilityReasonActive), string(CapabilityReasonProviderUnavailable),
		string(CapabilityReasonDisabledByAdmin), string(CapabilityReasonEntitlementExpired),
		string(CapabilityReasonReadOnly), string(CapabilityReasonInsufficientPermission),
		string(CapabilityReasonShadow), string(CapabilityReasonOptional),
		string(CapabilityReasonRequiredSelected), string(CapabilityReasonRequired):
		return true
	default:
		return false
	}
}

// SafeFields returns the only attributes permitted in logs and future traces.
func (e AuditEvent) SafeFields() map[string]any {
	if e.Validate() != nil {
		return map[string]any{
			"context": "enhanced_audit",
			"outcome": "dropped",
			"reason":  string(DroppedRecordInvalid),
		}
	}
	fields := map[string]any{
		"correlation_id": e.CorrelationID,
		"action":         e.Action,
		"target_type":    e.TargetType,
		"target_id":      e.TargetID,
		"outcome":        e.Outcome,
		"source":         e.Source,
		"reason":         e.Reason,
	}
	if e.EventID != "" {
		fields["event_id"] = e.EventID
	}
	if !e.OccurredAt.IsZero() {
		fields["occurred_at"] = e.OccurredAt
	}
	if e.SourceIP != "" {
		fields["source_ip"] = e.SourceIP
	}
	if e.UserAgent != "" {
		fields["user_agent"] = e.UserAgent
	}
	if e.ActorID != nil {
		fields["actor_id"] = *e.ActorID
	}
	if e.ProjectID != nil {
		fields["project_id"] = *e.ProjectID
	}
	if e.WorkflowPolicyRevision != 0 {
		fields["workflow_policy_revision"] = e.WorkflowPolicyRevision
	}
	if len(e.RoleProvenance) > 0 {
		fields["role_provenance"] = e.RoleProvenance
	}
	if e.ExecutionPreflightProvenance != nil {
		fields["execution_preflight_provenance"] = e.ExecutionPreflightProvenance
	}
	return fields
}

// EnsureDeliveryMetadata returns an event with a stable identifier and an
// immutable UTC occurrence time. Existing values are preserved so retries use
// the same logical event identity.
func (e AuditEvent) EnsureDeliveryMetadata(now time.Time) (AuditEvent, error) {
	if e.EventID == "" {
		id, err := NewAuditEventID()
		if err != nil {
			return AuditEvent{}, err
		}
		e.EventID = id
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = now.UTC()
	}
	if err := e.Validate(); err != nil {
		return AuditEvent{}, err
	}
	return e, nil
}

func NewAuditEventID() (string, error) {
	random := make([]byte, AuditEventIDBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate audit event ID: %w", err)
	}
	return hex.EncodeToString(random), nil
}

// AuditWebhookActor and AuditWebhookTarget deliberately expose only bounded,
// identifier-shaped values. There is no generic metadata or request payload.
type AuditWebhookActor struct {
	ID *int `json:"id,omitempty"`
}

type AuditWebhookTarget struct {
	Type      AuditTargetType `json:"type"`
	ID        string          `json:"id"`
	ProjectID *int            `json:"project_id,omitempty"`
}

// AuditWebhookEnvelope is the complete allow-listed wire schema. Adding a new
// field requires an explicit schema-version decision.
type AuditWebhookEnvelope struct {
	SchemaVersion string             `json:"schema_version"`
	EventID       string             `json:"event_id"`
	OccurredAt    time.Time          `json:"occurred_at"`
	Actor         AuditWebhookActor  `json:"actor"`
	Action        AuditAction        `json:"action"`
	Target        AuditWebhookTarget `json:"target"`
	Outcome       AuditOutcome       `json:"outcome"`
	Source        AuditSource        `json:"source"`
	SourceIP      string             `json:"source_ip,omitempty"`
	UserAgent     string             `json:"user_agent,omitempty"`
	CorrelationID string             `json:"correlation_id"`
	Reason        string             `json:"reason"`
}

func NewAuditWebhookEnvelope(event AuditEvent) (AuditWebhookEnvelope, error) {
	if err := event.Validate(); err != nil || event.EventID == "" || event.OccurredAt.IsZero() {
		return AuditWebhookEnvelope{}, fmt.Errorf("invalid audit webhook event")
	}
	return AuditWebhookEnvelope{
		SchemaVersion: AuditWebhookSchemaVersion,
		EventID:       event.EventID,
		OccurredAt:    event.OccurredAt,
		Actor:         AuditWebhookActor{ID: event.ActorID},
		Action:        event.Action,
		Target: AuditWebhookTarget{
			Type:      event.TargetType,
			ID:        event.TargetID,
			ProjectID: event.ProjectID,
		},
		Outcome:       event.Outcome,
		Source:        event.Source,
		SourceIP:      event.SourceIP,
		UserAgent:     event.UserAgent,
		CorrelationID: event.CorrelationID,
		Reason:        event.Reason,
	}, nil
}

// NormalizeAuditSourceIP extracts a literal peer IP without trusting forwarded
// headers. Invalid or unavailable addresses are omitted from the envelope.
func NormalizeAuditSourceIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// SanitizeAuditUserAgent keeps only printable characters and a fixed maximum
// size so this protocol field cannot become an unrestricted log channel.
func SanitizeAuditUserAgent(value string) string {
	value = strings.TrimSpace(value)
	var sanitized strings.Builder
	for _, r := range value {
		if unicode.IsPrint(r) && !unicode.IsControl(r) {
			if sanitized.Len()+utf8.RuneLen(r) > AuditUserAgentMaxLength {
				break
			}
			sanitized.WriteRune(r)
		}
	}
	return strings.TrimSpace(sanitized.String())
}

func validAuditUserAgent(value string) bool {
	return value == SanitizeAuditUserAgent(value) && len(value) <= AuditUserAgentMaxLength
}

type AuditServiceFacade interface {
	Record(context.Context, AuditEvent) error
}

// ExecutionPreflightAuditConfigurer is an optional route-wiring seam. It
// keeps Community controllers free of an audit dependency while allowing an
// Enhanced controller to record strictly value-free preview/start decisions.
type ExecutionPreflightAuditConfigurer interface {
	ConfigureExecutionPreflightAudit(AuditServiceFacade)
}

func validAuditAction(action AuditAction) bool {
	switch action {
	case AuditActionCapabilityResolve, AuditActionCapabilityRead, AuditActionCapabilityWrite,
		AuditActionCapabilityExecute, AuditActionCapabilityConfigure,
		AuditActionProjectRunnerList, AuditActionProjectRunnerRead,
		AuditActionProjectRunnerHealth, AuditActionProjectRunnerHistory,
		AuditActionProjectRunnerCreate, AuditActionProjectRunnerIssue,
		AuditActionProjectRunnerUpdate, AuditActionProjectRunnerActive,
		AuditActionProjectRunnerDelete, AuditActionProjectRunnerCache,
		AuditActionProjectRoleCreate, AuditActionProjectRoleUpdate, AuditActionProjectRoleDelete,
		AuditActionProjectRoleAssign, AuditActionProjectMemberAdd, AuditActionProjectMemberRemove,
		AuditActionGlobalRoleCreate, AuditActionGlobalRoleUpdate, AuditActionGlobalRoleDelete,
		AuditActionGlobalRoleAssign, AuditActionGlobalRoleUnassign, AuditActionGlobalRoleRead,
		AuditActionGlobalUserRead, AuditActionGlobalUserCreate, AuditActionGlobalUserUpdate,
		AuditActionGlobalUserDelete, AuditActionGlobalUserPassword,
		AuditActionGlobalSystemRead, AuditActionGlobalSystemWrite, AuditActionGlobalAuditRead,
		AuditActionTemplateRoleCreate, AuditActionTemplateRoleUpdate, AuditActionTemplateRoleDelete,
		AuditActionWebhookRead, AuditActionWebhookConfigure, AuditActionWebhookTest,
		AuditActionWebhookPause, AuditActionWebhookResume,
		AuditActionTOTPEnrollBegin, AuditActionTOTPEnrollConfirm,
		AuditActionTOTPRecoveryAck, AuditActionTOTPChallenge,
		AuditActionTOTPRecover, AuditActionTOTPReset, AuditActionTOTPRollout,
		AuditActionLDAPConfigure, AuditActionLDAPTest, AuditActionLDAPLogin, AuditActionLDAPLink,
		AuditActionLDAPGroupMappingRead, AuditActionLDAPGroupMappingWrite,
		AuditActionLDAPGroupMappingDelete, AuditActionLDAPGroupPreview,
		AuditActionLDAPGroupApply, AuditActionLDAPGroupReconcile:
		return true
	case AuditActionOIDCGroupMappingRead, AuditActionOIDCGroupMappingWrite,
		AuditActionOIDCGroupMappingDelete, AuditActionOIDCGroupPreview:
		return true
	case AuditActionGlobalCredentialCreate, AuditActionGlobalCredentialRead,
		AuditActionGrantedCredentialRead, AuditActionGlobalCredentialUpdate,
		AuditActionGlobalCredentialRotate, AuditActionGlobalCredentialGrant,
		AuditActionGlobalCredentialGrantRevoke, AuditActionGlobalCredentialGrantRestore,
		AuditActionGlobalCredentialGrantDelete,
		AuditActionGlobalCredentialDisable, AuditActionGlobalCredentialEnable,
		AuditActionGlobalCredentialDeleteAttempt:
		return true
	case AuditActionWorkflowList, AuditActionWorkflowRead, AuditActionWorkflowCreate,
		AuditActionWorkflowUpdate, AuditActionWorkflowDelete, AuditActionWorkflowStart,
		AuditActionWorkflowStop, AuditActionWorkflowRunRead, AuditActionWorkflowRunLogsRead,
		AuditActionWorkflowApprovalInbox, AuditActionWorkflowApprovalContribute:
		return true
	case AuditActionExecutionPreflightPreview, AuditActionExecutionPreflightStart:
		return true
	case AuditActionCrossProjectTemplateVersionPublish,
		AuditActionCrossProjectTemplateGrantCreate, AuditActionCrossProjectTemplateGrantUpdate,
		AuditActionCrossProjectTemplateGrantAccept, AuditActionCrossProjectTemplateGrantRevoke,
		AuditActionCrossProjectTemplateGrantDelete, AuditActionCrossProjectTemplateReferenceResolve:
		return true
	default:
		return false
	}
}

func validAuditOutcome(outcome AuditOutcome) bool {
	return outcome == AuditOutcomeAllowed || outcome == AuditOutcomeDenied || outcome == AuditOutcomeFailure
}

func validAuditSource(source AuditSource) bool {
	return source == AuditSourceAPI || source == AuditSourceWorker
}
