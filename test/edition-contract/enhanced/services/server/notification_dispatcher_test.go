package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	storeSql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dispatchAdapter struct {
	provider    string
	results     []pro_interfaces.NotificationDispatchResult
	requests    []pro_interfaces.NotificationDispatchRequest
	credentials []string
	started     chan struct{}
	block       bool
	mu          sync.Mutex
}

func (a *dispatchAdapter) ProviderName() string { return a.provider }

func (a *dispatchAdapter) Dispatch(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.credentials = append(a.credentials, string(request.Credential))
	if a.started != nil {
		select {
		case a.started <- struct{}{}:
		default:
		}
	}
	if a.block {
		a.mu.Unlock()
		<-ctx.Done()
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient}
	}
	if len(a.results) == 0 {
		a.mu.Unlock()
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}
	}
	result := a.results[0]
	a.results = a.results[1:]
	a.mu.Unlock()
	return result
}

func TestNewNotificationDispatcherRegistersOnlyPagerDutyAdapter(t *testing.T) {
	dispatcher := NewNotificationDispatcher(nil).(*notificationDeliveryDispatcher)
	require.Len(t, dispatcher.adapters, 1)
	assert.NotNil(t, dispatcher.adapters[pagerDutyProviderName])
}

func TestNotificationDispatcherRetriesWithoutChangingEventIdentity(t *testing.T) {
	store, service, cipher, delivery := dispatchDeliveryFixture(t)
	adapter := &dispatchAdapter{provider: "pagerduty", results: []pro_interfaces.NotificationDispatchResult{
		{Outcome: pro_interfaces.NotificationDispatchTransient}, {Outcome: pro_interfaces.NotificationDispatchSucceeded},
	}}
	now := notificationDispatchTestNow(delivery)
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, adapter)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	retrying, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryRetrying, retrying.Status)
	assert.Equal(t, 1, retrying.Attempts)
	assert.Equal(t, db.NotificationDeliveryReasonTransport, retrying.LastReason)
	assert.Equal(t, delivery.EventID, retrying.EventID)
	assert.Equal(t, delivery.IncidentKey, retrying.IncidentKey)
	assert.Equal(t, delivery.IdempotencyKey, retrying.IdempotencyKey)

	now = retrying.NextAttempt.Add(time.Second)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	succeeded, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliverySucceeded, succeeded.Status)
	assert.Equal(t, 2, succeeded.Attempts)
	assert.Equal(t, delivery.EventID, succeeded.EventID)
	assert.Equal(t, testPagerDutyRoutingKey, cipher.value)
	require.Len(t, adapter.requests, 2)
	assert.Equal(t, delivery.EventID, adapter.requests[0].Event.EventID)
	assert.Equal(t, delivery.IncidentKey, adapter.requests[0].IncidentKey)
	assert.Equal(t, delivery.IdempotencyKey, adapter.requests[0].IdempotencyKey)
	assert.Equal(t, testPagerDutyRoutingKey, adapter.credentials[0])
	assert.NotContains(t, mustJSON(t, succeeded), testPagerDutyRoutingKey)
	_ = service
}

func TestNotificationDispatcherDispatchesAfterBlankCredentialEdit(t *testing.T) {
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	cipher := &testCipher{enabled: true}
	service := NewGovernanceService(store, cipher).(*governanceService)
	service.now = func() time.Time { return time.Date(2026, time.August, 31, 16, 0, 0, 0, time.UTC) }

	credential := testPagerDutyRoutingKey
	created, err := service.CreateDestination(context.Background(), nil, destinationInput(&credential))
	require.NoError(t, err)
	updatedInput := destinationInput(nil)
	updatedInput.Name = "renamed"
	updated, err := service.UpdateDestination(context.Background(), nil, created.ID, created.Revision, updatedInput)
	require.NoError(t, err)
	assert.True(t, updated.CredentialConfigured)

	delivery, err := service.EnqueueTestDelivery(context.Background(), nil, updated.ID)
	require.NoError(t, err)
	adapter := &dispatchAdapter{provider: "pagerduty"}
	dispatcher := newNotificationDeliveryDispatcher(
		store,
		cipher,
		func() time.Time { return notificationDispatchTestNow(delivery) },
		func() float64 { return .5 },
		adapter,
	)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	delivered, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliverySucceeded, delivered.Status)
	require.Len(t, adapter.credentials, 1)
	assert.Equal(t, credential, adapter.credentials[0])
}

