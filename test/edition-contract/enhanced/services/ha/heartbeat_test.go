package ha

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	clusterSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisHeartbeatStoreUsesRedisTTLAndServerTimeOverTCP(t *testing.T) {
	server := miniredis.RunT(t)
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server.SetTime(serverNow)
	client := NewGoRedisHeartbeatClient(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	store := NewRedisHeartbeatStore(client, "semaphore:cluster:node")
	identity := pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"}

	lastSeen, err := store.Publish(context.Background(), identity, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, serverNow, lastSeen)
	assert.Equal(t, 30*time.Second, server.TTL("semaphore:cluster:node:node-a:boot-a"))

	server.FastForward(31 * time.Second)
	server.SetTime(serverNow.Add(31 * time.Second))
	alive, observedAt, err := store.IsLive(context.Background(), identity)
	require.NoError(t, err)
	assert.False(t, alive)
	assert.Equal(t, serverNow.Add(31*time.Second), observedAt)
}

func TestTwoNodeRegistriesShareSQLHistoryAndRedisLiveness(t *testing.T) {
	server := miniredis.RunT(t)
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	server.SetTime(serverNow)
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	repository := clusterSQL.NewClusterNodeStore(database.GetConnection())
	client := NewGoRedisHeartbeatClient(redis.NewClient(&redis.Options{Addr: server.Addr()}))
	heartbeats := NewRedisHeartbeatStore(client, "semaphore:cluster:node")
	requirements := pro_interfaces.ClusterCompatibilityRequirements{
		ProtocolVersion: 1, SchemaVersion: "2.20.23", RequiredCapabilities: []string{"cluster-dashboard"},
	}
	first := NewManagedNodeRegistry(repository, heartbeats, clusterRegistration("node-a", "boot-a"), time.Hour, 30*time.Second)
	second := NewManagedNodeRegistry(repository, heartbeats, clusterRegistration("node-b", "boot-b"), time.Hour, 30*time.Second)
	require.NoError(t, first.Start())
	require.NoError(t, second.Start())
	t.Cleanup(first.Stop)
	t.Cleanup(second.Stop)

	inspector := NewManagedClusterInspector(repository, heartbeats, requirements,
		pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"})
	nodes, err := inspector.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.True(t, nodeInfoByBootID(nodes, "boot-a").Ready)
	assert.True(t, nodeInfoByBootID(nodes, "boot-b").Ready)

	second.Stop()
	nodes, err = inspector.Nodes()
	require.NoError(t, err)
	assert.True(t, nodeInfoByBootID(nodes, "boot-a").Ready)
	assert.False(t, nodeInfoByBootID(nodes, "boot-b").Ready)
	assert.Equal(t, pro_interfaces.ClusterNodeStale, nodeInfoByBootID(nodes, "boot-b").CompatibilityState)
}

func clusterRegistration(nodeID string, bootID string) pro_interfaces.ClusterNodeRegistration {
	return pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: nodeID, BootID: bootID},
		Edition:             "enhanced", Version: "1.2.3", Build: "abc", ProtocolVersion: 1,
		SchemaVersion: "2.20.23", Capabilities: []string{"cluster-dashboard"},
	}
}

func nodeInfoByBootID(nodes []pro_interfaces.NodeInfo, bootID string) pro_interfaces.NodeInfo {
	for _, node := range nodes {
		if node.BootID == bootID {
			return node
		}
	}
	return pro_interfaces.NodeInfo{}
}

func TestRedisHeartbeatStoreUsesServerTimeAndTTLExistence(t *testing.T) {
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	client := &heartbeatClientFake{serverTime: serverNow}
	store := NewRedisHeartbeatStore(client, "semaphore:cluster:node")
	identity := pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"}

	lastSeen, err := store.Publish(context.Background(), identity, 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, serverNow, lastSeen)
	assert.Equal(t, 30*time.Second, client.ttl)
	assert.Equal(t, "semaphore:cluster:node:node-a:boot-a", client.key)

	client.exists = true
	alive, observedAt, err := store.IsLive(context.Background(), identity)
	require.NoError(t, err)
	assert.True(t, alive)
	assert.Equal(t, serverNow, observedAt)
}

