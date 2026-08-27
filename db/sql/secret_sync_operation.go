package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

func (d *SqlDb) CreateSecretSyncOperation(
	operation db.SecretSyncOperation,
) (db.SecretSyncOperation, error) {
	if operation.RequestID == "" || operation.SyncID <= 0 || operation.ProjectID <= 0 || operation.StorageID <= 0 {
		return db.SecretSyncOperation{}, db.ErrInvalidOperation
	}
	if existing, err := d.getSecretSyncOperationByRequest(operation.SyncID, operation.RequestID); err == nil {
		return existing, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return db.SecretSyncOperation{}, err
	}
	now := tz.Now()
	operation.Status = db.SecretSyncOperationPending
	operation.CreatedAt = now
	operation.UpdatedAt = now
	operation.OutcomeJSON = "[]"
	id, err := d.insert(
		"id",
		"insert into project__secret_sync_operation "+
			"(request_id, sync_id, project_id, storage_id, sync_revision, resolve_operation_id, "+
			"requested_by, status, attempt, lease_until, created_at, started_at, finished_at, updated_at, "+
			"changed_count, skipped_count, conflict_count, error_category, outcome) "+
			"values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		operation.RequestID, operation.SyncID, operation.ProjectID, operation.StorageID,
		operation.SyncRevision, operation.ResolveOperationID, operation.RequestedBy, operation.Status,
		0, nil, now, nil, nil, now, 0, 0, 0, "", operation.OutcomeJSON,
	)
	if err != nil {
		if existing, lookupErr := d.getSecretSyncOperationByRequest(
			operation.SyncID, operation.RequestID,
		); lookupErr == nil {
			return existing, nil
		}
		return db.SecretSyncOperation{}, err
	}
	operation.ID = id
	operation.Outcomes = []db.SecretSyncItemOutcome{}
	return operation, nil
}

func (d *SqlDb) getSecretSyncOperationByRequest(
	syncID int,
	requestID string,
) (db.SecretSyncOperation, error) {
	var operation db.SecretSyncOperation
	err := d.selectOne(
		&operation,
		"select * from project__secret_sync_operation where sync_id=? and request_id=?",
		syncID,
		requestID,
	)
	if err != nil {
		return db.SecretSyncOperation{}, err
	}
	return operation, decodeSecretSyncOutcomes(&operation)
}

func (d *SqlDb) GetSecretSyncOperation(
	projectID int,
	storageID int,
	operationID int,
) (db.SecretSyncOperation, error) {
	var operation db.SecretSyncOperation
	err := d.selectOne(&operation,
		"select * from project__secret_sync_operation where id=? and project_id=? and storage_id=?",
		operationID, projectID, storageID,
	)
	if err != nil {
		return db.SecretSyncOperation{}, err
	}
	return operation, decodeSecretSyncOutcomes(&operation)
}

func (d *SqlDb) GetSecretSyncOperations(
	projectID int,
	storageID int,
	limit int,
) ([]db.SecretSyncOperation, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	operations := make([]db.SecretSyncOperation, 0)
	_, err := d.selectAll(&operations,
		"select * from project__secret_sync_operation where project_id=? and storage_id=? "+
			"order by id desc limit ?",
		projectID, storageID, limit,
	)
	if err != nil {
		return nil, err
	}
	for index := range operations {
		if err = decodeSecretSyncOutcomes(&operations[index]); err != nil {
			return nil, err
		}
	}
	return operations, nil
}

func (d *SqlDb) ClaimSecretSyncOperation(
	operationID int,
	now time.Time,
	leaseUntil time.Time,
) (db.SecretSyncOperation, bool, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.SecretSyncOperation{}, false, err
	}
	query, args := secretSyncOperationClaimStatement(d.Sql().Dialect, operationID, now, leaseUntil)
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		_ = tx.Rollback()
		return db.SecretSyncOperation{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return db.SecretSyncOperation{}, false, err
	}
	if rows != 1 {
		_ = tx.Rollback()
		return db.SecretSyncOperation{}, false, nil
	}
	var operation db.SecretSyncOperation
	err = tx.SelectOne(&operation, d.PrepareQuery("select * from project__secret_sync_operation where id=?"), operationID)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			err = db.ErrNotFound
		}
		return db.SecretSyncOperation{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.SecretSyncOperation{}, false, err
	}
	return operation, true, decodeSecretSyncOutcomes(&operation)
}

