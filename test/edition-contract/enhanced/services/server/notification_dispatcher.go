package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const (
	notificationDispatchPollInterval  = 2 * time.Second
	notificationDispatchLease         = 30 * time.Second
	notificationDispatchBatchSize     = 20
	notificationDispatchMaxAttempts   = 8
	notificationDispatchBaseBackoff   = time.Second
	notificationDispatchMaxBackoff    = time.Hour
	notificationDispatchMaxRetryAfter = 24 * time.Hour
	notificationDispatchPauseDelay    = time.Minute
)

// notificationCredentialCipher is deliberately narrower than util.Config: the
// worker can decrypt a credential only immediately before handing it to a
// registered provider adapter.
type notificationCredentialCipher interface {
	OptionEncryptionEnabled() bool
	DecryptOption(string) ([]byte, error)
}

type notificationDeliveryDispatcher struct {
	repository db.NotificationRepository
	cipher     notificationCredentialCipher
	now        func() time.Time
	jitter     func() float64

	adaptersMu sync.RWMutex
	adapters   map[string]pro_interfaces.NotificationProviderAdapter
	wake       chan struct{}
	cancel     context.CancelFunc
	wait       sync.WaitGroup
	startOnce  sync.Once
	closeOnce  sync.Once
}

// NewNotificationDispatcher returns the sole process-level outbox worker. The
// PagerDuty adapter is registered before callers can start it; the Community
// replacement keeps notification transport unavailable.
func NewNotificationDispatcher(repository db.NotificationRepository) pro_interfaces.NotificationDeliveryDispatcher {
	return newNotificationDeliveryDispatcher(repository, util.Config, time.Now, rand.Float64, NewPagerDutyAdapter(), NewOpsgenieAdapter(), NewServiceNowAdapter())
}

func newNotificationDeliveryDispatcher(
	repository db.NotificationRepository,
	cipher notificationCredentialCipher,
	now func() time.Time,
	jitter func() float64,
	adapters ...pro_interfaces.NotificationProviderAdapter,
) *notificationDeliveryDispatcher {
	dispatcher := &notificationDeliveryDispatcher{
		repository: repository,
		cipher:     cipher,
		now:        now,
		jitter:     jitter,
		adapters:   make(map[string]pro_interfaces.NotificationProviderAdapter),
		wake:       make(chan struct{}, 1),
	}
	for _, adapter := range adapters {
		_ = dispatcher.RegisterAdapter(adapter)
	}
	return dispatcher
}

// RegisterAdapter rejects empty and duplicate provider names. Adapters expose
// only a bounded result, so provider response bodies and raw errors cannot be
// retained by the durable worker.
func (d *notificationDeliveryDispatcher) RegisterAdapter(adapter pro_interfaces.NotificationProviderAdapter) error {
	if adapter == nil || !providerPattern.MatchString(adapter.ProviderName()) {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	d.adaptersMu.Lock()
	defer d.adaptersMu.Unlock()
	if _, exists := d.adapters[adapter.ProviderName()]; exists {
		return pro_interfaces.ErrNotificationInvalidInput
	}
	d.adapters[adapter.ProviderName()] = adapter
	return nil
}

func (d *notificationDeliveryDispatcher) Start() {
	d.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		d.cancel = cancel
		d.wait.Add(1)
		go d.run(ctx)
	})
}

func (d *notificationDeliveryDispatcher) Close() error {
	d.closeOnce.Do(func() {
		if d.cancel != nil {
			d.cancel()
		}
		d.wait.Wait()
	})
	return nil
}

func (d *notificationDeliveryDispatcher) Notify() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *notificationDeliveryDispatcher) run(ctx context.Context) {
	defer d.wait.Done()
	ticker := time.NewTicker(notificationDispatchPollInterval)
	defer ticker.Stop()
	for {
		_ = d.DispatchOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.wake:
		}
	}
}

// DispatchOnce is exported for deterministic integration tests and safe
// maintenance invocations. It never returns provider response content or raw
// errors; database availability errors are returned only to its caller.
func (d *notificationDeliveryDispatcher) DispatchOnce(ctx context.Context) error {
	if d == nil || d.repository == nil || d.now == nil {
		return pro_interfaces.ErrNotificationUnavailable
	}
	now := d.now().UTC()
	claimed, err := d.repository.ClaimNotificationDeliveries(now, now.Add(notificationDispatchLease), notificationDispatchBatchSize)
	if err != nil {
		return err
	}
	for _, delivery := range claimed {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.dispatchClaimed(ctx, delivery, now); err != nil && !errors.Is(err, db.ErrNotificationDeliveryNotClaimed) {
			return err
		}
	}
	return nil
}