func TestManagedNodeRegistryPersistsServerTimedStartAndRemovesHeartbeat(t *testing.T) {
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	client := &heartbeatClientFake{serverTime: serverNow}
	heartbeats := NewRedisHeartbeatStore(client, "semaphore:cluster:node")
	repository := &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "old-boot"},
		LastSeenAt:          serverNow.Add(-8 * 24 * time.Hour),
	}}}
	registration := pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"},
		Edition:             "enhanced", Version: "1.2.3", Build: "abc", ProtocolVersion: 1,
		SchemaVersion: "2.20.23", Capabilities: []string{"workflows"},
	}
	registry := NewManagedNodeRegistry(repository, heartbeats, registration, time.Hour, 30*time.Second)

	require.NoError(t, registry.Start())
	t.Cleanup(registry.Stop)
	require.Len(t, repository.nodes, 1)
	assert.Equal(t, "boot-a", repository.nodes[0].BootID)
	assert.Equal(t, serverNow, repository.nodes[0].StartedAt)
	assert.Equal(t, serverNow, repository.nodes[0].LastSeenAt)
	assert.Equal(t, "node-a", registry.NodeID())
	assert.Equal(t, 1, registry.NodeCount())

	registry.Stop()
	assert.Equal(t, "semaphore:cluster:node:node-a:boot-a", client.deletedKey)
}

func TestManagedClusterInspectorDoesNotPresentStaleOrIncompatibleNodesAsReady(t *testing.T) {
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	client := &heartbeatClientFake{serverTime: serverNow, exists: true}
	heartbeats := NewRedisHeartbeatStore(client, "semaphore:cluster:node")
	repository := &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{
		{ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"}, ProtocolVersion: 1, SchemaVersion: "2.20.23", Capabilities: []string{"workflows"}, LastSeenAt: serverNow},
		{ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-b", BootID: "boot-b"}, ProtocolVersion: 1, SchemaVersion: "2.20.22", Capabilities: []string{"workflows"}, LastSeenAt: serverNow},
	}}
	inspector := NewManagedClusterInspector(repository, heartbeats,
		pro_interfaces.ClusterCompatibilityRequirements{ProtocolVersion: 1, SchemaVersion: "2.20.23"},
		pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"})

	nodes, err := inspector.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.True(t, nodes[0].Ready)
	assert.Equal(t, pro_interfaces.ClusterNodeCompatible, nodes[0].CompatibilityState)
	assert.False(t, nodes[1].Ready)
	assert.Equal(t, pro_interfaces.ClusterNodeIncompatibleSchema, nodes[1].CompatibilityState)
}

func TestManagedClusterInspectorKeepsSQLMembershipVisibleWhenRedisIsUnavailable(t *testing.T) {
	serverNow := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	repository := &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"},
		ProtocolVersion:     1,
		SchemaVersion:       "2.20.23",
		LastSeenAt:          serverNow,
	}}}
	inspector := NewManagedClusterInspector(repository, unavailableHeartbeatStore{},
		pro_interfaces.ClusterCompatibilityRequirements{ProtocolVersion: 1, SchemaVersion: "2.20.23"},
		pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"})

	nodes, err := inspector.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.False(t, nodes[0].Alive)
	assert.Equal(t, pro_interfaces.ClusterNodeStale, nodes[0].CompatibilityState)
	readiness := inspector.Readiness()
	assert.True(t, readiness.Ready)
	assert.False(t, readiness.AcceptingCoordinatedWork)
	assert.Equal(t, pro_interfaces.ClusterServiceDegradedLiveEvents, readiness.State)
}

