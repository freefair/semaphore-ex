package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesExecutionPolicyStoreUsesAliasScopedRevisionFence(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	initial, err := store.GetKubernetesExecutionPolicy("qa-cluster")
	require.NoError(t, err)
	assert.Empty(t, initial.Hash)
	policy := testKubernetesExecutionPolicyRecord("qa-cluster")
	saved, err := store.SaveKubernetesExecutionPolicy(policy, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, saved.Revision)
	assert.NotEmpty(t, saved.Hash)
	_, err = store.SaveKubernetesExecutionPolicy(policy, 0)
	assert.ErrorIs(t, err, db.ErrKubernetesExecutionPolicyRevisionConflict)
	loaded, err := store.GetKubernetesExecutionPolicy("qa-cluster")
	require.NoError(t, err)
	assert.Equal(t, saved, loaded)
}

func testKubernetesExecutionPolicyRecord(alias string) db.KubernetesExecutionPolicy {
	p := db.DefaultKubernetesExecutionPolicy(alias)
	p.AllowedNamespaces = []string{"semaphore-jobs"}
	p.AllowedImages = []string{"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	p.AllowedServiceAccounts = []string{"semaphore-task"}
	p.AllowedRuntimeClasses = []string{""}
	p.AllowedVolumeTypes = []db.KubernetesVolumeType{db.KubernetesVolumeEmptyDir, db.KubernetesVolumeSecret}
	p.AllowedNetworkProfiles = []string{"deny-all"}
	p.NetworkProfile = "deny-all"
	p.NetworkPolicyEnforcement = db.KubernetesNetworkPolicyEnforcementNetworkPolicy
	p.Resources = db.KubernetesExecutionResources{CPURequestMilli: 100, CPULimitMilli: 500, MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20, EphemeralStorageRequestBytes: 64 << 20, EphemeralStorageLimitBytes: 256 << 20}
	p.TerminalRetentionSeconds = 3600
	return p
}