func (d *notificationDeliveryDispatcher) dispatchClaimed(ctx context.Context, claimed db.NotificationDelivery, now time.Time) error {
	delivery, event, destination, err := d.repository.GetNotificationDispatchContext(claimed.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return d.repository.MarkNotificationDeliveryFailed(claimed.ID, claimed.LeaseToken, db.NotificationDeliveryReasonDestinationMissing, now)
		}
		return err
	}
	if delivery.LeaseToken != claimed.LeaseToken || delivery.Status != db.NotificationDeliveryRunning {
		return db.ErrNotificationDeliveryNotClaimed
	}
	if destination.Paused {
		return d.repository.ReleaseNotificationDelivery(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonDestinationPaused, now.Add(notificationDispatchPauseDelay), now)
	}
	if !destination.Enabled {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonDestinationDisabled, now)
	}
	if destination.ConfigurationRevision != delivery.DestinationConfigurationRevision || destination.Provider != delivery.DestinationProvider {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonDestinationChanged, now)
	}
	if !destination.CredentialConfigured || destination.EncryptedCredential == "" || d.cipher == nil || !d.cipher.OptionEncryptionEnabled() {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonCredentialUnavailable, now)
	}
	credential, decryptErr := d.cipher.DecryptOption(destination.EncryptedCredential)
	if decryptErr != nil || len(credential) == 0 {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonCredentialUnavailable, now)
	}
	defer clearNotificationCredential(credential)

	adapter := d.adapter(destination.Provider)
	if adapter == nil {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonProviderUnavailable, now)
	}
	notificationEvent, eventErr := dispatchNotificationEvent(event)
	if eventErr != nil {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonConfiguration, now)
	}
	configuration, valid := opsgenieConfiguration(destination.ProviderConfig)
	var serviceNow *pro_interfaces.NotificationServiceNowConfiguration
	if destination.Provider == serviceNowProviderName {
		serviceNow, valid = serviceNowConfiguration(destination.ProviderConfig)
		if !valid {
			return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonConfiguration, now)
		}
		return d.dispatchServiceNowClaimed(ctx, delivery, destination, notificationEvent, credential, serviceNow, adapter, now)
	}
	if !valid {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonConfiguration, now)
	}
	result := adapter.Dispatch(ctx, pro_interfaces.NotificationDispatchRequest{
		Event: notificationEvent, DestinationID: destination.ID, Provider: destination.Provider,
		Environment: destination.Environment, Region: pro_interfaces.NotificationProviderRegion(delivery.DestinationRegion), IncidentKey: delivery.IncidentKey,
		IdempotencyKey: delivery.IdempotencyKey, Credential: credential, Opsgenie: configuration, ProviderRequestID: delivery.ProviderRequestID, DestinationConfigurationRevision: destination.ConfigurationRevision,
	})
	return d.applyResult(delivery, result, now)
}

