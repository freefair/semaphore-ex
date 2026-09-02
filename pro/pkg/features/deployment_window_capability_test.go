package features

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityCapabilitySnapshotExplicitlyContainsDeploymentWindows(t *testing.T) {
	snapshot, err := NewCapabilityProvider(nil).Resolve(context.Background(), pro_interfaces.CapabilityRequest{UserID: 1, At: time.Now()})
	require.NoError(t, err)
	decision := snapshot.Decision(pro_interfaces.CapabilityDeploymentWindows)
	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable, decision.State())
	assert.False(t, decision.Allows(pro_interfaces.CapabilityAccessRead))
}
