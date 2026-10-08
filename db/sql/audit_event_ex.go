package sql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// GetAuditExportBacklog returns every event after the durable cursor, without
// applying the delivery batch limit.
func (d *SqlDb) GetAuditExportBacklog(ctx context.Context, destinationID string) (db.AuditExportBacklog, error) {
	exec := d.Sql().WithContext(ctx)
	now, err := d.auditNow(exec)
	if err != nil {
		return db.AuditExportBacklog{}, err
	}

	var oldest sql.NullString
	var pending int64
	err = exec.QueryRow(d.PrepareQuery(`
		select count(audit_event.seq), min(audit_event.created)
		from audit_event
		join audit_export_state on audit_export_state.destination_id = ?
		where audit_event.seq > audit_export_state.cursor_seq
	`), destinationID).Scan(&pending, &oldest)
	if err != nil {
		return db.AuditExportBacklog{}, err
	}
	if !oldest.Valid {
		return db.AuditExportBacklog{}, nil
	}

	oldestTime, err := parseAuditTimestamp(oldest.String)
	if err != nil {
		return db.AuditExportBacklog{}, err
	}
	age := now.Sub(oldestTime)
	if age < 0 {
		age = 0
	}
	return db.AuditExportBacklog{PendingEvents: pending, OldestPendingAge: age}, nil
}

func parseAuditTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse audit timestamp %q", value)
}
