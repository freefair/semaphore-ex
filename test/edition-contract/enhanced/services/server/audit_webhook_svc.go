package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const (
	auditWebhookPollInterval = 2 * time.Second
	auditWebhookLease        = 30 * time.Second
	auditWebhookBatchSize    = 20
	auditWebhookMaxAttempts  = 8
	auditWebhookBaseBackoff  = time.Second
	auditWebhookMaxBackoff   = time.Hour
	auditWebhookSecretLimit  = 4096
)

var errAuditWebhookCredentialUnreadable = errors.New("audit webhook credential unreadable")
var errAuditWebhookSigningSecretUnreadable = errors.New("audit webhook signing secret unreadable")

type auditWebhookService struct {
	repository db.AuditWebhookRepository
	metrics    *metrics.Metrics
	client     auditWebhookClient
	now        func() time.Time
	jitter     func() float64
	cipher     auditWebhookCipher
	wake       chan struct{}
	cancel     context.CancelFunc
	wait       sync.WaitGroup
	startOnce  sync.Once
	closeOnce  sync.Once
}

type auditWebhookCipher interface {
	OptionEncryptionEnabled() bool
	EncryptOption([]byte) (string, error)
	DecryptOption(string) ([]byte, error)
}

type auditWebhookCipherFunctions struct {
	enabled bool
	encrypt func([]byte) (string, error)
	decrypt func(string) ([]byte, error)
}

func (c auditWebhookCipherFunctions) OptionEncryptionEnabled() bool { return c.enabled }
func (c auditWebhookCipherFunctions) EncryptOption(value []byte) (string, error) {
	return c.encrypt(value)
}
func (c auditWebhookCipherFunctions) DecryptOption(value string) ([]byte, error) {
	return c.decrypt(value)
}

func NewAuditWebhookService(repository db.AuditWebhookRepository, appMetrics *metrics.Metrics) pro_interfaces.AuditWebhookService {
	return newAuditWebhookService(
		repository,
		appMetrics,
		newHTTPSAuditWebhookClient(),
		time.Now,
		rand.Float64,
		util.Config,
	)
}

func newAuditWebhookService(
	repository db.AuditWebhookRepository,
	appMetrics *metrics.Metrics,
	client auditWebhookClient,
	now func() time.Time,
	jitter func() float64,
	cipher auditWebhookCipher,
) *auditWebhookService {
	return &auditWebhookService{
		repository: repository,
		metrics:    appMetrics,
		client:     client,
		now:        now,
		jitter:     jitter,
		cipher:     cipher,
		wake:       make(chan struct{}, 1),
	}
}

func (s *auditWebhookService) PrepareDelivery(_ context.Context, event pro_interfaces.AuditEvent) (*db.AuditWebhookDelivery, error) {
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) || (err == nil && config.Endpoint == "") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load audit webhook configuration")
	}
	envelope, err := pro_interfaces.NewAuditWebhookEnvelope(event)
	if err != nil {
		s.metrics.RecordAuditWebhookRedactionFailure()
		return nil, fmt.Errorf("audit webhook redaction failed")
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		s.metrics.RecordAuditWebhookRedactionFailure()
		return nil, fmt.Errorf("audit webhook redaction failed")
	}
	now := s.now().UTC()
	return &db.AuditWebhookDelivery{
		EventID:     event.EventID,
		Payload:     string(payload),
		Status:      db.AuditWebhookDeliveryPending,
		NextAttempt: now,
	}, nil
}

func (s *auditWebhookService) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *auditWebhookService) Configuration(context.Context) (pro_interfaces.AuditWebhookConfigDTO, error) {
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.AuditWebhookConfigDTO{}, nil
	}
	if err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("load audit webhook configuration")
	}
	return auditWebhookConfigDTO(config), nil
}

