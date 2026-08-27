package db

import "time"

type AuditWebhookDeliveryStatus string

const (
	AuditWebhookDeliveryPending   AuditWebhookDeliveryStatus = "pending"
	AuditWebhookDeliveryRunning   AuditWebhookDeliveryStatus = "delivering"
	AuditWebhookDeliveryRetrying  AuditWebhookDeliveryStatus = "retrying"
	AuditWebhookDeliverySucceeded AuditWebhookDeliveryStatus = "succeeded"
	AuditWebhookDeliveryFailed    AuditWebhookDeliveryStatus = "failed"
)

type AuditWebhookConfig struct {
	ID                   int       `db:"id"`
	Endpoint             string    `db:"endpoint"`
	EncryptedCredential  string    `db:"encrypted_credential"`
	CredentialConfigured bool      `db:"credential_configured"`
	Paused               bool      `db:"paused"`
	Created              time.Time `db:"created"`
	Updated              time.Time `db:"updated"`
}

type AuditWebhookDelivery struct {
	ID          int                        `db:"id"`
	EventID     string                     `db:"event_id"`
	Payload     string                     `db:"payload"`
	Status      AuditWebhookDeliveryStatus `db:"status"`
	Attempts    int                        `db:"attempts"`
	NextAttempt time.Time                  `db:"next_attempt"`
	LeaseUntil  *time.Time                 `db:"lease_until"`
	HTTPStatus  *int                       `db:"http_status"`
	LastError   string                     `db:"last_error"`
	Created     time.Time                  `db:"created"`
	Updated     time.Time                  `db:"updated"`
	DeliveredAt *time.Time                 `db:"delivered_at"`
}

type AuditWebhookQueueHealth struct {
	Depth       int
	OldestEvent *time.Time
}

type AuditWebhookRepository interface {
	GetAuditWebhookConfig() (AuditWebhookConfig, error)
	SaveAuditWebhookConfig(AuditWebhookConfig) (AuditWebhookConfig, error)
	CreateAuditWebhookDelivery(AuditWebhookDelivery) (AuditWebhookDelivery, error)
	CreateEventWithAuditWebhook(Event, AuditWebhookDelivery) (Event, error)
	ClaimAuditWebhookDeliveries(time.Time, time.Time, int) ([]AuditWebhookDelivery, error)
	MarkAuditWebhookDeliverySucceeded(int, int, time.Time) error
	MarkAuditWebhookDeliveryRetrying(int, *int, string, time.Time, time.Time) error
	MarkAuditWebhookDeliveryFailed(int, *int, string, time.Time) error
	GetAuditWebhookDeliveries(RetrieveQueryParams) ([]AuditWebhookDelivery, error)
	GetAuditWebhookQueueHealth(time.Time) (AuditWebhookQueueHealth, error)
}
