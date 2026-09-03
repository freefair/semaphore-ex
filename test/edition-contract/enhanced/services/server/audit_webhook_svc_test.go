package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditWebhookServiceRetriesSameEventIDAfterTimeoutAndRecovers(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()

	var mutex sync.Mutex
	mode := "timeout"
	receivedIDs := make([]string, 0, 2)
	authorizations := make([]string, 0, 2)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope pro_interfaces.AuditWebhookEnvelope
		_ = json.NewDecoder(r.Body).Decode(&envelope)
		mutex.Lock()
		currentMode := mode
		receivedIDs = append(receivedIDs, envelope.EventID)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mutex.Unlock()
		if currentMode == "timeout" {
			time.Sleep(40 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	service := newAuditWebhookService(
		store, metrics.NewMetrics(), testAuditWebhookClient(receiver, 10*time.Millisecond),
		func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return "sealed:" + string(secret), nil }, decrypt: func(encrypted string) ([]byte, error) { return []byte(strings.TrimPrefix(encrypted, "sealed:")), nil }},
	)
	credential := "write-only-secret"
	configured, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{
		Endpoint: receiver.URL, Credential: &credential,
	})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), configured.Revision)
	require.NoError(t, err)
	assert.True(t, configured.CredentialConfigured)
	encodedConfig, err := json.Marshal(configured)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedConfig), credential)
	persistedConfig, err := store.GetAuditWebhookConfig()
	require.NoError(t, err)
	assert.Equal(t, "sealed:"+credential, persistedConfig.EncryptedCredential)
	assert.NotEqual(t, credential, persistedConfig.EncryptedCredential)

	event := enhancedWebhookTestEvent(t, now)
	delivery, err := service.PrepareDelivery(context.Background(), event)
	require.NoError(t, err)
	require.NotNil(t, delivery)
	description := delivery.Payload
	objectType := db.EventCapability
	_, err = store.CreateEventWithAuditWebhook(db.Event{ObjectType: &objectType, Description: &description}, *delivery)
	require.NoError(t, err)

	service.processOnce(context.Background())
	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryRetrying, history[0].Status)
	assert.Equal(t, 1, history[0].Attempts)
	assert.Equal(t, event.EventID, history[0].EventID)

	mutex.Lock()
	mode = "success"
	mutex.Unlock()
	now = now.Add(time.Second)
	service.processOnce(context.Background())
	history, err = store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliverySucceeded, history[0].Status)
	assert.Equal(t, 2, history[0].Attempts)
	assert.Equal(t, event.EventID, history[0].EventID)

	mutex.Lock()
	defer mutex.Unlock()
	require.Len(t, receivedIDs, 2)
	assert.Equal(t, receivedIDs[0], receivedIDs[1])
	assert.Equal(t, []string{"Bearer " + credential, "Bearer " + credential}, authorizations)
	attempts, err := service.DeliveryAttemptHistory(context.Background(), history[0].ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	assert.Equal(t, event.EventID, attempts[0].EventID)
	assert.True(t, attempts[0].SignedAt.After(attempts[1].SignedAt))
	assert.Equal(t, db.AuditWebhookDeliveryAttemptSucceeded, attempts[0].Outcome)
	assert.Equal(t, db.AuditWebhookDeliveryAttemptRetrying, attempts[1].Outcome)
}

func TestAuditWebhookSigningLifecycleEncryptsOneTimeSecretsAndUsesCAS(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	service := newAuditWebhookService(store, metrics.NewMetrics(), &fixedAuditWebhookClient{}, time.Now, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func([]byte) (string, error) { return "sealed", nil }, decrypt: func(string) ([]byte, error) { return nil, errors.New("not used by lifecycle test") }})

	initial, err := service.CreateSigningSecret(context.Background(), 0)
	require.NoError(t, err)
	require.NotEmpty(t, initial.Secret)
	persisted, err := store.GetAuditWebhookConfig()
	require.NoError(t, err)
	assert.NotContains(t, persisted.CurrentSigningSecretEncrypted, initial.Secret)
	assert.Equal(t, 1, initial.Revision)

	staged, err := service.StageSigningSecret(context.Background(), initial.Revision)
	require.NoError(t, err)
	promoted, err := service.PromoteSigningSecret(context.Background(), staged.Revision)
	require.NoError(t, err)
	assert.Equal(t, staged.CurrentKeyID, promoted.NextKeyID)
	assert.Equal(t, staged.NextKeyID, promoted.CurrentKeyID)
	_, err = service.RevokeNextSigningSecret(context.Background(), staged.Revision)
	assert.ErrorIs(t, err, pro_interfaces.ErrAuditWebhookSigningStateConflict)
	revoked, err := service.RevokeNextSigningSecret(context.Background(), promoted.Revision)
	require.NoError(t, err)
	assert.Empty(t, revoked.NextKeyID)
}

