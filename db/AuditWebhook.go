package db

import (
	"errors"
	"strings"
	"time"
)

const (
	MaxAuditWebhookSigningKeyIDBytes   = 64
	MaxAuditWebhookAttemptEventIDBytes = 128
	MaxAuditWebhookDeliveryAttemptPage = 100
)

var ErrAuditWebhookSigningStateConflict = errors.New("audit webhook signing state changed")
var ErrAuditWebhookDeliveryAttemptConflict = errors.New("audit webhook delivery attempt changed")

type AuditWebhookDeliveryStatus string

const (
	AuditWebhookDeliveryPending   AuditWebhookDeliveryStatus = "pending"
	AuditWebhookDeliveryRunning   AuditWebhookDeliveryStatus = "delivering"
	AuditWebhookDeliveryRetrying  AuditWebhookDeliveryStatus = "retrying"
	AuditWebhookDeliverySucceeded AuditWebhookDeliveryStatus = "succeeded"
	AuditWebhookDeliveryFailed    AuditWebhookDeliveryStatus = "failed"
)

type AuditWebhookConfig struct {
	ID       int    `db:"id"`
	Endpoint string `db:"endpoint"`
	// EncryptedCredential remains the optional legacy Bearer credential. Signed
	// delivery state is deliberately separate so a migration cannot mistake a
	// former Bearer token for HMAC key material.
	EncryptedCredential           string    `db:"encrypted_credential" json:"-"`
	CredentialConfigured          bool      `db:"credential_configured"`
	CurrentSigningSecretEncrypted string    `db:"current_signing_secret_encrypted" json:"-"`
	NextSigningSecretEncrypted    string    `db:"next_signing_secret_encrypted" json:"-"`
	CurrentSigningKeyID           string    `db:"current_signing_key_id"`
	NextSigningKeyID              string    `db:"next_signing_key_id"`
	CurrentSigningGeneration      int       `db:"current_signing_generation"`
	NextSigningGeneration         int       `db:"next_signing_generation"`
	SigningStateRevision          int       `db:"signing_state_revision"`
	Paused                        bool      `db:"paused"`
	Created                       time.Time `db:"created"`
	Updated                       time.Time `db:"updated"`
}

type AuditWebhookDelivery struct {
	ID           int                        `db:"id"`
	EventID      string                     `db:"event_id"`
	Payload      string                     `db:"payload"`
	Status       AuditWebhookDeliveryStatus `db:"status"`
	Attempts     int                        `db:"attempts"`
	NextAttempt  time.Time                  `db:"next_attempt"`
	LeaseUntil   *time.Time                 `db:"lease_until"`
	HTTPStatus   *int                       `db:"http_status"`
	LastError    string                     `db:"last_error"`
	Created      time.Time                  `db:"created"`
	Updated      time.Time                  `db:"updated"`
	DeliveredAt  *time.Time                 `db:"delivered_at"`
	LastSignedAt *time.Time                 `db:"last_signed_at" json:"-"`
}

type AuditWebhookDeliveryAttemptOutcome string

const (
	AuditWebhookDeliveryAttemptStarted   AuditWebhookDeliveryAttemptOutcome = "started"
	AuditWebhookDeliveryAttemptSucceeded AuditWebhookDeliveryAttemptOutcome = "succeeded"
	AuditWebhookDeliveryAttemptRetrying  AuditWebhookDeliveryAttemptOutcome = "retrying"
	AuditWebhookDeliveryAttemptFailed    AuditWebhookDeliveryAttemptOutcome = "failed"
)

type AuditWebhookDeliveryAttemptReason string

