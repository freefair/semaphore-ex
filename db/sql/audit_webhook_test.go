package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditWebhookConfigRoundTrip(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	_, err := store.GetAuditWebhookConfig()
	assert.ErrorIs(t, err, db.ErrNotFound)

	saved, err := store.SaveAuditWebhookConfig(db.AuditWebhookConfig{
		Endpoint:             "https://audit.example.test/events",
		EncryptedCredential:  "encrypted-value",
		CredentialConfigured: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, saved.ID)
	assert.False(t, saved.Created.IsZero())

	saved.Paused = true
	saved.Endpoint = "https://audit.example.test/v2/events"
	updated, err := store.SaveAuditWebhookConfig(saved)
	require.NoError(t, err)
	assert.True(t, updated.Paused)
	assert.Equal(t, saved.Created, updated.Created)

	persisted, err := store.GetAuditWebhookConfig()
	require.NoError(t, err)
	assert.Equal(t, updated.Endpoint, persisted.Endpoint)
	assert.Equal(t, "encrypted-value", persisted.EncryptedCredential)
	assert.True(t, persisted.CredentialConfigured)
}

func TestAuditWebhookOutboxCommitsWithAuditEvent(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	description := `{"event_id":"0123456789abcdef0123456789abcdef"}`
	objectType := db.EventCapability
	eventID := "0123456789abcdef0123456789abcdef"
	created, err := store.CreateEventWithAuditWebhook(db.Event{
		ObjectType:  &objectType,
		Description: &description,
	}, db.AuditWebhookDelivery{
		EventID: eventID,
		Payload: description,
	})
	require.NoError(t, err)
	assert.False(t, created.Created.IsZero())

	events, err := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	deliveries, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, deliveries, 1)
	assert.Equal(t, eventID, deliveries[0].EventID)
	assert.Equal(t, db.AuditWebhookDeliveryPending, deliveries[0].Status)
}

func TestAuditWebhookOutboxFailureRollsBackAuditEvent(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()

	eventID := "0123456789abcdef0123456789abcdef"
	_, err := store.CreateAuditWebhookDelivery(db.AuditWebhookDelivery{EventID: eventID, Payload: `{}`})
	require.NoError(t, err)
	description := `{}`
	objectType := db.EventCapability

	_, err = store.CreateEventWithAuditWebhook(db.Event{
		ObjectType:  &objectType,
		Description: &description,
	}, db.AuditWebhookDelivery{EventID: eventID, Payload: description})
	require.Error(t, err)

	events, getErr := store.GetAllEvents(db.RetrieveQueryParams{})
	require.NoError(t, getErr)
	assert.Empty(t, events)
	deliveries, getErr := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{})
	require.NoError(t, getErr)
	assert.Len(t, deliveries, 1)
}

func TestAuditWebhookDeliveryClaimRetryAndRecoveryPreserveEventID(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	eventID := "0123456789abcdef0123456789abcdef"
	_, err := store.CreateAuditWebhookDelivery(db.AuditWebhookDelivery{
		EventID:     eventID,
		Payload:     `{}`,
		NextAttempt: now,
	})
	require.NoError(t, err)

	claimed, err := store.ClaimAuditWebhookDeliveries(now, now.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, eventID, claimed[0].EventID)

	retryAt := now.Add(5 * time.Minute)
	status := 503
	require.NoError(t, store.MarkAuditWebhookDeliveryRetrying(claimed[0].ID, &status, "server_error", retryAt, now))
	claimed, err = store.ClaimAuditWebhookDeliveries(now.Add(time.Minute), now.Add(2*time.Minute), 10)
	require.NoError(t, err)
	assert.Empty(t, claimed)

	claimed, err = store.ClaimAuditWebhookDeliveries(retryAt, retryAt.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, eventID, claimed[0].EventID)
	require.NoError(t, store.MarkAuditWebhookDeliverySucceeded(claimed[0].ID, 204, retryAt))

	history, err := store.GetAuditWebhookDeliveries(db.RetrieveQueryParams{Count: 1})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, db.AuditWebhookDeliverySucceeded, history[0].Status)
	assert.Equal(t, 2, history[0].Attempts)
	assert.Equal(t, eventID, history[0].EventID)
	require.NotNil(t, history[0].DeliveredAt)
}
