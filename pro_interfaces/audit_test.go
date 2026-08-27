package pro_interfaces

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditEventAcceptsOnlyAllowlistedContext(t *testing.T) {
	actorID := 7
	event := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ActorID:       &actorID,
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}

	require.NoError(t, event.Validate())
	assert.Equal(t, map[string]any{
		"correlation_id": event.CorrelationID,
		"actor_id":       7,
		"action":         AuditActionCapabilityWrite,
		"target_type":    AuditTargetCapability,
		"target_id":      string(CapabilityLifecycleTest),
		"outcome":        AuditOutcomeDenied,
		"source":         AuditSourceAPI,
		"reason":         string(CapabilityReasonDisabledByAdmin),
	}, event.SafeFields())
}

func TestAuditWebhookEnvelopeUsesVersionedAllowlist(t *testing.T) {
	actorID := 7
	projectID := 42
	event := AuditEvent{
		EventID:       "0123456789abcdef0123456789abcdef",
		OccurredAt:    time.Date(2026, time.August, 27, 10, 11, 12, 0, time.UTC),
		CorrelationID: "abcdef0123456789abcdef0123456789",
		ActorID:       &actorID,
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerUpdate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "runner:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		SourceIP:      "192.0.2.10",
		UserAgent:     "audit-client/1.0",
		Reason:        string(CapabilityReasonActive),
	}

	envelope, err := NewAuditWebhookEnvelope(event)
	require.NoError(t, err)
	payload, err := json.Marshal(envelope)
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	assert.Equal(t, AuditWebhookSchemaVersion, fields["schema_version"])
	assert.Equal(t, event.EventID, fields["event_id"])
	assert.Equal(t, event.CorrelationID, fields["correlation_id"])
	assert.Equal(t, map[string]any{"id": float64(actorID)}, fields["actor"])
	assert.Equal(t, map[string]any{
		"id":         event.TargetID,
		"project_id": float64(projectID),
		"type":       string(event.TargetType),
	}, fields["target"])
	for _, forbidden := range []string{"credential", "token", "task_args", "request_body", "description"} {
		assert.NotContains(t, fields, forbidden)
	}
	securityfixtures.AssertTripwiresAbsent(t, string(payload))
}

func TestAuditDeliveryMetadataPreservesStableIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 27, 10, 11, 12, 0, time.UTC)
	event, err := validAuditEventWithoutDeliveryMetadata().EnsureDeliveryMetadata(now)
	require.NoError(t, err)
	assert.Regexp(t, eventIDPattern, event.EventID)
	assert.Equal(t, now, event.OccurredAt)

	later := now.Add(time.Hour)
	retry, err := event.EnsureDeliveryMetadata(later)
	require.NoError(t, err)
	assert.Equal(t, event.EventID, retry.EventID)
	assert.Equal(t, event.OccurredAt, retry.OccurredAt)
}

func TestAuditRequestMetadataIsBoundedAndValidated(t *testing.T) {
	assert.Equal(t, "192.0.2.10", NormalizeAuditSourceIP("192.0.2.10:4242"))
	assert.Equal(t, "2001:db8::1", NormalizeAuditSourceIP("[2001:db8::1]:4242"))
	assert.Empty(t, NormalizeAuditSourceIP("not-an-address"))

	userAgent := SanitizeAuditUserAgent(" agent\nsecret " + string(make([]byte, 300)))
	assert.Equal(t, "agentsecret", userAgent)
	assert.LessOrEqual(t, len(userAgent), AuditUserAgentMaxLength)
	unicodeUserAgent := SanitizeAuditUserAgent(strings.Repeat("ä", AuditUserAgentMaxLength))
	assert.True(t, utf8.ValidString(unicodeUserAgent))
	assert.Equal(t, AuditUserAgentMaxLength, len(unicodeUserAgent))

	event := validAuditEventWithoutDeliveryMetadata()
	event.SourceIP = "not-an-address"
	assert.Error(t, event.Validate())
	event.SourceIP = "192.0.2.10"
	event.UserAgent = "agent\n"
	assert.Error(t, event.Validate())
}

func validAuditEventWithoutDeliveryMetadata() AuditEvent {
	return AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}
}

func TestAuditEventRejectsTripwiresInEveryStringSlot(t *testing.T) {
	base := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        AuditActionCapabilityWrite,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeDenied,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonDisabledByAdmin),
	}

	for _, tripwire := range securityfixtures.TripwireValues {
		tests := []AuditEvent{
			withAuditCorrelation(base, tripwire),
			withAuditTarget(base, tripwire),
			withAuditReason(base, tripwire),
		}
		for _, event := range tests {
			assert.Error(t, event.Validate())
			fields, err := json.Marshal(event.SafeFields())
			require.NoError(t, err)
			securityfixtures.AssertTripwiresAbsent(t, string(fields))
		}
	}
}

func TestAuditEventAcceptsBoundedProjectRunnerTarget(t *testing.T) {
	projectID := 42
	event := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerCreate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "runner:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}

	require.NoError(t, event.Validate())
	event.TargetID = "runner:" + securityfixtures.TripwireValues[0]
	assert.Error(t, event.Validate())
}

func TestAuditEventRequiresConsistentProjectScope(t *testing.T) {
	projectID := 42
	otherProjectID := 43
	projectRunner := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		ProjectID:     &projectID,
		Action:        AuditActionProjectRunnerCreate,
		TargetType:    AuditTargetProjectRunner,
		TargetID:      "project:42",
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}

	require.NoError(t, projectRunner.Validate())
	assert.Equal(t, 42, projectRunner.SafeFields()["project_id"])

	missingScope := projectRunner
	missingScope.ProjectID = nil
	assert.Error(t, missingScope.Validate())
	missingScope.Outcome = AuditOutcomeDenied
	missingScope.Reason = AuditReasonUnauthenticated
	require.NoError(t, missingScope.Validate())
	missingScope.Reason = AuditReasonCrossOrigin
	require.NoError(t, missingScope.Validate())
	missingScope.Outcome = AuditOutcomeFailure
	assert.Error(t, missingScope.Validate())

	invalidScope := projectRunner
	invalidProjectID := 0
	invalidScope.ProjectID = &invalidProjectID
	assert.Error(t, invalidScope.Validate())

	mismatchedScope := projectRunner
	mismatchedScope.ProjectID = &otherProjectID
	assert.Error(t, mismatchedScope.Validate())

	globalCapability := AuditEvent{
		CorrelationID: "fedcba9876543210fedcba9876543210",
		Action:        AuditActionCapabilityRead,
		TargetType:    AuditTargetCapability,
		TargetID:      string(CapabilityLifecycleTest),
		Outcome:       AuditOutcomeAllowed,
		Source:        AuditSourceAPI,
		Reason:        string(CapabilityReasonActive),
	}
	require.NoError(t, globalCapability.Validate())
	globalCapability.ProjectID = &projectID
	assert.Error(t, globalCapability.Validate())
}

func withAuditCorrelation(event AuditEvent, value string) AuditEvent {
	event.CorrelationID = value
	return event
}

func withAuditTarget(event AuditEvent, value string) AuditEvent {
	event.TargetID = value
	return event
}

func withAuditReason(event AuditEvent, value string) AuditEvent {
	event.Reason = value
	return event
}