func TestNotificationDispatcherRegistryRejectsDuplicateProviderNames(t *testing.T) {
	store, _, cipher, _ := dispatchDeliveryFixture(t)
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, time.Now, func() float64 { return .5 })
	assert.NoError(t, dispatcher.RegisterAdapter(&dispatchAdapter{provider: "pagerduty"}))
	assert.ErrorIs(t, dispatcher.RegisterAdapter(&dispatchAdapter{provider: "pagerduty"}), pro_interfaces.ErrNotificationInvalidInput)
	assert.ErrorIs(t, dispatcher.RegisterAdapter(&dispatchAdapter{provider: "not valid"}), pro_interfaces.ErrNotificationInvalidInput)
}

func TestNotificationDispatcherPauseDefersWithoutBurningAttemptsAndResumeRuns(t *testing.T) {
	store, service, cipher, delivery := dispatchDeliveryFixture(t)
	destination, err := service.GetDestination(context.Background(), nil, delivery.DestinationID)
	require.NoError(t, err)
	paused, err := service.SetDestinationPaused(context.Background(), nil, destination.ID, destination.Revision, true)
	require.NoError(t, err)
	now := notificationDispatchTestNow(delivery)
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, &dispatchAdapter{provider: "pagerduty"})
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	deferred, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryRetrying, deferred.Status)
	assert.Equal(t, 0, deferred.Attempts)
	assert.Equal(t, db.NotificationDeliveryReasonDestinationPaused, deferred.LastReason)
	assert.True(t, deferred.NextAttempt.After(now))
	assert.Equal(t, 1, deferred.DestinationConfigurationRevision)

	resumed, err := service.SetDestinationPaused(context.Background(), nil, paused.ID, paused.Revision, false)
	require.NoError(t, err)
	assert.Equal(t, 3, resumed.Revision)
	persistedDestination, err := store.GetNotificationDestination(nil, resumed.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, persistedDestination.ConfigurationRevision, "pause changes administrative state without rebinding the configuration generation")
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	delivered, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliverySucceeded, delivered.Status)
	assert.Equal(t, 1, delivered.Attempts)
	assert.Equal(t, deferred.DestinationRevision, delivered.DestinationRevision)
	assert.Equal(t, deferred.DestinationConfigurationRevision, delivered.DestinationConfigurationRevision)
}

func TestNotificationDispatcherRejectsPausedDeliveryAfterConfigurationChange(t *testing.T) {
	rotatedCredential := "FEDCBA9876543210FEDCBA9876543210"
	for _, change := range []struct {
		name  string
		apply func(*pro_interfaces.NotificationDestinationInput)
	}{
		{name: "credential", apply: func(input *pro_interfaces.NotificationDestinationInput) { input.Credential = &rotatedCredential }},
		{name: "environment", apply: func(input *pro_interfaces.NotificationDestinationInput) { input.Environment = "staging" }},
		{name: "region", apply: func(input *pro_interfaces.NotificationDestinationInput) {
			input.Region = pro_interfaces.NotificationProviderRegionEU
		}},
		{name: "provider", apply: func(input *pro_interfaces.NotificationDestinationInput) {
			input.Provider, input.Region, input.Credential = "generic", "", &rotatedCredential
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			store, service, cipher, delivery := dispatchDeliveryFixture(t)
			destination, err := service.GetDestination(context.Background(), nil, delivery.DestinationID)
			require.NoError(t, err)
			paused, err := service.SetDestinationPaused(context.Background(), nil, destination.ID, destination.Revision, true)
			require.NoError(t, err)

			now := notificationDispatchTestNow(delivery)
			adapter := &dispatchAdapter{provider: "pagerduty"}
			dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, adapter)
			require.NoError(t, dispatcher.DispatchOnce(context.Background()))
			deferred, err := store.GetNotificationDelivery(nil, delivery.ID)
			require.NoError(t, err)
			require.Equal(t, db.NotificationDeliveryReasonDestinationPaused, deferred.LastReason)

			input := destinationInput(nil)
			change.apply(&input)
			updated, err := service.UpdateDestination(context.Background(), nil, paused.ID, paused.Revision, input)
			require.NoError(t, err)
			resumed, err := service.SetDestinationPaused(context.Background(), nil, updated.ID, updated.Revision, false)
			require.NoError(t, err)
			assert.Equal(t, 4, resumed.Revision)

			require.NoError(t, dispatcher.DispatchOnce(context.Background()))
			failed, err := store.GetNotificationDelivery(nil, delivery.ID)
			require.NoError(t, err)
			assert.Equal(t, db.NotificationDeliveryFailed, failed.Status)
			assert.Equal(t, db.NotificationDeliveryReasonDestinationChanged, failed.LastReason)
			assert.Equal(t, deferred.DestinationConfigurationRevision, failed.DestinationConfigurationRevision)
			assert.Empty(t, adapter.requests, "configuration must be bound before a credential is decrypted or an adapter is invoked")
		})
	}
}