func TestAuditWebhookSigningCreationAndStagingFailClosedWithoutOptionEncryption(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	disabled := newAuditWebhookService(store, metrics.NewMetrics(), &fixedAuditWebhookClient{}, time.Now, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: false, encrypt: func(secret []byte) (string, error) { return string(secret), nil }, decrypt: func(value string) ([]byte, error) { return []byte(value), nil }})

	_, err := disabled.CreateSigningSecret(context.Background(), 0)
	assert.ErrorIs(t, err, pro_interfaces.ErrAuditWebhookSigningNotConfigured)
	_, err = store.GetAuditWebhookConfig()
	assert.ErrorIs(t, err, db.ErrNotFound)

	enabled := newAuditWebhookService(store, metrics.NewMetrics(), &fixedAuditWebhookClient{}, time.Now, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return "sealed:" + string(secret), nil }, decrypt: func(value string) ([]byte, error) { return []byte(strings.TrimPrefix(value, "sealed:")), nil }})
	created, err := enabled.CreateSigningSecret(context.Background(), 0)
	require.NoError(t, err)
	_, err = disabled.StageSigningSecret(context.Background(), created.Revision)
	assert.ErrorIs(t, err, pro_interfaces.ErrAuditWebhookSigningNotConfigured)
	persisted, err := store.GetAuditWebhookConfig()
	require.NoError(t, err)
	assert.Empty(t, persisted.NextSigningSecretEncrypted)
	assert.Empty(t, persisted.NextSigningKeyID)
}

func TestAuditWebhookServiceLeavesPendingDeliveryUntilSigningIsConfigured(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	client := &countingAuditWebhookClient{}
	service := newAuditWebhookService(store, metrics.NewMetrics(), client, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return string(secret), nil }, decrypt: func(value string) ([]byte, error) { return []byte(value), nil }})
	_, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: "https://audit.example.test/events"})
	require.NoError(t, err)
	delivery, err := service.PrepareDelivery(context.Background(), enhancedWebhookTestEvent(t, now))
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)

	service.processOnce(context.Background())

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryPending, history[0].Status)
	assert.Zero(t, history[0].Attempts)
	assert.Zero(t, client.calls)
}

func TestAuditWebhookServiceLeavesPendingDeliveryWhenOptionEncryptionIsDisabled(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	enabled := newAuditWebhookService(store, metrics.NewMetrics(), &fixedAuditWebhookClient{}, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return "sealed:" + string(secret), nil }, decrypt: func(value string) ([]byte, error) { return []byte(strings.TrimPrefix(value, "sealed:")), nil }})
	configured, err := enabled.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: "https://audit.example.test/events"})
	require.NoError(t, err)
	_, err = enabled.CreateSigningSecret(context.Background(), configured.Revision)
	require.NoError(t, err)
	delivery, err := enabled.PrepareDelivery(context.Background(), enhancedWebhookTestEvent(t, now))
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)

	client := &countingAuditWebhookClient{}
	disabled := newAuditWebhookService(store, metrics.NewMetrics(), client, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: false, encrypt: func(secret []byte) (string, error) { return string(secret), nil }, decrypt: func(value string) ([]byte, error) { return []byte(value), nil }})
	disabled.processOnce(context.Background())

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryPending, history[0].Status)
	assert.Zero(t, history[0].Attempts)
	assert.Zero(t, client.calls)
}

func TestAuditWebhookServiceTerminatesUndecryptableSigningSecretWithRedactedAttempt(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	client := &countingAuditWebhookClient{}
	service := newAuditWebhookService(store, metrics.NewMetrics(), client, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return "sealed:" + string(secret), nil }, decrypt: func(string) ([]byte, error) { return nil, errors.New("corrupt ciphertext") }})
	configured, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: "https://audit.example.test/events"})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), configured.Revision)
	require.NoError(t, err)
	delivery, err := service.PrepareDelivery(context.Background(), enhancedWebhookTestEvent(t, now))
	require.NoError(t, err)
	created, err := store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)

	service.processOnce(context.Background())

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryFailed, history[0].Status)
	assert.Equal(t, string(db.AuditWebhookDeliveryAttemptReasonSigningFailure), history[0].LastError)
	assert.Equal(t, 1, history[0].Attempts)
	assert.Zero(t, client.calls)
	attempts, err := service.DeliveryAttemptHistory(context.Background(), created.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.AuditWebhookDeliveryAttemptFailed, attempts[0].Outcome)
	assert.Equal(t, db.AuditWebhookDeliveryAttemptReasonSigningFailure, attempts[0].Reason)
	assert.Empty(t, attempts[0].HTTPStatus)
}

