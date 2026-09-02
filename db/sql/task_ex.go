package sql

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// createTaskWithDeploymentWindowDecision binds an already-admitted Enhanced
// decision to its execution in the same transaction as the task insert. The
// decision table is intentionally referenced only when the private fence is
// populated, preserving the Community schema and its established path.
func (d *SqlDb) createTaskWithDeploymentWindowDecision(task db.Task, maxTasks int) (db.Task, error) {
	if task.DeploymentWindowDecisionID == nil || *task.DeploymentWindowDecisionID <= 0 {
		return db.Task{}, errors.New("deployment window task admission is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = validateDeploymentWindowTaskBindingTx(tx, d, task); err != nil {
		return db.Task{}, err
	}
	if err = tx.Insert(&task); err != nil {
		return db.Task{}, err
	}
	result, err := d.connection.ExecTx(tx,
		"update project__deployment_window_decision set task_id=? where id=? and project_id=? and state in (?, ?) and task_id is null",
		task.ID, *task.DeploymentWindowDecisionID, task.ProjectID, "allowed", "overridden",
	)
	if err != nil {
		return db.Task{}, err
	}
	bound, err := result.RowsAffected()
	if err != nil {
		return db.Task{}, err
	}
	if bound != 1 {
		return db.Task{}, errors.New("deployment window decision cannot be bound to task")
	}
	if err = attachDeploymentWindowWorkflowTaskTx(tx, d, task, nil); err != nil {
		return db.Task{}, err
	}
	if _, err = d.connection.ExecTx(tx,
		"update project__template set tasks=tasks+1 where project_id=? and id=?", task.ProjectID, task.TemplateID,
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

func validateDeploymentWindowTaskBindingTx(tx *gorp.Transaction, d *SqlDb, task db.Task) error {
	var decision struct {
		ProjectID         int    `db:"project_id"`
		TemplateID        *int   `db:"template_id"`
		WorkflowRunID     *int   `db:"workflow_run_id"`
		WorkflowRunNodeID *int   `db:"workflow_run_node_id"`
		TaskID            *int   `db:"task_id"`
		State             string `db:"state"`
	}
	if err := tx.SelectOne(&decision, d.PrepareQuery(
		"select project_id, template_id, workflow_run_id, workflow_run_node_id, task_id, state from project__deployment_window_decision where id=?"),
		*task.DeploymentWindowDecisionID,
	); err != nil {
		return errors.New("deployment window decision is unavailable")
	}
	if decision.ProjectID != task.ProjectID || decision.TemplateID == nil || *decision.TemplateID != task.TemplateID || decision.TaskID != nil ||
		(decision.State != "allowed" && decision.State != "overridden") || !sameTaskBindingID(decision.WorkflowRunID, task.WorkflowRunID) {
		return errors.New("deployment window decision does not match task")
	}
	if task.WorkflowNodeID == nil {
		if decision.WorkflowRunNodeID != nil {
			return errors.New("deployment window decision does not match task")
		}
		return nil
	}
	if decision.WorkflowRunID == nil || decision.WorkflowRunNodeID == nil {
		return errors.New("deployment window decision does not match task")
	}
	var nodeCount int
	if err := tx.SelectOne(&nodeCount, d.PrepareQuery(
		"select count(1) from project__workflow_run_node where id=? and project_id=? and workflow_run_id=? and workflow_node_id=?"),
		*decision.WorkflowRunNodeID, task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID,
	); err != nil || nodeCount != 1 {
		return errors.New("deployment window decision does not match task")
	}
	return nil
}

func sameTaskBindingID(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func attachDeploymentWindowWorkflowTaskTx(tx *gorp.Transaction, d *SqlDb, task db.Task, lease *pro_interfaces.WorkflowReconciliationLease) error {
	if task.WorkflowRunID == nil || task.WorkflowNodeID == nil || task.DeploymentWindowDecisionID == nil {
		return nil
	}
	query := "update project__workflow_run_node set task_id=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null"
	args := []any{task.ID, task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, db.WorkflowRunNodeQueued}
	if lease != nil {
		query += " and progression_fencing_token=?"
		args = append(args, lease.FencingToken)
	}
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("deployment window workflow task cannot be attached")
	}
	return nil
}

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
	if task.DeploymentWindowDecisionID != nil {
		if err = validateDeploymentWindowTaskBindingTx(tx, d, task); err != nil {
			return db.Task{}, err
		}
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
	if task.DeploymentWindowDecisionID != nil {
		result, bindErr := d.connection.ExecTx(tx,
			"update project__deployment_window_decision set task_id=? where id=? and project_id=? and state in (?, ?) and task_id is null",
			task.ID, *task.DeploymentWindowDecisionID, task.ProjectID, "allowed", "overridden",
		)
		if bindErr != nil {
			return db.Task{}, bindErr
		}
		bound, rowsErr := result.RowsAffected()
		if rowsErr != nil || bound != 1 {
			return db.Task{}, errors.New("deployment window decision cannot be bound to fenced workflow task")
		}
		if err = attachDeploymentWindowWorkflowTaskTx(tx, d, task, &lease); err != nil {
			return db.Task{}, err
		}
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
	tx, err := d.Sql().Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current db.Task
	if err = tx.SelectOne(&current, d.PrepareQuery("select * from task where id=? and task_control_fencing_token=?"), task.ID, expectedFencingToken); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	notify := current.Status != task.Status && task.Status.IsFinished()
	query, args := taskUpdateStatement(task, notify, &expectedFencingToken)
	result, err := d.execTx(tx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return rows == 1, err
	}
	if notify {
		task.NotificationRevision = current.NotificationRevision + 1
		if err = d.routeNotificationTx(tx, taskTerminalNotification(task)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func taskUpdateStatement(task db.Task, notify bool, fence *int64) (string, []any) {
	query := "update task set status=?, start=?, `end`=?"
	args := []any{task.Status, task.Start, task.End}
	if task.CommitHash != nil {
		query += ", commit_hash=?, commit_message=?"
		args = append(args, task.CommitHash, task.CommitMessage)
	}
	query += ", runner_id=?, runner_id_snapshot=?, runner_name=?, assignment_generation=?, runner_assigned_at=?, recovery_reason=?, placement_decision=?, message=?"
	args = append(args, task.RunnerID, task.RunnerSnapshotID, task.RunnerName, task.AssignmentGeneration, task.RunnerAssignedAt, task.RecoveryReason, task.PlacementDecision, task.Message)
	if notify {
		query += ", notification_revision=notification_revision+1"
	}
	query += " where id=?"
	args = append(args, task.ID)
	if fence != nil {
		query += " and task_control_fencing_token=?"
		args = append(args, *fence)
	}
	return query, args
}

func taskTerminalNotification(task db.Task) pro_interfaces.NotificationEvent {
	severity, action, status := pro_interfaces.NotificationSeverityWarning, pro_interfaces.NotificationLifecycleUpdate, "stopped"
	switch task.Status {
	case task_logger.TaskSuccessStatus:
		severity, action, status = pro_interfaces.NotificationSeverityInfo, pro_interfaces.NotificationLifecycleResolve, "succeeded"
	case task_logger.TaskFailStatus:
		severity, action, status = pro_interfaces.NotificationSeverityError, pro_interfaces.NotificationLifecycleTrigger, "failed"
	}
	projectID, taskID, templateID := task.ProjectID, task.ID, task.TemplateID
	return pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeProject, ProjectID: &projectID,
		Source:      pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: fmt.Sprintf("task:%d", task.ID)},
		LifecycleID: fmt.Sprintf("template:%d", task.TemplateID), SourceRevision: task.NotificationRevision,
		Severity: severity, LifecycleAction: action,
		Details: pro_interfaces.NotificationDetails{TaskID: &taskID, TemplateID: &templateID, Status: status},
	}
}
