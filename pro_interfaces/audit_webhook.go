package pro_interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

var ErrAuditWebhookUnavailable = errors.New("audit webhook export is unavailable")
var ErrAuditWebhookInvalidInput = errors.New("invalid audit webhook input")
var ErrAuditWebhookNotConfigured = errors.New("audit webhook is not configured")
var ErrAuditWebhookSigningNotConfigured = errors.New("audit webhook signing is not configured")
var ErrAuditWebhookSigningStateConflict = errors.New("audit webhook signing state changed")
var ErrAuditWebhookInvalidSigningKey = errors.New("invalid audit webhook signing key")

type AuditWebhookConfigInput struct {
	Endpoint   string  `json:"endpoint"`
	Credential *string `json:"credential,omitempty"`
}

type AuditWebhookConfigDTO struct {
	Endpoint             string    `json:"endpoint"`
	CredentialConfigured bool      `json:"credential_configured"`
	Paused               bool      `json:"paused"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
	AuditWebhookSigningStatusDTO
}

// AuditWebhookSigningStatusDTO contains non-secret signing state. Secret
// material is only returned in AuditWebhookSigningSecretDTO at creation time.
type AuditWebhookSigningStatusDTO struct {
	CurrentKeyID      string `json:"current_key_id,omitempty"`
	NextKeyID         string `json:"next_key_id,omitempty"`
	CurrentGeneration int    `json:"current_generation,omitempty"`
	NextGeneration    int    `json:"next_generation,omitempty"`
	Revision          int    `json:"signing_revision"`
}

// AuditWebhookSigningSecretDTO exposes a newly generated secret exactly once.
// It must never be persisted by callers or returned from status APIs.
type AuditWebhookSigningSecretDTO struct {
	Secret string `json:"secret"`
	AuditWebhookSigningStatusDTO
}

type AuditWebhookSigningKey string

const (
	AuditWebhookSigningKeyCurrent AuditWebhookSigningKey = "current"
	AuditWebhookSigningKeyNext    AuditWebhookSigningKey = "next"
)

type AuditWebhookDeliveryDTO struct {
	ID          int                           `json:"id"`
	EventID     string                        `json:"event_id"`
	Status      db.AuditWebhookDeliveryStatus `json:"status"`
	Attempts    int                           `json:"attempts"`
	NextAttempt time.Time                     `json:"next_attempt,omitempty"`
	HTTPStatus  *int                          `json:"http_status,omitempty"`
	LastError   string                        `json:"last_error,omitempty"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
	DeliveredAt *time.Time                    `json:"delivered_at,omitempty"`
}

// AuditWebhookDeliveryAttemptDTO intentionally omits payloads, credentials,
// signatures, and all request headers.
type AuditWebhookDeliveryAttemptDTO struct {
	ID          int                                   `json:"id"`
	DeliveryID  int                                   `json:"delivery_id"`
	EventID     string                                `json:"event_id"`
	Attempt     int                                   `json:"attempt"`
	KeyID       string                                `json:"key_id"`
	SignedAt    time.Time                             `json:"signed_at"`
	Outcome     db.AuditWebhookDeliveryAttemptOutcome `json:"outcome"`
	HTTPStatus  *int                                  `json:"http_status,omitempty"`
	Reason      db.AuditWebhookDeliveryAttemptReason  `json:"reason,omitempty"`
	CreatedAt   time.Time                             `json:"created_at"`
	CompletedAt *time.Time                            `json:"completed_at,omitempty"`
}

type AuditWebhookServiceFacade interface {
	Configuration(context.Context) (AuditWebhookConfigDTO, error)
	Configure(context.Context, AuditWebhookConfigInput) (AuditWebhookConfigDTO, error)
	TestDelivery(context.Context) (AuditWebhookDeliveryDTO, error)
	TestDeliveryWithSigningKey(context.Context, AuditWebhookSigningKey) (AuditWebhookDeliveryDTO, error)
	SetPaused(context.Context, bool) (AuditWebhookConfigDTO, error)
	DeliveryHistory(context.Context, db.RetrieveQueryParams) ([]AuditWebhookDeliveryDTO, error)
	DeliveryAttemptHistory(context.Context, int, db.RetrieveQueryParams) ([]AuditWebhookDeliveryAttemptDTO, error)
	CreateSigningSecret(context.Context, int) (AuditWebhookSigningSecretDTO, error)
	StageSigningSecret(context.Context, int) (AuditWebhookSigningSecretDTO, error)
	PromoteSigningSecret(context.Context, int) (AuditWebhookSigningStatusDTO, error)
	RevokeNextSigningSecret(context.Context, int) (AuditWebhookSigningStatusDTO, error)
}

type AuditWebhookService interface {
	AuditWebhookServiceFacade
	PrepareDelivery(context.Context, AuditEvent) (*db.AuditWebhookDelivery, error)
	Notify()
	Start()
	Close() error
}