func TestNotificationDispatcherManualRetryExplicitlyRebindsCorrectedConfiguration(t *testing.T) {
	store, service, cipher, delivery := dispatchDeliveryFixture(t)
	destination, err := service.GetDestination(context.Background(), nil, delivery.DestinationID)
	require.NoError(t, err)
	rotatedCredential := "FEDCBA9876543210FEDCBA9876543210"
	updated, err := service.UpdateDestination(context.Background(), nil, destination.ID, destination.Revision, destinationInput(&rotatedCredential))
	require.NoError(t, err)

	now := notificationDispatchTestNow(delivery)
	adapter := &dispatchAdapter{provider: "pagerduty"}
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, adapter)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	failed, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, db.NotificationDeliveryReasonDestinationChanged, failed.LastReason)
	assert.Empty(t, adapter.requests)

	retried, err := service.RetryDelivery(context.Background(), nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryReasonManualRetry, retried.LastReason)
	persisted, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	current, err := store.GetNotificationDestination(nil, updated.ID)
	require.NoError(t, err)
	assert.Equal(t, current.ConfigurationRevision, persisted.DestinationConfigurationRevision)
	assert.Equal(t, failed.DestinationRevision, persisted.DestinationRevision, "manual retry preserves the public historical destination revision")

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	delivered, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliverySucceeded, delivered.Status)
	require.Len(t, adapter.requests, 1)
	assert.Equal(t, rotatedCredential, adapter.credentials[0])
}

func TestNotificationDispatcherBoundsRateLimitAndConfigurationOutcomes(t *testing.T) {
	store, _, cipher, delivery := dispatchDeliveryFixture(t)
	now := notificationDispatchTestNow(delivery)
	rateLimited := &dispatchAdapter{provider: "pagerduty", results: []pro_interfaces.NotificationDispatchResult{{Outcome: pro_interfaces.NotificationDispatchRateLimited, RetryAfter: 72 * time.Hour}}}
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, rateLimited)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	retrying, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryReasonRateLimited, retrying.LastReason)
	assert.Equal(t, now.Add(notificationDispatchMaxRetryAfter), retrying.NextAttempt)

	storeTwo, _, cipherTwo, missingProvider := dispatchDeliveryFixture(t)
	missingNow := notificationDispatchTestNow(missingProvider)
	missingDispatcher := newNotificationDeliveryDispatcher(storeTwo, cipherTwo, func() time.Time { return missingNow }, func() float64 { return .5 })
	require.NoError(t, missingDispatcher.DispatchOnce(context.Background()))
	failed, err := storeTwo.GetNotificationDelivery(nil, missingProvider.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryFailed, failed.Status)
	assert.Equal(t, db.NotificationDeliveryReasonProviderUnavailable, failed.LastReason)
}