func (d *notificationDeliveryDispatcher) dispatchServiceNowClaimed(ctx context.Context, delivery db.NotificationDelivery, destination db.NotificationDestination, event pro_interfaces.NotificationEvent, credential []byte, configuration *pro_interfaces.NotificationServiceNowConfiguration, adapter pro_interfaces.NotificationProviderAdapter, now time.Time) error {
	origin := canonicalServiceNowOrigin(configuration.InstanceOrigin)
	if event.LifecycleAction != pro_interfaces.NotificationLifecycleTrigger {
		binding, lookupErr := d.repository.GetNotificationIncidentBinding(destination.ID, delivery.IncidentKey)
		if lookupErr != nil {
			if errors.Is(lookupErr, db.ErrNotFound) || errors.Is(lookupErr, sql.ErrNoRows) {
				return d.retryServiceNowAmbiguous(delivery, now)
			}
			return lookupErr
		}
		if binding.State != db.NotificationIncidentBindingBound {
			return d.retryServiceNowAmbiguous(delivery, now)
		}
		if binding.ProviderOrigin != origin || binding.ConfigurationRevision != destination.ConfigurationRevision {
			return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonDestinationChanged, now)
		}
		request := pro_interfaces.NotificationDispatchRequest{Event: event, DestinationID: destination.ID, Provider: serviceNowProviderName, IncidentKey: delivery.IncidentKey, IdempotencyKey: delivery.IdempotencyKey, Credential: credential, ServiceNow: configuration, ProviderRecordID: binding.ProviderRecordID, DestinationConfigurationRevision: destination.ConfigurationRevision}
		return d.applyServiceNowResult(delivery, adapter.Dispatch(ctx, request), origin, binding.ProviderRecordID, now)
	}
	binding, err := d.repository.ReserveNotificationIncidentBinding(db.NotificationIncidentBinding{
		DestinationID: destination.ID, IncidentKey: delivery.IncidentKey, Provider: serviceNowProviderName,
		ProviderOrigin: origin, ConfigurationRevision: destination.ConfigurationRevision, CorrelationID: delivery.IncidentKey,
		OwnerDeliveryID: delivery.ID,
	}, now.Add(notificationDispatchLease))
	if err != nil {
		return err
	}
	if binding.Provider != serviceNowProviderName || binding.ProviderOrigin != origin || binding.ConfigurationRevision != destination.ConfigurationRevision {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonDestinationChanged, now)
	}
	request := pro_interfaces.NotificationDispatchRequest{Event: event, DestinationID: destination.ID, Provider: serviceNowProviderName,
		IncidentKey: delivery.IncidentKey, IdempotencyKey: delivery.IdempotencyKey, Credential: credential, ServiceNow: configuration, DestinationConfigurationRevision: destination.ConfigurationRevision}
	if binding.State == db.NotificationIncidentBindingBound {
		request.ProviderRecordID = binding.ProviderRecordID
		return d.applyServiceNowResult(delivery, adapter.Dispatch(ctx, request), origin, binding.ProviderRecordID, now)
	}
	lifecycleAdapter, ok := adapter.(pro_interfaces.NotificationLifecycleAdapter)
	if !ok {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonProviderUnavailable, now)
	}
	if binding.State == db.NotificationIncidentBindingPostStarted || binding.State == db.NotificationIncidentBindingAmbiguous {
		reconciliation := lifecycleAdapter.ReconcileLifecycle(ctx, request)
		if reconciliation.Result.Outcome != pro_interfaces.NotificationDispatchSucceeded {
			return d.applyResult(delivery, reconciliation.Result, now)
		}
		if !reconciliation.Found {
			return d.retryServiceNowAmbiguous(delivery, now)
		}
		if _, err = d.repository.BindNotificationIncident(binding.ID, binding.LeaseToken, reconciliation.RecordID, serviceNowRecordURL(origin, reconciliation.RecordID), now); err != nil {
			return err
		}
		request.ProviderRecordID = reconciliation.RecordID
		return d.applyServiceNowResult(delivery, adapter.Dispatch(ctx, request), origin, reconciliation.RecordID, now)
	}
	if binding.State != db.NotificationIncidentBindingReservedNoPost || binding.OwnerDeliveryID != delivery.ID || binding.LeaseToken == "" {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonProviderAmbiguous, now)
	}
	reconciliation := lifecycleAdapter.ReconcileLifecycle(ctx, request)
	if reconciliation.Result.Outcome != pro_interfaces.NotificationDispatchSucceeded {
		return d.applyResult(delivery, reconciliation.Result, now)
	}
	if reconciliation.Found {
		if _, err = d.repository.BindNotificationIncident(binding.ID, binding.LeaseToken, reconciliation.RecordID, serviceNowRecordURL(origin, reconciliation.RecordID), now); err != nil {
			return err
		}
		request.ProviderRecordID = reconciliation.RecordID
		return d.applyServiceNowResult(delivery, adapter.Dispatch(ctx, request), origin, reconciliation.RecordID, now)
	}
	if _, err = d.repository.MarkNotificationIncidentPostStarted(binding.ID, binding.LeaseToken, now); err != nil {
		return err
	}
	result := adapter.Dispatch(ctx, request)
	if result.Outcome != pro_interfaces.NotificationDispatchSucceeded {
		return d.applyResult(delivery, result, now)
	}
	if !serviceNowSysID.MatchString(result.RecordID) {
		return d.retryServiceNowAmbiguous(delivery, now)
	}
	recordURL := serviceNowRecordURL(origin, result.RecordID)
	if _, err = d.repository.BindNotificationIncidentAndSucceed(binding.ID, binding.LeaseToken, result.RecordID, recordURL, delivery.ID, delivery.LeaseToken, now); err != nil {
		return err
	}
	return nil
}

func (d *notificationDeliveryDispatcher) retryServiceNowAmbiguous(delivery db.NotificationDelivery, now time.Time) error {
	if delivery.Attempts+1 >= notificationDispatchMaxAttempts {
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonAttemptsExhausted, now)
	}
	return d.repository.MarkNotificationDeliveryRetrying(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonProviderAmbiguous, d.backoff(delivery.Attempts+1), now)
}

func (d *notificationDeliveryDispatcher) applyServiceNowResult(delivery db.NotificationDelivery, result pro_interfaces.NotificationDispatchResult, origin, recordID string, now time.Time) error {
	if result.Outcome == pro_interfaces.NotificationDispatchSucceeded {
		if !serviceNowSysID.MatchString(recordID) {
			return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonConfiguration, now)
		}
		return d.repository.MarkNotificationDeliverySucceededWithRecord(delivery.ID, delivery.LeaseToken, recordID, serviceNowRecordURL(origin, recordID), now)
	}
	return d.applyResult(delivery, result, now)
}