func (s *auditWebhookService) Configure(_ context.Context, input pro_interfaces.AuditWebhookConfigInput) (pro_interfaces.AuditWebhookConfigDTO, error) {
	if err := validateAuditWebhookEndpoint(input.Endpoint); err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("%w: %v", pro_interfaces.ErrAuditWebhookInvalidInput, err)
	}
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) {
		config = db.AuditWebhookConfig{}
	} else if err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("load audit webhook configuration")
	}
	config.Endpoint = input.Endpoint
	if input.Credential != nil {
		if len(*input.Credential) > auditWebhookSecretLimit {
			return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("%w: credential is too long", pro_interfaces.ErrAuditWebhookInvalidInput)
		}
		if *input.Credential == "" {
			config.EncryptedCredential = ""
			config.CredentialConfigured = false
		} else {
			encrypted, encryptErr := s.cipher.EncryptOption([]byte(*input.Credential))
			if encryptErr != nil {
				return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("store audit webhook credential")
			}
			config.EncryptedCredential = encrypted
			config.CredentialConfigured = true
		}
	}
	saved, err := s.repository.SaveAuditWebhookConfig(config)
	if err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("save audit webhook configuration")
	}
	s.Notify()
	return auditWebhookConfigDTO(saved), nil
}

func (s *auditWebhookService) TestDelivery(ctx context.Context) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return s.TestDeliveryWithSigningKey(ctx, pro_interfaces.AuditWebhookSigningKeyCurrent)
}

func (s *auditWebhookService) TestDeliveryWithSigningKey(ctx context.Context, slot pro_interfaces.AuditWebhookSigningKey) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) || (err == nil && config.Endpoint == "") {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, pro_interfaces.ErrAuditWebhookNotConfigured
	}
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("load audit webhook configuration")
	}
	credential, signingKey, err := s.deliveryCredentials(config, slot)
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, err
	}
	event, err := (pro_interfaces.AuditEvent{
		CorrelationID: "internal",
		Action:        pro_interfaces.AuditActionWebhookTest,
		TargetType:    pro_interfaces.AuditTargetWebhook,
		TargetID:      "audit_webhook",
		Outcome:       pro_interfaces.AuditOutcomeAllowed,
		Source:        pro_interfaces.AuditSourceWorker,
		Reason:        string(pro_interfaces.CapabilityReasonActive),
	}).EnsureDeliveryMetadata(s.now())
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("prepare audit webhook test")
	}
	envelope, err := pro_interfaces.NewAuditWebhookEnvelope(event)
	if err != nil {
		s.metrics.RecordAuditWebhookRedactionFailure()
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("audit webhook redaction failed")
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		s.metrics.RecordAuditWebhookRedactionFailure()
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("audit webhook redaction failed")
	}
	now := s.now().UTC()
	delivery := db.AuditWebhookDelivery{
		EventID:     event.EventID,
		Payload:     string(payload),
		NextAttempt: now,
		Status:      db.AuditWebhookDeliveryRunning,
	}
	created, err := s.repository.CreateAuditWebhookDelivery(delivery)
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("store audit webhook test delivery")
	}
	finalized, err := s.processDelivery(ctx, config.Endpoint, credential, signingKey, created)
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("deliver audit webhook test")
	}
	if finalized.Status == db.AuditWebhookDeliveryRetrying {
		s.Notify()
	}
	s.updateQueueMetrics()
	return auditWebhookDeliveryDTO(finalized), nil
}

func (s *auditWebhookService) SetPaused(_ context.Context, paused bool) (pro_interfaces.AuditWebhookConfigDTO, error) {
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) {
		return pro_interfaces.AuditWebhookConfigDTO{}, pro_interfaces.ErrAuditWebhookNotConfigured
	}
	if err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("load audit webhook configuration")
	}
	config.Paused = paused
	saved, err := s.repository.SaveAuditWebhookConfig(config)
	if err != nil {
		return pro_interfaces.AuditWebhookConfigDTO{}, fmt.Errorf("save audit webhook configuration")
	}
	if !paused {
		s.Notify()
	}
	return auditWebhookConfigDTO(saved), nil
}

