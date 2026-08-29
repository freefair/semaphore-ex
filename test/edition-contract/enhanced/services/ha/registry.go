package ha

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

var (
	errClusterRegistryDependency    = errors.New("cluster registry dependencies are required")
	errClusterRegistryConfiguration = errors.New("cluster registry configuration is invalid")
)

// clusterHistoryRetention preserves node process history for seven days. Live
// membership remains a Redis TTL projection; SQL history is pruned only after
// it has been stale for this documented operational troubleshooting window.
const clusterHistoryRetention = 7 * 24 * time.Hour

type managedNodeRegistry struct {
	repository pro_interfaces.ClusterNodeRepository
	heartbeats pro_interfaces.ClusterHeartbeatStore
	node       pro_interfaces.ClusterNodeRegistration
	interval   time.Duration
	ttl        time.Duration

	mutex     sync.RWMutex
	startOnce sync.Once
	stopOnce  sync.Once
	startErr  error
	cancel    context.CancelFunc
	done      chan struct{}
}

var _ pro_interfaces.NodeRegistry = (*managedNodeRegistry)(nil)

// NewManagedNodeRegistry composes durable SQL membership with Redis TTL
// liveness. The registry owns one boot identity and never mutates history for
// a different boot identity.
func NewManagedNodeRegistry(
	repository pro_interfaces.ClusterNodeRepository,
	heartbeats pro_interfaces.ClusterHeartbeatStore,
	node pro_interfaces.ClusterNodeRegistration,
	interval time.Duration,
	ttl time.Duration,
) pro_interfaces.NodeRegistry {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &managedNodeRegistry{
		repository: repository,
		heartbeats: heartbeats,
		node:       node,
		interval:   interval,
		ttl:        ttl,
	}
}

func (r *managedNodeRegistry) Start() error {
	r.startOnce.Do(func() {
		if r.repository == nil || r.heartbeats == nil {
			r.startErr = errClusterRegistryDependency
			return
		}
		if r.node.NodeID == "" || r.node.BootID == "" || r.ttl <= 0 {
			r.startErr = errClusterRegistryConfiguration
			return
		}
		if r.startErr = r.refresh(context.Background(), true); r.startErr != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.done = make(chan struct{})
		go r.run(ctx)
	})
	return r.startErr
}

func (r *managedNodeRegistry) Stop() {
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
			<-r.done
		}
		if r.heartbeats != nil {
			_ = r.heartbeats.Remove(context.Background(), r.node.ClusterNodeIdentity)
		}
	})
}

func (r *managedNodeRegistry) NodeCount() int {
	if r.repository == nil {
		return 0
	}
	nodes, err := r.repository.ListClusterNodes()
	if err != nil {
		return 0
	}
	return len(nodes)
}

func (r *managedNodeRegistry) NodeID() string { return r.node.NodeID }

func (r *managedNodeRegistry) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = r.refresh(ctx, false)
		case <-ctx.Done():
			return
		}
	}
}

func (r *managedNodeRegistry) refresh(ctx context.Context, starting bool) error {
	lastSeen, err := r.heartbeats.Publish(ctx, r.node.ClusterNodeIdentity, r.ttl)
	if err != nil {
		return err
	}
	r.mutex.Lock()
	if starting || r.node.StartedAt.IsZero() {
		r.node.StartedAt = lastSeen
	}
	r.node.LastSeenAt = lastSeen
	node := r.node
	r.mutex.Unlock()
	if err := r.repository.UpsertClusterNode(node); err != nil {
		return err
	}
	_, err = r.repository.DeleteClusterNodesLastSeenBefore(lastSeen.Add(-clusterHistoryRetention))
	return err
}