const (
	AuditWebhookDeliveryAttemptReasonNone            AuditWebhookDeliveryAttemptReason = ""
	AuditWebhookDeliveryAttemptReasonNetworkError    AuditWebhookDeliveryAttemptReason = "network_error"
	AuditWebhookDeliveryAttemptReasonTimeout         AuditWebhookDeliveryAttemptReason = "timeout"
	AuditWebhookDeliveryAttemptReasonHTTP4xx         AuditWebhookDeliveryAttemptReason = "http_4xx"
	AuditWebhookDeliveryAttemptReasonHTTP5xx         AuditWebhookDeliveryAttemptReason = "http_5xx"
	AuditWebhookDeliveryAttemptReasonInvalidResponse AuditWebhookDeliveryAttemptReason = "invalid_response"
	AuditWebhookDeliveryAttemptReasonNoKey           AuditWebhookDeliveryAttemptReason = "no_key"
	AuditWebhookDeliveryAttemptReasonSigningFailure  AuditWebhookDeliveryAttemptReason = "signing_failure"
	AuditWebhookDeliveryAttemptReasonConfiguration   AuditWebhookDeliveryAttemptReason = "configuration_error"
	AuditWebhookDeliveryAttemptReasonDeliveryFailed  AuditWebhookDeliveryAttemptReason = "delivery_failed"
)

// AuditWebhookDeliveryAttempt contains only delivery metadata. It never stores
// a request body, header, HMAC, or plaintext signing material.
type AuditWebhookDeliveryAttempt struct {
	ID          int                                `db:"id" json:"id"`
	DeliveryID  int                                `db:"delivery_id" json:"delivery_id"`
	EventID     string                             `db:"event_id" json:"event_id"`
	Attempt     int                                `db:"attempt" json:"attempt"`
	KeyID       string                             `db:"key_id" json:"key_id"`
	SignedAt    time.Time                          `db:"signed_at" json:"signed_at"`
	Outcome     AuditWebhookDeliveryAttemptOutcome `db:"outcome" json:"outcome"`
	HTTPStatus  *int                               `db:"http_status" json:"http_status,omitempty"`
	Reason      AuditWebhookDeliveryAttemptReason  `db:"reason" json:"reason,omitempty"`
	Created     time.Time                          `db:"created" json:"created"`
	CompletedAt *time.Time                         `db:"completed_at" json:"completed_at,omitempty"`
}

// AuditWebhookDeliveryAttemptResult atomically couples the terminal parent
// queue update to its already-recorded signed attempt.
type AuditWebhookDeliveryAttemptResult struct {
	DeliveryID  int                                `json:"delivery_id"`
	Attempt     int                                `json:"attempt"`
	Outcome     AuditWebhookDeliveryAttemptOutcome `json:"outcome"`
	HTTPStatus  *int                               `json:"http_status,omitempty"`
	Reason      AuditWebhookDeliveryAttemptReason  `json:"reason,omitempty"`
	NextAttempt *time.Time                         `json:"next_attempt,omitempty"`
	CompletedAt time.Time                          `json:"completed_at"`
}

type AuditWebhookQueueHealth struct {
	Depth       int
	OldestEvent *time.Time
}

type AuditWebhookRepository interface {
	GetAuditWebhookConfig() (AuditWebhookConfig, error)
	SaveAuditWebhookConfig(AuditWebhookConfig) (AuditWebhookConfig, error)
	CompareAndSwapAuditWebhookSigningState(AuditWebhookConfig, int) (AuditWebhookConfig, error)
	CreateAuditWebhookDelivery(AuditWebhookDelivery) (AuditWebhookDelivery, error)
	CreateEventWithAuditWebhook(Event, AuditWebhookDelivery) (Event, error)
	ClaimAuditWebhookDeliveries(time.Time, time.Time, int) ([]AuditWebhookDelivery, error)
	MarkAuditWebhookDeliverySucceeded(int, int, time.Time) error
	MarkAuditWebhookDeliveryRetrying(int, *int, string, time.Time, time.Time) error
	MarkAuditWebhookDeliveryFailed(int, *int, string, time.Time) error
	GetAuditWebhookDeliveries(RetrieveQueryParams) ([]AuditWebhookDelivery, error)
	GetAuditWebhookQueueHealth(time.Time) (AuditWebhookQueueHealth, error)
	RecordAuditWebhookDeliveryAttempt(AuditWebhookDeliveryAttempt) (AuditWebhookDeliveryAttempt, error)
	FinalizeAuditWebhookDeliveryAttempt(AuditWebhookDeliveryAttemptResult) error
	GetAuditWebhookDeliveryAttempts(int, RetrieveQueryParams) ([]AuditWebhookDeliveryAttempt, error)
}

