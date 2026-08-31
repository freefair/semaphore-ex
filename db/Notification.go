package db

import (
	"errors"
	"time"
)

var (
	ErrNotificationDestinationRevisionConflict = errors.New("notification destination revision conflict")
	ErrNotificationRuleRevisionConflict        = errors.New("notification rule revision conflict")
	ErrNotificationDeliveryNotClaimed          = errors.New("notification delivery is not claimed")
)

type NotificationDeliveryStatus string

const (
	NotificationDeliveryPending   NotificationDeliveryStatus = "pending"
	NotificationDeliveryRunning   NotificationDeliveryStatus = "delivering"
	NotificationDeliveryRetrying  NotificationDeliveryStatus = "retrying"
	NotificationDeliverySucceeded NotificationDeliveryStatus = "succeeded"
	NotificationDeliveryFailed    NotificationDeliveryStatus = "failed"
)

type NotificationRoutingOutcome string

const (
	NotificationRoutingFiltered NotificationRoutingOutcome = "filtered"
	NotificationRoutingRouted   NotificationRoutingOutcome = "routed"
)

type NotificationDeliveryReason string

const (
	NotificationDeliveryReasonNone                  NotificationDeliveryReason = ""
	NotificationDeliveryReasonConfiguration         NotificationDeliveryReason = "configuration_error"
	NotificationDeliveryReasonRateLimited           NotificationDeliveryReason = "rate_limited"
	NotificationDeliveryReasonTransport             NotificationDeliveryReason = "transport_error"
	NotificationDeliveryReasonAttemptsExhausted     NotificationDeliveryReason = "attempts_exhausted"
	NotificationDeliveryReasonManualRetry           NotificationDeliveryReason = "manual_retry"
	NotificationDeliveryReasonDestinationPaused     NotificationDeliveryReason = "destination_paused"
	NotificationDeliveryReasonDestinationMissing    NotificationDeliveryReason = "destination_missing"
	NotificationDeliveryReasonDestinationDisabled   NotificationDeliveryReason = "destination_disabled"
	NotificationDeliveryReasonDestinationChanged    NotificationDeliveryReason = "destination_revision_changed"
	NotificationDeliveryReasonCredentialUnavailable NotificationDeliveryReason = "credential_unavailable"
	NotificationDeliveryReasonProviderUnavailable   NotificationDeliveryReason = "provider_unavailable"
	NotificationDeliveryReasonPermanent             NotificationDeliveryReason = "permanent_failure"
)

// NotificationDestination stores only opaque provider selection and encrypted
// credential material. Provider configuration and transports are intentionally
// absent until their owning provider slices.
type NotificationDestination struct {
	ID                    int       `db:"id" json:"id"`
	ProjectID             *int      `db:"project_id" json:"project_id,omitempty"`
	Name                  string    `db:"name" json:"name"`
	Provider              string    `db:"provider" json:"provider"`
	Environment           string    `db:"environment" json:"environment"`
	Region                string    `db:"region" json:"region"`
	EncryptedCredential   string    `db:"encrypted_credential" json:"-"`
	CredentialConfigured  bool      `db:"credential_configured" json:"credential_configured"`
	Enabled               bool      `db:"enabled" json:"enabled"`
	Paused                bool      `db:"paused" json:"paused"`
	Revision              int       `db:"revision" json:"revision"`
	ConfigurationRevision int       `db:"configuration_revision" json:"-"`
	Created               time.Time `db:"created" json:"created"`
	Updated               time.Time `db:"updated" json:"updated"`
}

type NotificationRule struct {
	ID               int       `db:"id" json:"id"`
	ProjectID        *int      `db:"project_id" json:"project_id,omitempty"`
	DestinationID    int       `db:"destination_id" json:"destination_id"`
	SourceKinds      string    `db:"source_kinds" json:"source_kinds"`
	LifecycleActions string    `db:"lifecycle_actions" json:"lifecycle_actions"`
	MinimumSeverity  string    `db:"minimum_severity" json:"minimum_severity"`
	Enabled          bool      `db:"enabled" json:"enabled"`
	Revision         int       `db:"revision" json:"revision"`
	Created          time.Time `db:"created" json:"created"`
	Updated          time.Time `db:"updated" json:"updated"`
}

// NotificationEvent is the durable, provider-neutral source event. Details is
// the serialized typed allow-list from pro_interfaces, never arbitrary input.
type NotificationEvent struct {
	ID              int                        `db:"id" json:"id"`
	SchemaVersion   string                     `db:"schema_version" json:"schema_version"`
	EventID         string                     `db:"event_id" json:"event_id"`
	SourceEventKey  string                     `db:"source_event_key" json:"source_event_key"`
	SourceRevision  int                        `db:"source_revision" json:"source_revision"`
	ProjectID       *int                       `db:"project_id" json:"project_id,omitempty"`
	SourceKind      string                     `db:"source_kind" json:"source_kind"`
	SourceID        string                     `db:"source_id" json:"source_id"`
	LifecycleID     string                     `db:"lifecycle_id" json:"lifecycle_id"`
	Severity        string                     `db:"severity" json:"severity"`
	LifecycleAction string                     `db:"lifecycle_action" json:"lifecycle_action"`
	IncidentKey     string                     `db:"incident_key" json:"incident_key"`
	Details         string                     `db:"details" json:"-"`
	RoutingOutcome  NotificationRoutingOutcome `db:"routing_outcome" json:"routing_outcome"`
	OccurredAt      time.Time                  `db:"occurred_at" json:"occurred_at"`
	Created         time.Time                  `db:"created" json:"created"`
}

