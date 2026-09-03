package sql

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
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
	return d.createWorkflowRun(run, false)
}

func (d *WorkflowStoreImpl) CreateWorkflowRunWithCrossProjectReferences(run db.WorkflowRun) (db.WorkflowRun, error) {
	return d.createWorkflowRun(run, true)
}

func (d *WorkflowStoreImpl) createWorkflowRun(run db.WorkflowRun, crossProjectReferences bool) (db.WorkflowRun, error) {
	if d.deploymentWindowRequired && run.DeploymentWindowDecisionID == nil {
		return db.WorkflowRun{}, errors.New("deployment window decision is required before workflow persistence")
	}
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
	if crossProjectReferences {
		if err = d.recheckWorkflowRunCrossProjectReferencesTx(tx, run); err != nil {
			return db.WorkflowRun{}, err
		}
	}
	run.ID, err = d.insertTx(tx,
		"insert into project__workflow_run(project_id, workflow_template_id, status, desired_state, reconciliation_state, reconciliation_attempts, reconciliation_last_error, reconciliation_next_retry_at, reconciliation_quarantined_at, version, start, `end`, root_task_id, actor_user_id, definition_version, definition_revision, workflow_version_id, definition_snapshot, parameter_snapshot, trigger_snapshot, correlation_id, created, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		run.ProjectID, run.WorkflowTemplateID, run.Status, run.DesiredState, run.ReconciliationState, run.ReconciliationAttempts, run.ReconciliationLastError, run.ReconciliationNextRetryAt, run.ReconciliationQuarantinedAt, run.Version, run.Start, run.End, run.RootTaskID,
		run.ActorUserID, run.DefinitionVersion, run.DefinitionRevision, run.WorkflowVersionID, run.DefinitionSnapshotJSON,
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
		if node.CrossProjectTemplateProvenance != nil {
			provenanceJSON, provenanceErr := node.CrossProjectTemplateProvenance.CanonicalJSON()
			if provenanceErr != nil {
				return db.WorkflowRun{}, fmt.Errorf("encode workflow run cross-project template provenance: %w", provenanceErr)
			}
			node.CrossProjectTemplateProvenanceJSON = provenanceJSON
		} else if node.CrossProjectTemplateProvenanceJSON != "" {
			return db.WorkflowRun{}, errors.New("workflow run cross-project template provenance must be decoded before persistence")
		}
		node.ID, err = d.insertTx(tx,
			"insert into project__workflow_run_node(project_id, workflow_run_id, workflow_node_id, template_id, status, task_id, template_snapshot, execution_snapshot, cross_project_template_provenance, result, artifact_inputs, override_snapshot, created, queued, start, `end`, reason) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			node.ProjectID, node.WorkflowRunID, node.WorkflowNodeID, node.TemplateID, node.Status,
			node.TaskID, node.TemplateSnapshotJSON, node.ExecutionSnapshotJSON, node.CrossProjectTemplateProvenanceJSON, node.ResultJSON, node.ArtifactInputsJSON, node.OverrideSnapshotJSON, node.Created, node.Queued, node.Start, node.End, node.Reason,
		)
		if err != nil {
			return db.WorkflowRun{}, err
		}
	}
	if run.DeploymentWindowDecisionID != nil {
		if err = d.bindDeploymentWindowWorkflowRunTx(tx, run); err != nil {
			return db.WorkflowRun{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowRun{}, err
	}
	rollback = false
	return run, nil
}

func (d *WorkflowStoreImpl) bindDeploymentWindowWorkflowRunTx(tx *gorp.Transaction, run db.WorkflowRun) error {
	if run.DeploymentWindowDecisionID == nil || *run.DeploymentWindowDecisionID <= 0 {
		return errors.New("deployment window workflow admission is invalid")
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__deployment_window_decision set workflow_run_id=? where id=? and project_id=? and workflow_template_id=? and state in (?, ?) and workflow_run_id is null and task_id is null"),
		run.ID, *run.DeploymentWindowDecisionID, run.ProjectID, run.WorkflowTemplateID, "allowed", "overridden",
	)
	if err != nil {
		return err
	}
	bound, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if bound != 1 {
		return errors.New("deployment window decision cannot be bound to workflow")
	}
	return nil
}

// BlockWorkflowRunNodeForDeploymentWindow atomically makes a queued/pending
// node terminal and links the exact blocked decision. A stale reconciler may
// not overwrite a successor because the optional progression fencing token is
// part of the same CAS predicate.
func (d *WorkflowStoreImpl) BlockWorkflowRunNodeForDeploymentWindow(projectID, runID, nodeID, decisionID int, resultJSON string, lease *pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if d == nil || d.connection == nil || projectID <= 0 || runID <= 0 || nodeID <= 0 || decisionID <= 0 || resultJSON == "" {
		return false, errors.New("workflow deployment-window block is invalid")
	}
	query := `update project__workflow_run_node
	 set status=?, reason='deployment_window_blocked', result=?,
	     deployment_window_decision_id=?,
	     next_eligible_at=(select next_eligible_at from project__deployment_window_decision where id=?),
	     next_eligible_known=(select next_eligible_known from project__deployment_window_decision where id=?),
	     blocked_at=CURRENT_TIMESTAMP, ` + "`end`" + `=CURRENT_TIMESTAMP
	 where project_id=? and workflow_run_id=? and workflow_node_id=? and task_id is null
	   and status in (?, ?)
	   and exists (
	     select 1 from project__deployment_window_decision d
	     where d.id=? and d.project_id=? and d.workflow_run_id=?
	       and d.workflow_run_node_id=project__workflow_run_node.id
	       and d.source='workflow_node' and d.origin='workflow_node'
	       and d.state='blocked' and d.task_id is null
	   )`
	args := []any{db.WorkflowRunNodeBlocked, resultJSON, decisionID, decisionID, decisionID,
		projectID, runID, nodeID, db.WorkflowRunNodePending, db.WorkflowRunNodeQueued,
		decisionID, projectID, runID}
	if lease != nil {
		if lease.ProjectID != projectID || lease.WorkflowRunID != runID || lease.OwnerBootID == "" || lease.FencingToken <= 0 {
			return false, errors.New("workflow deployment-window block lease is invalid")
		}
		query += " and progression_fencing_token=? and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)"
		args = append(args, lease.FencingToken, lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken)
	}
	result, err := d.connection.Exec(d.connection.PrepareQuery(query), args...)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated == 1 {
		return updated == 1, err
	}
	var existing struct {
		Status     db.WorkflowRunNodeStatus `db:"status"`
		DecisionID *int                     `db:"deployment_window_decision_id"`
	}
	err = d.connection.SelectOne(&existing, d.connection.PrepareQuery(
		"select status, deployment_window_decision_id from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=?"), projectID, runID, nodeID)
	if err != nil {
		return false, err
	}
	return existing.Status == db.WorkflowRunNodeBlocked && existing.DecisionID != nil && *existing.DecisionID == decisionID, nil
}

func (d *WorkflowStoreImpl) recheckWorkflowRunCrossProjectReferencesTx(tx *gorp.Transaction, run db.WorkflowRun) error {
	definitionNodes := make(map[int]db.WorkflowNode, len(run.DefinitionSnapshot.Nodes))
	for _, definitionNode := range run.DefinitionSnapshot.Nodes {
		definitionNodes[definitionNode.ID] = definitionNode
	}
	for _, node := range run.Nodes {
		if node.CrossProjectTemplateProvenance == nil {
			continue
		}
		provenance := node.CrossProjectTemplateProvenance
		definitionNode, found := definitionNodes[node.WorkflowNodeID]
		if !found || definitionNode.CrossProjectTemplateReference == nil ||
			*definitionNode.CrossProjectTemplateReference != provenance.Reference || node.TemplateID != definitionNode.TemplateID {
			return errors.New("workflow run-node cross-project provenance does not match definition")
		}
		normalized, version, err := d.resolveActiveCrossProjectTemplateGrantTx(tx, run.ProjectID, provenance.Reference, db.CrossProjectTemplateGrantRun)
		if err != nil {
			return err
		}
		if normalized != provenance.Reference || version.ContentFingerprint != provenance.Reference.ContentFingerprint {
			return db.ErrNotFound
		}
		canonical, canonicalErr := provenance.CanonicalJSON()
		if canonicalErr != nil || canonical == "" {
			if canonicalErr != nil {
				return canonicalErr
			}
			return errors.New("workflow cross-project template provenance is empty")
		}
	}
	return nil
}

// CreateCrossProjectWorkflowTaskFenced binds one consumer-owned task to a
// queued external workflow node. The grant/version recheck, run-node
// reconciliation, task insert, and attachment share one transaction so a
// revoke or stop cannot leave a dispatchable orphan behind.
func (d *WorkflowStoreImpl) CreateCrossProjectWorkflowTaskFenced(
	task db.Task,
	provenance db.CrossProjectTemplateProvenance,
	lease *pro_interfaces.WorkflowReconciliationLease,
) (db.Task, error) {
	if d.connection == nil || task.ProjectID <= 0 || task.WorkflowRunID == nil || task.WorkflowNodeID == nil ||
		task.PolicyGuardrailEvaluationID == nil || *task.PolicyGuardrailEvaluationID <= 0 ||
		provenance.Validate() != nil || task.TemplateID != provenance.Reference.TemplateID {
		return db.Task{}, errors.New("cross-project workflow task provenance is invalid")
	}
	if lease != nil && (lease.ProjectID != task.ProjectID || lease.WorkflowRunID != *task.WorkflowRunID ||
		lease.OwnerBootID == "" || lease.FencingToken <= 0) {
		return db.Task{}, errors.New("cross-project workflow reconciliation ownership is invalid")
	}
	if existing, err := d.GetWorkflowRunNodeTask(task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID); err == nil {
		if crossProjectWorkflowTaskMatches(existing, provenance) {
			return existing, nil
		}
		return db.Task{}, errors.New("cross-project workflow task attachment conflicts with existing provenance")
	} else if !errors.Is(err, db.ErrNotFound) {
		return db.Task{}, err
	}

	expectedTemplate, err := provenance.TemplateSnapshot.ReconstructTemplate(
		provenance.Reference.OwnerProjectID, provenance.Reference.TemplateID,
	)
	if err != nil {
		return db.Task{}, err
	}
	expectedTemplateJSON, err := json.Marshal(expectedTemplate)
	if err != nil {
		return db.Task{}, fmt.Errorf("encode immutable cross-project template snapshot: %w", err)
	}
	if task.WorkflowTemplateSnapshot == nil || *task.WorkflowTemplateSnapshot != string(expectedTemplateJSON) {
		return db.Task{}, errors.New("cross-project workflow task template snapshot is invalid")
	}
	taskProvenance := db.WorkflowTemplateProvenance{CrossProject: &provenance}
	canonicalTaskProvenance, err := taskProvenance.CanonicalJSON()
	if err != nil {
		return db.Task{}, err
	}
	if task.WorkflowTemplateProvenance != nil {
		provided, providedErr := task.WorkflowTemplateProvenance.CanonicalJSON()
		if providedErr != nil || provided != canonicalTaskProvenance {
			return db.Task{}, errors.New("cross-project workflow task provenance is invalid")
		}
	}
	task.WorkflowTemplateProvenance = &taskProvenance
	task.WorkflowTemplateProvenanceJSON = &canonicalTaskProvenance

	tx, err := d.connection.Begin()
	if err != nil {
		return db.Task{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()
	if lease != nil {
		result, updateErr := d.connection.ExecTx(tx,
			"update cluster__workflow_reconciliation set operation_sequence=operation_sequence+1, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
			lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
		)
		if updateErr != nil {
			return db.Task{}, updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return db.Task{}, rowsErr
		}
		if updated != 1 {
			return db.Task{}, errors.New("stale workflow reconciliation owner")
		}
	}

	var run struct {
		DefinitionSnapshotJSON string                     `db:"definition_snapshot"`
		DesiredState           db.WorkflowRunDesiredState `db:"desired_state"`
	}
	if err = tx.SelectOne(&run, d.connection.PrepareQuery(
		"select definition_snapshot, desired_state from project__workflow_run where project_id=? and id=?"),
		task.ProjectID, *task.WorkflowRunID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Task{}, db.ErrNotFound
		}
		return db.Task{}, err
	}
	if run.DesiredState != db.WorkflowRunDesiredRunning {
		return db.Task{}, db.ErrNotFound
	}
	var definition db.WorkflowTemplate
	if err = json.Unmarshal([]byte(run.DefinitionSnapshotJSON), &definition); err != nil {
		return db.Task{}, fmt.Errorf("decode workflow definition snapshot: %w", err)
	}
	definitionNode, found := workflowDefinitionNodeByID(definition, *task.WorkflowNodeID)
	if !found || definitionNode.CrossProjectTemplateReference == nil ||
		definitionNode.TemplateID != task.TemplateID || *definitionNode.CrossProjectTemplateReference != provenance.Reference {
		return db.Task{}, errors.New("cross-project workflow definition provenance does not match task")
	}

	var node struct {
		TemplateID                 int                      `db:"template_id"`
		Status                     db.WorkflowRunNodeStatus `db:"status"`
		TaskID                     *int                     `db:"task_id"`
		CrossProjectProvenanceJSON string                   `db:"cross_project_template_provenance"`
		ProgressionFencingToken    int64                    `db:"progression_fencing_token"`
	}
	if err = tx.SelectOne(&node, d.connection.PrepareQuery(
		"select template_id, status, task_id, cross_project_template_provenance, progression_fencing_token from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=?"),
		task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Task{}, db.ErrNotFound
		}
		return db.Task{}, err
	}
	if node.TemplateID != task.TemplateID || node.Status != db.WorkflowRunNodeQueued || node.TaskID != nil ||
		lease != nil && node.ProgressionFencingToken != lease.FencingToken {
		return db.Task{}, db.ErrNotFound
	}
	storedProvenance, decodeErr := db.DecodeCrossProjectTemplateProvenance(node.CrossProjectProvenanceJSON)
	if decodeErr != nil || storedProvenance == nil || !crossProjectTemplateProvenanceEqual(*storedProvenance, provenance) {
		return db.Task{}, errors.New("cross-project workflow run-node provenance does not match task")
	}

	normalized, version, resolveErr := d.resolveActiveCrossProjectTemplateGrantTx(
		tx, task.ProjectID, provenance.Reference, db.CrossProjectTemplateGrantRun,
	)
	if resolveErr != nil {
		return db.Task{}, resolveErr
	}
	if normalized != provenance.Reference || version.ContentFingerprint != provenance.Reference.ContentFingerprint {
		return db.Task{}, db.ErrNotFound
	}
	if task.DeploymentWindowDecisionID != nil {
		if err = d.validateCrossProjectDeploymentWindowTaskBindingTx(tx, task); err != nil {
			return db.Task{}, err
		}
	}
	if err = d.validateCrossProjectPolicyGuardrailTaskBindingTx(tx, task); err != nil {
		return db.Task{}, err
	}
	if err = tx.Insert(&task); err != nil {
		_ = tx.Rollback()
		rollback = false
		return d.resolveExistingCrossProjectWorkflowTask(task, provenance, err)
	}
	if task.DeploymentWindowDecisionID != nil {
		result, bindErr := tx.Exec(d.connection.PrepareQuery(
			"update project__deployment_window_decision set task_id=? where id=? and project_id=? and state in (?, ?) and task_id is null"),
			task.ID, *task.DeploymentWindowDecisionID, task.ProjectID, "allowed", "overridden",
		)
		if bindErr != nil {
			return db.Task{}, bindErr
		}
		bound, rowsErr := result.RowsAffected()
		if rowsErr != nil || bound != 1 {
			return db.Task{}, errors.New("deployment window decision cannot be bound to cross-project workflow task")
		}
	}
	if err = d.bindCrossProjectPolicyGuardrailTaskEvaluationTx(tx, task); err != nil {
		return db.Task{}, err
	}
	attachQuery := "update project__workflow_run_node set task_id=? where project_id=? and workflow_run_id=? and workflow_node_id=? and status=? and task_id is null and exists (select 1 from project__workflow_run where project_id=? and id=? and desired_state=?)"
	attachArgs := []any{task.ID, task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, db.WorkflowRunNodeQueued, task.ProjectID, *task.WorkflowRunID, db.WorkflowRunDesiredRunning}
	if lease != nil {
		attachQuery += " and progression_fencing_token=?"
		attachArgs = append(attachArgs, lease.FencingToken)
	}
	result, attachErr := tx.Exec(d.connection.PrepareQuery(attachQuery), attachArgs...)
	if attachErr != nil {
		return db.Task{}, attachErr
	}
	attached, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return db.Task{}, rowsErr
	}
	if attached != 1 {
		_ = tx.Rollback()
		rollback = false
		return d.resolveExistingCrossProjectWorkflowTask(task, provenance, db.ErrNotFound)
	}
	if err = tx.Commit(); err != nil {
		rollback = false
		return d.resolveExistingCrossProjectWorkflowTask(task, provenance, err)
	}
	rollback = false
	return task, nil
}

func (d *WorkflowStoreImpl) validateCrossProjectPolicyGuardrailTaskBindingTx(tx *gorp.Transaction, task db.Task) error {
	if task.PolicyGuardrailEvaluationID == nil || *task.PolicyGuardrailEvaluationID <= 0 || task.WorkflowRunID == nil || task.WorkflowNodeID == nil {
		return errors.New("cross-project policy guardrail evaluation is invalid")
	}
	var evaluation struct {
		ProjectID         int    `db:"project_id"`
		Intent            string `db:"intent"`
		TemplateID        *int   `db:"template_id"`
		WorkflowRunID     *int   `db:"workflow_run_id"`
		WorkflowRunNodeID *int   `db:"workflow_run_node_id"`
		TaskID            *int   `db:"task_id"`
		Decision          string `db:"decision"`
	}
	if err := tx.SelectOne(&evaluation, d.connection.PrepareQuery(
		"select project_id, intent, template_id, workflow_run_id, workflow_run_node_id, task_id, decision from policy_guardrail_evaluation where id=?"), *task.PolicyGuardrailEvaluationID); err != nil {
		return errors.New("cross-project policy guardrail evaluation is unavailable")
	}
	if evaluation.ProjectID != task.ProjectID || evaluation.Intent != "task" || evaluation.TemplateID == nil || *evaluation.TemplateID != task.TemplateID ||
		evaluation.WorkflowRunID == nil || *evaluation.WorkflowRunID != *task.WorkflowRunID || evaluation.WorkflowRunNodeID == nil || evaluation.TaskID != nil ||
		evaluation.Decision != string(db.PolicyGuardrailDecisionAllow) {
		return errors.New("cross-project policy guardrail evaluation does not match task")
	}
	var nodeCount int
	if err := tx.SelectOne(&nodeCount, d.connection.PrepareQuery(
		"select count(1) from project__workflow_run_node where id=? and project_id=? and workflow_run_id=? and workflow_node_id=?"),
		*evaluation.WorkflowRunNodeID, task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID,
	); err != nil || nodeCount != 1 {
		return errors.New("cross-project policy guardrail evaluation does not match task")
	}
	return nil
}

func (d *WorkflowStoreImpl) bindCrossProjectPolicyGuardrailTaskEvaluationTx(tx *gorp.Transaction, task db.Task) error {
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update policy_guardrail_evaluation set task_id=? where id=? and project_id=? and intent=? and template_id=? and workflow_run_id=? and workflow_run_node_id in (select id from project__workflow_run_node where project_id=? and workflow_run_id=? and workflow_node_id=?) and decision=? and task_id is null"),
		task.ID, *task.PolicyGuardrailEvaluationID, task.ProjectID, "task", task.TemplateID, *task.WorkflowRunID,
		task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, db.PolicyGuardrailDecisionAllow,
	)
	if err != nil {
		return err
	}
	bound, err := result.RowsAffected()
	if err != nil || bound != 1 {
		return errors.New("cross-project policy guardrail evaluation cannot be bound to task")
	}
	return nil
}

