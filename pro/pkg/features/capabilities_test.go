package features

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityProviderAndWorkerRemainUnavailable(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	require.NoError(t, db.Rollback(store, "2.20.1"))
	provider := NewCapabilityProvider(store)
	service := NewCapabilityTestService(store)
	request := pro_interfaces.CapabilityRequest{
		UserID:  1,
		IsAdmin: true,
		At:      time.Unix(1_700_000_000, 0).UTC(),
	}

	snapshot, err := provider.Resolve(context.Background(), request)
	require.NoError(t, err)
	decision := snapshot.Decision(pro_interfaces.CapabilityLifecycleTest)
	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable, decision.State())
	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable,
		snapshot.Decision(pro_interfaces.CapabilityRuntimeSecrets).State())
	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable,
		snapshot.Decision(pro_interfaces.CapabilityLDAP).State())
	_, err = service.RunBackgroundAction(context.Background(), snapshot, "blocked")
	assertCommunityDenied(t, err, pro_interfaces.CapabilityAccessExecute)
	_, err = provider.Configure(context.Background(), request, pro_interfaces.CapabilityConfiguration{
		ID:    pro_interfaces.CapabilityLifecycleTest,
		State: pro_interfaces.CapabilityStateActive,
	})
	assertCommunityDenied(t, err, pro_interfaces.CapabilityAccessWrite)
	_, err = provider.Configure(context.Background(), request, pro_interfaces.CapabilityConfiguration{
		ID: pro_interfaces.CapabilityRuntimeSecrets, State: pro_interfaces.CapabilityStateActive,
	})
	assertCommunityDenied(t, err, pro_interfaces.CapabilityAccessWrite)
}

func assertCommunityDenied(t *testing.T, err error, access pro_interfaces.CapabilityAccess) {
	t.Helper()
	var denied pro_interfaces.CapabilityDeniedError
	require.True(t, errors.As(err, &denied))
	assert.Equal(t, pro_interfaces.CapabilityStateUnavailable, denied.Decision.State())
	assert.Equal(t, access, denied.Required)
}
