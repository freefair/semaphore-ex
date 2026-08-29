package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
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
	query := "select * from project__workflow_run where status not in (?, ?, ?, ?, ?, ?) and reconciliation_state<>? and (reconciliation_next_retry_at is null or reconciliation_next_retry_at<=CURRENT_TIMESTAMP) order by id"
	if _, err := d.connection.SelectAll(&runs, query,
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunCanceled, db.WorkflowRunBlocked,
		db.WorkflowRunReconciliationQuarantined); err != nil {
		return nil, err
	}
	for index := range runs {
		if err := d.loadWorkflowRun(&runs[index]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (d *WorkflowStoreImpl) GetActiveWorkflowRunsFair() ([]db.WorkflowRun, error) {
	var runs []db.WorkflowRun
	query := "select * from project__workflow_run where status not in (?, ?, ?, ?, ?, ?) and reconciliation_state<>? and (reconciliation_next_retry_at is null or reconciliation_next_retry_at<=CURRENT_TIMESTAMP) order by project_id, id"
	if _, err := d.connection.SelectAll(&runs, query,
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunCanceled, db.WorkflowRunBlocked,
		db.WorkflowRunReconciliationQuarantined); err != nil {
		return nil, err
	}
	runs = fairWorkflowRunOrder(runs)
	for index := range runs {
		if err := d.loadWorkflowRun(&runs[index]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func fairWorkflowRunOrder(runs []db.WorkflowRun) []db.WorkflowRun {
	if len(runs) < 2 {
		return append([]db.WorkflowRun(nil), runs...)
	}
	projects := make([]int, 0)
	byProject := make(map[int][]db.WorkflowRun)
	for _, run := range runs {
		if _, exists := byProject[run.ProjectID]; !exists {
			projects = append(projects, run.ProjectID)
		}
		byProject[run.ProjectID] = append(byProject[run.ProjectID], run)
	}
	ordered := make([]db.WorkflowRun, 0, len(runs))
	for offset := 0; len(ordered) < len(runs); offset++ {
		for _, projectID := range projects {
			projectRuns := byProject[projectID]
			if offset < len(projectRuns) {
				ordered = append(ordered, projectRuns[offset])
			}
		}
	}
	return ordered
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
	if run.TriggerSnapshotJSON == "" {
		run.TriggerSnapshotJSON = "{}"
	}
	if run.DesiredState == "" {
		run.DesiredState = db.WorkflowRunDesiredRunning
	}
	if run.ReconciliationState == "" {
		run.ReconciliationState = db.WorkflowRunReconciliationHealthy
	}
	run.ID, err = d.insertTx(tx,
		"insert into project__workflow_run(project_id, workflow_template_id, status, desired_state, reconciliation_state, reconciliation_attempts, reconciliation_last_error, reconciliation_next_retry_at, reconciliation_quarantined_at, version, start, `end`, root_task_id, actor_user_id, definition_version, definition_revision, definition_snapshot, parameter_snapshot, trigger_snapshot, correlation_id, created, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		run.ProjectID, run.WorkflowTemplateID, run.Status, run.DesiredState, run.ReconciliationState, run.ReconciliationAttempts, run.ReconciliationLastError, run.ReconciliationNextRetryAt, run.ReconciliationQuarantinedAt, run.Version, run.Start, run.End, run.RootTaskID,
		run.ActorUserID, run.DefinitionVersion, run.DefinitionRevision, run.DefinitionSnapshotJSON,
		run.ParameterSnapshotJSON, run.TriggerSnapshotJSON, run.CorrelationID, run.Created, run.Reason,
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
		if node.ArtifactInputsJSON == "" {
			node.ArtifactInputsJSON = "[]"
		}
		if node.OverrideSnapshotJSON == "" {
			node.OverrideSnapshotJSON = "{}"
		}
		node.ID, err = d.insertTx(tx,
			"insert into project__workflow_run_node(project_id, workflow_run_id, workflow_node_id, template_id, status, task_id, template_snapshot, result, artifact_inputs, override_snapshot, created, queued, start, `end`, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			node.ProjectID, node.WorkflowRunID, node.WorkflowNodeID, node.TemplateID, node.Status,
			node.TaskID, node.TemplateSnapshotJSON, node.ResultJSON, node.ArtifactInputsJSON, node.OverrideSnapshotJSON, node.Created, node.Queued, node.Start, node.End, node.Reason,
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
		"update project__workflow_run set status=?, desired_state=?, reconciliation_state=?, reconciliation_attempts=?, reconciliation_last_error=?, reconciliation_next_retry_at=?, reconciliation_quarantined_at=?, reason=?, `end`=?, root_task_id=? where project_id=? and id=?",
		run.Status, run.DesiredState, run.ReconciliationState, run.ReconciliationAttempts, run.ReconciliationLastError, run.ReconciliationNextRetryAt, run.ReconciliationQuarantinedAt, run.Reason, run.End, run.RootTaskID, run.ProjectID, run.ID,
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

// UpdateWorkflowRunReconciliation changes only scheduler-owned diagnostics so a
// concurrently persisted user stop request cannot be reverted by a stale scan.
func (d *WorkflowStoreImpl) UpdateWorkflowRunReconciliation(run db.WorkflowRun) error {
	result, err := d.connection.Exec(
		"update project__workflow_run set reconciliation_state=?, reconciliation_attempts=?, reconciliation_last_error=?, reconciliation_next_retry_at=?, reconciliation_quarantined_at=? where project_id=? and id=?",
		run.ReconciliationState, run.ReconciliationAttempts, run.ReconciliationLastError, run.ReconciliationNextRetryAt, run.ReconciliationQuarantinedAt, run.ProjectID, run.ID,
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

func (d *WorkflowStoreImpl) RequestWorkflowRunStop(projectID int, runID int) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run set desired_state=?, status=? where project_id=? and id=? and status not in (?, ?, ?, ?, ?, ?) and desired_state<>?",
		db.WorkflowRunDesiredStopping, db.WorkflowRunStopping, projectID, runID,
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunCanceled, db.WorkflowRunBlocked,
		db.WorkflowRunDesiredStopped,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunStatusUnless(run db.WorkflowRun, excluded []db.WorkflowRunStatus) (bool, error) {
	query := "update project__workflow_run set status=?, reason=?, `end`=? where project_id=? and id=?"
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

func (d *WorkflowStoreImpl) GetWorkflowApprovals(projectID int, runID int) ([]db.WorkflowApproval, error) {
	var approvals []db.WorkflowApproval
	if _, err := d.connection.SelectAll(&approvals,
		"select * from project__workflow_approval where project_id=? and workflow_run_id=? order by id",
		projectID, runID,
	); err != nil {
		return nil, err
	}
	return approvals, nil
}

func (d *WorkflowStoreImpl) GetPendingWorkflowApprovals(projectID int) ([]db.WorkflowApproval, error) {
	var approvals []db.WorkflowApproval
	if _, err := d.connection.SelectAll(&approvals,
		`select approval.*, workflow_run.workflow_template_id, workflow_template.name as workflow_name
		 from project__workflow_approval approval
		 join project__workflow_run workflow_run on workflow_run.id=approval.workflow_run_id and workflow_run.project_id=approval.project_id
		 join project__workflow_template workflow_template on workflow_template.id=workflow_run.workflow_template_id and workflow_template.project_id=approval.project_id
		 where approval.project_id=? and approval.status=? order by approval.deadline is null, approval.deadline, approval.id`,
		projectID, db.WorkflowApprovalPending,
	); err != nil {
		return nil, err
	}
	return approvals, nil
}

func (d *WorkflowStoreImpl) GetWorkflowApproval(projectID int, runID int, nodeID int) (db.WorkflowApproval, error) {
	var approval db.WorkflowApproval
	err := d.connection.SelectOne(&approval,
		"select * from project__workflow_approval where project_id=? and workflow_run_id=? and workflow_node_id=?",
		projectID, runID, nodeID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowApproval{}, db.ErrNotFound
	}
	return approval, err
}

func (d *WorkflowStoreImpl) OpenWorkflowApproval(approval db.WorkflowApproval) (db.WorkflowApproval, bool, error) {
	if err := validateWorkflowApprovalForOpen(approval); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__workflow_run_node set status=?, queued=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowRunNodeApproval, approval.Created, approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodePending,
		approval.ProjectID, approval.WorkflowRunID, db.WorkflowRunDesiredRunning,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if updated == 0 {
		_ = tx.Rollback()
		existing, getErr := d.GetWorkflowApproval(approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID)
		if getErr == nil {
			return existing, false, nil
		}
		if errors.Is(getErr, db.ErrNotFound) {
			return db.WorkflowApproval{}, false, nil
		}
		return db.WorkflowApproval{}, false, getErr
	}
	approval.ID, err = d.insertTx(tx,
		"insert into project__workflow_approval(project_id, workflow_run_id, workflow_node_id, status, created, resolved, resolved_by_user_id, deadline, prompt, eligible_permission, separation_of_duties, request_actor_user_id, timeout_outcome, decision_comment, decision_source, correlation_id) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, approval.Status, approval.Created,
		approval.Resolved, approval.ResolvedByUserID, approval.Deadline, approval.Prompt, approval.EligiblePermission,
		sqlBool(approval.SeparationOfDuties), approval.RequestActorUserID, approval.TimeoutOutcome, approval.DecisionComment,
		approval.DecisionSource, approval.CorrelationID,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	return approval, true, nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowApproval(approval db.WorkflowApproval) error {
	return errors.New("workflow approvals are immutable")
}

func (d *WorkflowStoreImpl) ResolveWorkflowApprovalIfPending(approval db.WorkflowApproval) (bool, error) {
	if approval.Status != db.WorkflowApprovalApproved && approval.Status != db.WorkflowApprovalRejected && approval.Status != db.WorkflowApprovalExpired && approval.Status != db.WorkflowApprovalCanceled {
		return false, errors.New("workflow approval terminal status is invalid")
	}
	if approval.Resolved == nil || len(approval.DecisionComment) > db.MaxWorkflowApprovalCommentBytes {
		return false, errors.New("workflow approval decision is invalid")
	}
	result, err := d.connection.Exec(
		"update project__workflow_approval set status=?, resolved=?, resolved_by_user_id=?, decision_comment=?, decision_source=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=?",
		approval.Status, approval.Resolved, approval.ResolvedByUserID, approval.DecisionComment, approval.DecisionSource,
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowApprovalPending,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunApprovalNode(projectID int, runID int, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time) (bool, error) {
	if status != db.WorkflowRunNodeSucceeded && status != db.WorkflowRunNodeBlocked && status != db.WorkflowRunNodeCanceled {
		return false, fmt.Errorf("workflow approval cannot finalize node as %s", status)
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, result=?, `end`=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null",
		status, reason, resultJSON, at, projectID, runID, nodeID, db.WorkflowRunNodeApproval,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func validateWorkflowApprovalForOpen(approval db.WorkflowApproval) error {
	if approval.ProjectID <= 0 || approval.WorkflowRunID <= 0 || approval.WorkflowNodeID <= 0 || approval.RequestActorUserID <= 0 || approval.Status != db.WorkflowApprovalPending || approval.Created.IsZero() {
		return errors.New("workflow approval ownership is invalid")
	}
	if len(approval.Prompt) > db.MaxWorkflowApprovalPromptBytes || approval.EligiblePermission == 0 || approval.TimeoutOutcome.Validate() != nil || approval.CorrelationID == "" {
		return errors.New("workflow approval configuration is invalid")
	}
	if approval.Deadline != nil && !approval.Deadline.After(approval.Created) {
		return errors.New("workflow approval deadline is invalid")
	}
	return nil
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
		"update project__workflow_run_node set status=?, queued=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)",
		db.WorkflowRunNodeQueued, queuedAt, projectID, runID, nodeID, db.WorkflowRunNodePending,
		projectID, runID, db.WorkflowRunDesiredRunning,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) ClaimWorkflowRunNodeFenced(lease pro_interfaces.WorkflowReconciliationLease, nodeID int, queuedAt time.Time) (bool, error) {
	if lease.ProjectID <= 0 || lease.WorkflowRunID <= 0 || lease.OwnerBootID == "" || lease.FencingToken <= 0 || nodeID <= 0 {
		return false, errors.New("workflow reconciliation lease is invalid")
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, queued=coalesce(queued, ?), progression_fencing_token=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status in (?, ?) and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?) and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)",
		db.WorkflowRunNodeQueued, queuedAt, lease.FencingToken,
		lease.ProjectID, lease.WorkflowRunID, nodeID, db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
		lease.ProjectID, lease.WorkflowRunID, db.WorkflowRunDesiredRunning,
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunNodeFenced(
	lease pro_interfaces.WorkflowReconciliationLease,
	nodeID int,
	status db.WorkflowRunNodeStatus,
	reason string,
	resultJSON string,
	at time.Time,
) (bool, error) {
	if status != db.WorkflowRunNodeSkipped && status != db.WorkflowRunNodeBlocked && status != db.WorkflowRunNodeCanceled {
		return false, fmt.Errorf("workflow planner cannot finalize node as %s", status)
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, result=?, `end`=?, progression_fencing_token=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status in (?, ?) and task_id is null and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)",
		status, reason, resultJSON, at, lease.FencingToken,
		lease.ProjectID, lease.WorkflowRunID, nodeID, db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) OpenWorkflowApprovalFenced(lease pro_interfaces.WorkflowReconciliationLease, approval db.WorkflowApproval) (db.WorkflowApproval, bool, error) {
	if err := validateWorkflowApprovalForOpen(approval); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if approval.ProjectID != lease.ProjectID || approval.WorkflowRunID != lease.WorkflowRunID {
		return db.WorkflowApproval{}, false, errors.New("workflow approval reconciliation ownership is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := d.connection.ExecTx(tx,
		"update cluster__workflow_reconciliation set operation_sequence=operation_sequence+1, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if updated != 1 {
		return db.WorkflowApproval{}, false, errors.New("stale workflow reconciliation owner")
	}
	result, err = d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_run_node set status=?, queued=?, progression_fencing_token=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"),
		db.WorkflowRunNodeApproval, approval.Created, lease.FencingToken,
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowRunNodePending,
		approval.ProjectID, approval.WorkflowRunID, db.WorkflowRunDesiredRunning,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	updated, err = result.RowsAffected()
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if updated == 0 {
		_ = tx.Rollback()
		existing, getErr := d.GetWorkflowApproval(approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID)
		if getErr == nil {
			return existing, false, nil
		}
		if errors.Is(getErr, db.ErrNotFound) {
			return db.WorkflowApproval{}, false, nil
		}
		return db.WorkflowApproval{}, false, getErr
	}
	approval.ID, err = d.insertTx(tx,
		"insert into project__workflow_approval(project_id, workflow_run_id, workflow_node_id, status, created, resolved, resolved_by_user_id, deadline, prompt, eligible_permission, separation_of_duties, request_actor_user_id, timeout_outcome, decision_comment, decision_source, correlation_id) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, approval.Status, approval.Created,
		approval.Resolved, approval.ResolvedByUserID, approval.Deadline, approval.Prompt, approval.EligiblePermission,
		sqlBool(approval.SeparationOfDuties), approval.RequestActorUserID, approval.TimeoutOutcome, approval.DecisionComment,
		approval.DecisionSource, approval.CorrelationID,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	return approval, true, nil
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunApprovalNodeFenced(
	lease pro_interfaces.WorkflowReconciliationLease,
	nodeID int,
	status db.WorkflowRunNodeStatus,
	reason string,
	resultJSON string,
	at time.Time,
) (bool, error) {
	if status != db.WorkflowRunNodeSucceeded && status != db.WorkflowRunNodeBlocked && status != db.WorkflowRunNodeCanceled {
		return false, fmt.Errorf("workflow approval cannot finalize node as %s", status)
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, result=?, `end`=?, progression_fencing_token=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)",
		status, reason, resultJSON, at, lease.FencingToken,
		lease.ProjectID, lease.WorkflowRunID, nodeID, db.WorkflowRunNodeApproval,
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunStatusUnlessFenced(lease pro_interfaces.WorkflowReconciliationLease, run db.WorkflowRun, excluded []db.WorkflowRunStatus) (bool, error) {
	if len(excluded) == 0 {
		return false, errors.New("workflow run status exclusion is required")
	}
	placeholders := make([]string, len(excluded))
	args := []any{run.Status, run.Reason, run.Start, run.End, run.RootTaskID, run.ProjectID, run.ID}
	for index, status := range excluded {
		placeholders[index] = "?"
		args = append(args, status)
	}
	args = append(args, lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken)
	result, err := d.connection.Exec(
		"update project__workflow_run set status=?, reason=?, start=?, `end`=?, root_task_id=? where project_id=? and id=? and status not in ("+strings.Join(placeholders, ",")+") and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)",
		args...,
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
	resultJSON string,
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
		"update project__workflow_run_node set status=?, reason=?, result=?, start=coalesce(start, ?), `end`=coalesce(`end`, ?) where project_id=? and workflow_run_id=? and workflow_node_id=? and task_id=? and status not in (?, ?, ?, ?, ?, ?)",
		status, reason, resultJSON, start, end, projectID, runID, nodeID, taskID,
		db.WorkflowRunNodeSucceeded, db.WorkflowRunNodeFailed, db.WorkflowRunNodeStopped, db.WorkflowRunNodeCanceled, db.WorkflowRunNodeBlocked, db.WorkflowRunNodeSkipped,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) FinalizeWorkflowRunNode(
	projectID int,
	runID int,
	nodeID int,
	status db.WorkflowRunNodeStatus,
	reason string,
	resultJSON string,
	at time.Time,
) (bool, error) {
	if status != db.WorkflowRunNodeSkipped && status != db.WorkflowRunNodeBlocked && status != db.WorkflowRunNodeCanceled {
		return false, fmt.Errorf("workflow planner cannot finalize node as %s", status)
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, result=?, `end`=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status in (?, ?) and task_id is null",
		status, reason, resultJSON, at, projectID, runID, nodeID,
		db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) BlockWorkflowRunNode(projectID int, runID int, nodeID int, reason string, at time.Time) (bool, error) {
	result, err := d.connection.Exec(
		"update project__workflow_run_node set status=?, reason=?, `end`=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status in (?, ?) and task_id is null",
		db.WorkflowRunNodeBlocked, reason, at, projectID, runID, nodeID,
		db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) UpdateWorkflowRunNodeArtifactInputs(projectID int, runID int, nodeID int, inputsJSON string) (bool, error) {
	var inputs []db.WorkflowArtifactInputSnapshot
	if err := json.Unmarshal([]byte(inputsJSON), &inputs); err != nil {
		return false, fmt.Errorf("decode workflow artifact input snapshot: %w", err)
	}
	canonical, err := json.Marshal(inputs)
	if err != nil {
		return false, fmt.Errorf("encode workflow artifact input snapshot: %w", err)
	}
	result, err := d.connection.Exec(
		"update project__workflow_run_node set artifact_inputs=? where project_id=? and workflow_run_id=? and workflow_node_id=? and task_id is null and status in (?, ?)",
		string(canonical), projectID, runID, nodeID, db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (d *WorkflowStoreImpl) ReplaceWorkflowTaskArtifacts(
	projectID int,
	runID int,
	nodeID int,
	taskID int,
	attempt int,
	artifacts []db.WorkflowArtifact,
) error {
	if attempt < 0 {
		return errors.New("workflow artifact attempt must not be negative")
	}
	if len(artifacts) > db.MaxWorkflowArtifactsPerNode {
		return fmt.Errorf("workflow task produced more than %d declared artifacts", db.MaxWorkflowArtifactsPerNode)
	}
	prepared := make([]db.WorkflowArtifact, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for index, artifact := range artifacts {
		if _, duplicate := seen[artifact.Name]; duplicate {
			return fmt.Errorf("workflow artifact %q is duplicated", artifact.Name)
		}
		seen[artifact.Name] = struct{}{}
		if err := validateStoredWorkflowArtifact(&artifact); err != nil {
			return fmt.Errorf("workflow artifact %q: %w", artifact.Name, err)
		}
		artifact.ProjectID = projectID
		artifact.WorkflowRunID = runID
		artifact.WorkflowNodeID = nodeID
		artifact.TaskID = taskID
		artifact.Attempt = attempt
		prepared[index] = artifact
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var boundTasks int64
	boundTasks, err = tx.SelectInt(d.connection.PrepareQuery(
		"select count(*) from task where id=? and project_id=? and workflow_run_id=? and workflow_node_id=? and assignment_generation=? and exists (select 1 from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=?)"),
		taskID, projectID, runID, nodeID, attempt, projectID, runID, nodeID,
	)
	if err != nil {
		return err
	}
	if boundTasks != 1 {
		return db.ErrNotFound
	}
	if _, err = tx.Exec(d.connection.PrepareQuery(
		"delete from project__workflow_artifact where project_id=? and workflow_run_id=? and workflow_node_id=? and task_id=? and attempt=?"),
		projectID, runID, nodeID, taskID, attempt,
	); err != nil {
		return err
	}
	for index := range prepared {
		artifact := &prepared[index]
		artifact.ID, err = d.insertTx(tx,
			"insert into project__workflow_artifact(project_id, workflow_run_id, workflow_node_id, task_id, attempt, name, schema, sensitive, availability, size_bytes, reference_fingerprint, diagnostic, value_json, encrypted_value) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			artifact.ProjectID, artifact.WorkflowRunID, artifact.WorkflowNodeID, artifact.TaskID, artifact.Attempt,
			artifact.Name, artifact.SchemaJSON, artifact.Sensitive, artifact.Availability, artifact.SizeBytes,
			artifact.Fingerprint, artifact.Diagnostic, nullableWorkflowArtifactValue(artifact.ValueJSON), nullableWorkflowArtifactValue(artifact.EncryptedValue),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *WorkflowStoreImpl) GetWorkflowRunArtifacts(projectID int, runID int) ([]db.WorkflowArtifact, error) {
	var artifacts []db.WorkflowArtifact
	if _, err := d.connection.SelectAll(&artifacts,
		"select id, project_id, workflow_run_id, workflow_node_id, task_id, attempt, name, schema, sensitive, availability, size_bytes, reference_fingerprint, diagnostic, coalesce(value_json, '') as value_json, coalesce(encrypted_value, '') as encrypted_value from project__workflow_artifact where project_id=? and workflow_run_id=? order by workflow_node_id, task_id, attempt, name",
		projectID, runID,
	); err != nil {
		return nil, err
	}
	for index := range artifacts {
		if err := json.Unmarshal([]byte(artifacts[index].SchemaJSON), &artifacts[index].Schema); err != nil {
			return nil, fmt.Errorf("decode workflow artifact schema: %w", err)
		}
	}
	return artifacts, nil
}

func validateStoredWorkflowArtifact(artifact *db.WorkflowArtifact) error {
	if len(artifact.Diagnostic) > db.MaxWorkflowArtifactDiagnostic {
		return fmt.Errorf("diagnostic exceeds %d bytes", db.MaxWorkflowArtifactDiagnostic)
	}
	if artifact.SizeBytes < 0 || artifact.SizeBytes > db.MaxWorkflowArtifactObservedBytes {
		return errors.New("size is outside the supported range")
	}
	if len(artifact.Fingerprint) == 0 || len(artifact.Fingerprint) > 80 {
		return errors.New("reference fingerprint is invalid")
	}
	declaration := db.WorkflowArtifactDeclaration{
		Name: artifact.Name, Schema: artifact.Schema, MaxBytes: db.MaxWorkflowArtifactBytes,
	}
	if err := declaration.Validate(); err != nil {
		return err
	}
	schemaJSON, err := json.Marshal(artifact.Schema)
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}
	artifact.SchemaJSON = string(schemaJSON)
	switch artifact.Availability {
	case db.WorkflowArtifactAvailable:
		if artifact.SizeBytes < 1 || artifact.SizeBytes > db.MaxWorkflowArtifactBytes {
			return errors.New("available value size is outside the supported range")
		}
		if artifact.Sensitive {
			if artifact.EncryptedValue == "" || artifact.ValueJSON != "" {
				return errors.New("available sensitive value must contain ciphertext only")
			}
		} else {
			if artifact.ValueJSON == "" || artifact.EncryptedValue != "" || !json.Valid([]byte(artifact.ValueJSON)) {
				return errors.New("available value must contain valid plaintext JSON only")
			}
			if artifact.SizeBytes != len(artifact.ValueJSON) {
				return errors.New("available plaintext size does not match stored JSON")
			}
		}
	case db.WorkflowArtifactUnavailable:
		if artifact.SizeBytes != 0 {
			return errors.New("unavailable value size must be zero")
		}
		if artifact.ValueJSON != "" || artifact.EncryptedValue != "" {
			return errors.New("unavailable value must not be stored")
		}
	case db.WorkflowArtifactInvalid:
		if artifact.ValueJSON != "" || artifact.EncryptedValue != "" {
			return errors.New("invalid value must not be stored")
		}
	default:
		return errors.New("availability is invalid")
	}
	return nil
}

func nullableWorkflowArtifactValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (d *WorkflowStoreImpl) loadWorkflowRun(run *db.WorkflowRun) error {
	if run.DefinitionSnapshotJSON != "" {
		if err := json.Unmarshal([]byte(run.DefinitionSnapshotJSON), &run.DefinitionSnapshot); err != nil {
			return fmt.Errorf("decode workflow definition snapshot: %w", err)
		}
	}
	if run.ParameterSnapshotJSON != "" {
		if err := json.Unmarshal([]byte(run.ParameterSnapshotJSON), &run.ParameterSnapshot); err != nil {
			return fmt.Errorf("decode workflow parameter snapshot: %w", err)
		}
	}
	if run.TriggerSnapshotJSON != "" && run.TriggerSnapshotJSON != "{}" {
		if err := json.Unmarshal([]byte(run.TriggerSnapshotJSON), &run.TriggerSnapshot); err != nil {
			return fmt.Errorf("decode workflow trigger snapshot: %w", err)
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
	diagnostics, found, err := NewWorkflowReconciliationStore(d.connection).GetWorkflowReconciliationDiagnostics(run.ProjectID, run.ID)
	if err != nil {
		return err
	}
	if found {
		run.ReconciliationOwnership = &diagnostics
	}
	return nil
}

func decodeWorkflowRunNode(node *db.WorkflowRunNode) error {
	if node.TemplateSnapshotJSON != "" {
		if err := json.Unmarshal([]byte(node.TemplateSnapshotJSON), &node.TemplateSnapshot); err != nil {
			return fmt.Errorf("decode workflow template snapshot: %w", err)
		}
	}
	if node.ResultJSON != "" && node.ResultJSON != "{}" {
		if err := json.Unmarshal([]byte(node.ResultJSON), &node.Result); err != nil {
			return fmt.Errorf("decode workflow node result: %w", err)
		}
	}
	if node.ArtifactInputsJSON != "" {
		if err := json.Unmarshal([]byte(node.ArtifactInputsJSON), &node.ArtifactInputs); err != nil {
			return fmt.Errorf("decode workflow artifact input snapshot: %w", err)
		}
	}
	if node.OverrideSnapshotJSON != "" {
		if err := json.Unmarshal([]byte(node.OverrideSnapshotJSON), &node.OverrideSnapshot); err != nil {
			return fmt.Errorf("decode workflow node override snapshot: %w", err)
		}
	}
	return nil
}
