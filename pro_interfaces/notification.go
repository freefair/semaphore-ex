package pro_interfaces

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

const NotificationSchemaVersion = "semaphore.notification.v1"

var (
	ErrNotificationUnavailable              = errors.New("notification governance is unavailable")
	ErrNotificationInvalidInput             = errors.New("invalid notification input")
	ErrNotificationEncryptionRequired       = errors.New("notification credential encryption is required")
	ErrNotificationDestinationMissing       = errors.New("notification destination not found")
	ErrNotificationDestinationNotConfigured = errors.New("notification destination is not configured")
	ErrNotificationDestinationDisabled      = errors.New("notification destination is disabled")
	ErrNotificationDestinationPaused        = errors.New("notification destination is paused")
	ErrNotificationRuleMissing              = errors.New("notification rule not found")
	ErrNotificationRevisionConflict         = errors.New("notification revision conflict")
	ErrNotificationRetryUnavailable         = errors.New("notification delivery cannot be retried")
)

type NotificationScope string

const (
	NotificationScopeGlobal  NotificationScope = "global"
	NotificationScopeProject NotificationScope = "project"
)

type NotificationSourceKind string

const (
	NotificationSourceTask     NotificationSourceKind = "task"
	NotificationSourceWorkflow NotificationSourceKind = "workflow"
	NotificationSourceApproval NotificationSourceKind = "approval"
	NotificationSourceSystem   NotificationSourceKind = "system"
)

type NotificationSeverity string

const (
	NotificationSeverityInfo     NotificationSeverity = "info"
	NotificationSeverityWarning  NotificationSeverity = "warning"
	NotificationSeverityError    NotificationSeverity = "error"
	NotificationSeverityCritical NotificationSeverity = "critical"
)

type NotificationLifecycleAction string

const (
	NotificationLifecycleTrigger NotificationLifecycleAction = "trigger"
	NotificationLifecycleUpdate  NotificationLifecycleAction = "update"
	NotificationLifecycleResolve NotificationLifecycleAction = "resolve"
)

// NotificationProviderRegion selects a provider's documented regional service
// endpoint. It is intentionally an allow-list value rather than a URL.
type NotificationProviderRegion string

const (
	NotificationProviderRegionUS NotificationProviderRegion = "us"
	NotificationProviderRegionEU NotificationProviderRegion = "eu"
)

// NotificationOpsgeniePriority is deliberately limited to Alert API v2's
// documented priority values. An empty value means the adapter maps severity.
type NotificationOpsgeniePriority string

const (
	NotificationOpsgeniePriorityP1 NotificationOpsgeniePriority = "P1"
	NotificationOpsgeniePriorityP2 NotificationOpsgeniePriority = "P2"
	NotificationOpsgeniePriorityP3 NotificationOpsgeniePriority = "P3"
	NotificationOpsgeniePriorityP4 NotificationOpsgeniePriority = "P4"
	NotificationOpsgeniePriorityP5 NotificationOpsgeniePriority = "P5"
)

type NotificationOpsgenieResponderType string

const (
	NotificationOpsgenieResponderTeam       NotificationOpsgenieResponderType = "team"
	NotificationOpsgenieResponderUser       NotificationOpsgenieResponderType = "user"
	NotificationOpsgenieResponderEscalation NotificationOpsgenieResponderType = "escalation"
	NotificationOpsgenieResponderSchedule   NotificationOpsgenieResponderType = "schedule"
)

// NotificationOpsgenieResponder has exactly one provider-recognized identity.
// It is a typed configuration field, not an arbitrary provider payload.
type NotificationOpsgenieResponder struct {
	Type     NotificationOpsgenieResponderType `json:"type"`
	ID       string                            `json:"id,omitempty"`
	Name     string                            `json:"name,omitempty"`
	Username string                            `json:"username,omitempty"`
}

type NotificationOpsgenieConfiguration struct {
	Priority   NotificationOpsgeniePriority    `json:"priority,omitempty"`
	Responders []NotificationOpsgenieResponder `json:"responders,omitempty"`
}