func TestAuditWebhookServiceTerminatesMalformedSigningSecretWithRedactedAttempt(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	client := &countingAuditWebhookClient{}
	service := newAuditWebhookService(store, metrics.NewMetrics(), client, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func([]byte) (string, error) { return "malformed-secret", nil }, decrypt: func(value string) ([]byte, error) { return []byte(value), nil }})
	configured, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: "https://audit.example.test/events"})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), configured.Revision)
	require.NoError(t, err)
	delivery, err := service.PrepareDelivery(context.Background(), enhancedWebhookTestEvent(t, now))
	require.NoError(t, err)
	created, err := store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)

	service.processOnce(context.Background())

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryFailed, history[0].Status)
	assert.Equal(t, string(db.AuditWebhookDeliveryAttemptReasonSigningFailure), history[0].LastError)
	assert.Zero(t, client.calls)
	attempts, err := service.DeliveryAttemptHistory(context.Background(), created.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.AuditWebhookDeliveryAttemptFailed, attempts[0].Outcome)
	assert.Equal(t, db.AuditWebhookDeliveryAttemptReasonSigningFailure, attempts[0].Reason)
}

func TestAuditWebhookServiceRecoversAfterOversizedResponseAndRecordsPermanentFailure(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()

	var mutex sync.Mutex
	mode := "oversized"
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mutex.Lock()
		currentMode := mode
		mutex.Unlock()
		switch currentMode {
		case "oversized":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", auditWebhookResponseLimit+1)))
		case "bad-request":
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer receiver.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	service := newUnencryptedAuditWebhookService(store, receiver, &now)
	_, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: receiver.URL})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), 0)
	require.NoError(t, err)

	first := enhancedWebhookTestEvent(t, now)
	firstDelivery, err := service.PrepareDelivery(context.Background(), first)
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*firstDelivery)
	require.NoError(t, err)
	service.processOnce(context.Background())
	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryRetrying, history[0].Status)
	assert.Equal(t, string(db.AuditWebhookDeliveryAttemptReasonInvalidResponse), history[0].LastError)

	mutex.Lock()
	mode = "success"
	mutex.Unlock()
	now = now.Add(time.Second)
	service.processOnce(context.Background())
	history, err = store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Equal(t, db.AuditWebhookDeliverySucceeded, history[0].Status)
	assert.Equal(t, first.EventID, history[0].EventID)

	mutex.Lock()
	mode = "bad-request"
	mutex.Unlock()
	now = now.Add(time.Second)
	second := enhancedWebhookTestEvent(t, now)
	secondDelivery, err := service.PrepareDelivery(context.Background(), second)
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*secondDelivery)
	require.NoError(t, err)
	service.processOnce(context.Background())
	history, err = store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, second.EventID, history[0].EventID)
	assert.Equal(t, db.AuditWebhookDeliveryFailed, history[0].Status)
	assert.Equal(t, string(db.AuditWebhookDeliveryAttemptReasonHTTP4xx), history[0].LastError)
	assert.Equal(t, 1, history[0].Attempts)
}

func TestAuditWebhookServicePauseResumeAndTestDelivery(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	requests := 0
	received := make([]pro_interfaces.AuditWebhookEnvelope, 0, 2)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var envelope pro_interfaces.AuditWebhookEnvelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Errorf("decode audit webhook envelope: %v", err)
		}
		received = append(received, envelope)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	service := newUnencryptedAuditWebhookService(store, receiver, &now)
	_, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: receiver.URL})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), 0)
	require.NoError(t, err)
	paused, err := service.SetPaused(context.Background(), true)
	require.NoError(t, err)
	assert.True(t, paused.Paused)

	event := enhancedWebhookTestEvent(t, now)
	delivery, err := service.PrepareDelivery(context.Background(), event)
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)
	service.processOnce(context.Background())
	assert.Zero(t, requests)

	resumed, err := service.SetPaused(context.Background(), false)
	require.NoError(t, err)
	assert.False(t, resumed.Paused)
	service.processOnce(context.Background())
	assert.Equal(t, 1, requests)

	testDelivery, err := service.TestDelivery(context.Background())
	require.NoError(t, err)
	assert.Equal(t, db.AuditWebhookDeliverySucceeded, testDelivery.Status)
	assert.Equal(t, 1, testDelivery.Attempts)
	assert.Equal(t, 2, requests)
	require.Len(t, received, 2)
	assert.Equal(t, pro_interfaces.AuditActionWebhookTest, received[1].Action)
	assert.Equal(t, pro_interfaces.AuditTargetWebhook, received[1].Target.Type)
	assert.Equal(t, "audit_webhook", received[1].Target.ID)
	history, err := service.DeliveryHistory(context.Background(), db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, testDelivery.EventID, history[0].EventID)
}

