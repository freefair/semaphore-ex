package ha

import (
	"context"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type managedClusterInspector struct {
	repository        pro_interfaces.ClusterNodeRepository
	heartbeats        pro_interfaces.ClusterHeartbeatStore
	diagnostics       pro_interfaces.ClusterRedisDiagnosticsClient
	requirements      pro_interfaces.ClusterCompatibilityRequirements
	self              pro_interfaces.ClusterNodeIdentity
	coordinatorHealth pro_interfaces.ClusterCoordinatorHealthSource
	drainer           pro_interfaces.OrphanCleaner
}

var _ pro_interfaces.ClusterInspector = (*managedClusterInspector)(nil)

func NewManagedClusterInspector(
	repository pro_interfaces.ClusterNodeRepository,
	heartbeats pro_interfaces.ClusterHeartbeatStore,
	requirements pro_interfaces.ClusterCompatibilityRequirements,
	self pro_interfaces.ClusterNodeIdentity,
	diagnostics ...pro_interfaces.ClusterRedisDiagnosticsClient,
) *managedClusterInspector {
	inspector := &managedClusterInspector{repository: repository, heartbeats: heartbeats, requirements: requirements, self: self}
	if len(diagnostics) > 0 {
		inspector.diagnostics = diagnostics[0]
	}
	return inspector
}

func (i *managedClusterInspector) Nodes() ([]pro_interfaces.NodeInfo, error) {
	nodes, err := i.repository.ListClusterNodes()
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		alive, observedAt, livenessErr := i.heartbeats.IsLive(context.Background(), node.ClusterNodeIdentity)
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
	return i.diagnostics.RedisDiagnostics(context.Background())
}

func (i *managedClusterInspector) CoordinatorHealth() pro_interfaces.ClusterCoordinatorHealth {
	if i.coordinatorHealth == nil {
		return pro_interfaces.ClusterCoordinatorHealth{SQLAuthoritative: true, LiveEvents: "unavailable"}
	}
	return i.coordinatorHealth.CoordinatorHealth()
}

func (i *managedClusterInspector) SetNodeDraining(bootID string, draining bool) error {
	self := bootID == i.self.BootID
	if self && draining && i.drainer != nil {
		if err := i.drainer.Drain(); err != nil {
			return err
		}
	}
	if err := i.repository.SetClusterNodeDraining(bootID, draining); err != nil {
		return err
	}
	if self && !draining && i.drainer != nil {
		i.drainer.Resume()
	}
	return nil
}