// NotificationSource is the immutable source identity for one lifecycle.
// Source IDs are intentionally identifier-shaped and never include names,
// message text, credentials, request content, or provider payloads.
type NotificationSource struct {
	Kind NotificationSourceKind `json:"kind"`
	ID   string                 `json:"id"`
}

// NotificationDetails is the complete version-one allow-list. Message is a
// defensive tripwire: a non-empty value is rejected rather than becoming a
// future generic content channel by accident.
type NotificationDetails struct {
	TaskID        *int   `json:"task_id,omitempty"`
	TemplateID    *int   `json:"template_id,omitempty"`
	WorkflowID    *int   `json:"workflow_id,omitempty"`
	WorkflowRunID *int   `json:"workflow_run_id,omitempty"`
	ApprovalID    *int   `json:"approval_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Decision      string `json:"decision,omitempty"`
	Message       string `json:"-"`
}

// NotificationEvent carries the provider-neutral event identity and the
// bounded fields used for routing. Provider adapters receive no arbitrary
// metadata from this contract.
type NotificationEvent struct {
	SchemaVersion   string                      `json:"schema_version"`
	EventID         string                      `json:"event_id"`
	SourceEventKey  string                      `json:"source_event_key"`
	SourceRevision  int                         `json:"source_revision"`
	OccurredAt      time.Time                   `json:"occurred_at"`
	Scope           NotificationScope           `json:"scope"`
	ProjectID       *int                        `json:"project_id,omitempty"`
	Source          NotificationSource          `json:"source"`
	LifecycleID     string                      `json:"lifecycle_id"`
	Severity        NotificationSeverity        `json:"severity"`
	LifecycleAction NotificationLifecycleAction `json:"lifecycle_action"`
	IncidentKey     string                      `json:"incident_key"`
	Details         NotificationDetails         `json:"details"`
}

// NotificationRoutingRule is the provider-neutral filtering projection used
// while a source mutation is committed. The persisted rule additionally owns
// scope, destination, revision, and lifecycle fields in db.NotificationRule.
type NotificationRoutingRule struct {
	SourceKinds     []NotificationSourceKind      `json:"source_kinds"`
	Actions         []NotificationLifecycleAction `json:"actions"`
	MinimumSeverity NotificationSeverity          `json:"minimum_severity"`
	Enabled         bool                          `json:"enabled"`
}

// NotificationDestinationInput carries write-only provider credential material.
// Credential is never represented in a DTO and is retained on update when nil.
type NotificationDestinationInput struct {
	Name        string                             `json:"name"`
	Provider    string                             `json:"provider"`
	Environment string                             `json:"environment"`
	Region      NotificationProviderRegion         `json:"region"`
	Credential  *string                            `json:"credential,omitempty"`
	Opsgenie    *NotificationOpsgenieConfiguration `json:"opsgenie,omitempty"`
	Enabled     bool                               `json:"enabled"`
}

type NotificationDestinationDTO struct {
	ID                   int                                `json:"id"`
	ProjectID            *int                               `json:"project_id,omitempty"`
	Name                 string                             `json:"name"`
	Provider             string                             `json:"provider"`
	Environment          string                             `json:"environment"`
	Region               NotificationProviderRegion         `json:"region"`
	CredentialConfigured bool                               `json:"credential_configured"`
	Opsgenie             *NotificationOpsgenieConfiguration `json:"opsgenie,omitempty"`
	Enabled              bool                               `json:"enabled"`
	Paused               bool                               `json:"paused"`
	Revision             int                                `json:"revision"`
	CreatedAt            time.Time                          `json:"created_at"`
	UpdatedAt            time.Time                          `json:"updated_at"`
}

type NotificationRuleInput struct {
	DestinationID    int                           `json:"destination_id"`
	SourceKinds      []NotificationSourceKind      `json:"source_kinds"`
	LifecycleActions []NotificationLifecycleAction `json:"lifecycle_actions"`
	MinimumSeverity  NotificationSeverity          `json:"minimum_severity"`
	Enabled          bool                          `json:"enabled"`
}

type NotificationRuleDTO struct {
	ID               int                           `json:"id"`
	ProjectID        *int                          `json:"project_id,omitempty"`
	DestinationID    int                           `json:"destination_id"`
	SourceKinds      []NotificationSourceKind      `json:"source_kinds"`
	LifecycleActions []NotificationLifecycleAction `json:"lifecycle_actions"`
	MinimumSeverity  NotificationSeverity          `json:"minimum_severity"`
	Enabled          bool                          `json:"enabled"`
	Revision         int                           `json:"revision"`
	CreatedAt        time.Time                     `json:"created_at"`
	UpdatedAt        time.Time                     `json:"updated_at"`
}

type NotificationRoutingPreviewDTO struct {
	DestinationID int    `json:"destination_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Environment   string `json:"environment"`
}

