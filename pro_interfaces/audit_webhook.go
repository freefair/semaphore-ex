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

type AuditWebhookConfigInput struct {
	Endpoint   string  `json:"endpoint"`
	Credential *string `json:"credential,omitempty"`
}

type AuditWebhookConfigDTO struct {
	Endpoint             string    `json:"endpoint"`
	CredentialConfigured bool      `json:"credential_configured"`
	Paused               bool      `json:"paused"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

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

type AuditWebhookServiceFacade interface {
	Configuration(context.Context) (AuditWebhookConfigDTO, error)
	Configure(context.Context, AuditWebhookConfigInput) (AuditWebhookConfigDTO, error)
	TestDelivery(context.Context) (AuditWebhookDeliveryDTO, error)
	SetPaused(context.Context, bool) (AuditWebhookConfigDTO, error)
	DeliveryHistory(context.Context, db.RetrieveQueryParams) ([]AuditWebhookDeliveryDTO, error)
}

type AuditWebhookService interface {
	AuditWebhookServiceFacade
	PrepareDelivery(context.Context, AuditEvent) (*db.AuditWebhookDelivery, error)
	Notify()
	Start()
	Close() error
}
