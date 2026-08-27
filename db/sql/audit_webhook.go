package sql

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

const auditWebhookConfigID = 1

func (d *SqlDb) GetAuditWebhookConfig() (config db.AuditWebhookConfig, err error) {
	err = d.selectOne(&config, "select * from audit_webhook_config where id=?", auditWebhookConfigID)
	return
}

func (d *SqlDb) SaveAuditWebhookConfig(config db.AuditWebhookConfig) (db.AuditWebhookConfig, error) {
	now := tz.Now()
	configArgs := formatArgs([]any{
		config.Endpoint, config.EncryptedCredential, config.CredentialConfigured, config.Paused, now, auditWebhookConfigID,
	})
	result, err := d.exec(
		"update audit_webhook_config set endpoint=?, encrypted_credential=?, credential_configured=?, paused=?, updated=? where id=?",
		configArgs...,
	)
	if err != nil {
		return db.AuditWebhookConfig{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.AuditWebhookConfig{}, err
	}
	if updated == 0 {
		insertArgs := formatArgs([]any{
			auditWebhookConfigID, config.Endpoint, config.EncryptedCredential, config.CredentialConfigured, config.Paused, now, now,
		})
		_, err = d.exec(
			"insert into audit_webhook_config(id, endpoint, encrypted_credential, credential_configured, paused, created, updated) values (?, ?, ?, ?, ?, ?, ?)",
			insertArgs...,
		)
		if err != nil {
			return db.AuditWebhookConfig{}, err
		}
		config.Created = now
	} else if config.Created.IsZero() {
		persisted, getErr := d.GetAuditWebhookConfig()
		if getErr != nil {
			return db.AuditWebhookConfig{}, getErr
		}
		config.Created = persisted.Created
	}
	config.ID = auditWebhookConfigID
	config.Updated = now
	return config, nil
}

func (d *SqlDb) CreateAuditWebhookDelivery(delivery db.AuditWebhookDelivery) (db.AuditWebhookDelivery, error) {
	now := tz.Now()
	if delivery.NextAttempt.IsZero() {
		delivery.NextAttempt = now
	}
	if delivery.Status == "" {
		delivery.Status = db.AuditWebhookDeliveryPending
	}
	id, err := d.insert("id",
		"insert into audit_webhook_delivery(event_id, payload, status, attempts, next_attempt, lease_until, http_status, last_error, created, updated, delivered_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		delivery.EventID, delivery.Payload, delivery.Status, delivery.Attempts, delivery.NextAttempt,
		delivery.LeaseUntil, delivery.HTTPStatus, delivery.LastError, now, now, delivery.DeliveredAt,
	)
	if err != nil {
		return db.AuditWebhookDelivery{}, err
	}
	delivery.ID = id
	delivery.Created = now
	delivery.Updated = now
	return delivery, nil
}

func (d *SqlDb) CreateEventWithAuditWebhook(event db.Event, delivery db.AuditWebhookDelivery) (db.Event, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.Event{}, err
	}
	created := tz.Now()
	eventArgs := formatArgs([]any{event.UserID, event.ProjectID, event.ObjectID, event.ObjectType, event.Description, created})
	if _, err = tx.Exec(d.PrepareQuery(
		"insert into event(user_id, project_id, object_id, object_type, description, created) values (?, ?, ?, ?, ?, ?)"),
		eventArgs...,
	); err != nil {
		_ = tx.Rollback()
		return db.Event{}, err
	}
	if delivery.NextAttempt.IsZero() {
		delivery.NextAttempt = created
	}
	if delivery.Status == "" {
		delivery.Status = db.AuditWebhookDeliveryPending
	}
	deliveryArgs := formatArgs([]any{
		delivery.EventID, delivery.Payload, delivery.Status, delivery.Attempts, delivery.NextAttempt,
		delivery.LeaseUntil, delivery.HTTPStatus, delivery.LastError, created, created, delivery.DeliveredAt,
	})
	if _, err = tx.Exec(d.PrepareQuery(
		"insert into audit_webhook_delivery(event_id, payload, status, attempts, next_attempt, lease_until, http_status, last_error, created, updated, delivered_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		deliveryArgs...,
	); err != nil {
		_ = tx.Rollback()
		return db.Event{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.Event{}, err
	}
	event.Created = created
	return event, nil
}

func (d *SqlDb) ClaimAuditWebhookDeliveries(now, leaseUntil time.Time, limit int) ([]db.AuditWebhookDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return nil, err
	}
	query, args, err := squirrel.Select("*").From("audit_webhook_delivery").
		Where("((status in (?, ?) and next_attempt <= ?) or (status = ? and lease_until <= ?))",
			db.AuditWebhookDeliveryPending, db.AuditWebhookDeliveryRetrying, now,
			db.AuditWebhookDeliveryRunning, now).
		OrderBy("next_attempt asc", "id asc").Limit(uint64(limit)).ToSql()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	var candidates []db.AuditWebhookDelivery
	if _, err = tx.Select(&candidates, d.PrepareQuery(query), formatArgs(args)...); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	claimed := make([]db.AuditWebhookDelivery, 0, len(candidates))
	for _, candidate := range candidates {
		updateArgs := formatArgs([]any{
			db.AuditWebhookDeliveryRunning, leaseUntil, now, candidate.ID,
			db.AuditWebhookDeliveryPending, db.AuditWebhookDeliveryRetrying, now,
			db.AuditWebhookDeliveryRunning, now,
		})
		result, updateErr := tx.Exec(d.PrepareQuery(
			"update audit_webhook_delivery set status=?, lease_until=?, updated=? where id=? and ((status in (?, ?) and next_attempt <= ?) or (status=? and lease_until <= ?))"),
			updateArgs...,
		)
		if updateErr != nil {
			_ = tx.Rollback()
			return nil, updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			_ = tx.Rollback()
			return nil, rowsErr
		}
		if rows == 1 {
			candidate.Status = db.AuditWebhookDeliveryRunning
			candidate.LeaseUntil = &leaseUntil
			claimed = append(claimed, candidate)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (d *SqlDb) MarkAuditWebhookDeliverySucceeded(id, statusCode int, now time.Time) error {
	return d.updateAuditWebhookDelivery(id, db.AuditWebhookDeliverySucceeded, &statusCode, "", now, now, true)
}

func (d *SqlDb) MarkAuditWebhookDeliveryRetrying(id int, statusCode *int, reason string, nextAttempt, now time.Time) error {
	return d.updateAuditWebhookDelivery(id, db.AuditWebhookDeliveryRetrying, statusCode, reason, nextAttempt, now, false)
}

func (d *SqlDb) MarkAuditWebhookDeliveryFailed(id int, statusCode *int, reason string, now time.Time) error {
	return d.updateAuditWebhookDelivery(id, db.AuditWebhookDeliveryFailed, statusCode, reason, now, now, false)
}

func (d *SqlDb) updateAuditWebhookDelivery(
	id int,
	status db.AuditWebhookDeliveryStatus,
	statusCode *int,
	reason string,
	nextAttempt time.Time,
	now time.Time,
	delivered bool,
) error {
	var deliveredAt *time.Time
	if delivered {
		deliveredAt = &now
	}
	updateArgs := formatArgs([]any{status, nextAttempt, statusCode, reason, now, deliveredAt, id, db.AuditWebhookDeliveryRunning})
	result, err := d.exec(
		"update audit_webhook_delivery set status=?, attempts=attempts+1, next_attempt=?, lease_until=null, http_status=?, last_error=?, updated=?, delivered_at=? where id=? and status=?",
		updateArgs...,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("audit webhook delivery %d is not claimed", id)
	}
	return nil
}

func (d *SqlDb) GetAuditWebhookDeliveries(params db.RetrieveQueryParams) ([]db.AuditWebhookDelivery, error) {
	query := squirrel.Select("*").From("audit_webhook_delivery").OrderBy("id desc")
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	if params.Offset > 0 {
		query = query.Offset(uint64(params.Offset))
	}
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var deliveries []db.AuditWebhookDelivery
	_, err = d.selectAll(&deliveries, sqlQuery, args...)
	return deliveries, err
}

func (d *SqlDb) GetAuditWebhookQueueHealth(_ time.Time) (db.AuditWebhookQueueHealth, error) {
	type queueHealthRow struct {
		Depth       int        `db:"depth"`
		OldestEvent *time.Time `db:"oldest_event"`
	}
	var row queueHealthRow
	err := d.selectOne(&row,
		"select count(*) as depth, min(created) as oldest_event from audit_webhook_delivery where status in (?, ?, ?)",
		db.AuditWebhookDeliveryPending, db.AuditWebhookDeliveryRetrying, db.AuditWebhookDeliveryRunning,
	)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.AuditWebhookQueueHealth{}, nil
	}
	return db.AuditWebhookQueueHealth{Depth: row.Depth, OldestEvent: row.OldestEvent}, err
}

var _ db.AuditWebhookRepository = (*SqlDb)(nil)