func (d *WorkflowStoreImpl) validateCrossProjectDeploymentWindowTaskBindingTx(tx *gorp.Transaction, task db.Task) error {
	if task.DeploymentWindowDecisionID == nil || task.WorkflowRunID == nil || task.WorkflowNodeID == nil {
		return errors.New("cross-project deployment window admission is invalid")
	}
	var decision struct {
		ProjectID         int    `db:"project_id"`
		WorkflowRunID     *int   `db:"workflow_run_id"`
		WorkflowRunNodeID *int   `db:"workflow_run_node_id"`
		TaskID            *int   `db:"task_id"`
		State             string `db:"state"`
	}
	if err := tx.SelectOne(&decision, d.connection.PrepareQuery(
		"select project_id, workflow_run_id, workflow_run_node_id, task_id, state from project__deployment_window_decision where id=?"), *task.DeploymentWindowDecisionID); err != nil {
		return errors.New("cross-project deployment window decision is unavailable")
	}
	if decision.ProjectID != task.ProjectID || decision.WorkflowRunID == nil || *decision.WorkflowRunID != *task.WorkflowRunID || decision.WorkflowRunNodeID == nil || decision.TaskID != nil ||
		(decision.State != "allowed" && decision.State != "overridden") {
		return errors.New("cross-project deployment window decision does not match task")
	}
	var count int
	if err := tx.SelectOne(&count, d.connection.PrepareQuery(
		"select count(1) from project__workflow_run_node where id=? and project_id=? and workflow_run_id=? and workflow_node_id=?"),
		*decision.WorkflowRunNodeID, task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID); err != nil || count != 1 {
		return errors.New("cross-project deployment window decision does not match task")
	}
	return nil
}

