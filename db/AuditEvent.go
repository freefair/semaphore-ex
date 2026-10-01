package db

import (
	"context"
	"time"
)

type AuditEvent struct {
	Seq                   int64     `db:"seq"`
	EventID               string    `db:"event_id"`
	Created               time.Time `db:"created"`
	SchemaVersion         string    `db:"schema_version"`
	Category              string    `db:"category"`
	EventCode             string    `db:"event_code"`
	Type                  string    `db:"type"`
	Action                string    `db:"action"`
	Outcome               string    `db:"outcome"`
	Reason                string    `db:"reason"`
	ActorType             string    `db:"actor_type"`
	ActorID               string    `db:"actor_id"`
	ActorName             string    `db:"actor_name"`
	ActorAuth             string    `db:"actor_auth"`
	ActorTokenFingerprint string    `db:"actor_token_fingerprint"`
	SourceIP              string    `db:"source_ip"`
	UserAgent             string    `db:"user_agent"`
	TargetType            string    `db:"target_type"`
	TargetID              string    `db:"target_id"`
	TargetName            string    `db:"target_name"`
	ProjectID             *int      `db:"project_id"`
	RequestID             string    `db:"request_id"`
	InstanceID            string    `db:"instance_id"`
	NodeID                string    `db:"node_id"`
	Metadata              string    `db:"metadata"`
}

type AuditEventManager interface {
	CreateAuditEvent(ctx context.Context, event AuditEvent) (AuditEvent, error)
}

// AuditExportRepository is the durable source of truth for audit delivery.
// Destination cursor updates are compare-and-swap operations so a stale HA
// exporter cannot advance another node's cursor.
type AuditExportRepository interface {
	InitializeAuditExportState(ctx context.Context, destinationID string) (cursor int64, err error)
	GetAuditEventsAfter(ctx context.Context, cursor int64, limit int) ([]AuditEvent, error)
	AdvanceAuditExportState(ctx context.Context, destinationID string, expectedCursor int64, nextCursor int64) (advanced bool, err error)
}
