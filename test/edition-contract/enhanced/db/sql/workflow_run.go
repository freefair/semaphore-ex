package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

func (d *WorkflowStoreImpl) GetWorkflowRunTasks(projectID int, runID int, params db.RetrieveQueryParams) ([]db.TaskWithTpl, error) {
	if d.workflowTaskStore == nil {
		return []db.TaskWithTpl{}, nil
	}
	return d.workflowTaskStore.GetWorkflowRunTasks(projectID, runID, params)
}

func (d *WorkflowStoreImpl) GetWorkflowRuns(projectID int, workflowTemplateID int, params db.RetrieveQueryParams) ([]db.WorkflowRun, error) {
	query := "select * from project__workflow_run where project_id=? and workflow_template_id=?"
	args := []any{projectID, workflowTemplateID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc"
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
		if params.Offset > 0 {
			query += " offset ?"
			args = append(args, params.Offset)
		}
	}
	var runs []db.WorkflowRun
	if _, err := d.connection.SelectAll(&runs, query, args...); err != nil {
		return nil, err
	}
	for index := range runs {
		if err := d.loadWorkflowRun(&runs[index]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (d *WorkflowStoreImpl) GetWorkflowRun(projectID int, workflowTemplateID int, runID int) (db.WorkflowRun, error) {
	return d.getWorkflowRun("select * from project__workflow_run where project_id=? and workflow_template_id=? and id=?", projectID, workflowTemplateID, runID)
}

func (d *WorkflowStoreImpl) GetWorkflowRunByID(projectID int, runID int) (db.WorkflowRun, error) {
	return d.getWorkflowRun("select * from project__workflow_run where project_id=? and id=?", projectID, runID)
}

func (d *WorkflowStoreImpl) GetWorkflowRunByCorrelationID(projectID int, workflowTemplateID int, correlationID string) (db.WorkflowRun, error) {
	return d.getWorkflowRun("select * from project__workflow_run where project_id=? and workflow_template_id=? and correlation_id=?", projectID, workflowTemplateID, correlationID)
}

func (d *WorkflowStoreImpl) getWorkflowRun(query string, args ...any) (db.WorkflowRun, error) {
	var run db.WorkflowRun
	if err := d.connection.SelectOne(&run, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.WorkflowRun{}, db.ErrNotFound
		}
		return db.WorkflowRun{}, err
	}
	if err := d.loadWorkflowRun(&run); err != nil {
		return db.WorkflowRun{}, err
	}
	return run, nil
}

func (d *WorkflowStoreImpl) GetActiveWorkflowRuns() ([]db.WorkflowRun, error) {
	var runs []db.WorkflowRun
	query := "select * from project__workflow_run where status not in (?, ?, ?, ?, ?) order by id"
	if _, err := d.connection.SelectAll(&runs, query,
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunBlocked); err != nil {
		return nil, err
	}
	for index := range runs {
		if err := d.loadWorkflowRun(&runs[index]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (d *WorkflowStoreImpl) CreateWorkflowRun(run db.WorkflowRun) (db.WorkflowRun, error) {
	if existing, err := d.GetWorkflowRunByCorrelationID(run.ProjectID, run.WorkflowTemplateID, run.CorrelationID); err == nil {
		return existing, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return db.WorkflowRun{}, err
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowRun{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	run.ID, err = d.insertTx(tx,
		"insert into project__workflow_run(project_id, workflow_template_id, status, version, start, end, root_task_id, actor_user_id, definition_version, definition_revision, definition_snapshot, correlation_id, created, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		run.ProjectID, run.WorkflowTemplateID, run.Status, run.Version, run.Start, run.End, run.RootTaskID,
		run.ActorUserID, run.DefinitionVersion, run.DefinitionRevision, run.DefinitionSnapshotJSON,
		run.CorrelationID, run.Created, run.Reason,
	)
	if err != nil {
		_ = tx.Rollback()
		rollback = false
		if existing, getErr := d.GetWorkflowRunByCorrelationID(run.ProjectID, run.WorkflowTemplateID, run.CorrelationID); getErr == nil {
			return existing, nil
		}
		return db.WorkflowRun{}, err
	}
	for index := range run.Nodes {
		node := &run.Nodes[index]
		node.ProjectID = run.ProjectID
		node.WorkflowRunID = run.ID
		node.ID, err = d.insertTx(tx,
			"insert into project__workflow_run_node(project_id, workflow_run_id, workflow_node_id, template_id, status, task_id, template_snapshot, created, queued, start, end, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			node.ProjectID, node.WorkflowRunID, node.WorkflowNodeID, node.TemplateID, node.Status,
			node.TaskID, node.TemplateSnapshotJSON, node.Created, node.Queued, node.Start, node.End, node.Reason,
		)
		if err != nil {
			return db.WorkflowRun{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowRun{}, err
	}
	rollback = false
	return run, nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowRun(run db.WorkflowRun) error {
	result, err := d.connection.Exec(
		"update project__workflow_run set status=?, reason=?, end=?, root_task_id=? where project_id=? and id=?",
		run.Status, run.Reason, run.End, run.RootTaskID, run.ProjectID, run.ID,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return db.ErrNotFound
	}
	return nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunStatusUnless(run db.WorkflowRun, excluded []db.WorkflowRunStatus) (bool, error) {
	query := "update project__workflow_run set status=?, reason=?, end=? where project_id=? and id=?"
	args := []any{run.Status, run.Reason, run.End, run.ProjectID, run.ID}
	if len(excluded) > 0 {
		query += " and status not in (" + strings.TrimRight(strings.Repeat("?,", len(excluded)), ",") + ")"
		for _, status := range excluded {
			args = append(args, status)
		}
	}
	result, err := d.connection.Exec(query, args...)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) SetWorkflowRunRootTask(projectID int, runID int, taskID int) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run set root_task_id=?, status=? where project_id=? and id=? and root_task_id is null and status in (?, ?)",
		taskID, db.WorkflowRunQueued, projectID, runID, db.WorkflowRunPending, db.WorkflowRunQueued,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) GetWorkflowRunNode(projectID int, runID int, nodeID int) (db.WorkflowRunNode, error) {
	var node db.WorkflowRunNode
	err := d.connection.SelectOne(&node,
		"select * from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=?",
		projectID, runID, nodeID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowRunNode{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowRunNode{}, err
	}
	if err = decodeWorkflowRunNode(&node); err != nil {
		return db.WorkflowRunNode{}, err
	}
	return node, nil
}

func (d *WorkflowStoreImpl) GetWorkflowRunNodeTask(projectID int, runID int, nodeID int) (db.Task, error) {
	var task db.Task
	err := d.connection.SelectOne(&task,
		"select * from task where project_id=? and workflow_run_id=? and workflow_node_id=?",
		projectID, runID, nodeID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Task{}, db.ErrNotFound
	}
	return task, err
}

func (d *WorkflowStoreImpl) ClaimWorkflowRunNode(projectID int, runID int, nodeID int, queuedAt time.Time) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, queued=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null",
		db.WorkflowRunNodeQueued, queuedAt, projectID, runID, nodeID, db.WorkflowRunNodePending,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) AttachWorkflowRunNodeTask(projectID int, runID int, nodeID int, taskID int) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run_node set task_id=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from task where id=? and project_id=? and workflow_run_id=? and workflow_node_id=?)",
		taskID, projectID, runID, nodeID, db.WorkflowRunNodeQueued,
		taskID, projectID, runID, nodeID,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunNodeFromTask(
	projectID int,
	runID int,
	nodeID int,
	taskID int,
	status db.WorkflowRunNodeStatus,
	reason string,
	at time.Time,
) (bool, error) {
	if status == db.WorkflowRunNodePending || status == db.WorkflowRunNodeBlocked {
		return false, fmt.Errorf("workflow task cannot transition node to %s", status)
	}
	start := any(nil)
	end := any(nil)
	if status == db.WorkflowRunNodeRunning {
		start = at
	}
	if status.IsFinished() {
		end = at
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, start=coalesce(start, ?), end=coalesce(end, ?) where project_id=? and workflow_run_id=? and workflow_node_id=? and task_id=? and status not in (?, ?, ?, ?)",
		status, reason, start, end, projectID, runID, nodeID, taskID,
		db.WorkflowRunNodeSucceeded, db.WorkflowRunNodeFailed, db.WorkflowRunNodeStopped, db.WorkflowRunNodeBlocked,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) BlockWorkflowRunNode(projectID int, runID int, nodeID int, reason string, at time.Time) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, end=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status in (?, ?) and task_id is null",
		db.WorkflowRunNodeBlocked, reason, at, projectID, runID, nodeID,
		db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) loadWorkflowRun(run *db.WorkflowRun) error {
	if run.DefinitionSnapshotJSON != "" {
		if err := json.Unmarshal([]byte(run.DefinitionSnapshotJSON), &run.DefinitionSnapshot); err != nil {
			return fmt.Errorf("decode workflow definition snapshot: %w", err)
		}
	}
	if _, err := d.connection.SelectAll(&run.Nodes,
		"select * from project__workflow_run_node where project_id=? and workflow_run_id=? order by id",
		run.ProjectID, run.ID,
	); err != nil {
		return err
	}
	for index := range run.Nodes {
		if err := decodeWorkflowRunNode(&run.Nodes[index]); err != nil {
			return err
		}
	}
	return nil
}

func decodeWorkflowRunNode(node *db.WorkflowRunNode) error {
	if node.TemplateSnapshotJSON == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(node.TemplateSnapshotJSON), &node.TemplateSnapshot); err != nil {
		return fmt.Errorf("decode workflow template snapshot: %w", err)
	}
	return nil
}
