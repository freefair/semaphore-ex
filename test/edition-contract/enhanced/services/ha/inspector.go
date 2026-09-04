package ha

import (
	"context"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const clusterDependencyTimeout = 5 * time.Second

type managedClusterInspector struct {
	repository        pro_interfaces.ClusterNodeRepository
	heartbeats        pro_interfaces.ClusterHeartbeatStore
	diagnostics       pro_interfaces.ClusterRedisDiagnosticsClient
	requirements      pro_interfaces.ClusterCompatibilityRequirements
	self              pro_interfaces.ClusterNodeIdentity
	coordinatorHealth pro_interfaces.ClusterCoordinatorHealthSource
	workflowHealth    pro_interfaces.WorkflowProgressionHealthSource
	drainers          []pro_interfaces.ClusterDrainer
	drainMu           sync.Mutex
	dependencyTimeout time.Duration
}

var _ pro_interfaces.ClusterInspector = (*managedClusterInspector)(nil)
var _ pro_interfaces.ClusterReadinessProvider = (*managedClusterInspector)(nil)

func NewManagedClusterInspector(
	repository pro_interfaces.ClusterNodeRepository,
	heartbeats pro_interfaces.ClusterHeartbeatStore,
	requirements pro_interfaces.ClusterCompatibilityRequirements,
	self pro_interfaces.ClusterNodeIdentity,
	diagnostics ...pro_interfaces.ClusterRedisDiagnosticsClient,
) *managedClusterInspector {
	inspector := &managedClusterInspector{
		repository: repository, heartbeats: heartbeats, requirements: requirements, self: self,
		dependencyTimeout: clusterDependencyTimeout,
	}
	if len(diagnostics) > 0 {
		inspector.diagnostics = diagnostics[0]
	}
	return inspector
}

func (i *managedClusterInspector) Nodes() ([]pro_interfaces.NodeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout())
	defer cancel()
	nodes, err := i.repository.ListClusterNodes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		alive, observedAt, livenessErr := i.heartbeats.IsLive(ctx, node.ClusterNodeIdentity)
		compatibility := pro_interfaces.EvaluateClusterNodeCompatibility(node, i.requirements)
		if livenessErr != nil || !alive {
			compatibility = pro_interfaces.ClusterNodeCompatibility{State: pro_interfaces.ClusterNodeStale, Reason: "redis heartbeat expired"}
		}
		result = append(result, pro_interfaces.NodeInfo{
			NodeID: node.NodeID, BootID: node.BootID, LastHeartbeat: node.LastSeenAt, ObservedAt: observedAt,
			Alive: alive, Ready: alive && compatibility.Ready,
			IsSelf:    node.NodeID == i.self.NodeID && node.BootID == i.self.BootID,
			StartedAt: node.StartedAt, Edition: node.Edition, Version: node.Version, Build: node.Build,
			Capabilities: append([]string{}, node.Capabilities...), Draining: node.Draining,
			CompatibilityState: compatibility.State, CompatibilityReason: compatibility.Reason,
		})
	}
	return result, nil
}

func (i *managedClusterInspector) RedisInfo() (pro_interfaces.RedisInfo, error) {
	if i.diagnostics == nil {
		return pro_interfaces.RedisInfo{Connected: false, KeyGroups: map[string]int{}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout())
	defer cancel()
	return i.diagnostics.RedisDiagnostics(ctx)
}

func (i *managedClusterInspector) CoordinatorHealth() pro_interfaces.ClusterCoordinatorHealth {
	var health pro_interfaces.ClusterCoordinatorHealth
	if i.coordinatorHealth == nil {
		health = pro_interfaces.ClusterCoordinatorHealth{SQLAuthoritative: true, LiveEvents: "unavailable"}
	} else {
		health = i.coordinatorHealth.CoordinatorHealth()
	}
	if i.workflowHealth != nil {
		if workflow, err := i.workflowHealth.WorkflowProgressionHealth(); err == nil {
			health.WorkflowProgression = &workflow
		}
	}
	return health
}

func (i *managedClusterInspector) Readiness() pro_interfaces.ClusterServiceReadiness {
	result := pro_interfaces.ClusterServiceReadiness{NodeID: i.self.NodeID, BootID: i.self.BootID}
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout())
	defer cancel()
	nodes, err := i.repository.ListClusterNodes(ctx)
	if err != nil {
		result.State = pro_interfaces.ClusterServiceDatabaseUnavailable
		result.Reason = "cluster registration database is unavailable"
		return result
	}
	for _, node := range nodes {
		if node.BootID != i.self.BootID {
			continue
		}
		compatibility := pro_interfaces.EvaluateClusterNodeCompatibility(node, i.requirements)
		if !compatibility.Ready {
			result.State = pro_interfaces.ClusterServiceReadinessState(compatibility.State)
			result.Reason = compatibility.Reason
			return result
		}
		result.Ready = true
		alive, _, heartbeatErr := i.heartbeats.IsLive(ctx, i.self)
		if heartbeatErr != nil || !alive {
			result.State = pro_interfaces.ClusterServiceDegradedLiveEvents
			result.Reason = "redis heartbeat is unavailable; SQL API traffic remains available"
			return result
		}
		result.AcceptingCoordinatedWork = true
		result.State = pro_interfaces.ClusterServiceReady
		return result
	}
	result.State = pro_interfaces.ClusterServiceRegistrationPending
	result.Reason = "cluster registration is not visible yet"
	return result
}

func (i *managedClusterInspector) SetNodeDraining(bootID string, draining bool) error {
	i.drainMu.Lock()
	defer i.drainMu.Unlock()
	self := bootID == i.self.BootID
	drained := make([]pro_interfaces.ClusterDrainer, 0, len(i.drainers))
	resumeDrained := func() {
		for index := len(drained) - 1; index >= 0; index-- {
			drained[index].Resume()
		}
	}
	if self && draining {
		for _, drainer := range i.drainers {
			if drainer == nil {
				continue
			}
			if err := drainer.Drain(); err != nil {
				resumeDrained()
				return err
			}
			drained = append(drained, drainer)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), i.timeout())
	defer cancel()
	if err := i.repository.SetClusterNodeDraining(ctx, bootID, draining); err != nil {
		if self && draining {
			resumeDrained()
		}
		return err
	}
	if self && !draining {
		for _, drainer := range i.drainers {
			if drainer != nil {
				drainer.Resume()
			}
		}
	}
	return nil
}

func (i *managedClusterInspector) timeout() time.Duration {
	if i.dependencyTimeout > 0 {
		return i.dependencyTimeout
	}
	return clusterDependencyTimeout
}