func (s *auditWebhookService) DeliveryHistory(_ context.Context, params db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryDTO, error) {
	deliveries, err := s.repository.GetAuditWebhookDeliveries(params)
	if err != nil {
		return nil, fmt.Errorf("load audit webhook delivery history")
	}
	result := make([]pro_interfaces.AuditWebhookDeliveryDTO, len(deliveries))
	for i := range deliveries {
		result[i] = auditWebhookDeliveryDTO(deliveries[i])
	}
	return result, nil
}

func (s *auditWebhookService) DeliveryAttemptHistory(_ context.Context, deliveryID int, params db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryAttemptDTO, error) {
	attempts, err := s.repository.GetAuditWebhookDeliveryAttempts(deliveryID, params)
	if err != nil {
		return nil, fmt.Errorf("load audit webhook delivery attempt history")
	}
	result := make([]pro_interfaces.AuditWebhookDeliveryAttemptDTO, len(attempts))
	for index := range attempts {
		result[index] = auditWebhookDeliveryAttemptDTO(attempts[index])
	}
	return result, nil
}

func (s *auditWebhookService) CreateSigningSecret(_ context.Context, expectedRevision int) (pro_interfaces.AuditWebhookSigningSecretDTO, error) {
	config, err := s.signingConfig(expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, err
	}
	if config.CurrentSigningSecretEncrypted != "" || config.NextSigningSecretEncrypted != "" {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	key, encrypted, err := s.newEncryptedSigningKey()
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, err
	}
	config.CurrentSigningSecretEncrypted = encrypted
	config.CurrentSigningKeyID = key.ID
	config.CurrentSigningGeneration = 1
	saved, err := s.repository.CompareAndSwapAuditWebhookSigningState(config, expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, mapAuditWebhookSigningStateError(err)
	}
	return pro_interfaces.AuditWebhookSigningSecretDTO{Secret: key.Secret, AuditWebhookSigningStatusDTO: auditWebhookSigningStatusDTO(saved)}, nil
}

func (s *auditWebhookService) StageSigningSecret(_ context.Context, expectedRevision int) (pro_interfaces.AuditWebhookSigningSecretDTO, error) {
	config, err := s.signingConfig(expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, err
	}
	if config.CurrentSigningSecretEncrypted == "" || config.NextSigningSecretEncrypted != "" {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	key, encrypted, err := s.newEncryptedSigningKey()
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, err
	}
	config.NextSigningSecretEncrypted = encrypted
	config.NextSigningKeyID = key.ID
	config.NextSigningGeneration = config.CurrentSigningGeneration + 1
	saved, err := s.repository.CompareAndSwapAuditWebhookSigningState(config, expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningSecretDTO{}, mapAuditWebhookSigningStateError(err)
	}
	return pro_interfaces.AuditWebhookSigningSecretDTO{Secret: key.Secret, AuditWebhookSigningStatusDTO: auditWebhookSigningStatusDTO(saved)}, nil
}

func (s *auditWebhookService) PromoteSigningSecret(_ context.Context, expectedRevision int) (pro_interfaces.AuditWebhookSigningStatusDTO, error) {
	config, err := s.signingConfig(expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, err
	}
	if config.CurrentSigningSecretEncrypted == "" || config.NextSigningSecretEncrypted == "" {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	config.CurrentSigningSecretEncrypted, config.NextSigningSecretEncrypted = config.NextSigningSecretEncrypted, config.CurrentSigningSecretEncrypted
	config.CurrentSigningKeyID, config.NextSigningKeyID = config.NextSigningKeyID, config.CurrentSigningKeyID
	config.CurrentSigningGeneration, config.NextSigningGeneration = config.NextSigningGeneration, config.CurrentSigningGeneration
	saved, err := s.repository.CompareAndSwapAuditWebhookSigningState(config, expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, mapAuditWebhookSigningStateError(err)
	}
	return auditWebhookSigningStatusDTO(saved), nil
}

func (s *auditWebhookService) RevokeNextSigningSecret(_ context.Context, expectedRevision int) (pro_interfaces.AuditWebhookSigningStatusDTO, error) {
	config, err := s.signingConfig(expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, err
	}
	if config.NextSigningSecretEncrypted == "" {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	config.NextSigningSecretEncrypted = ""
	config.NextSigningKeyID = ""
	config.NextSigningGeneration = 0
	saved, err := s.repository.CompareAndSwapAuditWebhookSigningState(config, expectedRevision)
	if err != nil {
		return pro_interfaces.AuditWebhookSigningStatusDTO{}, mapAuditWebhookSigningStateError(err)
	}
	return auditWebhookSigningStatusDTO(saved), nil
}

func (s *auditWebhookService) Start() {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wait.Add(1)
		go s.run(ctx)
	})
}