func TestNotificationDispatcherTerminatesInvalidCredentialAndRevision(t *testing.T) {
	store, _, cipher, delivery := dispatchDeliveryFixture(t)
	now := notificationDispatchTestNow(delivery)
	cipher.decryptErr = context.DeadlineExceeded
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, &dispatchAdapter{provider: "pagerduty"})
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	credentialFailed, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryFailed, credentialFailed.Status)
	assert.Equal(t, db.NotificationDeliveryReasonCredentialUnavailable, credentialFailed.LastReason)

	storeTwo, serviceTwo, cipherTwo, staleDelivery := dispatchDeliveryFixture(t)
	staleNow := notificationDispatchTestNow(staleDelivery)
	destination, err := serviceTwo.GetDestination(context.Background(), nil, staleDelivery.DestinationID)
	require.NoError(t, err)
	input := destinationInput(nil)
	input.Name = "replacement"
	_, err = serviceTwo.UpdateDestination(context.Background(), nil, destination.ID, destination.Revision, input)
	require.NoError(t, err)
	staleDispatcher := newNotificationDeliveryDispatcher(storeTwo, cipherTwo, func() time.Time { return staleNow }, func() float64 { return .5 }, &dispatchAdapter{provider: "pagerduty"})
	require.NoError(t, staleDispatcher.DispatchOnce(context.Background()))
	revisionFailed, err := storeTwo.GetNotificationDelivery(nil, staleDelivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryFailed, revisionFailed.Status)
	assert.Equal(t, db.NotificationDeliveryReasonDestinationChanged, revisionFailed.LastReason)
}

func TestNotificationDispatcherExhaustsBoundedTransientAttempts(t *testing.T) {
	store, service, cipher, delivery := dispatchDeliveryFixture(t)
	now := notificationDispatchTestNow(delivery)
	_, err := store.GetConnection().Exec("update notification_delivery set attempts=? where id=?", notificationDispatchMaxAttempts-1, delivery.ID)
	require.NoError(t, err)
	dispatcher := newNotificationDeliveryDispatcher(store, cipher, func() time.Time { return now }, func() float64 { return .5 }, &dispatchAdapter{provider: "pagerduty", results: []pro_interfaces.NotificationDispatchResult{{Outcome: pro_interfaces.NotificationDispatchTransient}}})
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	failed, err := store.GetNotificationDelivery(nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, db.NotificationDeliveryFailed, failed.Status)
	assert.Equal(t, notificationDispatchMaxAttempts, failed.Attempts)
	assert.Equal(t, db.NotificationDeliveryReasonAttemptsExhausted, failed.LastReason)

	retried, err := service.RetryDelivery(context.Background(), nil, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, retried.Attempts)
	assert.Equal(t, delivery.EventID, retried.EventID)
	assert.Equal(t, delivery.IncidentKey, retried.IncidentKey)
}

func TestNotificationDispatcherFencesReclaimedDeliveryAndCloseCancelsWorker(t *testing.T) {
	store, _, _, delivery := dispatchDeliveryFixture(t)
	now := notificationDispatchTestNow(delivery)
	first, err := store.ClaimNotificationDeliveries(now, now.Add(time.Second), 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	second, err := store.ClaimNotificationDeliveries(now.Add(2*time.Second), now.Add(3*time.Second), 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.NotEqual(t, first[0].LeaseToken, second[0].LeaseToken)
	assert.ErrorIs(t, store.MarkNotificationDeliverySucceeded(delivery.ID, first[0].LeaseToken, now.Add(2*time.Second)), db.ErrNotificationDeliveryNotClaimed)

	storeTwo, _, cipherTwo, closeDelivery := dispatchDeliveryFixture(t)
	closeNow := notificationDispatchTestNow(closeDelivery)
	adapter := &dispatchAdapter{provider: "pagerduty", started: make(chan struct{}, 1), block: true}
	dispatcher := newNotificationDeliveryDispatcher(storeTwo, cipherTwo, func() time.Time { return closeNow }, func() float64 { return .5 }, adapter)
	dispatcher.Start()
	select {
	case <-adapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not start delivery")
	}
	done := make(chan struct{})
	go func() { _ = dispatcher.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher Close did not cancel and join worker")
	}
}

func dispatchDeliveryFixture(t *testing.T) (*storeSql.SqlDb, *governanceService, *testCipher, pro_interfaces.NotificationDeliveryDTO) {
	t.Helper()
	store := storeSql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	cipher := &testCipher{enabled: true}
	service := NewGovernanceService(store, cipher).(*governanceService)
	service.now = func() time.Time { return time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC) }
	credential := testPagerDutyRoutingKey
	destination, err := service.CreateDestination(context.Background(), nil, destinationInput(&credential))
	require.NoError(t, err)
	delivery, err := service.EnqueueTestDelivery(context.Background(), nil, destination.ID)
	require.NoError(t, err)
	return store, service, cipher, delivery
}

func notificationDispatchTestNow(delivery pro_interfaces.NotificationDeliveryDTO) time.Time {
	return delivery.NextAttempt.Add(time.Second)
}