type NotificationDeliveryDTO struct {
	ID                     int                           `json:"id"`
	EventID                string                        `json:"event_id"`
	DestinationID          int                           `json:"destination_id"`
	DestinationRevision    int                           `json:"destination_revision"`
	DestinationName        string                        `json:"destination_name"`
	DestinationProvider    string                        `json:"destination_provider"`
	DestinationEnvironment string                        `json:"destination_environment"`
	DestinationRegion      NotificationProviderRegion    `json:"destination_region"`
	IncidentKey            string                        `json:"incident_key"`
	IdempotencyKey         string                        `json:"idempotency_key"`
	ProviderRequestID      string                        `json:"provider_request_id,omitempty"`
	Status                 db.NotificationDeliveryStatus `json:"status"`
	Attempts               int                           `json:"attempts"`
	NextAttempt            time.Time                     `json:"next_attempt"`
	LastReason             db.NotificationDeliveryReason `json:"last_reason,omitempty"`
	CreatedAt              time.Time                     `json:"created_at"`
	UpdatedAt              time.Time                     `json:"updated_at"`
	DeliveredAt            *time.Time                    `json:"delivered_at,omitempty"`
	SourceKind             NotificationSourceKind        `json:"source_kind"`
	SourceID               string                        `json:"source_id"`
	LifecycleAction        NotificationLifecycleAction   `json:"lifecycle_action"`
	Severity               NotificationSeverity          `json:"severity"`
	OccurredAt             time.Time                     `json:"occurred_at"`
}

// NotificationEventHistoryDTO is the complete browser-visible event
// projection. It deliberately excludes Details and every credential/provider
// response channel while making filtered routing outcomes inspectable.
type NotificationEventHistoryDTO struct {
	EventID         string                        `json:"event_id"`
	SourceKind      NotificationSourceKind        `json:"source_kind"`
	SourceID        string                        `json:"source_id"`
	LifecycleID     string                        `json:"lifecycle_id"`
	Severity        NotificationSeverity          `json:"severity"`
	LifecycleAction NotificationLifecycleAction   `json:"lifecycle_action"`
	IncidentKey     string                        `json:"incident_key"`
	RoutingOutcome  db.NotificationRoutingOutcome `json:"routing_outcome"`
	OccurredAt      time.Time                     `json:"occurred_at"`
	CreatedAt       time.Time                     `json:"created_at"`
}

// NotificationDispatchRequest is the intentionally small adapter boundary for
// provider slices. It contains the typed event and the credential only while a
// delivery is in flight; implementations must never persist or log either
// credential material or provider response content.
type NotificationDispatchRequest struct {
	Event             NotificationEvent
	DestinationID     int
	Provider          string
	Environment       string
	Region            NotificationProviderRegion
	IncidentKey       string
	IdempotencyKey    string
	Credential        []byte
	Opsgenie          *NotificationOpsgenieConfiguration
	ProviderRequestID string
}

// NotificationDispatchOutcome deliberately carries no HTTP status, headers,
// body, or arbitrary error text. Provider slices map their transport details
// to this bounded result before returning to the durable worker.
type NotificationDispatchOutcome string

const (
	NotificationDispatchSucceeded   NotificationDispatchOutcome = "succeeded"
	NotificationDispatchTransient   NotificationDispatchOutcome = "transient_failure"
	NotificationDispatchPermanent   NotificationDispatchOutcome = "permanent_failure"
	NotificationDispatchRateLimited NotificationDispatchOutcome = "rate_limited"
	NotificationDispatchPending     NotificationDispatchOutcome = "pending"
)