func (s *auditWebhookService) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
			s.wait.Wait()
		}
	})
	return nil
}

func (s *auditWebhookService) run(ctx context.Context) {
	defer s.wait.Done()
	ticker := time.NewTicker(auditWebhookPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		s.processOnce(ctx)
	}
}

func (s *auditWebhookService) processOnce(ctx context.Context) {
	config, err := s.repository.GetAuditWebhookConfig()
	if err != nil || config.Endpoint == "" || config.Paused {
		s.updateQueueMetrics()
		return
	}
	// A database upgraded before signing was configured has a valid empty
	// signing state. Keep its queued events pending until an operator creates a
	// key; claiming them here would turn normal setup into permanent loss.
	if auditWebhookSigningKeyIsUnconfigured(config) || s.cipher == nil || !s.cipher.OptionEncryptionEnabled() {
		s.updateQueueMetrics()
		return
	}
	now := s.now().UTC()
	deliveries, err := s.repository.ClaimAuditWebhookDeliveries(now, now.Add(auditWebhookLease), auditWebhookBatchSize)
	if err != nil {
		s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, 0, false)
		return
	}
	credential, signingKey, configurationErr := s.deliveryCredentials(config, pro_interfaces.AuditWebhookSigningKeyCurrent)
	if configurationErr != nil {
		for i := range deliveries {
			_, _ = s.failClaimedDelivery(deliveries[i], config.CurrentSigningKeyID, auditWebhookConfigurationFailureReason(configurationErr))
		}
		s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, 0, false)
		s.updateQueueMetrics()
		return
	}
	for i := range deliveries {
		_, _ = s.processDelivery(ctx, config.Endpoint, credential, signingKey, deliveries[i])
	}
	s.updateQueueMetrics()
}

