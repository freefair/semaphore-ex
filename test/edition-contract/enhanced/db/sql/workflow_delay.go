package sql

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (d *WorkflowStoreImpl) GetWorkflowDelays(projectID int, runID int) ([]db.WorkflowDelay, error) {
	var delays []db.WorkflowDelay
	if _, err := d.connection.SelectAll(&delays, d.connection.PrepareQuery(
		"select * from project__workflow_delay where project_id=? and workflow_run_id=? order by id"), projectID, runID); err != nil {
		return nil, err
	}
	return delays, nil
}

func (d *WorkflowStoreImpl) GetWorkflowDelay(projectID int, runID int, nodeID int) (db.WorkflowDelay, error) {
	var delay db.WorkflowDelay
	err := d.connection.SelectOne(&delay, d.connection.PrepareQuery(
		"select * from project__workflow_delay where project_id=? and workflow_run_id=? and workflow_node_id=?"), projectID, runID, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowDelay{}, db.ErrNotFound
	}
	return delay, err
}

func (d *WorkflowStoreImpl) GetExpiredWorkflowDelays() ([]db.WorkflowDelay, error) {
	var delays []db.WorkflowDelay
	if _, err := d.connection.SelectAll(&delays, d.connection.PrepareQuery(
		"select * from project__workflow_delay where status=? and resume_at<=CURRENT_TIMESTAMP order by resume_at, id"), db.WorkflowDelayWaiting); err != nil {
		return nil, err
	}
	return delays, nil
}

// CreateWorkflowDelay moves a durable run node from pending to waiting and
// persists its one delay record in the same transaction. The unique run-node
// key makes restart replay idempotent.
func (d *WorkflowStoreImpl) CreateWorkflowDelay(delay db.WorkflowDelay) (db.WorkflowDelay, error) {
	if err := validateWorkflowDelay(delay); err != nil {
		return db.WorkflowDelay{}, err
	}
	created, _, err := d.createWorkflowDelay(nil, delay)
	return created, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowDelay(delay db.WorkflowDelay) error {
	if delay.ID <= 0 || delay.ProjectID <= 0 || delay.WorkflowRunID <= 0 || delay.WorkflowNodeID <= 0 || delay.Resolved == nil || delay.Status == db.WorkflowDelayWaiting {
		return errors.New("workflow delay update is invalid")
	}
	if err := delay.Status.Validate(); err != nil {
		return err
	}
	current, err := d.GetWorkflowDelay(delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID)
	if err != nil {
		return err
	}
	if current.ID != delay.ID || current.Status != delay.Status || !current.ResumeAt.Equal(delay.ResumeAt) ||
		current.Resolved == nil || delay.Resolved == nil || !current.Resolved.Equal(*delay.Resolved) {
		return errors.New("workflow delay is immutable")
	}
	return nil
}

func (d *WorkflowStoreImpl) ResolveWorkflowDelayIfWaitingWithNode(delay db.WorkflowDelay, resultJSON string) (bool, error) {
	if delay.ProjectID <= 0 || delay.WorkflowRunID <= 0 || delay.WorkflowNodeID <= 0 || resultJSON == "" {
		return false, errors.New("workflow delay resolution is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_delay set status=?, resolved=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and resume_at<=CURRENT_TIMESTAMP"),
		db.WorkflowDelaySuccess, delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID, db.WorkflowDelayWaiting)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, nil
	}
	result, err = d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_run_node set status=?, result=?, `end`=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowRunNodeSucceeded, resultJSON, delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID, db.WorkflowRunNodeWaiting, delay.ProjectID, delay.WorkflowRunID, db.WorkflowRunDesiredRunning)
	if err != nil {
		return false, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, errors.New("workflow delay node cannot be completed")
	}
	return true, tx.Commit()
}