type NotificationDispatchResult struct {
	Outcome    NotificationDispatchOutcome
	RetryAfter time.Duration
	RequestID  string
}

// ValidNotificationProviderRequestID bounds the safe asynchronous operation
// identity shared by provider adapters and the durable dispatcher.
func ValidNotificationProviderRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

// NotificationProviderAdapter is registered by later provider slices. Slice
// 056 itself registers no network transport, which keeps governance usable and
// testable without adding an arbitrary outbound HTTP surface.
type NotificationProviderAdapter interface {
	ProviderName() string
	Dispatch(context.Context, NotificationDispatchRequest) NotificationDispatchResult
}

// NotificationDeliveryDispatcher owns the outbox worker lifecycle. The CLI
// starts the one dispatcher built with the active service graph and closes it
// during shutdown; routers only receive the already-created governance facade.
type NotificationDeliveryDispatcher interface {
	RegisterAdapter(NotificationProviderAdapter) error
	Start()
	Close() error
}

// NotificationGovernanceServiceFacade is the API-facing, provider-neutral
// governance boundary. Permission checks live at the caller boundary; every
// method still takes an explicit scope so data cannot cross tenants.
type NotificationGovernanceServiceFacade interface {
	CreateDestination(context.Context, *int, NotificationDestinationInput) (NotificationDestinationDTO, error)
	GetDestination(context.Context, *int, int) (NotificationDestinationDTO, error)
	ListDestinations(context.Context, *int, db.RetrieveQueryParams) ([]NotificationDestinationDTO, error)
	UpdateDestination(context.Context, *int, int, int, NotificationDestinationInput) (NotificationDestinationDTO, error)
	DeleteDestination(context.Context, *int, int, int) error
	SetDestinationPaused(context.Context, *int, int, int, bool) (NotificationDestinationDTO, error)
	CreateRule(context.Context, *int, NotificationRuleInput) (NotificationRuleDTO, error)
	ListRules(context.Context, *int, db.RetrieveQueryParams) ([]NotificationRuleDTO, error)
	UpdateRule(context.Context, *int, int, int, NotificationRuleInput) (NotificationRuleDTO, error)
	DeleteRule(context.Context, *int, int, int) error
	PreviewRouting(context.Context, *int, NotificationEvent) ([]NotificationRoutingPreviewDTO, error)
	EnqueueTestDelivery(context.Context, *int, int) (NotificationDeliveryDTO, error)
	DeliveryHistory(context.Context, *int, db.RetrieveQueryParams) ([]NotificationDeliveryDTO, error)
	EventHistory(context.Context, *int, db.RetrieveQueryParams) ([]NotificationEventHistoryDTO, error)
	RetryDelivery(context.Context, *int, int) (NotificationDeliveryDTO, error)
}

var (
	notificationEventIDPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	notificationIncidentKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	notificationSourceIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z0-9][a-z0-9_.:-]{0,127}$`)
	notificationDetailPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,63}$`)
)

func (event NotificationEvent) Validate() error {
	if event.SchemaVersion != "" && event.SchemaVersion != NotificationSchemaVersion {
		return fmt.Errorf("unsupported notification schema version")
	}
	if event.EventID != "" && !notificationEventIDPattern.MatchString(event.EventID) {
		return fmt.Errorf("invalid notification event ID")
	}
	if event.SourceEventKey != "" && !notificationIncidentKeyPattern.MatchString(event.SourceEventKey) {
		return fmt.Errorf("invalid notification source event key")
	}
	if event.SourceRevision < 1 {
		return fmt.Errorf("notification source revision must be positive")
	}
	if !event.OccurredAt.IsZero() && event.OccurredAt.Location() != time.UTC {
		return fmt.Errorf("notification occurrence time must be UTC")
	}
	if err := validateNotificationScope(event.Scope, event.ProjectID); err != nil {
		return err
	}
	if !validNotificationSource(event.Source) || !validNotificationLifecycle(event.Source.Kind, event.LifecycleID) || !validNotificationSeverity(event.Severity) ||
		!validNotificationLifecycleAction(event.LifecycleAction) {
		return fmt.Errorf("unsupported notification event")
	}
	if event.IncidentKey != "" && !notificationIncidentKeyPattern.MatchString(event.IncidentKey) {
		return fmt.Errorf("invalid notification incident key")
	}
	if !validNotificationDetails(event.Source, event.LifecycleID, event.Details) {
		return fmt.Errorf("invalid notification details")
	}
	return nil
}

