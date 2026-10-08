package db

import (
	"context"
	"time"
)

// AuditExportBacklogRepository adds read-only delivery backlog observations to
// the shared export cursor contract.
type AuditExportBacklogRepository interface {
	AuditExportRepository
	GetAuditExportBacklog(ctx context.Context, destinationID string) (AuditExportBacklog, error)
}

// AuditExportBacklog is the durable delivery backlog for one destination.
// OldestPendingAge is derived from the database clock so HA nodes agree.
type AuditExportBacklog struct {
	PendingEvents    int64
	OldestPendingAge time.Duration
}
