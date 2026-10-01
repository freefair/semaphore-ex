package sql

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
)

// The counter row stays locked until commit, so committed seq has no gaps.
func (d *SqlDb) CreateAuditEvent(ctx context.Context, event db.AuditEvent) (db.AuditEvent, error) {
	if !json.Valid([]byte(event.Metadata)) {
		return event, errors.New("audit event metadata must be valid JSON")
	}
	// The context also bounds the wait for a free connection.
	tx, err := d.Sql().WithContext(ctx).(*gorp.DbMap).Begin()
	if err != nil {
		return event, err
	}
	exec := tx.WithContext(ctx)

	_, err = exec.Exec(d.PrepareQuery("update audit_event_sequence set last_seq = last_seq + 1 where id = 1"))
	if err != nil {
		handleRollbackError(tx.Rollback())
		return event, err
	}

	event.Seq, err = exec.SelectInt(d.PrepareQuery("select last_seq from audit_event_sequence where id = 1"))
	if err != nil {
		handleRollbackError(tx.Rollback())
		return event, err
	}

	event.Created, err = d.auditNow(exec)
	if err != nil {
		handleRollbackError(tx.Rollback())
		return event, err
	}

	if err = exec.Insert(&event); err != nil {
		handleRollbackError(tx.Rollback())
		return event, err
	}

	return event, tx.Commit()
}

func (d *SqlDb) InitializeAuditExportState(ctx context.Context, destinationID string) (int64, error) {
	tx, err := d.Sql().WithContext(ctx).(*gorp.DbMap).Begin()
	if err != nil {
		return 0, err
	}
	exec := tx.WithContext(ctx)

	var insert string
	switch d.GetDialect() {
	case "mysql":
		insert = "insert ignore into audit_export_state (destination_id, cursor_seq) select ?, coalesce(max(seq), 0) from audit_event"
	case "postgres":
		insert = "insert into audit_export_state (destination_id, cursor_seq) select ?, coalesce(max(seq), 0) from audit_event on conflict (destination_id) do nothing"
	default:
		insert = "insert or ignore into audit_export_state (destination_id, cursor_seq) select ?, coalesce(max(seq), 0) from audit_event"
	}
	if _, err = exec.Exec(d.PrepareQuery(insert), destinationID); err != nil {
		handleRollbackError(tx.Rollback())
		return 0, err
	}
	var cursor int64
	if err = exec.QueryRow(d.PrepareQuery("select cursor_seq from audit_export_state where destination_id = ?"), destinationID).Scan(&cursor); err != nil {
		handleRollbackError(tx.Rollback())
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return cursor, nil
}

func (d *SqlDb) GetAuditEventsAfter(ctx context.Context, cursor int64, limit int) ([]db.AuditEvent, error) {
	var events []db.AuditEvent
	_, err := d.Sql().WithContext(ctx).Select(&events, d.PrepareQuery("select * from audit_event where seq > ? order by seq asc limit ?"), cursor, limit)
	return events, err
}

func (d *SqlDb) AdvanceAuditExportState(ctx context.Context, destinationID string, expectedCursor int64, nextCursor int64) (bool, error) {
	result, err := d.Sql().WithContext(ctx).Exec(
		d.PrepareQuery("update audit_export_state set cursor_seq = ? where destination_id = ? and cursor_seq = ?"),
		nextCursor,
		destinationID,
		expectedCursor,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

// Database time, so HA nodes with skewed clocks agree.
func (d *SqlDb) auditNow(tx gorp.SqlExecutor) (time.Time, error) {
	var now time.Time
	switch d.Sql().Dialect.(type) {
	case gorp.MySQLDialect:
		// Text, because the driver would read a DATETIME in the DSN loc.
		var text string
		if err := tx.QueryRow("select date_format(utc_timestamp(6), '%Y-%m-%d %H:%i:%s.%f')").Scan(&text); err != nil {
			return now, err
		}
		return time.ParseInLocation("2006-01-02 15:04:05.000000", text, time.UTC)
	case gorp.PostgresDialect:
		err := tx.QueryRow("select clock_timestamp()").Scan(&now)
		return now.UTC(), err
	default:
		var text string
		if err := tx.QueryRow("select strftime('%Y-%m-%d %H:%M:%f', 'now')").Scan(&text); err != nil {
			return now, err
		}
		return time.Parse("2006-01-02 15:04:05.000", text)
	}
}