func workflowDefinitionNodeByID(definition db.WorkflowTemplate, nodeID int) (db.WorkflowNode, bool) {
	for _, node := range definition.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return db.WorkflowNode{}, false
}

func (d *WorkflowStoreImpl) resolveExistingCrossProjectWorkflowTask(task db.Task, provenance db.CrossProjectTemplateProvenance, cause error) (db.Task, error) {
	existing, err := d.GetWorkflowRunNodeTask(task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID)
	if err == nil && crossProjectWorkflowTaskMatches(existing, provenance) {
		return existing, nil
	}
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return db.Task{}, err
	}
	return db.Task{}, cause
}

func crossProjectWorkflowTaskMatches(task db.Task, provenance db.CrossProjectTemplateProvenance) bool {
	if task.TemplateID != provenance.Reference.TemplateID || task.WorkflowTemplateProvenance == nil ||
		task.WorkflowTemplateProvenance.CrossProject == nil {
		return false
	}
	return crossProjectTemplateProvenanceEqual(*task.WorkflowTemplateProvenance.CrossProject, provenance)
}

func crossProjectTemplateProvenanceEqual(left db.CrossProjectTemplateProvenance, right db.CrossProjectTemplateProvenance) bool {
	leftJSON, leftErr := left.CanonicalJSON()
	rightJSON, rightErr := right.CanonicalJSON()
	return leftErr == nil && rightErr == nil && leftJSON == rightJSON
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
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current db.WorkflowRun
	if err = tx.SelectOne(&current, d.connection.PrepareQuery("select * from project__workflow_run where project_id=? and id=?"), run.ProjectID, run.ID); err != nil {
		return false, err
	}
	query := "update project__workflow_run set status=?, reason=?, `end`=?"
	args := []any{run.Status, run.Reason, run.End}
	notify := current.Status != run.Status && run.Status.IsFinished()
	if notify {
		query += ", notification_revision=notification_revision+1"
	}
	query += " where project_id=? and id=?"
	args = append(args, run.ProjectID, run.ID)
	if len(excluded) > 0 {
		query += " and status not in (" + strings.TrimRight(strings.Repeat("?,", len(excluded)), ",") + ")"
		for _, status := range excluded {
			args = append(args, status)
		}
	}
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(query), args...)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return updated == 1, err
	}
	if notify {
		run.NotificationRevision = current.NotificationRevision + 1
		if err = d.notificationRouter.RouteTx(tx, workflowTerminalNotification(run)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func workflowTerminalNotification(run db.WorkflowRun) pro_interfaces.NotificationEvent {
	severity, action, status := pro_interfaces.NotificationSeverityWarning, pro_interfaces.NotificationLifecycleUpdate, "stopped"
	switch run.Status {
	case db.WorkflowRunSucceeded, db.WorkflowRunSuccess:
		severity, action, status = pro_interfaces.NotificationSeverityInfo, pro_interfaces.NotificationLifecycleResolve, "succeeded"
	case db.WorkflowRunFailed, db.WorkflowRunBlocked:
		severity, action, status = pro_interfaces.NotificationSeverityError, pro_interfaces.NotificationLifecycleTrigger, string(run.Status)
	case db.WorkflowRunCanceled, db.WorkflowRunStopped:
		status = string(run.Status)
	}
	projectID, workflowID, runID := run.ProjectID, run.WorkflowTemplateID, run.ID
	return pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeProject, ProjectID: &projectID,
		Source:      pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceWorkflow, ID: fmt.Sprintf("workflow_run:%d", run.ID)},
		LifecycleID: fmt.Sprintf("workflow:%d", run.WorkflowTemplateID), SourceRevision: run.NotificationRevision,
		Severity: severity, LifecycleAction: action,
		Details: pro_interfaces.NotificationDetails{WorkflowID: &workflowID, WorkflowRunID: &runID, Status: status},
	}
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
	for index := range approvals {
		if err := decodeWorkflowApprovalPolicy(&approvals[index]); err != nil {
			return nil, err
		}
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
	for index := range approvals {
		if err := decodeWorkflowApprovalPolicy(&approvals[index]); err != nil {
			return nil, err
		}
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
	if err == nil {
		err = decodeWorkflowApprovalPolicy(&approval)
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
		"insert into project__workflow_approval(project_id, workflow_run_id, workflow_node_id, status, created, resolved, resolved_by_user_id, deadline, prompt, eligible_permission, role_policy_snapshot, role_policy_revision, separation_of_duties, request_actor_user_id, timeout_outcome, decision_comment, decision_source, correlation_id) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, approval.Status, approval.Created,
		approval.Resolved, approval.ResolvedByUserID, approval.Deadline, approval.Prompt, approval.EligiblePermission,
		approval.RolePolicySnapshotJSON, approval.RolePolicySnapshotRevision, sqlBool(approval.SeparationOfDuties), approval.RequestActorUserID, approval.TimeoutOutcome, approval.DecisionComment,
		approval.DecisionSource, approval.CorrelationID,
	)
	if err != nil {
		return db.WorkflowApproval{}, false, err
	}
	var run db.WorkflowRun
	if err = tx.SelectOne(&run, d.connection.PrepareQuery("select * from project__workflow_run where project_id=? and id=?"), approval.ProjectID, approval.WorkflowRunID); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	approval.WorkflowTemplateID = run.WorkflowTemplateID
	approval.NotificationRevision = 1
	if _, err = d.connection.ExecTx(tx, d.connection.PrepareQuery("update project__workflow_approval set notification_revision=1 where id=?"), approval.ID); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if err = d.notificationRouter.RouteTx(tx, workflowApprovalNotification(approval, pro_interfaces.NotificationSeverityWarning, pro_interfaces.NotificationLifecycleTrigger, "pending")); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowApproval{}, false, err
	}
	return approval, true, nil
}

func workflowApprovalNotification(approval db.WorkflowApproval, severity pro_interfaces.NotificationSeverity, action pro_interfaces.NotificationLifecycleAction, status string) pro_interfaces.NotificationEvent {
	projectID, workflowID, runID, approvalID := approval.ProjectID, approval.WorkflowTemplateID, approval.WorkflowRunID, approval.ID
	return pro_interfaces.NotificationEvent{Scope: pro_interfaces.NotificationScopeProject, ProjectID: &projectID,
		Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceApproval, ID: fmt.Sprintf("approval:%d", approval.ID)}, LifecycleID: fmt.Sprintf("approval:%d", approval.ID), SourceRevision: approval.NotificationRevision,
		Severity: severity, LifecycleAction: action, Details: pro_interfaces.NotificationDetails{WorkflowID: &workflowID, WorkflowRunID: &runID, ApprovalID: &approvalID, Status: status}}
}

