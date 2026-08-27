package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
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

type auditWebhookService struct {
	repository db.AuditWebhookRepository
	metrics    *metrics.Metrics
	client     auditWebhookClient
	now        func() time.Time
	jitter     func() float64
	encrypt    func([]byte) (string, error)
	decrypt    func(string) ([]byte, error)
	wake       chan struct{}
	cancel     context.CancelFunc
	wait       sync.WaitGroup
	startOnce  sync.Once
	closeOnce  sync.Once
}

func NewAuditWebhookService(repository db.AuditWebhookRepository, appMetrics *metrics.Metrics) pro_interfaces.AuditWebhookService {
	return newAuditWebhookService(
		repository,
		appMetrics,
		newHTTPSAuditWebhookClient(),
		time.Now,
		rand.Float64,
		util.Config.EncryptOption,
		util.Config.DecryptOption,
	)
}

func newAuditWebhookService(
	repository db.AuditWebhookRepository,
	appMetrics *metrics.Metrics,
	client auditWebhookClient,
	now func() time.Time,
	jitter func() float64,
	encrypt func([]byte) (string, error),
	decrypt func(string) ([]byte, error),
) *auditWebhookService {
	return &auditWebhookService{
		repository: repository,
		metrics:    appMetrics,
		client:     client,
		now:        now,
		jitter:     jitter,
		encrypt:    encrypt,
		decrypt:    decrypt,
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
			encrypted, encryptErr := s.encrypt([]byte(*input.Credential))
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
	config, credential, err := s.deliveryConfiguration()
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
	result := s.client.Deliver(ctx, config.Endpoint, credential, payload)
	s.metrics.RecordAuditWebhookAttempt()
	delivery := db.AuditWebhookDelivery{
		EventID:     event.EventID,
		Payload:     string(payload),
		Attempts:    1,
		HTTPStatus:  result.StatusCode,
		LastError:   result.Reason,
		NextAttempt: now,
	}
	switch {
	case result.Succeeded:
		delivery.Status = db.AuditWebhookDeliverySucceeded
		delivery.DeliveredAt = &now
		s.metrics.RecordAuditWebhookSuccess()
	case result.Retryable:
		delivery.Status = db.AuditWebhookDeliveryRetrying
		delivery.NextAttempt = now.Add(auditWebhookBackoff(1, s.jitter()))
	default:
		delivery.Status = db.AuditWebhookDeliveryFailed
		s.metrics.RecordAuditWebhookPermanentFailure()
	}
	created, err := s.repository.CreateAuditWebhookDelivery(delivery)
	if err != nil {
		return pro_interfaces.AuditWebhookDeliveryDTO{}, fmt.Errorf("store audit webhook test delivery")
	}
	if created.Status == db.AuditWebhookDeliveryRetrying {
		s.Notify()
	}
	s.updateQueueMetrics()
	return auditWebhookDeliveryDTO(created), nil
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
	now := s.now().UTC()
	deliveries, err := s.repository.ClaimAuditWebhookDeliveries(now, now.Add(auditWebhookLease), auditWebhookBatchSize)
	if err != nil {
		s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, 0, false)
		return
	}
	credential := ""
	configurationValid := validateAuditWebhookEndpoint(config.Endpoint) == nil
	if configurationValid && config.CredentialConfigured {
		decrypted, decryptErr := s.decrypt(config.EncryptedCredential)
		if decryptErr != nil {
			configurationValid = false
		} else {
			credential = string(decrypted)
		}
	}
	if !configurationValid {
		for i := range deliveries {
			if markErr := s.repository.MarkAuditWebhookDeliveryFailed(
				deliveries[i].ID, nil, auditWebhookReasonConfiguration, now,
			); markErr == nil {
				s.metrics.RecordAuditWebhookAttempt()
				s.metrics.RecordAuditWebhookPermanentFailure()
			}
		}
		s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, 0, false)
		s.updateQueueMetrics()
		return
	}
	for i := range deliveries {
		s.processDelivery(ctx, config, credential, deliveries[i])
	}
	s.updateQueueMetrics()
}

func (s *auditWebhookService) processDelivery(ctx context.Context, config db.AuditWebhookConfig, credential string, delivery db.AuditWebhookDelivery) {
	started := s.now()
	result := s.client.Deliver(ctx, config.Endpoint, credential, []byte(delivery.Payload))
	s.metrics.RecordAuditWebhookAttempt()
	now := s.now().UTC()
	attempt := delivery.Attempts + 1
	var err error
	switch {
	case result.Succeeded:
		err = s.repository.MarkAuditWebhookDeliverySucceeded(delivery.ID, *result.StatusCode, now)
		if err == nil {
			s.metrics.RecordAuditWebhookSuccess()
		}
	case result.Retryable && attempt < auditWebhookMaxAttempts:
		err = s.repository.MarkAuditWebhookDeliveryRetrying(
			delivery.ID, result.StatusCode, result.Reason,
			now.Add(auditWebhookBackoff(attempt, s.jitter())), now,
		)
	default:
		reason := result.Reason
		if result.Retryable {
			reason = auditWebhookReasonAttemptsExhausted
		}
		err = s.repository.MarkAuditWebhookDeliveryFailed(delivery.ID, result.StatusCode, reason, now)
		if err == nil {
			s.metrics.RecordAuditWebhookPermanentFailure()
		}
	}
	s.metrics.ObserveDependency(pro_interfaces.DependencyAuditWebhook, s.now().Sub(started), err == nil && result.Succeeded)
}

func (s *auditWebhookService) deliveryConfiguration() (db.AuditWebhookConfig, string, error) {
	config, err := s.repository.GetAuditWebhookConfig()
	if errors.Is(err, db.ErrNotFound) || (err == nil && config.Endpoint == "") {
		return db.AuditWebhookConfig{}, "", pro_interfaces.ErrAuditWebhookNotConfigured
	}
	if err != nil {
		return db.AuditWebhookConfig{}, "", fmt.Errorf("load audit webhook configuration")
	}
	if err = validateAuditWebhookEndpoint(config.Endpoint); err != nil {
		return db.AuditWebhookConfig{}, "", fmt.Errorf("audit webhook configuration is invalid")
	}
	if !config.CredentialConfigured {
		return config, "", nil
	}
	credential, err := s.decrypt(config.EncryptedCredential)
	if err != nil {
		return db.AuditWebhookConfig{}, "", fmt.Errorf("read audit webhook credential")
	}
	return config, string(credential), nil
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
		Endpoint:             config.Endpoint,
		CredentialConfigured: config.CredentialConfigured,
		Paused:               config.Paused,
		UpdatedAt:            config.Updated,
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

var _ pro_interfaces.AuditWebhookService = (*auditWebhookService)(nil)
