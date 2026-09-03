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

// CompareAndSwapAuditWebhookSigningState persists only the versioned signing
// state. The historical optional Bearer credential remains untouched so key
// rotation cannot accidentally overwrite receiver authentication.
func (d *SqlDb) CompareAndSwapAuditWebhookSigningState(config db.AuditWebhookConfig, expectedRevision int) (db.AuditWebhookConfig, error) {
	if expectedRevision < 0 || config.SigningStateRevision != expectedRevision {
		return db.AuditWebhookConfig{}, db.ErrAuditWebhookSigningStateConflict
	}
	if err := db.ValidateAuditWebhookSigningState(config); err != nil {
		return db.AuditWebhookConfig{}, err
	}
	now := tz.Now()
	nextRevision := expectedRevision + 1
	configArgs := formatArgs([]any{
		config.CurrentSigningSecretEncrypted, config.NextSigningSecretEncrypted,
		config.CurrentSigningKeyID, config.NextSigningKeyID,
		config.CurrentSigningGeneration, config.NextSigningGeneration,
		nextRevision, now, auditWebhookConfigID, expectedRevision,
	})
	result, err := d.exec(
		"update audit_webhook_config set current_signing_secret_encrypted=?, next_signing_secret_encrypted=?, current_signing_key_id=?, next_signing_key_id=?, current_signing_generation=?, next_signing_generation=?, signing_state_revision=?, updated=? where id=? and signing_state_revision=?",
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
		if expectedRevision != 0 {
			return db.AuditWebhookConfig{}, db.ErrAuditWebhookSigningStateConflict
		}
		insertArgs := formatArgs([]any{
			auditWebhookConfigID, config.Endpoint, config.EncryptedCredential, config.CredentialConfigured, config.Paused,
			config.CurrentSigningSecretEncrypted, config.NextSigningSecretEncrypted,
			config.CurrentSigningKeyID, config.NextSigningKeyID,
			config.CurrentSigningGeneration, config.NextSigningGeneration,
			nextRevision, now, now,
		})
		_, err = d.exec(
			"insert into audit_webhook_config(id, endpoint, encrypted_credential, credential_configured, paused, current_signing_secret_encrypted, next_signing_secret_encrypted, current_signing_key_id, next_signing_key_id, current_signing_generation, next_signing_generation, signing_state_revision, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
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
	config.SigningStateRevision = nextRevision
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

func (d *SqlDb) RecordAuditWebhookDeliveryAttempt(attempt db.AuditWebhookDeliveryAttempt) (db.AuditWebhookDeliveryAttempt, error) {
	if err := db.ValidateAuditWebhookDeliveryAttempt(attempt); err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	if attempt.Outcome != db.AuditWebhookDeliveryAttemptStarted {
		return db.AuditWebhookDeliveryAttempt{}, errors.New("audit webhook delivery attempt must start before completion")
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()

	type parent struct {
		EventID string `db:"event_id"`
	}
	var delivery parent
	if err = tx.SelectOne(&delivery, d.PrepareQuery("select event_id from audit_webhook_delivery where id=?"), attempt.DeliveryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.AuditWebhookDeliveryAttempt{}, db.ErrNotFound
		}
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	if delivery.EventID != attempt.EventID {
		return db.AuditWebhookDeliveryAttempt{}, db.ErrAuditWebhookDeliveryAttemptConflict
	}

	now := tz.Now()
	updateArgs := formatArgs([]any{
		attempt.Attempt, attempt.SignedAt, now, attempt.DeliveryID, db.AuditWebhookDeliveryRunning, attempt.Attempt - 1, attempt.SignedAt,
	})
	result, err := tx.Exec(d.PrepareQuery(
		"update audit_webhook_delivery set attempts=?, last_signed_at=?, updated=? where id=? and status=? and attempts=? and (last_signed_at is null or last_signed_at<?)"),
		updateArgs...,
	)
	if err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	if updated != 1 {
		return db.AuditWebhookDeliveryAttempt{}, db.ErrAuditWebhookDeliveryAttemptConflict
	}

	insertArgs := formatArgs([]any{
		attempt.DeliveryID, attempt.EventID, attempt.Attempt, attempt.KeyID, attempt.SignedAt,
		attempt.Outcome, attempt.HTTPStatus, attempt.Reason, now, attempt.CompletedAt,
	})
	insertResult, err := tx.Exec(d.PrepareQuery(
		"insert into audit_webhook_delivery_attempt(delivery_id, event_id, attempt, key_id, signed_at, outcome, http_status, reason, created, completed_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		insertArgs...,
	)
	if err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	id, err := insertResult.LastInsertId()
	if err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.AuditWebhookDeliveryAttempt{}, err
	}
	attempt.ID = int(id)
	attempt.Created = now
	return attempt, nil
}

func (d *SqlDb) FinalizeAuditWebhookDeliveryAttempt(result db.AuditWebhookDeliveryAttemptResult) error {
	if err := db.ValidateAuditWebhookDeliveryAttemptResult(result); err != nil {
		return err
	}
	parentStatus, deliveredAt, nextAttempt := auditWebhookDeliveryAttemptParentState(result)
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	parentArgs := formatArgs([]any{
		parentStatus, nextAttempt, result.HTTPStatus, result.Reason, result.CompletedAt, deliveredAt,
		result.DeliveryID, db.AuditWebhookDeliveryRunning, result.Attempt,
	})
	updatedParent, err := tx.Exec(d.PrepareQuery(
		"update audit_webhook_delivery set status=?, next_attempt=?, lease_until=null, http_status=?, last_error=?, updated=?, delivered_at=? where id=? and status=? and attempts=?"),
		parentArgs...,
	)
	if err != nil {
		return err
	}
	rows, err := updatedParent.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrAuditWebhookDeliveryAttemptConflict
	}

	attemptArgs := formatArgs([]any{result.Outcome, result.HTTPStatus, result.Reason, result.CompletedAt, result.DeliveryID, result.Attempt, db.AuditWebhookDeliveryAttemptStarted, result.CompletedAt})
	updatedAttempt, err := tx.Exec(d.PrepareQuery(
		"update audit_webhook_delivery_attempt set outcome=?, http_status=?, reason=?, completed_at=? where delivery_id=? and attempt=? and outcome=? and signed_at<=?"),
		attemptArgs...,
	)
	if err != nil {
		return err
	}
	rows, err = updatedAttempt.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrAuditWebhookDeliveryAttemptConflict
	}
	return tx.Commit()
}

func (d *SqlDb) GetAuditWebhookDeliveryAttempts(deliveryID int, params db.RetrieveQueryParams) ([]db.AuditWebhookDeliveryAttempt, error) {
	if deliveryID <= 0 {
		return nil, db.ErrNotFound
	}
	query := squirrel.Select("*").From("audit_webhook_delivery_attempt").Where("delivery_id=?", deliveryID)
	if params.BeforeID > 0 {
		query = query.Where("id<?", params.BeforeID)
	}
	query = query.OrderBy("attempt desc", "id desc")
	count := params.Count
	if count <= 0 || count > db.MaxAuditWebhookDeliveryAttemptPage {
		count = db.MaxAuditWebhookDeliveryAttemptPage
	}
	query = query.Limit(uint64(count))
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	var attempts []db.AuditWebhookDeliveryAttempt
	_, err = d.selectAll(&attempts, sqlQuery, args...)
	return attempts, err
}

func auditWebhookDeliveryAttemptParentState(result db.AuditWebhookDeliveryAttemptResult) (db.AuditWebhookDeliveryStatus, *time.Time, time.Time) {
	nextAttempt := result.CompletedAt
	switch result.Outcome {
	case db.AuditWebhookDeliveryAttemptSucceeded:
		return db.AuditWebhookDeliverySucceeded, &result.CompletedAt, nextAttempt
	case db.AuditWebhookDeliveryAttemptRetrying:
		return db.AuditWebhookDeliveryRetrying, nil, *result.NextAttempt
	default:
		return db.AuditWebhookDeliveryFailed, nil, nextAttempt
	}
}

var _ db.AuditWebhookRepository = (*SqlDb)(nil)