func decodeWorkflowApprovalPolicy(approval *db.WorkflowApproval) error {
	if approval.RolePolicySnapshotRevision == 0 {
		return nil
	}
	if approval.RolePolicySnapshotJSON == "" || approval.RolePolicySnapshotJSON == "{}" {
		return errors.New("workflow approval role policy snapshot is missing")
	}
	if err := json.Unmarshal([]byte(approval.RolePolicySnapshotJSON), &approval.RolePolicySnapshot); err != nil {
		return fmt.Errorf("decode workflow approval role policy snapshot: %w", err)
	}
	if approval.RolePolicySnapshot.PolicyRevision != approval.RolePolicySnapshotRevision ||
		approval.RolePolicySnapshot.Policy.Validate() != nil {
		return errors.New("workflow approval role policy snapshot is invalid")
	}
	return nil
}

func (d *WorkflowStoreImpl) GetWorkflowApprovalContributions(approvalID int) ([]db.WorkflowApprovalContribution, error) {
	contributions := make([]db.WorkflowApprovalContribution, 0)
	if _, err := d.connection.SelectAll(&contributions,
		"select * from project__workflow_approval_contribution where workflow_approval_id=? order by created, actor_user_id", approvalID,
	); err != nil {
		return nil, err
	}
	return contributions, nil
}