// EnsureIdentity assigns immutable delivery identity for a newly created event.
// Re-applying it to a retry preserves the original event, incident, and time.
func (event NotificationEvent) EnsureIdentity(now time.Time) (NotificationEvent, error) {
	if event.SchemaVersion == "" {
		event.SchemaVersion = NotificationSchemaVersion
	}
	if event.EventID == "" {
		id, err := newNotificationEventID()
		if err != nil {
			return NotificationEvent{}, err
		}
		event.EventID = id
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = now.UTC()
	}
	if event.IncidentKey == "" {
		incident, err := NotificationIncidentKey(event.Scope, event.ProjectID, event.LifecycleID)
		if err != nil {
			return NotificationEvent{}, err
		}
		event.IncidentKey = incident
	}
	if event.SourceEventKey == "" {
		key, err := NotificationSourceEventKey(event.Scope, event.ProjectID, event.Source, event.SourceRevision)
		if err != nil {
			return NotificationEvent{}, err
		}
		event.SourceEventKey = key
	}
	if err := event.Validate(); err != nil {
		return NotificationEvent{}, err
	}
	return event, nil
}

// NotificationSourceEventKey deterministically identifies one exact persisted
// source transition. Source.ID remains the lifecycle identity for incidents;
// the monotonic source revision separates trigger, update, and resolve events.
func NotificationSourceEventKey(scope NotificationScope, projectID *int, source NotificationSource, revision int) (string, error) {
	if revision < 1 {
		return "", fmt.Errorf("invalid notification source revision")
	}
	if err := validateNotificationScope(scope, projectID); err != nil {
		return "", err
	}
	if !validNotificationSource(source) {
		return "", fmt.Errorf("invalid notification source")
	}
	project := ""
	if projectID != nil {
		project = strconv.Itoa(*projectID)
	}
	sum := sha256.Sum256([]byte(string(scope) + "|" + project + "|" + string(source.Kind) + "|" + source.ID + "|" + strconv.Itoa(revision)))
	return hex.EncodeToString(sum[:]), nil
}

// NotificationIncidentKey is deterministic for the immutable source lifecycle
// and deliberately independent from action and severity so trigger/update/
// resolve are deduplicated as one logical incident.
func NotificationIncidentKey(scope NotificationScope, projectID *int, lifecycleID string) (string, error) {
	if err := validateNotificationScope(scope, projectID); err != nil || !notificationSourceIDPattern.MatchString(lifecycleID) {
		return "", fmt.Errorf("invalid notification incident source")
	}
	project := ""
	if projectID != nil {
		project = strconv.Itoa(*projectID)
	}
	sum := sha256.Sum256([]byte(string(scope) + "|" + project + "|" + lifecycleID))
	return hex.EncodeToString(sum[:]), nil
}

func (rule NotificationRoutingRule) Matches(event NotificationEvent) bool {
	if !rule.Enabled || event.Validate() != nil || !validNotificationSeverity(rule.MinimumSeverity) ||
		!slices.Contains(rule.SourceKinds, event.Source.Kind) || !slices.Contains(rule.Actions, event.LifecycleAction) {
		return false
	}
	return notificationSeverityRank(event.Severity) >= notificationSeverityRank(rule.MinimumSeverity)
}

func validateNotificationScope(scope NotificationScope, projectID *int) error {
	switch scope {
	case NotificationScopeGlobal:
		if projectID != nil {
			return fmt.Errorf("global notification cannot have a project")
		}
	case NotificationScopeProject:
		if projectID == nil || *projectID <= 0 {
			return fmt.Errorf("project notification requires a project")
		}
	default:
		return fmt.Errorf("invalid notification scope")
	}
	return nil
}

