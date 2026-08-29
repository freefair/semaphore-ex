package sql

import (
	"testing"
	"time"

	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterNodeStoreRetainsProcessBootHistory(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	repository := NewClusterNodeStore(store.GetConnection())
	started := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	first := pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-1"},
		Edition:             "enhanced", Version: "1.2.3", Build: "abc123", ProtocolVersion: 1,
		SchemaVersion: "2.20.23", Capabilities: []string{"workflows"}, StartedAt: started, LastSeenAt: started,
	}
	require.NoError(t, repository.UpsertClusterNode(first))

	second := first
	second.BootID = "boot-2"
	second.StartedAt = started.Add(time.Hour)
	second.LastSeenAt = second.StartedAt
	require.NoError(t, repository.UpsertClusterNode(second))

	nodes, err := repository.ListClusterNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, []string{"boot-2", "boot-1"}, []string{nodes[0].BootID, nodes[1].BootID})
	assert.Equal(t, []string{"workflows"}, nodes[0].Capabilities)
}

func TestClusterNodeStorePersistsDrainAndRemovesOnlyExpiredHistory(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	repository := NewClusterNodeStore(store.GetConnection())
	old := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	first := pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-old"},
		Edition:             "enhanced", Version: "1", Build: "old", ProtocolVersion: 1, SchemaVersion: "2.20.23",
		Capabilities: []string{"cluster-dashboard"}, StartedAt: old, LastSeenAt: old,
	}
	recent := first
	recent.BootID = "boot-recent"
	recent.LastSeenAt = old.Add(30 * 24 * time.Hour)
	recent.StartedAt = recent.LastSeenAt
	require.NoError(t, repository.UpsertClusterNode(first))
	require.NoError(t, repository.UpsertClusterNode(recent))

	require.NoError(t, repository.SetClusterNodeDraining("boot-recent", true))
	recent.LastSeenAt = recent.LastSeenAt.Add(time.Minute)
	require.NoError(t, repository.UpsertClusterNode(recent))
	nodes, err := repository.ListClusterNodes()
	require.NoError(t, err)
	assert.True(t, nodes[0].Draining)

	removed, err := repository.DeleteClusterNodesLastSeenBefore(old.Add(7 * 24 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	nodes, err = repository.ListClusterNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "boot-recent", nodes[0].BootID)
}
