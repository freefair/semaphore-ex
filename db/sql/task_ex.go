package sql

import (
	databaseSQL "database/sql"
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// CreateWorkflowTaskFenced creates a task while holding the workflow lease row
// write lock. A takeover must update that same row first, so it cannot cross
// this transaction and an expired or stale owner cannot create the attempt.
func (d *SqlDb) CreateWorkflowTaskFenced(task db.Task, maxTasks int, lease pro_interfaces.WorkflowReconciliationLease) (db.Task, error) {
	if task.WorkflowRunID == nil || task.WorkflowNodeID == nil ||
		*task.WorkflowRunID != lease.WorkflowRunID || task.ProjectID != lease.ProjectID ||
		lease.OwnerBootID == "" || lease.FencingToken <= 0 {
		return db.Task{}, errors.New("workflow task reconciliation ownership is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := d.connection.ExecTx(tx,
		"update cluster__workflow_reconciliation set operation_sequence=operation_sequence+1, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return db.Task{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.Task{}, err
	}
	if updated != 1 {
		return db.Task{}, errors.New("stale workflow reconciliation owner")
	}
	var nodeCount int
	err = tx.SelectOne(&nodeCount, d.connection.PrepareQuery(
		"select count(1) from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and progression_fencing_token=?"),
		lease.ProjectID, lease.WorkflowRunID, *task.WorkflowNodeID, db.WorkflowRunNodeQueued, lease.FencingToken,
	)
	if err != nil {
		return db.Task{}, err
	}
	if nodeCount != 1 {
		return db.Task{}, errors.New("stale workflow reconciliation owner")
	}
	if err = tx.Insert(&task); err != nil {
		return db.Task{}, err
	}
	if _, err = d.connection.ExecTx(tx,
		"update project__template set tasks=tasks+1 where project_id=? and id=?",
		task.ProjectID, task.TemplateID,
	); err != nil {
		return db.Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.Task{}, err
	}
	if maxTasks > 0 {
		d.clearTasks(task.ProjectID, task.TemplateID, maxTasks)
	}
	return task, nil
}

// UpdateTaskFenced applies a recovery-only task update when the caller still
// owns the exact fencing token installed by the task-control claim.
func (d *SqlDb) UpdateTaskFenced(task db.Task, expectedFencingToken int64) (bool, error) {
	if expectedFencingToken <= 0 {
		return false, errors.New("task recovery fencing token is invalid")
	}
	if err := task.PreUpdate(d.Sql()); err != nil {
		return false, err
	}
	var (
		result databaseSQL.Result
		err    error
	)
	if task.CommitHash != nil {
		result, err = d.exec(
			"update task set status=?, start=?, `end`=?, commit_hash=?, commit_message=?, runner_id=?, runner_id_snapshot=?, runner_name=?, assignment_generation=?, runner_assigned_at=?, recovery_reason=?, placement_decision=?, message=? where id=? and task_control_fencing_token=?",
			task.Status, task.Start, task.End, task.CommitHash, task.CommitMessage, task.RunnerID,
			task.RunnerSnapshotID, task.RunnerName, task.AssignmentGeneration, task.RunnerAssignedAt,
			task.RecoveryReason, task.PlacementDecision, task.Message, task.ID, expectedFencingToken,
		)
	} else {
		result, err = d.exec(
			"update task set status=?, start=?, `end`=?, runner_id=?, runner_id_snapshot=?, runner_name=?, assignment_generation=?, runner_assigned_at=?, recovery_reason=?, placement_decision=?, message=? where id=? and task_control_fencing_token=?",
			task.Status, task.Start, task.End, task.RunnerID, task.RunnerSnapshotID, task.RunnerName,
			task.AssignmentGeneration, task.RunnerAssignedAt, task.RecoveryReason, task.PlacementDecision,
			task.Message, task.ID, expectedFencingToken,
		)
	}
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}
