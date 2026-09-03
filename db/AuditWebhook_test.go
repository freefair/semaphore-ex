package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAuditWebhookSigningStateRequiresCoherentRotation(t *testing.T) {
	valid := AuditWebhookConfig{
		CurrentSigningSecretEncrypted: "ciphertext-current",
		CurrentSigningKeyID:           "swhkid_current",
		CurrentSigningGeneration:      1,
		NextSigningSecretEncrypted:    "ciphertext-next",
		NextSigningKeyID:              "swhkid_next",
		NextSigningGeneration:         2,
	}
	require.NoError(t, ValidateAuditWebhookSigningState(valid))

	withoutCurrent := valid
	withoutCurrent.CurrentSigningSecretEncrypted = ""
	withoutCurrent.CurrentSigningKeyID = ""
	withoutCurrent.CurrentSigningGeneration = 0
	assert.Error(t, ValidateAuditWebhookSigningState(withoutCurrent))

	duplicateID := valid
	duplicateID.NextSigningKeyID = duplicateID.CurrentSigningKeyID
	assert.Error(t, ValidateAuditWebhookSigningState(duplicateID))
}

func TestValidateAuditWebhookDeliveryAttemptBoundsOutcomeAndReason(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	started := AuditWebhookDeliveryAttempt{
		DeliveryID: 7, EventID: "evt_1234567890123456", Attempt: 1,
		KeyID: "swhkid_current", SignedAt: now, Outcome: AuditWebhookDeliveryAttemptStarted,
	}
	require.NoError(t, ValidateAuditWebhookDeliveryAttempt(started))

	status := 204
	completed := started
	completed.Outcome = AuditWebhookDeliveryAttemptSucceeded
	completed.HTTPStatus = &status
	completed.CompletedAt = pointer(now.Add(time.Second))
	require.NoError(t, ValidateAuditWebhookDeliveryAttempt(completed))

	invalidSuccess := completed
	invalidSuccess.Reason = AuditWebhookDeliveryAttemptReasonNetworkError
	assert.Error(t, ValidateAuditWebhookDeliveryAttempt(invalidSuccess))

	failed := completed
	failed.Outcome = AuditWebhookDeliveryAttemptFailed
	failed.HTTPStatus = nil
	failed.Reason = AuditWebhookDeliveryAttemptReasonNetworkError
	require.NoError(t, ValidateAuditWebhookDeliveryAttempt(failed))

	invalidReason := failed
	invalidReason.Reason = AuditWebhookDeliveryAttemptReason("unbounded\nreason")
	assert.Error(t, ValidateAuditWebhookDeliveryAttempt(invalidReason))
}

func pointer(value time.Time) *time.Time { return &value }