type NotificationDelivery struct {
	ID                               int                        `db:"id" json:"id"`
	NotificationEventID              int                        `db:"notification_event_id" json:"-"`
	EventID                          string                     `db:"event_id" json:"event_id"`
	DestinationID                    int                        `db:"destination_id" json:"destination_id"`
	DestinationRevision              int                        `db:"destination_revision" json:"destination_revision"`
	DestinationConfigurationRevision int                        `db:"destination_configuration_revision" json:"-"`
	DestinationName                  string                     `db:"destination_name" json:"destination_name"`
	DestinationProvider              string                     `db:"destination_provider" json:"destination_provider"`
	DestinationEnvironment           string                     `db:"destination_environment" json:"destination_environment"`
	DestinationRegion                string                     `db:"destination_region" json:"destination_region"`
	IncidentKey                      string                     `db:"incident_key" json:"incident_key"`
	IdempotencyKey                   string                     `db:"idempotency_key" json:"idempotency_key"`
	Status                           NotificationDeliveryStatus `db:"status" json:"status"`
	Attempts                         int                        `db:"attempts" json:"attempts"`
	NextAttempt                      time.Time                  `db:"next_attempt" json:"next_attempt"`
	LeaseToken                       string                     `db:"lease_token" json:"-"`
	LeaseUntil                       *time.Time                 `db:"lease_until" json:"-"`
	LastReason                       NotificationDeliveryReason `db:"last_reason" json:"last_reason,omitempty"`
	Created                          time.Time                  `db:"created" json:"created"`
	Updated                          time.Time                  `db:"updated" json:"updated"`
	DeliveredAt                      *time.Time                 `db:"delivered_at" json:"delivered_at,omitempty"`
	SourceKind                       string                     `db:"source_kind" json:"source_kind"`
	SourceID                         string                     `db:"source_id" json:"source_id"`
	LifecycleAction                  string                     `db:"lifecycle_action" json:"lifecycle_action"`
	Severity                         string                     `db:"severity" json:"severity"`
	OccurredAt                       time.Time                  `db:"occurred_at" json:"occurred_at"`
}

// NotificationEventHistory is the intentionally narrow persisted projection
// used for operator-visible event history. It cannot contain Details,
// credentials, provider payloads, or worker lease data.
type NotificationEventHistory struct {
	EventID         string                     `db:"event_id" json:"event_id"`
	SourceKind      string                     `db:"source_kind" json:"source_kind"`
	SourceID        string                     `db:"source_id" json:"source_id"`
	LifecycleID     string                     `db:"lifecycle_id" json:"lifecycle_id"`
	Severity        string                     `db:"severity" json:"severity"`
	LifecycleAction string                     `db:"lifecycle_action" json:"lifecycle_action"`
	IncidentKey     string                     `db:"incident_key" json:"incident_key"`
	RoutingOutcome  NotificationRoutingOutcome `db:"routing_outcome" json:"routing_outcome"`
	OccurredAt      time.Time                  `db:"occurred_at" json:"occurred_at"`
	Created         time.Time                  `db:"created" json:"created"`
}

type NotificationRepository interface {
	CreateNotificationDestination(NotificationDestination) (NotificationDestination, error)
	GetNotificationDestination(*int, int) (NotificationDestination, error)
	GetNotificationDestinations(*int, RetrieveQueryParams) ([]NotificationDestination, error)
	UpdateNotificationDestination(NotificationDestination, int) (NotificationDestination, error)
	SetNotificationDestinationPaused(NotificationDestination, int) (NotificationDestination, error)
	DeleteNotificationDestination(*int, int, int) error
	CreateNotificationRule(NotificationRule) (NotificationRule, error)
	GetNotificationRule(*int, int) (NotificationRule, error)
	GetNotificationRules(*int, RetrieveQueryParams) ([]NotificationRule, error)
	UpdateNotificationRule(NotificationRule, int) (NotificationRule, error)
	DeleteNotificationRule(*int, int, int) error
	CreateNotificationEventWithRouting(NotificationEvent, []NotificationDelivery) (NotificationEvent, error)
	ClaimNotificationDeliveries(time.Time, time.Time, int) ([]NotificationDelivery, error)
	MarkNotificationDeliverySucceeded(int, string, time.Time) error
	MarkNotificationDeliveryRetrying(int, string, NotificationDeliveryReason, time.Time, time.Time) error
	MarkNotificationDeliveryFailed(int, string, NotificationDeliveryReason, time.Time) error
	ReleaseNotificationDelivery(int, string, NotificationDeliveryReason, time.Time, time.Time) error
	ResumePausedNotificationDeliveries(int, time.Time) error
	RetryNotificationDelivery(int, int, int, time.Time) error
	GetNotificationDelivery(*int, int) (NotificationDelivery, error)
	GetNotificationDeliveries(*int, RetrieveQueryParams) ([]NotificationDelivery, error)
	GetNotificationEventHistory(*int, RetrieveQueryParams) ([]NotificationEventHistory, error)
	GetNotificationDispatchContext(int) (NotificationDelivery, NotificationEvent, NotificationDestination, error)
}