func secretSyncOperationClaimStatement(
	dialect gorp.Dialect,
	operationID int,
	now time.Time,
	leaseUntil time.Time,
) (string, []any) {
	if _, ok := dialect.(gorp.MySQLDialect); ok {
		return "update project__secret_sync_operation target " +
				"left join project__secret_sync_operation active on active.sync_id=target.sync_id " +
				"and active.id<>target.id and active.status=? and active.lease_until>? " +
				"set target.status=?, target.attempt=target.attempt+1, target.lease_until=?, " +
				"target.started_at=coalesce(target.started_at, ?), target.updated_at=? " +
				"where target.id=? and (target.status=? or (target.status=? and target.lease_until<=?)) " +
				"and active.id is null", []any{
				db.SecretSyncOperationRunning, now,
				db.SecretSyncOperationRunning, leaseUntil, now, now, operationID,
				db.SecretSyncOperationPending, db.SecretSyncOperationRunning, now,
			}
	}
	return "update project__secret_sync_operation set status=?, attempt=attempt+1, lease_until=?, " +
			"started_at=coalesce(started_at, ?), updated_at=? where id=? and " +
			"(status=? or (status=? and lease_until<=?)) and not exists (" +
			"select 1 from project__secret_sync_operation active where active.sync_id=" +
			"project__secret_sync_operation.sync_id and active.id<>project__secret_sync_operation.id " +
			"and active.status=? and active.lease_until>?)", []any{
			db.SecretSyncOperationRunning, leaseUntil, now, now, operationID,
			db.SecretSyncOperationPending, db.SecretSyncOperationRunning, now,
			db.SecretSyncOperationRunning, now,
		}
}

func (d *SqlDb) ClaimPendingSecretSyncOperations(
	now time.Time,
	leaseUntil time.Time,
	limit int,
) ([]db.SecretSyncOperation, error) {
	if limit <= 0 {
		return []db.SecretSyncOperation{}, nil
	}
	var candidates []struct {
		ID int `db:"id"`
	}
	_, err := d.selectAll(&candidates,
		"select id from project__secret_sync_operation where status=? or "+
			"(status=? and lease_until<=?) order by id limit ?",
		db.SecretSyncOperationPending, db.SecretSyncOperationRunning, now, limit,
	)
	if err != nil {
		return nil, err
	}
	claimed := make([]db.SecretSyncOperation, 0, len(candidates))
	for _, candidate := range candidates {
		operation, ok, claimErr := d.ClaimSecretSyncOperation(candidate.ID, now, leaseUntil)
		if claimErr != nil {
			return nil, claimErr
		}
		if ok {
			claimed = append(claimed, operation)
		}
	}
	return claimed, nil
}

func (d *SqlDb) RenewSecretSyncOperationLease(
	operationID int,
	attempt int,
	now time.Time,
	leaseUntil time.Time,
) (bool, error) {
	result, err := d.exec(
		"update project__secret_sync_operation set lease_until=?, updated_at=? "+
			"where id=? and status=? and attempt=? and lease_until>?",
		leaseUntil, now, operationID, db.SecretSyncOperationRunning, attempt, now,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) CompleteSecretSyncOperation(
	operation db.SecretSyncOperation,
	paths []db.SecretSyncPath,
) error {
	if operation.Status != db.SecretSyncOperationSucceeded &&
		operation.Status != db.SecretSyncOperationFailed &&
		operation.Status != db.SecretSyncOperationConflict {
		return db.ErrInvalidOperation
	}
	outcome, err := json.Marshal(operation.Outcomes)
	if err != nil {
		return err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	for _, path := range paths {
		result, updateErr := tx.Exec(d.PrepareQuery(
			"update project__secret_sync_path set remote_version=?, content_fingerprint=? "+
				"where id=? and sync_id=?"),
			path.RemoteVersion, path.ContentFingerprint, path.ID, operation.SyncID,
		)
		if updateErr != nil {
			_ = tx.Rollback()
			return updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil || rows != 1 {
			_ = tx.Rollback()
			if rowsErr != nil {
				return rowsErr
			}
			return fmt.Errorf("secret sync mapping %d changed during operation", path.ID)
		}
	}
	now := tz.Now()
	result, err := tx.Exec(d.PrepareQuery(
		"update project__secret_sync_operation set status=?, lease_until=null, finished_at=?, updated_at=?, "+
			"changed_count=?, skipped_count=?, conflict_count=?, error_category=?, outcome=? "+
			"where id=? and status=? and attempt=?"),
		operation.Status, now, now, operation.ChangedCount, operation.SkippedCount,
		operation.ConflictCount, operation.ErrorCategory, string(outcome), operation.ID,
		db.SecretSyncOperationRunning, operation.Attempt,
	)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		_ = tx.Rollback()
		if err != nil {
			return err
		}
		return fmt.Errorf("secret sync operation %d is not claimed", operation.ID)
	}
	return tx.Commit()
}

func decodeSecretSyncOutcomes(operation *db.SecretSyncOperation) error {
	operation.Outcomes = []db.SecretSyncItemOutcome{}
	if operation.OutcomeJSON == "" {
		return nil
	}
	return json.Unmarshal([]byte(operation.OutcomeJSON), &operation.Outcomes)
}
