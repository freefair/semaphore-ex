package pro_interfaces

import (
	"encoding/json"
	"testing"

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
	event := AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
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