func TestManagedClusterInspectorReadinessFailsClosedForDrainEditionAndDatabase(t *testing.T) {
	identity := pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"}
	requirements := pro_interfaces.ClusterCompatibilityRequirements{
		Edition: "enhanced", ProtocolVersion: 1, SchemaVersion: "2.20.23",
	}
	for name, testCase := range map[string]struct {
		repository    *clusterNodeRepositoryFake
		expectedState pro_interfaces.ClusterServiceReadinessState
	}{
		"draining": {
			repository: &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{{
				ClusterNodeIdentity: identity, Edition: "enhanced", ProtocolVersion: 1,
				SchemaVersion: "2.20.23", Draining: true,
			}}},
			expectedState: pro_interfaces.ClusterServiceReadinessState(pro_interfaces.ClusterNodeDraining),
		},
		"edition": {
			repository: &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{{
				ClusterNodeIdentity: identity, Edition: "community", ProtocolVersion: 1,
				SchemaVersion: "2.20.23",
			}}},
			expectedState: pro_interfaces.ClusterServiceReadinessState(pro_interfaces.ClusterNodeIncompatibleEdition),
		},
		"database": {
			repository:    &clusterNodeRepositoryFake{listErr: errors.New("database unavailable")},
			expectedState: pro_interfaces.ClusterServiceDatabaseUnavailable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			inspector := NewManagedClusterInspector(testCase.repository,
				NewRedisHeartbeatStore(&heartbeatClientFake{exists: true}, "semaphore:cluster:node"),
				requirements, identity)
			readiness := inspector.Readiness()
			assert.False(t, readiness.Ready)
			assert.False(t, readiness.AcceptingCoordinatedWork)
			assert.Equal(t, testCase.expectedState, readiness.State)
		})
	}
}

type unavailableHeartbeatStore struct{}

func (unavailableHeartbeatStore) Publish(context.Context, pro_interfaces.ClusterNodeIdentity, time.Duration) (time.Time, error) {
	return time.Time{}, assert.AnError
}

func (unavailableHeartbeatStore) IsLive(context.Context, pro_interfaces.ClusterNodeIdentity) (bool, time.Time, error) {
	return false, time.Time{}, assert.AnError
}

func (unavailableHeartbeatStore) Remove(context.Context, pro_interfaces.ClusterNodeIdentity) error {
	return nil
}

type heartbeatClientFake struct {
	serverTime time.Time
	key        string
	ttl        time.Duration
	exists     bool
	deletedKey string
}

func (f *heartbeatClientFake) ServerTime(context.Context) (time.Time, error) {
	return f.serverTime, nil
}

func (f *heartbeatClientFake) SetWithTTL(_ context.Context, key string, _ string, ttl time.Duration) error {
	f.key = key
	f.ttl = ttl
	return nil
}

func (f *heartbeatClientFake) Exists(_ context.Context, key string) (bool, error) {
	f.key = key
	return f.exists, nil
}

func (f *heartbeatClientFake) Delete(_ context.Context, key string) error {
	f.deletedKey = key
	return nil
}

type clusterNodeRepositoryFake struct {
	nodes   []pro_interfaces.ClusterNodeRegistration
	listErr error
}

func (f *clusterNodeRepositoryFake) UpsertClusterNode(node pro_interfaces.ClusterNodeRegistration) error {
	for index := range f.nodes {
		if f.nodes[index].BootID == node.BootID {
			f.nodes[index] = node
			return nil
		}
	}
	f.nodes = append(f.nodes, node)
	return nil
}

func (f *clusterNodeRepositoryFake) ListClusterNodes() ([]pro_interfaces.ClusterNodeRegistration, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]pro_interfaces.ClusterNodeRegistration{}, f.nodes...), nil
}

func (f *clusterNodeRepositoryFake) SetClusterNodeDraining(bootID string, draining bool) error {
	for index := range f.nodes {
		if f.nodes[index].BootID == bootID {
			f.nodes[index].Draining = draining
		}
	}
	return nil
}

func (f *clusterNodeRepositoryFake) DeleteClusterNodesLastSeenBefore(before time.Time) (int, error) {
	kept := f.nodes[:0]
	deleted := 0
	for _, node := range f.nodes {
		if node.LastSeenAt.Before(before) {
			deleted++
			continue
		}
		kept = append(kept, node)
	}
	f.nodes = kept
	return deleted, nil
}