func validNotificationSource(source NotificationSource) bool {
	if !notificationSourceIDPattern.MatchString(source.ID) {
		return false
	}
	switch source.Kind {
	case NotificationSourceTask:
		return hasNotificationSourcePrefix(source.ID, "task:")
	case NotificationSourceWorkflow:
		return hasNotificationSourcePrefix(source.ID, "workflow_run:")
	case NotificationSourceApproval:
		return hasNotificationSourcePrefix(source.ID, "approval:")
	case NotificationSourceSystem:
		return hasNotificationSourcePrefix(source.ID, "system:")
	default:
		return false
	}
}

func validNotificationLifecycle(kind NotificationSourceKind, lifecycleID string) bool {
	if !notificationSourceIDPattern.MatchString(lifecycleID) {
		return false
	}
	switch kind {
	case NotificationSourceTask:
		return hasNotificationSourcePrefix(lifecycleID, "template:")
	case NotificationSourceWorkflow:
		return hasNotificationSourcePrefix(lifecycleID, "workflow:")
	case NotificationSourceApproval:
		return hasNotificationSourcePrefix(lifecycleID, "approval:")
	case NotificationSourceSystem:
		return hasNotificationSourcePrefix(lifecycleID, "system:")
	default:
		return false
	}
}

func hasNotificationSourcePrefix(value, prefix string) bool {
	return len(value) > len(prefix) && value[:len(prefix)] == prefix
}

func validNotificationSeverity(severity NotificationSeverity) bool {
	return severity == NotificationSeverityInfo || severity == NotificationSeverityWarning || severity == NotificationSeverityError || severity == NotificationSeverityCritical
}

func validNotificationLifecycleAction(action NotificationLifecycleAction) bool {
	return action == NotificationLifecycleTrigger || action == NotificationLifecycleUpdate || action == NotificationLifecycleResolve
}

func validNotificationDetails(source NotificationSource, lifecycleID string, details NotificationDetails) bool {
	if details.Message != "" || (details.Status != "" && !notificationDetailPattern.MatchString(details.Status)) ||
		(details.Decision != "" && !notificationDetailPattern.MatchString(details.Decision)) {
		return false
	}
	switch source.Kind {
	case NotificationSourceTask:
		return details.TaskID != nil && details.TemplateID != nil && *details.TaskID > 0 && *details.TemplateID > 0 &&
			matchesNumericIdentifier(source.ID, "task:", *details.TaskID) && matchesNumericIdentifier(lifecycleID, "template:", *details.TemplateID) &&
			details.WorkflowID == nil && details.WorkflowRunID == nil && details.ApprovalID == nil
	case NotificationSourceWorkflow:
		return details.WorkflowID != nil && details.WorkflowRunID != nil && *details.WorkflowID > 0 && *details.WorkflowRunID > 0 &&
			matchesNumericIdentifier(source.ID, "workflow_run:", *details.WorkflowRunID) && matchesNumericIdentifier(lifecycleID, "workflow:", *details.WorkflowID) &&
			details.TaskID == nil && details.TemplateID == nil && details.ApprovalID == nil
	case NotificationSourceApproval:
		return details.WorkflowID != nil && details.WorkflowRunID != nil && details.ApprovalID != nil && *details.WorkflowID > 0 && *details.WorkflowRunID > 0 && *details.ApprovalID > 0 &&
			matchesNumericIdentifier(source.ID, "approval:", *details.ApprovalID) && matchesNumericIdentifier(lifecycleID, "approval:", *details.ApprovalID) &&
			details.TaskID == nil && details.TemplateID == nil
	case NotificationSourceSystem:
		return details.TaskID == nil && details.TemplateID == nil && details.WorkflowID == nil && details.WorkflowRunID == nil && details.ApprovalID == nil
	default:
		return false
	}
}

func matchesNumericIdentifier(value, prefix string, expected int) bool {
	parsed, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	return err == nil && parsed == expected
}

func notificationSeverityRank(severity NotificationSeverity) int {
	switch severity {
	case NotificationSeverityInfo:
		return 0
	case NotificationSeverityWarning:
		return 1
	case NotificationSeverityError:
		return 2
	case NotificationSeverityCritical:
		return 3
	default:
		return -1
	}
}

func newNotificationEventID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate notification event ID: %w", err)
	}
	return hex.EncodeToString(random), nil
}