func ValidateAuditWebhookSigningState(config AuditWebhookConfig) error {
	if config.SigningStateRevision < 0 || config.CurrentSigningGeneration < 0 || config.NextSigningGeneration < 0 {
		return errors.New("audit webhook signing generation is invalid")
	}
	if !validAuditWebhookSigningKeyPair(config.CurrentSigningSecretEncrypted, config.CurrentSigningKeyID, config.CurrentSigningGeneration) ||
		!validAuditWebhookSigningKeyPair(config.NextSigningSecretEncrypted, config.NextSigningKeyID, config.NextSigningGeneration) {
		return errors.New("audit webhook signing key state is invalid")
	}
	if config.NextSigningSecretEncrypted != "" && config.CurrentSigningSecretEncrypted == "" {
		return errors.New("audit webhook next signing key requires a current key")
	}
	if config.NextSigningKeyID != "" && config.NextSigningKeyID == config.CurrentSigningKeyID {
		return errors.New("audit webhook signing key ids must be distinct")
	}
	// Next may be either a staged future key or the retired predecessor after a
	// promotion. Both states are valid; only duplicate generations are not.
	if config.NextSigningGeneration > 0 && config.NextSigningGeneration == config.CurrentSigningGeneration {
		return errors.New("audit webhook next signing generation is invalid")
	}
	return nil
}

func ValidateAuditWebhookDeliveryAttempt(attempt AuditWebhookDeliveryAttempt) error {
	if attempt.DeliveryID <= 0 || attempt.Attempt <= 0 || attempt.SignedAt.IsZero() {
		return errors.New("audit webhook delivery attempt identity is invalid")
	}
	if !validAuditWebhookEventID(attempt.EventID) {
		return errors.New("audit webhook delivery attempt event id is invalid")
	}
	if !validAuditWebhookSigningKeyID(attempt.KeyID) {
		return errors.New("audit webhook delivery attempt key id is invalid")
	}
	if !containsAuditWebhookDeliveryAttemptOutcome(attempt.Outcome) {
		return errors.New("audit webhook delivery attempt outcome is invalid")
	}
	if err := validateAuditWebhookDeliveryAttemptOutcome(attempt.Outcome, attempt.HTTPStatus, attempt.Reason); err != nil {
		return err
	}
	if attempt.Outcome != AuditWebhookDeliveryAttemptStarted && (attempt.CompletedAt == nil || attempt.CompletedAt.Before(attempt.SignedAt)) {
		return errors.New("completed audit webhook delivery attempt timestamp is invalid")
	}
	return nil
}

func ValidateAuditWebhookDeliveryAttemptResult(result AuditWebhookDeliveryAttemptResult) error {
	if result.DeliveryID <= 0 || result.Attempt <= 0 || result.CompletedAt.IsZero() {
		return errors.New("audit webhook delivery attempt result identity is invalid")
	}
	if result.Outcome == AuditWebhookDeliveryAttemptStarted || !containsAuditWebhookDeliveryAttemptOutcome(result.Outcome) {
		return errors.New("audit webhook delivery attempt result outcome is invalid")
	}
	if err := validateAuditWebhookDeliveryAttemptOutcome(result.Outcome, result.HTTPStatus, result.Reason); err != nil {
		return err
	}
	if result.Outcome == AuditWebhookDeliveryAttemptRetrying {
		if result.NextAttempt == nil || !result.NextAttempt.After(result.CompletedAt) {
			return errors.New("retrying audit webhook delivery attempt requires a future retry")
		}
	} else if result.NextAttempt != nil {
		return errors.New("terminal audit webhook delivery attempt cannot declare a retry")
	}
	return nil
}

