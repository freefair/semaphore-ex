// Package ha adapts the public Community surface for the workspace fixture.
package ha

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/semaphoreui/semaphore/api/sockets"
	community "github.com/semaphoreui/semaphore/community-pro/services/ha"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	clusterSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
)

type NodeRegistry = community.NodeRegistry
type OrphanCleaner = community.OrphanCleaner
type ClusterInspector = community.ClusterInspector

var clusterIdentityState struct {
	sync.Mutex
	nodeID   string
	identity pro_interfaces.ClusterNodeIdentity
}

func NewWorkflowRunLocker(store db.Store) pro_interfaces.WorkflowRunLocker {
	if !util.HAEnabled() || util.Config.HA == nil || util.Config.HA.NodeID == "" {
		return nil
	}
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return nil
	}
	return NewManagedWorkflowRunLocker(
		clusterSQL.NewWorkflowReconciliationStore(connectionStore.GetConnection()),
		identity.BootID,
		15*time.Second,
	)
}

// NewScheduleDeduplicator binds every Enhanced scheduler to durable SQL
// occurrence authority. HA supplies its configured boot identity; single-node
// mode derives a process-lifetime identity so a restart cannot revive a
// terminal blocked occurrence. Community still receives nil from its own
// replaceable implementation.
func NewScheduleDeduplicator(store db.Store) schedules.ScheduleDeduplicator {
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	nodeID := "single-node"
	if util.HAEnabled() {
		if util.Config.HA == nil || util.Config.HA.NodeID == "" {
			return nil
		}
		nodeID = util.Config.HA.NodeID
	}
	identity, err := clusterIdentity(nodeID)
	if err != nil {
		return nil
	}
	return NewManagedScheduleDeduplicator(clusterSQL.NewScheduleCoordinatorStore(connectionStore.GetConnection()), identity.BootID)
}

func NewWSBroadcaster(store db.Store) sockets.Broadcaster {
	if !util.HAEnabled() || util.Config.HA == nil || util.Config.HA.NodeID == "" || util.Config.HA.Redis == nil || util.Config.HA.Redis.Addr == "" {
		return nil
	}
	if _, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	}); !ok {
		return nil
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return nil
	}
	options, err := redisOptionsForHA(util.Config.HA.Redis)
	if err != nil {
		return nil
	}
	return NewManagedWSBroadcaster(NewGoRedisEventTransport(redis.NewClient(options)), identity.BootID, sockets.LocalBroadcast, coordinatorHealthFor(util.Config.HA.NodeID))
}

func NewTaskExecutionEvidenceRecorder(store db.Store) db.TaskExecutionEvidenceRecorder {
	if !util.HAEnabled() {
		return nil
	}
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	return clusterSQL.NewTaskControlStore(connectionStore.GetConnection())
}

func NewOrphanCleaner(store db.Store, pool *tasks.TaskPool) OrphanCleaner {
	if !util.HAEnabled() || pool == nil || util.Config.HA == nil || util.Config.HA.NodeID == "" || util.Config.HA.Redis == nil || util.Config.HA.Redis.Addr == "" {
		return nil
	}
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return nil
	}
	redisOptions, err := redisOptionsForHA(util.Config.HA.Redis)
	if err != nil {
		return nil
	}
	connection := connectionStore.GetConnection()
	heartbeats := NewRedisHeartbeatStore(NewGoRedisHeartbeatClient(redis.NewClient(redisOptions)), defaultClusterHeartbeatPrefix)
	repository := clusterSQL.NewClusterNodeStore(connection)
	requirements := pro_interfaces.ClusterCompatibilityRequirements{
		Edition:         util.BuildEdition,
		ProtocolVersion: clusterProtocolVersion, SchemaVersion: currentSchemaVersion(connection.GetDialect()),
		RequiredCapabilities: []string{"cluster-dashboard"},
	}
	readiness := func() (bool, error) {
		nodes, registrationErr := repository.ListClusterNodes()
		if registrationErr != nil {
			return false, registrationErr
		}
		var registration pro_interfaces.ClusterNodeRegistration
		found := false
		for _, node := range nodes {
			if node.BootID == identity.BootID {
				registration = node
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
		live, _, heartbeatErr := heartbeats.IsLive(context.Background(), identity)
		if heartbeatErr != nil || !live {
			return false, heartbeatErr
		}
		return pro_interfaces.EvaluateClusterNodeCompatibility(registration, requirements).Ready, nil
	}
	cleaner := NewManagedOrphanCleaner(
		clusterSQL.NewTaskControlStore(connection), pool, identity.BootID, readiness,
		5*time.Second, 20*time.Second, 30*time.Second,
	)
	pool.SetTaskControlLifecycle(cleaner)
	return cleaner
}

const clusterProtocolVersion = 1

// NewNodeRegistry enables durable cluster membership only when HA has an
// explicit stable node ID and a Redis endpoint. An auto-generated process ID
// would make restart history and operator diagnostics ambiguous.
func NewNodeRegistry(store db.Store) NodeRegistry {
	if !util.HAEnabled() {
		return nil
	}
	if util.Config.HA == nil || util.Config.HA.NodeID == "" || util.Config.HA.Redis == nil || util.Config.HA.Redis.Addr == "" {
		return failedNodeRegistry{err: errors.New("ha requires an explicit node_id and redis address")}
	}
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return failedNodeRegistry{err: errors.New("ha requires a SQL connection store")}
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return failedNodeRegistry{err: err}
	}
	redisOptions, err := redisOptionsForHA(util.Config.HA.Redis)
	if err != nil {
		return failedNodeRegistry{err: err}
	}
	connection := connectionStore.GetConnection()
	registration := pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: identity,
		Edition:             util.BuildEdition,
		Version:             util.Version(),
		Build:               util.Commit,
		ProtocolVersion:     clusterProtocolVersion,
		SchemaVersion:       currentSchemaVersion(connection.GetDialect()),
		Capabilities:        []string{"cluster-dashboard"},
	}
	redisClient := NewGoRedisHeartbeatClient(redis.NewClient(redisOptions))
	return NewManagedNodeRegistry(
		clusterSQL.NewClusterNodeStore(connection),
		NewRedisHeartbeatStore(redisClient, defaultClusterHeartbeatPrefix),
		registration,
		10*time.Second,
		30*time.Second,
	)
}