func (d *notificationDeliveryDispatcher) adapter(provider string) pro_interfaces.NotificationProviderAdapter {
	d.adaptersMu.RLock()
	defer d.adaptersMu.RUnlock()
	return d.adapters[provider]
}

func (d *notificationDeliveryDispatcher) applyResult(delivery db.NotificationDelivery, result pro_interfaces.NotificationDispatchResult, now time.Time) error {
	switch result.Outcome {
	case pro_interfaces.NotificationDispatchSucceeded:
		return d.repository.MarkNotificationDeliverySucceeded(delivery.ID, delivery.LeaseToken, now)
	case pro_interfaces.NotificationDispatchPermanent:
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonPermanent, now)
	case pro_interfaces.NotificationDispatchPending:
		if delivery.Attempts+1 >= notificationDispatchMaxAttempts || !pro_interfaces.ValidNotificationProviderRequestID(result.RequestID) {
			return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonAttemptsExhausted, now)
		}
		return d.repository.StoreNotificationDeliveryPending(delivery.ID, delivery.LeaseToken, result.RequestID, d.backoff(delivery.Attempts+1), now)
	case pro_interfaces.NotificationDispatchAmbiguous:
		return d.retryServiceNowAmbiguous(delivery, now)
	case pro_interfaces.NotificationDispatchTransient, pro_interfaces.NotificationDispatchRateLimited:
		if delivery.Attempts+1 >= notificationDispatchMaxAttempts {
			return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonAttemptsExhausted, now)
		}
		reason := db.NotificationDeliveryReasonTransport
		next := d.backoff(delivery.Attempts + 1)
		if result.Outcome == pro_interfaces.NotificationDispatchRateLimited {
			reason = db.NotificationDeliveryReasonRateLimited
			next = boundedNotificationRetryAfter(result.RetryAfter, now, d.backoff(delivery.Attempts+1))
		}
		return d.repository.MarkNotificationDeliveryRetrying(delivery.ID, delivery.LeaseToken, reason, next, now)
	default:
		return d.repository.MarkNotificationDeliveryFailed(delivery.ID, delivery.LeaseToken, db.NotificationDeliveryReasonConfiguration, now)
	}
}

func (d *notificationDeliveryDispatcher) backoff(attempt int) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	exponent := min(attempt-1, 20)
	delay := float64(notificationDispatchBaseBackoff) * math.Pow(2, float64(exponent))
	if delay > float64(notificationDispatchMaxBackoff) {
		delay = float64(notificationDispatchMaxBackoff)
	}
	jitter := 0.5
	if d.jitter != nil {
		jitter = d.jitter()
	}
	jitter = min(1, max(0, jitter))
	return d.now().UTC().Add(time.Duration(delay * (0.8 + 0.4*jitter)))
}

func boundedNotificationRetryAfter(retryAfter time.Duration, now time.Time, fallback time.Time) time.Time {
	if retryAfter <= 0 {
		return fallback
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	if retryAfter > notificationDispatchMaxRetryAfter {
		retryAfter = notificationDispatchMaxRetryAfter
	}
	return now.Add(retryAfter)
}

func dispatchNotificationEvent(event db.NotificationEvent) (pro_interfaces.NotificationEvent, error) {
	var details pro_interfaces.NotificationDetails
	if err := json.Unmarshal([]byte(event.Details), &details); err != nil {
		return pro_interfaces.NotificationEvent{}, err
	}
	scope := pro_interfaces.NotificationScopeGlobal
	if event.ProjectID != nil {
		scope = pro_interfaces.NotificationScopeProject
	}
	result := pro_interfaces.NotificationEvent{SchemaVersion: event.SchemaVersion, EventID: event.EventID,
		SourceEventKey: event.SourceEventKey, SourceRevision: event.SourceRevision, OccurredAt: event.OccurredAt,
		Scope: scope, ProjectID: event.ProjectID, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceKind(event.SourceKind), ID: event.SourceID},
		LifecycleID: event.LifecycleID, Severity: pro_interfaces.NotificationSeverity(event.Severity),
		LifecycleAction: pro_interfaces.NotificationLifecycleAction(event.LifecycleAction), IncidentKey: event.IncidentKey, Details: details}
	if err := result.Validate(); err != nil {
		return pro_interfaces.NotificationEvent{}, err
	}
	return result, nil
}

func clearNotificationCredential(credential []byte) {
	for index := range credential {
		credential[index] = 0
	}
}

var _ pro_interfaces.NotificationDeliveryDispatcher = (*notificationDeliveryDispatcher)(nil)
