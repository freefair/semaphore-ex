package pro_interfaces

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ClusterNodeIdentity separates the configured stable node identity from the
// boot identity that identifies one process lifetime.
type ClusterNodeIdentity struct {
	NodeID string `json:"node_id"`
	BootID string `json:"boot_id"`
}

// NewClusterNodeIdentity creates an identity for one server process.
// NodeID must come from stable operator configuration, while BootID is always
// newly generated so restarts remain observable even on the same host.
func NewClusterNodeIdentity(nodeID string) (ClusterNodeIdentity, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return ClusterNodeIdentity{}, errors.New("cluster node id is required")
	}
	bootBytes := make([]byte, 16)
	if _, err := rand.Read(bootBytes); err != nil {
		return ClusterNodeIdentity{}, err
	}
	return ClusterNodeIdentity{NodeID: nodeID, BootID: hex.EncodeToString(bootBytes)}, nil
}

// ClusterHeartbeatRedisClient is the narrow Redis dependency of the cluster
// registry. ServerTime is authoritative for observed timestamps and key TTLs
// are authoritative for liveness.
type ClusterHeartbeatRedisClient interface {
	ServerTime(ctx context.Context) (time.Time, error)
	SetWithTTL(ctx context.Context, key string, value string, ttl time.Duration) error
	Exists(ctx context.Context, key string) (bool, error)
	Delete(ctx context.Context, key string) error
}

type ClusterRedisDiagnosticsClient interface {
	RedisDiagnostics(ctx context.Context) (RedisInfo, error)
}

// ClusterHeartbeatStore is the live Redis projection of a durable SQL node
// registration. It does not use a node's local clock to determine liveness.
type ClusterHeartbeatStore interface {
	Publish(ctx context.Context, identity ClusterNodeIdentity, ttl time.Duration) (time.Time, error)
	IsLive(ctx context.Context, identity ClusterNodeIdentity) (bool, time.Time, error)
	Remove(ctx context.Context, identity ClusterNodeIdentity) error
}

type ClusterNodeCompatibilityState string

const (
	ClusterNodeCompatible               ClusterNodeCompatibilityState = "compatible"
	ClusterNodeDraining                 ClusterNodeCompatibilityState = "draining"
	ClusterNodeIncompatibleSchema       ClusterNodeCompatibilityState = "incompatible_schema"
	ClusterNodeIncompatibleProtocol     ClusterNodeCompatibilityState = "incompatible_protocol"
	ClusterNodeIncompatibleCapabilities ClusterNodeCompatibilityState = "incompatible_capabilities"
	ClusterNodeStale                    ClusterNodeCompatibilityState = "stale"
)

// ClusterNodeRegistration is the durable SQL record for one process lifetime.
// A new BootID creates a new row even when the stable NodeID is unchanged.
type ClusterNodeRegistration struct {
	ClusterNodeIdentity
	Edition         string    `json:"edition"`
	Version         string    `json:"version"`
	Build           string    `json:"build"`
	ProtocolVersion int       `json:"protocol_version"`
	SchemaVersion   string    `json:"schema_version"`
	Capabilities    []string  `json:"capabilities"`
	StartedAt       time.Time `json:"started_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	Draining        bool      `json:"draining"`
}

type ClusterCompatibilityRequirements struct {
	ProtocolVersion      int
	SchemaVersion        string
	RequiredCapabilities []string
}

type ClusterNodeCompatibility struct {
	State  ClusterNodeCompatibilityState `json:"state"`
	Ready  bool                          `json:"ready"`
	Reason string                        `json:"reason,omitempty"`
}

// ClusterNodeRepository persists durable membership history separately from
// the Redis live-heartbeat projection.
type ClusterNodeRepository interface {
	UpsertClusterNode(node ClusterNodeRegistration) error
	ListClusterNodes() ([]ClusterNodeRegistration, error)
	SetClusterNodeDraining(bootID string, draining bool) error
	DeleteClusterNodesLastSeenBefore(before time.Time) (int, error)
}

// EvaluateClusterNodeCompatibility is deterministic and deliberately separate
// from liveness: a Redis-live node can still be unsafe for coordinated work.
func EvaluateClusterNodeCompatibility(node ClusterNodeRegistration, required ClusterCompatibilityRequirements) ClusterNodeCompatibility {
	if node.ProtocolVersion != required.ProtocolVersion {
		return ClusterNodeCompatibility{State: ClusterNodeIncompatibleProtocol, Reason: "cluster protocol version differs"}
	}
	if node.SchemaVersion != required.SchemaVersion {
		return ClusterNodeCompatibility{State: ClusterNodeIncompatibleSchema, Reason: "database schema version differs"}
	}
	available := make(map[string]struct{}, len(node.Capabilities))
	for _, capability := range node.Capabilities {
		available[capability] = struct{}{}
	}
	for _, requiredCapability := range required.RequiredCapabilities {
		if _, exists := available[requiredCapability]; !exists {
			return ClusterNodeCompatibility{State: ClusterNodeIncompatibleCapabilities, Reason: "required cluster capability is unavailable"}
		}
	}
	if node.Draining {
		return ClusterNodeCompatibility{State: ClusterNodeDraining, Reason: "node is draining"}
	}
	return ClusterNodeCompatibility{State: ClusterNodeCompatible, Ready: true}
}