func (s *auditWebhookService) processDelivery(
	ctx context.Context,
	endpoint string,
	credential string,
	signingKey pro_interfaces.WebhookSigningKey,
	delivery db.AuditWebhookDelivery,
) (db.AuditWebhookDelivery, error) {
	started := s.now()
	attempt := delivery.Attempts + 1
	signed, signedAt, err := s.signDelivery(endpoint, delivery, signingKey)
	if err != nil {
		return s.failClaimedDelivery(delivery, signingKey.ID, db.AuditWebhookDeliveryAttemptReasonSigningFailure)
	}
	if _, err = s.repository.RecordAuditWebhookDeliveryAttempt(db.AuditWebhookDeliveryAttempt{
		DeliveryID: delivery.ID,
		EventID:    delivery.EventID,
		Attempt:    attempt,
		KeyID:      signingKey.ID,
		SignedAt:   signedAt,
		Outcome:    db.AuditWebhookDeliveryAttemptStarted,
	}); err != nil {
		return db.AuditWebhookDelivery{}, err
	}

	result := s.client.Deliver(ctx, endpoint, credential, signed)
	s.metrics.RecordAuditWebhookAttempt()
	now := s.now().UTC()
	finalization := db.AuditWebhookDeliveryAttemptResult{
		DeliveryID:  delivery.ID,
		Attempt:     attempt,
		HTTPStatus:  result.StatusCode,
		CompletedAt: now,
	}
	var finalStatus db.AuditWebhookDeliveryStatus
	switch {
	case result.Succeeded:
		finalization.Outcome = db.AuditWebhookDeliveryAttemptSucceeded
		finalStatus = db.AuditWebhookDeliverySucceeded
	case result.Retryable && attempt < auditWebhookMaxAttempts:
		nextAttempt := now.Add(auditWebhookBackoff(attempt, s.jitter()))
		finalization.Outcome = db.AuditWebhookDeliveryAttemptRetrying
		finalization.Reason = auditWebhookDeliveryAttemptReason(result)
		finalization.NextAttempt = &nextAttempt
		finalStatus = db.AuditWebhookDeliveryRetrying
	default:
		finalization.Outcome = db.AuditWebhookDeliveryAttemptFailed
		finalization.Reason = auditWebhookDeliveryAttemptReason(result)
		finalStatus = db.AuditWebhookDeliveryFailed
	}
	err = s.repository.FinalizeAuditWebhookDeliveryAttempt(finalization)
	if err == nil && finalStatus == db.AuditWebhookDeliverySucceeded {
		s.metrics.RecordAuditWebhookSuccess()
	}
	if err == nil && finalStatus == db.AuditWebhookDeliveryFailed {
		s.metrics.RecordAuditWebhookPermanentFailure()
	}
	s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, s.now().Sub(started), err == nil && result.Succeeded)
	if err != nil {
		return db.AuditWebhookDelivery{}, err
	}
	delivery.Attempts = attempt
	delivery.Status = finalStatus
	delivery.HTTPStatus = result.StatusCode
	delivery.LastError = string(finalization.Reason)
	delivery.LastSignedAt = &signedAt
	delivery.Updated = now
	if finalization.NextAttempt != nil {
		delivery.NextAttempt = *finalization.NextAttempt
	} else {
		delivery.NextAttempt = now
	}
	if finalStatus == db.AuditWebhookDeliverySucceeded {
		delivery.DeliveredAt = &now
	}
	return delivery, nil
}

// failClaimedDelivery clears a claimed lease and records a bounded, redacted
// failure. A signing key may be malformed in persisted configuration, so use a
// non-secret sentinel key id only when the configured identifier is invalid.
func (s *auditWebhookService) failClaimedDelivery(
	delivery db.AuditWebhookDelivery,
	keyID string,
	reason db.AuditWebhookDeliveryAttemptReason,
) (db.AuditWebhookDelivery, error) {
	if !pro_interfaces.ValidWebhookSigningKeyID(keyID) {
		keyID = "swhkid_unavailable"
	}
	signedAt := s.nextSignedAt(delivery)
	attempt := delivery.Attempts + 1
	if _, err := s.repository.RecordAuditWebhookDeliveryAttempt(db.AuditWebhookDeliveryAttempt{
		DeliveryID: delivery.ID,
		EventID:    delivery.EventID,
		Attempt:    attempt,
		KeyID:      keyID,
		SignedAt:   signedAt,
		Outcome:    db.AuditWebhookDeliveryAttemptStarted,
	}); err != nil {
		// The legacy finalizer remains a safety net if the attempt ledger cannot
		// be written. It still clears the lease rather than repeatedly claiming a
		// delivery that cannot be signed.
		if markErr := s.repository.MarkAuditWebhookDeliveryFailed(delivery.ID, nil, string(reason), s.now().UTC()); markErr != nil {
			return db.AuditWebhookDelivery{}, err
		}
	} else {
		completedAt := s.now().UTC()
		if completedAt.Before(signedAt) {
			completedAt = signedAt
		}
		if err := s.repository.FinalizeAuditWebhookDeliveryAttempt(db.AuditWebhookDeliveryAttemptResult{
			DeliveryID:  delivery.ID,
			Attempt:     attempt,
			Outcome:     db.AuditWebhookDeliveryAttemptFailed,
			Reason:      reason,
			CompletedAt: completedAt,
		}); err != nil {
			return db.AuditWebhookDelivery{}, err
		}
	}
	s.metrics.RecordAuditWebhookAttempt()
	s.metrics.RecordAuditWebhookPermanentFailure()
	delivery.Attempts = attempt
	delivery.Status = db.AuditWebhookDeliveryFailed
	delivery.LastError = string(reason)
	delivery.LastSignedAt = &signedAt
	delivery.NextAttempt = s.now().UTC()
	delivery.Updated = s.now().UTC()
	return delivery, nil
}