func (d *WorkflowStoreImpl) CreateWorkflowApprovalContribution(contribution db.WorkflowApprovalContribution) error {
	if err := contribution.Validate(); err != nil {
		return err
	}
	_, err := d.connection.Exec(d.connection.PrepareQuery(
		"insert into project__workflow_approval_contribution(workflow_approval_id, actor_user_id, role_id, role_revision, role_origin, directory_provider_id, directory_mapping_id, directory_mapping_revision, directory_revision_fingerprint, decision, comment, created, policy_revision, correlation_id) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		contribution.ApprovalID, contribution.ActorUserID, contribution.RoleID, contribution.RoleRevision, contribution.RoleOrigin,
		contribution.DirectoryProviderID, contribution.DirectoryMappingID, contribution.DirectoryMappingRevision, contribution.DirectoryRevisionFingerprint,
		contribution.Decision, contribution.Comment, contribution.Created, contribution.PolicyRevision, contribution.CorrelationID,
	)
	return err
}

func (d *WorkflowStoreImpl) SubmitWorkflowApprovalContribution(
	submission db.WorkflowApprovalContributionSubmission,
) (db.WorkflowApprovalContributionResult, error) {
	if submission.ProjectID <= 0 || submission.WorkflowRunID <= 0 || submission.WorkflowNodeID <= 0 ||
		submission.ActorUserID <= 0 || submission.At.IsZero() || submission.Decision.Validate() != nil {
		return db.WorkflowApprovalContributionResult{}, errors.New("workflow approval contribution is invalid")
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var approval db.WorkflowApproval
	err = tx.SelectOne(&approval, d.connection.PrepareQuery(
		"select * from project__workflow_approval where project_id=? and workflow_run_id=? and workflow_node_id=?"),
		submission.ProjectID, submission.WorkflowRunID, submission.WorkflowNodeID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowApprovalContributionResult{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	if err = decodeWorkflowApprovalPolicy(&approval); err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	if approval.Status != db.WorkflowApprovalPending {
		return db.WorkflowApprovalContributionResult{Approval: approval, Terminal: true}, nil
	}
	if approval.Deadline != nil && !approval.Deadline.After(submission.At) {
		if err = finalizeWorkflowApprovalTx(tx, d, &approval, db.WorkflowApprovalExpired, submission.At, nil, "", db.WorkflowApprovalDecisionSourceTimeout); err != nil {
			return db.WorkflowApprovalContributionResult{}, err
		}
		if err = tx.Commit(); err != nil {
			return db.WorkflowApprovalContributionResult{}, err
		}
		return db.WorkflowApprovalContributionResult{Approval: approval, Terminal: true, TimedOut: true}, nil
	}
	if approval.RolePolicySnapshotRevision == 0 {
		if submission.LegacySnapshot == nil || submission.LegacySnapshot.PolicyRevision <= 0 || submission.LegacySnapshot.Policy.Validate() != nil {
			return db.WorkflowApprovalContributionResult{}, errors.New("workflow approval legacy policy snapshot is unavailable")
		}
		payload, marshalErr := json.Marshal(submission.LegacySnapshot)
		if marshalErr != nil {
			return db.WorkflowApprovalContributionResult{}, marshalErr
		}
		result, updateErr := tx.Exec(d.connection.PrepareQuery(
			"update project__workflow_approval set role_policy_snapshot=?, role_policy_revision=? where id=? and role_policy_revision=0"),
			string(payload), submission.LegacySnapshot.PolicyRevision, approval.ID,
		)
		if updateErr != nil {
			return db.WorkflowApprovalContributionResult{}, updateErr
		}
		updated, updateErr := result.RowsAffected()
		if updateErr != nil || updated != 1 {
			return db.WorkflowApprovalContributionResult{}, errors.New("workflow approval legacy policy snapshot conflict")
		}
		approval.RolePolicySnapshot = *submission.LegacySnapshot
		approval.RolePolicySnapshotJSON = string(payload)
		approval.RolePolicySnapshotRevision = submission.LegacySnapshot.PolicyRevision
	}
	identity, knownRoles, identityErr := d.resolveWorkflowApprovalIdentityTx(tx, submission.ProjectID, submission.ActorUserID)
	if identityErr != nil {
		if err = tx.Commit(); err != nil {
			return db.WorkflowApprovalContributionResult{}, err
		}
		return db.WorkflowApprovalContributionResult{Approval: approval, Denied: true}, nil
	}
	eligibility := pro_interfaces.EvaluateWorkflowApprovalEligibility(pro_interfaces.WorkflowApprovalEligibilityRequest{
		Policy: approval.RolePolicySnapshot.Policy, ActorUserID: submission.ActorUserID,
		InitiatorUserID:         approval.RequestActorUserID,
		EffectiveRoleReferences: []db.ProjectRoleReference{identity.Reference}, KnownRoleReferences: knownRoles,
	})
	if !eligibility.Allowed || len(eligibility.MatchingRoleIDs) == 0 {
		if err = tx.Commit(); err != nil {
			return db.WorkflowApprovalContributionResult{}, err
		}
		return db.WorkflowApprovalContributionResult{Approval: approval, Denied: true}, nil
	}
	contribution := db.WorkflowApprovalContribution{
		ApprovalID: approval.ID, ActorUserID: submission.ActorUserID, RoleID: eligibility.MatchingRoleIDs[0],
		RoleRevision: identity.Revision, RoleOrigin: workflowApprovalRoleOrigin(identity.Origin),
		DirectoryProviderID: identity.DirectoryProviderID, DirectoryMappingID: identity.DirectoryMappingID,
		DirectoryMappingRevision:     identity.DirectoryMappingRevision,
		DirectoryRevisionFingerprint: identity.DirectoryRevisionFingerprint,
		Decision:                     submission.Decision.Status, Comment: submission.Decision.Comment, Created: submission.At,
		PolicyRevision: approval.RolePolicySnapshotRevision, CorrelationID: submission.CorrelationID,
	}
	if err = contribution.Validate(); err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	_, err = tx.Exec(d.connection.PrepareQuery(
		"insert into project__workflow_approval_contribution(workflow_approval_id, actor_user_id, role_id, role_revision, role_origin, directory_provider_id, directory_mapping_id, directory_mapping_revision, directory_revision_fingerprint, decision, comment, created, policy_revision, correlation_id) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		contribution.ApprovalID, contribution.ActorUserID, contribution.RoleID,
		contribution.RoleRevision, contribution.RoleOrigin,
		contribution.DirectoryProviderID, contribution.DirectoryMappingID,
		contribution.DirectoryMappingRevision, contribution.DirectoryRevisionFingerprint, contribution.Decision,
		contribution.Comment, contribution.Created, contribution.PolicyRevision,
		contribution.CorrelationID,
	)
	if err != nil {
		if isWorkflowApprovalContributionDuplicate(err) {
			if err = tx.Rollback(); err != nil {
				return db.WorkflowApprovalContributionResult{}, err
			}
			current, getErr := d.GetWorkflowApproval(submission.ProjectID, submission.WorkflowRunID, submission.WorkflowNodeID)
			if getErr != nil {
				return db.WorkflowApprovalContributionResult{}, getErr
			}
			return db.WorkflowApprovalContributionResult{Approval: current, Terminal: current.Status != db.WorkflowApprovalPending, Duplicate: true}, nil
		}
		return db.WorkflowApprovalContributionResult{}, err
	}
	contributions := make([]db.WorkflowApprovalContribution, 0)
	if _, err = tx.Select(&contributions, d.connection.PrepareQuery(
		"select * from project__workflow_approval_contribution where workflow_approval_id=? order by created, actor_user_id"), approval.ID,
	); err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	terminalStatus := db.WorkflowApprovalPending
	if contribution.Decision == db.WorkflowApprovalRejected {
		terminalStatus = db.WorkflowApprovalRejected
	} else if pro_interfaces.AuthorizeWorkflowApprovalQuorum(
		approval.RolePolicySnapshot.Policy, approval.RequestActorUserID, knownRoles, contributions,
	).Satisfied {
		terminalStatus = db.WorkflowApprovalApproved
	}
	if terminalStatus != db.WorkflowApprovalPending {
		if err = finalizeWorkflowApprovalTx(tx, d, &approval, terminalStatus, submission.At, &contribution.ActorUserID, contribution.Comment, db.WorkflowApprovalDecisionSourceUser); err != nil {
			return db.WorkflowApprovalContributionResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowApprovalContributionResult{}, err
	}
	return db.WorkflowApprovalContributionResult{Approval: approval, Committed: true, Terminal: terminalStatus != db.WorkflowApprovalPending, Contribution: &contribution}, nil
}

func finalizeWorkflowApprovalTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	approval *db.WorkflowApproval,
	status db.WorkflowApprovalStatus,
	at time.Time,
	actorID *int,
	comment string,
	source db.WorkflowApprovalDecisionSource,
) error {
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__workflow_approval set status=?, resolved=?, resolved_by_user_id=?, decision_comment=?, decision_source=?, notification_revision=notification_revision+1 where id=? and status=?"),
		status, at, actorID, comment, source, approval.ID, db.WorkflowApprovalPending,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("workflow approval terminal transition conflict")
	}
	var workflowRun db.WorkflowRun
	if err = tx.SelectOne(&workflowRun, d.connection.PrepareQuery("select * from project__workflow_run where project_id=? and id=?"), approval.ProjectID, approval.WorkflowRunID); err != nil {
		return err
	}
	approval.Status, approval.Resolved, approval.ResolvedByUserID = status, &at, actorID
	approval.DecisionComment, approval.DecisionSource = comment, source
	approval.WorkflowTemplateID = workflowRun.WorkflowTemplateID
	approval.NotificationRevision++
	severity, action, notificationStatus := pro_interfaces.NotificationSeverityError, pro_interfaces.NotificationLifecycleUpdate, string(status)
	if status == db.WorkflowApprovalApproved {
		severity, action, notificationStatus = pro_interfaces.NotificationSeverityInfo, pro_interfaces.NotificationLifecycleResolve, "approved"
	}
	return d.notificationRouter.RouteTx(tx, workflowApprovalNotification(*approval, severity, action, notificationStatus))
}

func isWorkflowApprovalContributionDuplicate(err error) bool {
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "unique") || strings.Contains(value, "duplicate")
}

func (d *WorkflowStoreImpl) resolveWorkflowApprovalIdentityTx(
	tx *gorp.Transaction,
	projectID int,
	userID int,
) (db.ProjectWorkflowRoleIdentity, map[db.ProjectRoleReference]bool, error) {
	var member db.ProjectUser
	if err := tx.SelectOne(&member, d.connection.PrepareQuery(
		"select * from project__user where project_id=? and user_id=?"), projectID, userID,
	); err != nil {
		return db.ProjectWorkflowRoleIdentity{}, nil, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	roleIDs := make([]db.ProjectRoleID, 0)
	var roles []db.Role
	if _, err := tx.Select(&roles, d.connection.PrepareQuery(
		"select * from `role` where project_id=? and revision>=1 order by role_id"), projectID,
	); err != nil {
		return db.ProjectWorkflowRoleIdentity{}, nil, err
	}
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}
	known := db.KnownProjectRoleReferences(roleIDs)
	identity, roleID, builtIn, err := workflowApprovalIdentityBase(member, roles)
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, nil, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if member.LDAPGroupManagedAssignmentID != nil && member.OIDCGroupManagedAssignmentID != nil {
		return db.ProjectWorkflowRoleIdentity{}, nil, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if member.LDAPGroupManagedAssignmentID != nil {
		identity, err = d.resolveWorkflowApprovalDirectoryIdentityTx(tx, identity, member, roleID, *member.LDAPGroupManagedAssignmentID, "ldap")
	} else if member.OIDCGroupManagedAssignmentID != nil {
		identity, err = d.resolveWorkflowApprovalDirectoryIdentityTx(tx, identity, member, roleID, *member.OIDCGroupManagedAssignmentID, "oidc")
	} else if builtIn {
		identity.Origin = db.ProjectWorkflowRoleOriginBuiltIn
	} else {
		identity.Origin = db.ProjectWorkflowRoleOriginManual
	}
	if err != nil || identity.Validate() != nil {
		return db.ProjectWorkflowRoleIdentity{}, nil, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	return identity, known, nil
}

func workflowApprovalIdentityBase(member db.ProjectUser, roles []db.Role) (db.ProjectWorkflowRoleIdentity, string, bool, error) {
	if member.RoleID == nil {
		reference, ok := db.ProjectRoleReferenceForBuiltInRole(member.Role)
		if !ok || member.Revision < 1 {
			return db.ProjectWorkflowRoleIdentity{}, "", false, errors.New("workflow role is unavailable")
		}
		return db.ProjectWorkflowRoleIdentity{Reference: reference, Permissions: member.Role.GetPermissions(), Revision: member.Revision}, string(member.Role), true, nil
	}
	for _, role := range roles {
		if role.ID == *member.RoleID {
			return db.ProjectWorkflowRoleIdentity{Reference: db.ProjectRoleReferenceForCustomRole(role.ID), Permissions: role.Permissions, Revision: role.Revision}, string(role.ID), false, nil
		}
	}
	return db.ProjectWorkflowRoleIdentity{}, "", false, errors.New("workflow role is unavailable")
}

func (d *WorkflowStoreImpl) resolveWorkflowApprovalDirectoryIdentityTx(
	tx *gorp.Transaction,
	identity db.ProjectWorkflowRoleIdentity,
	member db.ProjectUser,
	roleID string,
	ledgerID int,
	kind string,
) (db.ProjectWorkflowRoleIdentity, error) {
	table := "ldap"
	origin := db.ProjectWorkflowRoleOriginLDAP
	revisionColumn := "directory_revision"
	if kind == "oidc" {
		table, origin, revisionColumn = "oidc", db.ProjectWorkflowRoleOriginOIDC, "claim_revision"
	}
	query := fmt.Sprintf(`select assignment.provider_id, assignment.mapping_id, mapping.revision as mapping_revision,
       reconciliation.%s as directory_revision
 from %s_group_managed_assignment assignment
 join %s_group_mapping mapping on mapping.provider_id=assignment.provider_id and mapping.id=assignment.mapping_id
 join %s_group_mapping_state state on state.provider_id=assignment.provider_id
 join %s_group_reconciliation reconciliation on reconciliation.id=(
   select latest.id from %s_group_reconciliation latest
   where latest.provider_id=assignment.provider_id %s and latest.mapping_revision=state.revision
     and latest.status='applied' and latest.applied_at is not null order by latest.id desc limit 1)
 where assignment.id=? and assignment.user_id=? and assignment.project_id=?
   and assignment.target_scope='project' and assignment.role_id=?
   and mapping.target_scope='project' and mapping.project_id=? and mapping.role_id=? and mapping.enabled=true`,
		revisionColumn, table, table, table, table, table,
		map[bool]string{true: "and latest.user_id=assignment.user_id", false: ""}[kind == "oidc"],
	)
	var provenance struct {
		ProviderID        string `db:"provider_id"`
		MappingID         string `db:"mapping_id"`
		MappingRevision   int    `db:"mapping_revision"`
		DirectoryRevision string `db:"directory_revision"`
	}
	if err := tx.SelectOne(&provenance, d.connection.PrepareQuery(query), ledgerID, member.UserID, member.ProjectID, roleID, member.ProjectID, roleID); err != nil {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if provenance.ProviderID == "" || provenance.MappingID == "" || provenance.MappingRevision < 1 || provenance.DirectoryRevision == "" {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	digest := sha256.Sum256([]byte(provenance.DirectoryRevision))
	identity.Origin, identity.DirectoryProviderID, identity.DirectoryMappingID = origin, provenance.ProviderID, provenance.MappingID
	identity.DirectoryMappingRevision, identity.DirectoryRevisionFingerprint = provenance.MappingRevision, hex.EncodeToString(digest[:])
	return identity, nil
}

func workflowApprovalRoleOrigin(origin db.ProjectWorkflowRoleOrigin) db.WorkflowApprovalRoleOrigin {
	switch origin {
	case db.ProjectWorkflowRoleOriginBuiltIn:
		return db.WorkflowApprovalRoleOriginBuiltIn
	case db.ProjectWorkflowRoleOriginLDAP:
		return db.WorkflowApprovalRoleOriginLDAP
	case db.ProjectWorkflowRoleOriginOIDC:
		return db.WorkflowApprovalRoleOriginOIDC
	default:
		return db.WorkflowApprovalRoleOriginManual
	}
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
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current db.WorkflowApproval
	if err = tx.SelectOne(&current, d.connection.PrepareQuery("select * from project__workflow_approval where project_id=? and workflow_run_id=? and workflow_node_id=? and status=?"), approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowApprovalPending); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(
		"update project__workflow_approval set status=?, resolved=?, resolved_by_user_id=?, decision_comment=?, decision_source=?, notification_revision=notification_revision+1 where project_id=? and workflow_run_id=? and workflow_node_id=? and status=?"),
		approval.Status, approval.Resolved, approval.ResolvedByUserID, approval.DecisionComment, approval.DecisionSource,
		approval.ProjectID, approval.WorkflowRunID, approval.WorkflowNodeID, db.WorkflowApprovalPending,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return updated == 1, err
	}
	var workflowRun db.WorkflowRun
	if err = tx.SelectOne(&workflowRun, d.connection.PrepareQuery("select * from project__workflow_run where project_id=? and id=?"), approval.ProjectID, approval.WorkflowRunID); err != nil {
		return false, err
	}
	approval.ID, approval.WorkflowTemplateID, approval.NotificationRevision = current.ID, workflowRun.WorkflowTemplateID, current.NotificationRevision+1
	severity, action, status := pro_interfaces.NotificationSeverityError, pro_interfaces.NotificationLifecycleUpdate, string(approval.Status)
	if approval.Status == db.WorkflowApprovalApproved {
		severity, action, status = pro_interfaces.NotificationSeverityInfo, pro_interfaces.NotificationLifecycleResolve, "approved"
	}
	if err = d.notificationRouter.RouteTx(tx, workflowApprovalNotification(approval, severity, action, status)); err != nil {
		return false, err
	}
	return true, tx.Commit()
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
	if err == nil {
		err = task.DecodeWorkflowTemplateProvenance()
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
	tx, err := d.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var current db.WorkflowRun
	if err = tx.SelectOne(&current, d.connection.PrepareQuery("select * from project__workflow_run where project_id=? and id=?"), run.ProjectID, run.ID); err != nil {
		return false, err
	}
	notify := current.Status != run.Status && run.Status.IsFinished()
	placeholders := make([]string, len(excluded))
	args := []any{run.Status, run.Reason, run.Start, run.End, run.RootTaskID, run.ProjectID, run.ID}
	for index, status := range excluded {
		placeholders[index] = "?"
		args = append(args, status)
	}
	args = append(args, lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken)
	query := "update project__workflow_run set status=?, reason=?, start=?, `end`=?, root_task_id=?"
	if notify {
		query += ", notification_revision=notification_revision+1"
	}
	query += " where project_id=? and id=? and status not in (" + strings.Join(placeholders, ",") + ") and exists (select 1 from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP)"
	result, err := d.connection.ExecTx(tx, d.connection.PrepareQuery(query),
		args...,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return updated == 1, err
	}
	if notify {
		run.NotificationRevision = current.NotificationRevision + 1
		if err = d.notificationRouter.RouteTx(tx, workflowTerminalNotification(run)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
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
	if status == db.WorkflowRunNodePending {
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
	provenance, err := db.DecodeCrossProjectTemplateProvenance(node.CrossProjectTemplateProvenanceJSON)
	if err != nil {
		return fmt.Errorf("decode workflow run cross-project template provenance: %w", err)
	}
	node.CrossProjectTemplateProvenance = provenance
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
