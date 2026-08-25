package pro_interfaces_test

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilitySnapshotRetainsOneEffectiveDecision(t *testing.T) {
	request := pro_interfaces.CapabilityRequest{
		UserID:  41,
		IsAdmin: true,
		At:      time.Unix(1_700_000_000, 0).UTC(),
	}
	decision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateActive,
		pro_interfaces.CapabilityReasonActive,
		[]pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		},
		map[pro_interfaces.LimitID]int64{
			pro_interfaces.LimitLifecycleTestRecordBytes: 256,
		},
	)
	snapshot := pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{decision})

	resolved := snapshot.Decision(pro_interfaces.CapabilityLifecycleTest)
	assert.True(t, resolved.Allows(pro_interfaces.CapabilityAccessWrite))
	limit, ok := resolved.Limit(pro_interfaces.LimitLifecycleTestRecordBytes)
	require.True(t, ok)
	assert.Equal(t, int64(256), limit)

	copyOfDecisions := snapshot.Decisions()
	copyOfDecisions[0] = pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityLifecycleTest,
		pro_interfaces.CapabilityStateDisabled,
		pro_interfaces.CapabilityReasonDisabledByAdmin,
		nil,
		nil,
	)
	assert.True(t, snapshot.Decision(pro_interfaces.CapabilityLifecycleTest).Allows(pro_interfaces.CapabilityAccessWrite))
}

func TestCapabilitySnapshotReturnsUnavailableForUnknownCapability(t *testing.T) {
	snapshot := pro_interfaces.NewCapabilitySnapshot(pro_interfaces.CapabilityRequest{}, nil)
	decision := snapshot.Decision(pro_interfaces.CapabilityID("unknown"))

	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable, decision.State())
	assert.Equal(t, pro_interfaces.CapabilityReasonProviderUnavailable, decision.Reason())
}

func TestCapabilitySnapshotMarshalsOnlySanitizedFields(t *testing.T) {
	request := pro_interfaces.CapabilityRequest{
		UserID:  41,
		IsAdmin: true,
		At:      time.Unix(1_700_000_000, 0).UTC(),
	}
	snapshot := pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityLifecycleTest,
			pro_interfaces.CapabilityStateActive,
			pro_interfaces.CapabilityReasonActive,
			[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead},
			nil,
		),
	})

	encoded, err := snapshot.MarshalJSON()
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"resolved_at":"2023-11-14T22:13:20Z",
		"capabilities":[{
			"id":"lifecycle_test",
			"state":"active",
			"reason":"active",
			"access":["read"],
			"limits":{}
		}]
	}`, string(encoded))
	assert.NotContains(t, string(encoded), "user_id")
	assert.NotContains(t, string(encoded), "is_admin")
}