func (d *WorkflowStoreImpl) StopWorkflowDelayIfWaitingWithNode(lease *pro_interfaces.WorkflowReconciliationLease, projectID int, runID int, nodeID int, resultJSON string) (bool, error) {
	if projectID <= 0 || runID <= 0 || nodeID <= 0 || resultJSON == "" {
		return false, errors.New("workflow delay stop is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if lease != nil {
		if lease.ProjectID != projectID || lease.WorkflowRunID != runID {
			return false, errors.New("workflow delay reconciliation ownership is invalid")
		}
		if err = requireCurrentWorkflowLease(tx, d, *lease); err != nil {
			return false, err
		}
	}
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_delay set status=?, resolved=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=?"),
		db.WorkflowDelayStopped, projectID, runID, nodeID, db.WorkflowDelayWaiting)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, nil
	}
	result, err = d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_run_node set status=?, reason=?, result=?, `end`=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, projectID, runID, nodeID, db.WorkflowRunNodeWaiting, projectID, runID, db.WorkflowRunDesiredStopping)
	if err != nil {
		return false, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, errors.New("workflow delay node cannot be stopped")
	}
	return true, tx.Commit()
}

func (d *WorkflowStoreImpl) OpenWorkflowDelayFenced(lease pro_interfaces.WorkflowReconciliationLease, delay db.WorkflowDelay) (db.WorkflowDelay, bool, error) {
	if err := validateWorkflowDelay(delay); err != nil {
		return db.WorkflowDelay{}, false, err
	}
	if err := validateWorkflowDelayLease(lease, delay); err != nil {
		return db.WorkflowDelay{}, false, err
	}
	return d.createWorkflowDelay(&lease, delay)
}

