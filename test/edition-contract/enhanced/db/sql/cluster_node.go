package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type ClusterNodeStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.ClusterNodeRepository = (*ClusterNodeStore)(nil)

func NewClusterNodeStore(connection *coresql.SqlDbConnection) *ClusterNodeStore {
	return &ClusterNodeStore{connection: connection}
}

func (s *ClusterNodeStore) UpsertClusterNode(ctx context.Context, node pro_interfaces.ClusterNodeRegistration) error {
	if s.connection == nil {
		return fmt.Errorf("cluster node database connection is required")
	}
	capabilities, err := json.Marshal(node.Capabilities)
	if err != nil {
		return fmt.Errorf("encode cluster node capabilities: %w", err)
	}
	updated, err := s.updateClusterNode(ctx, node, string(capabilities))
	if err != nil || updated {
		return err
	}
	_, err = s.connection.ExecContext(ctx,
		"insert into cluster__node(boot_id, node_id, edition, version, build, protocol_version, schema_version, capabilities, started_at, last_seen_at, draining, retired_at, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, null, ?)",
		node.BootID, node.NodeID, node.Edition, node.Version, node.Build, node.ProtocolVersion, node.SchemaVersion, string(capabilities), node.StartedAt, node.LastSeenAt, node.Draining, node.StartedAt,
	)
	if err == nil {
		return nil
	}
	// A concurrent heartbeat can insert the same boot identity after the
	// initial update observes no row. Retry the conditional update so the
	// history remains one row per process lifetime on every SQL dialect.
	updated, updateErr := s.updateClusterNode(ctx, node, string(capabilities))
	if updateErr == nil && updated {
		return nil
	}
	return err
}

func (s *ClusterNodeStore) updateClusterNode(ctx context.Context, node pro_interfaces.ClusterNodeRegistration, capabilities string) (bool, error) {
	result, err := s.connection.ExecContext(ctx,
		"update cluster__node set node_id=?, edition=?, version=?, build=?, protocol_version=?, schema_version=?, capabilities=?, last_seen_at=? where boot_id=?",
		node.NodeID, node.Edition, node.Version, node.Build, node.ProtocolVersion, node.SchemaVersion, capabilities, node.LastSeenAt, node.BootID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *ClusterNodeStore) ListClusterNodes(ctx context.Context) ([]pro_interfaces.ClusterNodeRegistration, error) {
	if s.connection == nil {
		return nil, fmt.Errorf("cluster node database connection is required")
	}
	var records []clusterNodeRecord
	if _, err := s.connection.SelectAllContext(ctx, &records, "select * from cluster__node order by last_seen_at desc, boot_id desc"); err != nil {
		return nil, err
	}
	nodes := make([]pro_interfaces.ClusterNodeRegistration, len(records))
	for index := range records {
		if err := records[index].decode(&nodes[index]); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (s *ClusterNodeStore) SetClusterNodeDraining(ctx context.Context, bootID string, draining bool) error {
	result, err := s.connection.ExecContext(ctx, "update cluster__node set draining=? where boot_id=?", draining, bootID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("cluster node not found")
	}
	return nil
}

func (s *ClusterNodeStore) DeleteClusterNodesLastSeenBefore(ctx context.Context, before time.Time) (int, error) {
	result, err := s.connection.ExecContext(ctx, "delete from cluster__node where last_seen_at<?", before)
	if err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}

type clusterNodeRecord struct {
	NodeID           string     `db:"node_id"`
	BootID           string     `db:"boot_id"`
	Edition          string     `db:"edition"`
	Version          string     `db:"version"`
	Build            string     `db:"build"`
	ProtocolVersion  int        `db:"protocol_version"`
	SchemaVersion    string     `db:"schema_version"`
	CapabilitiesJSON string     `db:"capabilities"`
	StartedAt        time.Time  `db:"started_at"`
	LastSeenAt       time.Time  `db:"last_seen_at"`
	Draining         bool       `db:"draining"`
	RetiredAt        *time.Time `db:"retired_at"`
	Created          time.Time  `db:"created"`
}

func (r *clusterNodeRecord) decode(node *pro_interfaces.ClusterNodeRegistration) error {
	*node = pro_interfaces.ClusterNodeRegistration{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: r.NodeID, BootID: r.BootID},
		Edition:             r.Edition,
		Version:             r.Version,
		Build:               r.Build,
		ProtocolVersion:     r.ProtocolVersion,
		SchemaVersion:       r.SchemaVersion,
		StartedAt:           r.StartedAt,
		LastSeenAt:          r.LastSeenAt,
		Draining:            r.Draining,
	}
	if err := json.Unmarshal([]byte(r.CapabilitiesJSON), &node.Capabilities); err != nil {
		return fmt.Errorf("decode cluster node capabilities: %w", err)
	}
	return nil
}