func NewClusterInspector(store db.Store, drainers ...pro_interfaces.ClusterDrainer) ClusterInspector {
	if !util.HAEnabled() || util.Config.HA == nil || util.Config.HA.NodeID == "" || util.Config.HA.Redis == nil || util.Config.HA.Redis.Addr == "" {
		return nil
	}
	connectionStore, ok := store.(interface {
		GetConnection() *coresql.SqlDbConnection
	})
	if !ok {
		return nil
	}
	identity, err := clusterIdentity(util.Config.HA.NodeID)
	if err != nil {
		return nil
	}
	redisOptions, err := redisOptionsForHA(util.Config.HA.Redis)
	if err != nil {
		return nil
	}
	connection := connectionStore.GetConnection()
	redisClient := NewGoRedisHeartbeatClient(redis.NewClient(redisOptions))
	inspector := NewManagedClusterInspector(
		clusterSQL.NewClusterNodeStore(connection),
		NewRedisHeartbeatStore(redisClient, defaultClusterHeartbeatPrefix),
		pro_interfaces.ClusterCompatibilityRequirements{
			Edition:              util.BuildEdition,
			ProtocolVersion:      clusterProtocolVersion,
			SchemaVersion:        currentSchemaVersion(connection.GetDialect()),
			RequiredCapabilities: []string{"cluster-dashboard"},
		},
		identity,
		redisClient,
	)
	inspector.coordinatorHealth = coordinatorHealthFor(util.Config.HA.NodeID)
	inspector.drainers = append(inspector.drainers, drainers...)
	for _, drainer := range drainers {
		if source, ok := drainer.(pro_interfaces.WorkflowProgressionHealthSource); ok {
			inspector.workflowHealth = source
			break
		}
	}
	return inspector
}

func clusterIdentity(nodeID string) (pro_interfaces.ClusterNodeIdentity, error) {
	clusterIdentityState.Lock()
	defer clusterIdentityState.Unlock()
	if clusterIdentityState.identity.BootID != "" && clusterIdentityState.nodeID == nodeID {
		return clusterIdentityState.identity, nil
	}
	identity, err := pro_interfaces.NewClusterNodeIdentity(nodeID)
	if err != nil {
		return pro_interfaces.ClusterNodeIdentity{}, err
	}
	clusterIdentityState.nodeID = nodeID
	clusterIdentityState.identity = identity
	return identity, nil
}

func redisOptionsForHA(config *util.HARedisConfig) (*redis.Options, error) {
	scheme := "redis"
	if config.TLS {
		scheme = "rediss"
	}
	dsn := fmt.Sprintf("%s://%s:%s@%s/%d", scheme, url.PathEscape(config.User), url.PathEscape(config.Pass), config.Addr, config.DB)
	options, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse HA redis configuration: %w", err)
	}
	if config.TLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: config.TLSSkipVerify}
	}
	return options, nil
}

func currentSchemaVersion(dialect string) string {
	migrations := db.GetMigrations(dialect)
	if len(migrations) == 0 {
		return ""
	}
	return migrations[len(migrations)-1].Version
}

type failedNodeRegistry struct{ err error }

func (r failedNodeRegistry) Start() error { return r.err }
func (failedNodeRegistry) Stop()          {}
func (failedNodeRegistry) NodeCount() int { return 0 }
func (failedNodeRegistry) NodeID() string { return "" }