func (d *WorkflowStoreImpl) createWorkflowDelay(lease *pro_interfaces.WorkflowReconciliationLease, delay db.WorkflowDelay) (db.WorkflowDelay, bool, error) {
	delaySeconds := int64(delay.ResumeAt.Sub(delay.Created) / time.Second)
	expiresAt, err := d.workflowDelayExpiryExpression(delaySeconds)
	if err != nil {
		return db.WorkflowDelay{}, false, err
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowDelay{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if lease != nil {
		if err = requireCurrentWorkflowLease(tx, d, *lease); err != nil {
			return db.WorkflowDelay{}, false, err
		}
	}
	var existing db.WorkflowDelay
	err = tx.SelectOne(&existing, d.connection.PrepareQuery(
		"select * from project__workflow_delay where project_id=? and workflow_run_id=? and workflow_node_id=?"),
		delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowDelay{}, false, err
	}

	query := "update project__workflow_run_node set status=?, queued=?, progression_fencing_token=? where workflow_node_id=? and project_id=? and workflow_run_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"
	args := []any{db.WorkflowRunNodeWaiting, delay.Created, int64(0), delay.WorkflowNodeID, delay.ProjectID, delay.WorkflowRunID, db.WorkflowRunNodePending, delay.ProjectID, delay.WorkflowRunID, db.WorkflowRunDesiredRunning}
	if lease != nil {
		query += " and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)"
		args[2] = lease.FencingToken
		args = append(args, lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken)
	}
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(query), args...)
	if err != nil {
		return db.WorkflowDelay{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowDelay{}, false, err
	}
	if updated != 1 {
		return db.WorkflowDelay{}, false, nil
	}
	delay.ID, err = d.insertTx(tx,
		"insert into project__workflow_delay(project_id, workflow_run_id, workflow_node_id, status, resume_at, created, resolved) values (?, ?, ?, ?, "+expiresAt+", CURRENT_TIMESTAMP, null)",
		delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID, delay.Status, delaySeconds)
	if err != nil {
		return db.WorkflowDelay{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowDelay{}, false, err
	}
	created, err := d.GetWorkflowDelay(delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID)
	return created, true, err
}

func (d *WorkflowStoreImpl) ResolveWorkflowDelayIfWaiting(delay db.WorkflowDelay) (bool, error) {
	if delay.Status != db.WorkflowDelaySuccess || delay.Resolved == nil {
		return false, errors.New("workflow delay resolution is invalid")
	}
	result, err := d.connection.Exec(d.connection.PrepareQuery(
		"update project__workflow_delay set status=?, resolved=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and resume_at<=CURRENT_TIMESTAMP and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowDelaySuccess, delay.ProjectID, delay.WorkflowRunID, delay.WorkflowNodeID, db.WorkflowDelayWaiting,
		delay.ProjectID, delay.WorkflowRunID, db.WorkflowRunDesiredRunning)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

// ResolveWorkflowDelayIfWaitingFenced atomically records expiry and finalizes
// the run node. A stale owner cannot expose downstream readiness.
func (d *WorkflowStoreImpl) ResolveWorkflowDelayIfWaitingFenced(lease pro_interfaces.WorkflowReconciliationLease, nodeID int, resolvedAt time.Time, resultJSON string) (bool, error) {
	if err := validateWorkflowDelayLease(lease, db.WorkflowDelay{ProjectID: lease.ProjectID, WorkflowRunID: lease.WorkflowRunID, WorkflowNodeID: nodeID}); err != nil {
		return false, err
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = requireCurrentWorkflowLease(tx, d, lease); err != nil {
		return false, err
	}
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_delay set status=?, resolved=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and resume_at<=CURRENT_TIMESTAMP"),
		db.WorkflowDelaySuccess, lease.ProjectID, lease.WorkflowRunID, nodeID, db.WorkflowDelayWaiting)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, nil
	}
	result, err = d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_run_node set status=?, result=?, `end`=CURRENT_TIMESTAMP, progression_fencing_token=? where workflow_node_id=? and project_id=? and workflow_run_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowRunNodeSucceeded, resultJSON, lease.FencingToken, nodeID, lease.ProjectID, lease.WorkflowRunID, db.WorkflowRunNodeWaiting, lease.ProjectID, lease.WorkflowRunID, db.WorkflowRunDesiredRunning)
	if err != nil {
		return false, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, fmt.Errorf("workflow delay node cannot be completed")
	}
	return true, tx.Commit()
}

func validateWorkflowDelay(delay db.WorkflowDelay) error {
	if delay.ProjectID <= 0 || delay.WorkflowRunID <= 0 || delay.WorkflowNodeID <= 0 || delay.Status != db.WorkflowDelayWaiting || delay.Created.IsZero() || delay.ResumeAt.Before(delay.Created) {
		return errors.New("workflow delay is invalid")
	}
	return nil
}

func (d *WorkflowStoreImpl) workflowDelayExpiryExpression(seconds int64) (string, error) {
	if seconds <= 0 || seconds > int64(db.MaxWorkflowDelaySeconds) {
		return "", errors.New("workflow delay duration is invalid")
	}
	switch d.connection.GetDialect() {
	case "sqlite":
		return "DATETIME(CURRENT_TIMESTAMP, '+' || ? || ' seconds')", nil
	case "mysql":
		return "DATE_ADD(CURRENT_TIMESTAMP, INTERVAL ? SECOND)", nil
	case "postgres":
		return "CURRENT_TIMESTAMP + (? * INTERVAL '1 second')", nil
	default:
		return "", errors.New("workflow delay database dialect is unsupported")
	}
}

func validateWorkflowDelayLease(lease pro_interfaces.WorkflowReconciliationLease, delay db.WorkflowDelay) error {
	if lease.ProjectID <= 0 || lease.WorkflowRunID <= 0 || lease.OwnerBootID == "" || lease.FencingToken <= 0 || delay.ProjectID != lease.ProjectID || delay.WorkflowRunID != lease.WorkflowRunID || delay.WorkflowNodeID <= 0 {
		return errors.New("workflow delay reconciliation ownership is invalid")
	}
	return nil
}

func requireCurrentWorkflowLease(tx *gorp.Transaction, d *WorkflowStoreImpl, lease pro_interfaces.WorkflowReconciliationLease) error {
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update cluster__workflow_reconciliation set operation_sequence=operation_sequence+1, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP"),
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("stale workflow reconciliation owner")
	}
	return nil
}
