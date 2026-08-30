package pro_interfaces

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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

var (
	correlationPattern            = regexp.MustCompile(`^(?:[a-f0-9]{32}|internal)$`)
	eventIDPattern                = regexp.MustCompile(`^[a-f0-9]{32}$`)
	identifierPattern             = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)
	projectRunnerTargetPattern    = regexp.MustCompile(`^(?:project|runner):[1-9][0-9]*$`)
	projectRoleTargetPattern      = regexp.MustCompile(`^(?:project:[1-9][0-9]*|role:[a-z0-9][a-z0-9_.-]{0,49})$`)
	projectMemberTargetPattern    = regexp.MustCompile(`^(?:project:[1-9][0-9]*|member:[1-9][0-9]*)$`)
	globalRoleTargetPattern       = regexp.MustCompile(`^(?:roles|role:[a-z0-9][a-z0-9_.-]{0,49})$`)
	globalAssignmentTargetPattern = regexp.MustCompile(`^(?:user|assignment):[1-9][0-9]*$`)
	globalUserTargetPattern       = regexp.MustCompile(`^(?:users|user:[1-9][0-9]*)$`)
	globalSystemTargetPattern     = regexp.MustCompile(`^(?:subscription|options|cache)$`)
	templateRoleTargetPattern     = regexp.MustCompile(`^(?:template|template-role):[1-9][0-9]*$`)
	ldapGroupTargetPattern        = regexp.MustCompile(`^(?:(?:entryuuid|objectguid|nsuniqueid|ipauniqueid):[0-9a-f-]{36}|provider:[a-z][a-z0-9_-]{0,63})$`)
)

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
	if !validAuditAction(e.Action) || !validAuditTarget(e) ||
		!validAuditOutcome(e.Outcome) || !validAuditSource(e.Source) || !validAuditReason(e.Reason) {
		return fmt.Errorf("unsupported audit context")
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
	default:
		return false
	}
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
		AuditReasonLDAPInvalidCredentials, AuditReasonLDAPPolicy,
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