func (s *auditWebhookService) deliveryCredentials(config db.AuditWebhookConfig, slot pro_interfaces.AuditWebhookSigningKey) (string, pro_interfaces.WebhookSigningKey, error) {
	if err := validateAuditWebhookEndpoint(config.Endpoint); err != nil {
		return "", pro_interfaces.WebhookSigningKey{}, fmt.Errorf("audit webhook configuration is invalid: %w", err)
	}
	if err := db.ValidateAuditWebhookSigningState(config); err != nil {
		return "", pro_interfaces.WebhookSigningKey{}, fmt.Errorf("audit webhook signing state is invalid: %w", err)
	}
	credential := ""
	if config.CredentialConfigured {
		decrypted, err := s.cipher.DecryptOption(config.EncryptedCredential)
		if err != nil {
			return "", pro_interfaces.WebhookSigningKey{}, fmt.Errorf("%w: %v", errAuditWebhookCredentialUnreadable, err)
		}
		credential = string(decrypted)
	}
	key, err := s.decryptSigningKey(config, slot)
	if err != nil {
		return "", pro_interfaces.WebhookSigningKey{}, err
	}
	return credential, key, nil
}

func (s *auditWebhookService) decryptSigningKey(config db.AuditWebhookConfig, slot pro_interfaces.AuditWebhookSigningKey) (pro_interfaces.WebhookSigningKey, error) {
	var encrypted, keyID string
	switch slot {
	case pro_interfaces.AuditWebhookSigningKeyCurrent:
		encrypted, keyID = config.CurrentSigningSecretEncrypted, config.CurrentSigningKeyID
	case pro_interfaces.AuditWebhookSigningKeyNext:
		encrypted, keyID = config.NextSigningSecretEncrypted, config.NextSigningKeyID
	default:
		return pro_interfaces.WebhookSigningKey{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	if encrypted == "" || !pro_interfaces.ValidWebhookSigningKeyID(keyID) {
		return pro_interfaces.WebhookSigningKey{}, pro_interfaces.ErrAuditWebhookSigningNotConfigured
	}
	if s.cipher == nil || !s.cipher.OptionEncryptionEnabled() {
		return pro_interfaces.WebhookSigningKey{}, pro_interfaces.ErrAuditWebhookSigningNotConfigured
	}
	secret, err := s.cipher.DecryptOption(encrypted)
	if err != nil {
		return pro_interfaces.WebhookSigningKey{}, fmt.Errorf("%w: %v", errAuditWebhookSigningSecretUnreadable, err)
	}
	return pro_interfaces.WebhookSigningKey{ID: keyID, Secret: string(secret)}, nil
}

func auditWebhookSigningKeyIsUnconfigured(config db.AuditWebhookConfig) bool {
	return config.CurrentSigningSecretEncrypted == "" &&
		config.CurrentSigningKeyID == "" &&
		config.CurrentSigningGeneration == 0 &&
		config.NextSigningSecretEncrypted == "" &&
		config.NextSigningKeyID == "" &&
		config.NextSigningGeneration == 0
}

func auditWebhookConfigurationFailureReason(err error) db.AuditWebhookDeliveryAttemptReason {
	if errors.Is(err, pro_interfaces.ErrAuditWebhookSigningNotConfigured) {
		return db.AuditWebhookDeliveryAttemptReasonNoKey
	}
	if errors.Is(err, errAuditWebhookSigningSecretUnreadable) {
		return db.AuditWebhookDeliveryAttemptReasonSigningFailure
	}
	return db.AuditWebhookDeliveryAttemptReasonConfiguration
}

func (s *auditWebhookService) signDelivery(endpoint string, delivery db.AuditWebhookDelivery, key pro_interfaces.WebhookSigningKey) (pro_interfaces.WebhookSignedRequest, time.Time, error) {
	target, err := auditWebhookRequestTarget(endpoint)
	if err != nil {
		return pro_interfaces.WebhookSignedRequest{}, time.Time{}, err
	}
	signedAt := s.nextSignedAt(delivery)
	request, err := pro_interfaces.BindWebhookSignedRequest(http.MethodPost, target, pro_interfaces.WebhookSignatureHeaders{
		Version: pro_interfaces.WebhookSignatureProtocolVersion, EventID: delivery.EventID,
		Timestamp: strconv.FormatInt(signedAt.Unix(), 10), KeyID: key.ID,
	}, []byte(delivery.Payload))
	if err != nil {
		return pro_interfaces.WebhookSignedRequest{}, time.Time{}, err
	}
	signature, err := pro_interfaces.SignWebhookRequest(key.Secret, request)
	if err != nil {
		return pro_interfaces.WebhookSignedRequest{}, time.Time{}, err
	}
	request.Signature = signature
	return request, signedAt, nil
}

func (s *auditWebhookService) nextSignedAt(delivery db.AuditWebhookDelivery) time.Time {
	next := s.now().UTC().Unix()
	if delivery.LastSignedAt != nil && next <= delivery.LastSignedAt.Unix() {
		next = delivery.LastSignedAt.Unix() + 1
	}
	return time.Unix(next, 0).UTC()
}

func (s *auditWebhookService) signingConfig(expectedRevision int) (db.AuditWebhookConfig, error) {
	if expectedRevision < 0 {
		return db.AuditWebhookConfig{}, pro_interfaces.ErrAuditWebhookInvalidSigningKey
	}
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) {
		if expectedRevision != 0 {
			return db.AuditWebhookConfig{}, pro_interfaces.ErrAuditWebhookSigningStateConflict
		}
		return db.AuditWebhookConfig{}, nil
	}
	if err != nil {
		return db.AuditWebhookConfig{}, fmt.Errorf("load audit webhook signing state")
	}
	if config.SigningStateRevision != expectedRevision {
		return db.AuditWebhookConfig{}, pro_interfaces.ErrAuditWebhookSigningStateConflict
	}
	return config, nil
}

func (s *auditWebhookService) newEncryptedSigningKey() (pro_interfaces.WebhookSigningKey, string, error) {
	key, err := pro_interfaces.NewWebhookSigningKey()
	if err != nil {
		return pro_interfaces.WebhookSigningKey{}, "", fmt.Errorf("generate audit webhook signing secret")
	}
	if s.cipher == nil || !s.cipher.OptionEncryptionEnabled() {
		return pro_interfaces.WebhookSigningKey{}, "", pro_interfaces.ErrAuditWebhookSigningNotConfigured
	}
	encrypted, err := s.cipher.EncryptOption([]byte(key.Secret))
	if err != nil || encrypted == "" {
		return pro_interfaces.WebhookSigningKey{}, "", fmt.Errorf("store audit webhook signing secret")
	}
	return key, encrypted, nil
}

func mapAuditWebhookSigningStateError(err error) error {
	if errors.Is(err, db.ErrAuditWebhookSigningStateConflict) {
		return pro_interfaces.ErrAuditWebhookSigningStateConflict
	}
	return fmt.Errorf("save audit webhook signing state")
}

func auditWebhookDeliveryAttemptReason(result auditWebhookDeliveryResult) db.AuditWebhookDeliveryAttemptReason {
	if result.Succeeded {
		return db.AuditWebhookDeliveryAttemptReasonNone
	}
	switch result.Reason {
	case auditWebhookReasonTimeout:
		return db.AuditWebhookDeliveryAttemptReasonTimeout
	case auditWebhookReasonNetwork:
		return db.AuditWebhookDeliveryAttemptReasonNetworkError
	case auditWebhookReasonClientResponse:
		return db.AuditWebhookDeliveryAttemptReasonHTTP4xx
	case auditWebhookReasonServerResponse:
		return db.AuditWebhookDeliveryAttemptReasonHTTP5xx
	case auditWebhookReasonConfiguration:
		return db.AuditWebhookDeliveryAttemptReasonConfiguration
	default:
		return db.AuditWebhookDeliveryAttemptReasonInvalidResponse
	}
}

func (s *auditWebhookService) updateQueueMetrics() {
	now := s.now().UTC()
	health, err := s.repository.GetAuditWebhookQueueHealth(now)
	if err != nil {
		return
	}
	age := time.Duration(0)
	if health.OldestEvent != nil && health.OldestEvent.Before(now) {
		age = now.Sub(*health.OldestEvent)
	}
	s.metrics.SetAuditWebhookQueueHealth(health.Depth, age)
}

func auditWebhookBackoff(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	exponent := math.Min(float64(attempt-1), math.Log2(float64(auditWebhookMaxBackoff/auditWebhookBaseBackoff)))
	maximum := time.Duration(float64(auditWebhookBaseBackoff) * math.Pow(2, exponent))
	if maximum > auditWebhookMaxBackoff {
		maximum = auditWebhookMaxBackoff
	}
	minimum := maximum / 2
	return minimum + time.Duration(float64(maximum-minimum)*jitter)
}

func auditWebhookConfigDTO(config db.AuditWebhookConfig) pro_interfaces.AuditWebhookConfigDTO {
	return pro_interfaces.AuditWebhookConfigDTO{
		Endpoint:                     config.Endpoint,
		CredentialConfigured:         config.CredentialConfigured,
		Paused:                       config.Paused,
		UpdatedAt:                    config.Updated,
		AuditWebhookSigningStatusDTO: auditWebhookSigningStatusDTO(config),
	}
}

func auditWebhookSigningStatusDTO(config db.AuditWebhookConfig) pro_interfaces.AuditWebhookSigningStatusDTO {
	return pro_interfaces.AuditWebhookSigningStatusDTO{
		CurrentKeyID: config.CurrentSigningKeyID, NextKeyID: config.NextSigningKeyID,
		CurrentGeneration: config.CurrentSigningGeneration, NextGeneration: config.NextSigningGeneration,
		Revision: config.SigningStateRevision,
	}
}

func auditWebhookDeliveryDTO(delivery db.AuditWebhookDelivery) pro_interfaces.AuditWebhookDeliveryDTO {
	return pro_interfaces.AuditWebhookDeliveryDTO{
		ID:          delivery.ID,
		EventID:     delivery.EventID,
		Status:      delivery.Status,
		Attempts:    delivery.Attempts,
		NextAttempt: delivery.NextAttempt,
		HTTPStatus:  delivery.HTTPStatus,
		LastError:   delivery.LastError,
		CreatedAt:   delivery.Created,
		UpdatedAt:   delivery.Updated,
		DeliveredAt: delivery.DeliveredAt,
	}
}

func auditWebhookDeliveryAttemptDTO(attempt db.AuditWebhookDeliveryAttempt) pro_interfaces.AuditWebhookDeliveryAttemptDTO {
	return pro_interfaces.AuditWebhookDeliveryAttemptDTO{
		ID: attempt.ID, DeliveryID: attempt.DeliveryID, EventID: attempt.EventID, Attempt: attempt.Attempt,
		KeyID: attempt.KeyID, SignedAt: attempt.SignedAt, Outcome: attempt.Outcome,
		HTTPStatus: attempt.HTTPStatus, Reason: attempt.Reason, CreatedAt: attempt.Created, CompletedAt: attempt.CompletedAt,
	}
}

var _ pro_interfaces.AuditWebhookService = (*auditWebhookService)(nil)