func TestAuditWebhookServiceUsesSelectedTestKeyAndCurrentKeyForQueuedDelivery(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	keyIDs := make([]string, 0, 3)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyIDs = append(keyIDs, r.Header.Get(pro_interfaces.WebhookHeaderKeyID))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	now := time.Now().UTC().Truncate(time.Second)
	service := newUnencryptedAuditWebhookService(store, receiver, &now)
	configured, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{Endpoint: receiver.URL})
	require.NoError(t, err)
	current, err := service.CreateSigningSecret(context.Background(), configured.Revision)
	require.NoError(t, err)
	next, err := service.StageSigningSecret(context.Background(), current.Revision)
	require.NoError(t, err)

	_, err = service.TestDeliveryWithSigningKey(context.Background(), pro_interfaces.AuditWebhookSigningKeyCurrent)
	require.NoError(t, err)
	_, err = service.TestDeliveryWithSigningKey(context.Background(), pro_interfaces.AuditWebhookSigningKeyNext)
	require.NoError(t, err)
	delivery, err := service.PrepareDelivery(context.Background(), enhancedWebhookTestEvent(t, now))
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)
	service.processOnce(context.Background())

	assert.Equal(t, []string{current.CurrentKeyID, next.NextKeyID, current.CurrentKeyID}, keyIDs)
}

func TestAuditWebhookServiceMarksUndecryptableCredentialAsTerminal(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	service := newAuditWebhookService(
		store, metrics.NewMetrics(), &fixedAuditWebhookClient{}, func() time.Time { return now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func([]byte) (string, error) { return "sealed", nil }, decrypt: func(string) ([]byte, error) { return nil, errors.New("missing key") }},
	)
	credential := "secret"
	_, err := service.Configure(context.Background(), pro_interfaces.AuditWebhookConfigInput{
		Endpoint: "https://audit.example.test/events", Credential: &credential,
	})
	require.NoError(t, err)
	_, err = service.CreateSigningSecret(context.Background(), 0)
	require.NoError(t, err)
	event := enhancedWebhookTestEvent(t, now)
	delivery, err := service.PrepareDelivery(context.Background(), event)
	require.NoError(t, err)
	_, err = store.CreateAuditWebhookDelivery(*delivery)
	require.NoError(t, err)

	service.processOnce(context.Background())

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliveryFailed, history[0].Status)
	assert.Equal(t, auditWebhookReasonConfiguration, history[0].LastError)
}

type fixedAuditWebhookClient struct{}

func (*fixedAuditWebhookClient) Deliver(context.Context, string, string, pro_interfaces.WebhookSignedRequest) auditWebhookDeliveryResult {
	status := http.StatusNoContent
	return auditWebhookDeliveryResult{Succeeded: true, StatusCode: &status}
}

type countingAuditWebhookClient struct{ calls int }

func (c *countingAuditWebhookClient) Deliver(context.Context, string, string, pro_interfaces.WebhookSignedRequest) auditWebhookDeliveryResult {
	c.calls++
	status := http.StatusNoContent
	return auditWebhookDeliveryResult{Succeeded: true, StatusCode: &status}
}

func newUnencryptedAuditWebhookService(store db.AuditWebhookRepository, receiver *httptest.Server, now *time.Time) *auditWebhookService {
	return newAuditWebhookService(
		store, metrics.NewMetrics(), testAuditWebhookClient(receiver, time.Second),
		func() time.Time { return *now }, func() float64 { return 0 },
		auditWebhookCipherFunctions{enabled: true, encrypt: func(secret []byte) (string, error) { return string(secret), nil }, decrypt: func(encrypted string) ([]byte, error) { return []byte(encrypted), nil }},
	)
}

func enhancedWebhookTestEvent(t *testing.T, now time.Time) pro_interfaces.AuditEvent {
	t.Helper()
	event, err := (pro_interfaces.AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        pro_interfaces.AuditActionCapabilityWrite,
		TargetType:    pro_interfaces.AuditTargetCapability,
		TargetID:      string(pro_interfaces.CapabilityLifecycleTest),
		Outcome:       pro_interfaces.AuditOutcomeAllowed,
		Source:        pro_interfaces.AuditSourceAPI,
		SourceIP:      "192.0.2.10",
		UserAgent:     "enhanced-contract-test/1.0",
		Reason:        string(pro_interfaces.CapabilityReasonActive),
	}).EnsureDeliveryMetadata(now)
	require.NoError(t, err)
	return event
}