func validAuditWebhookEventID(value string) bool {
	if len(value) < 16 || len(value) > MaxAuditWebhookAttemptEventIDBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			character != '.' && character != '_' && character != '-' && character != ':' {
			return false
		}
	}
	return true
}

func validAuditWebhookSigningKeyID(value string) bool {
	const prefix = "swhkid_"
	if len(value) < len(prefix)+1 || len(value) > MaxAuditWebhookSigningKeyIDBytes || !strings.HasPrefix(value, prefix) {
		return false
	}
	for index := len(prefix); index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validAuditWebhookSigningKeyPair(secret, keyID string, generation int) bool {
	if secret == "" && keyID == "" && generation == 0 {
		return true
	}
	return secret != "" && validAuditWebhookSigningKeyID(keyID) && generation > 0
}

func containsAuditWebhookDeliveryAttemptReason(wanted AuditWebhookDeliveryAttemptReason) bool {
	switch wanted {
	case AuditWebhookDeliveryAttemptReasonNone,
		AuditWebhookDeliveryAttemptReasonNetworkError,
		AuditWebhookDeliveryAttemptReasonTimeout,
		AuditWebhookDeliveryAttemptReasonHTTP4xx,
		AuditWebhookDeliveryAttemptReasonHTTP5xx,
		AuditWebhookDeliveryAttemptReasonInvalidResponse,
		AuditWebhookDeliveryAttemptReasonNoKey,
		AuditWebhookDeliveryAttemptReasonSigningFailure,
		AuditWebhookDeliveryAttemptReasonConfiguration,
		AuditWebhookDeliveryAttemptReasonDeliveryFailed:
		return true
	default:
		return false
	}
}

func validateAuditWebhookDeliveryAttemptOutcome(
	outcome AuditWebhookDeliveryAttemptOutcome,
	status *int,
	reason AuditWebhookDeliveryAttemptReason,
) error {
	if !containsAuditWebhookDeliveryAttemptReason(reason) {
		return errors.New("audit webhook delivery attempt reason is invalid")
	}
	if status != nil && (*status < 100 || *status > 599) {
		return errors.New("audit webhook delivery attempt HTTP status is invalid")
	}
	switch outcome {
	case AuditWebhookDeliveryAttemptStarted:
		if status != nil || reason != AuditWebhookDeliveryAttemptReasonNone {
			return errors.New("started audit webhook delivery attempt cannot have a result")
		}
	case AuditWebhookDeliveryAttemptSucceeded:
		if reason != AuditWebhookDeliveryAttemptReasonNone || status == nil || *status < 200 || *status > 299 {
			return errors.New("successful audit webhook delivery attempt result is invalid")
		}
	case AuditWebhookDeliveryAttemptRetrying, AuditWebhookDeliveryAttemptFailed:
		if reason == AuditWebhookDeliveryAttemptReasonNone {
			return errors.New("failed audit webhook delivery attempt requires a reason")
		}
		if reason == AuditWebhookDeliveryAttemptReasonHTTP4xx && (status == nil || *status < 400 || *status > 499) {
			return errors.New("HTTP 4xx audit webhook delivery attempt status is invalid")
		}
		if reason == AuditWebhookDeliveryAttemptReasonHTTP5xx && (status == nil || *status < 500 || *status > 599) {
			return errors.New("HTTP 5xx audit webhook delivery attempt status is invalid")
		}
	default:
		return errors.New("audit webhook delivery attempt outcome is invalid")
	}
	return nil
}

func containsAuditWebhookDeliveryAttemptOutcome(wanted AuditWebhookDeliveryAttemptOutcome) bool {
	switch wanted {
	case AuditWebhookDeliveryAttemptStarted,
		AuditWebhookDeliveryAttemptSucceeded,
		AuditWebhookDeliveryAttemptRetrying,
		AuditWebhookDeliveryAttemptFailed:
		return true
	default:
		return false
	}
}
