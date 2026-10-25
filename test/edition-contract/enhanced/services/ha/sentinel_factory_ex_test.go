package ha

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHAFactoriesBorrowOneConnectionAndKeepSQLAuthority(t *testing.T) {
	preserveFactoryIdentity(t)
	old := util.Config
	t.Cleanup(func() { util.Config = old })
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	server := miniredis.RunT(t)
	util.Config.HA = &util.HAConfig{Enabled: true, NodeID: "factory-node", Redis: &util.HARedisConfig{Addr: server.Addr()}}
	shared := NewHARedis()
	require.NotNil(t, shared)
	t.Cleanup(func() { require.NoError(t, shared.Close()) })
	require.NoError(t, shared.Ping(context.Background()))
	// Consumers must borrow the supplied pool, even if a different backend is
	// present in configuration. Discovery itself is tested at the connection seam.
	util.Config.HA.Engine = util.HAEngineRedisSentinel
	util.Config.HA.Redis = nil
	util.Config.HA.RedisSentinel = &util.HARedisSentinelConfig{MasterName: "factory", Addrs: []string{"127.0.0.1:1"}}
	registry := NewNodeRegistry(shared, database)
	require.NotNil(t, registry)
	require.NoError(t, registry.Start())
	t.Cleanup(registry.Stop)
	inspector := NewClusterInspector(shared, database)
	require.NotNil(t, inspector)
	nodes, err := inspector.Nodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.True(t, nodes[0].Ready)

	broadcaster := NewWSBroadcaster(shared, database).(*managedWSBroadcaster)
	// Replace only the local delivery sink; use the factory's real transport.
	broadcaster.deliver = func(int, []byte) {}
	broadcaster.Start()
	t.Cleanup(broadcaster.Stop)
	require.Eventually(t, func() bool {
		select {
		case <-broadcaster.ready:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	received := make(chan []byte, 1)
	client, err := sharedRedisClient(shared)
	require.NoError(t, err)
	other := NewManagedWSBroadcaster(NewGoRedisEventTransport(client), "other-boot", func(_ int, p []byte) { received <- p })
	other.Start()
	t.Cleanup(other.Stop)
	require.Eventually(t, func() bool {
		select {
		case <-other.ready:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	broadcaster.Publish(1, []byte(`{"type":"fixture"}`))
	select {
	case payload := <-received:
		assert.JSONEq(t, `{"type":"fixture"}`, string(payload))
	case <-time.After(time.Second):
		require.FailNow(t, "factory event transport did not deliver")
	}

	pool := &tasks.TaskPool{}
	cleaner := NewOrphanCleaner(shared, database, pool)
	require.NotNil(t, cleaner)
	assert.Implements(t, (*pro_interfaces.ClusterDrainer)(nil), cleaner)
	// These factories retain SQL semantics even though the upstream seam now
	// carries a Redis handle. A real occurrence is claimed and deduplicated.
	dedup := NewScheduleDeduplicator(shared, database)
	require.NotNil(t, dedup)
	occurrence := schedules.ScheduleOccurrence{ScheduleID: 9123, Revision: "fixture", IntendedAt: time.Now().UTC()}
	lease, acquired, err := dedup.ClaimScheduleOccurrence(occurrence)
	require.NoError(t, err)
	require.True(t, acquired)
	competitor := NewScheduleDeduplicator(shared, database).(*managedScheduleDeduplicator)
	competitor.ownerBootID = "competing-boot"
	_, acquired, err = competitor.ClaimScheduleOccurrence(occurrence)
	require.NoError(t, err)
	assert.False(t, acquired)
	_, err = lease.Release()
	require.NoError(t, err)
	locker := NewWorkflowRunLocker(shared, database)
	require.NotNil(t, locker)
	assert.Implements(t, (*pro_interfaces.ClusterDrainer)(nil), locker)

	leaser := NewAuditExportLeaser(shared)
	auditLease, acquired, err := leaser.TryAcquire(context.Background(), "factory-audit")
	require.NoError(t, err)
	require.True(t, acquired)
	_, acquired, err = NewAuditExportLeaser(shared).TryAcquire(context.Background(), "factory-audit")
	require.NoError(t, err)
	assert.False(t, acquired)
	auditLease.Release()
	broadcaster.Stop()
	other.Stop()
	registry.Stop()
	require.NoError(t, shared.Ping(context.Background()), "borrower shutdown must not close the pool")
}

func TestHAFactoriesRejectMissingSharedRedis(t *testing.T) {
	preserveFactoryIdentity(t)
	old := util.Config
	t.Cleanup(func() { util.Config = old })
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	util.Config.HA = &util.HAConfig{Enabled: true, NodeID: "missing-pool"}
	require.Error(t, NewNodeRegistry(nil, database).Start())
	assert.Nil(t, NewClusterInspector(nil, database))
	assert.Nil(t, NewWSBroadcaster(nil, database))
	assert.Nil(t, NewOrphanCleaner(nil, database, &tasks.TaskPool{}))
	_, acquired, err := NewAuditExportLeaser(nil).TryAcquire(context.Background(), "fixture")
	assert.False(t, acquired)
	require.Error(t, err)
}

type diagnosticResult struct {
	info pro_interfaces.RedisInfo
	err  error
}

func (d diagnosticResult) RedisDiagnostics(context.Context) (pro_interfaces.RedisInfo, error) {
	return d.info, d.err
}

type backendDescription struct{ pro_interfaces.HARedis }

func (backendDescription) Describe() string { return "sentinel/fixture@127.0.0.1:26379" }
func TestSharedRedisDiagnosticsPreservesStateAndReportsConfiguredBackend(t *testing.T) {
	for _, connected := range []bool{false, true} {
		source := pro_interfaces.RedisInfo{Connected: connected, Addr: "localhost:6379", Version: "fixture", TotalKeys: 3}
		info, err := (sharedRedisDiagnostics{client: diagnosticResult{info: source}, shared: backendDescription{}}).RedisDiagnostics(context.Background())
		require.NoError(t, err)
		assert.Equal(t, connected, info.Connected)
		assert.Equal(t, 3, info.TotalKeys)
		assert.Equal(t, "sentinel/fixture@127.0.0.1:26379", info.Addr)
	}
}

func preserveFactoryIdentity(t *testing.T) {
	t.Helper()
	clusterIdentityState.Lock()
	previousNode, previousIdentity := clusterIdentityState.nodeID, clusterIdentityState.identity
	clusterIdentityState.Unlock()
	t.Cleanup(func() {
		clusterIdentityState.Lock()
		clusterIdentityState.nodeID, clusterIdentityState.identity = previousNode, previousIdentity
		clusterIdentityState.Unlock()
	})
}
