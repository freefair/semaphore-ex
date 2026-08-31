package sql

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

// ClaimTaskStart advances one queued task to starting only while its persisted
// assignment generation is still the snapshot observed by the dispatcher.
// SQL is the authority for this transition: node-local or degraded coordination
// may expose the same queue entry to more than one server, but only one server
// may proceed to runner assignment.
func (d *SqlDb) ClaimTaskStart(
	projectID int,
	taskID int,
	expectedGeneration int,
) (task db.Task, started bool, err error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return task, false, err
	}
	result, err := tx.Exec(d.PrepareQuery(
		"update task set status=? where id=? and project_id=? and status=? "+
			"and runner_id is null and assignment_generation=? and `end` is null"),
		task_logger.TaskStartingStatus, taskID, projectID,
		task_logger.TaskWaitingStatus, expectedGeneration,
	)
	if err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if rows != 1 {
		_ = tx.Rollback()
		return task, false, nil
	}
	if err = tx.SelectOne(&task, d.PrepareQuery(
		"select * from task where id=? and project_id=?"), taskID, projectID,
	); err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if err = tx.Commit(); err != nil {
		return task, false, err
	}
	return task, true, nil
}

func (d *SqlDb) AssignTaskRunner(
	projectID int,
	taskID int,
	runnerID int,
	runnerName string,
	assignedAt time.Time,
	placements ...db.RunnerPlacementDecision,
) (task db.Task, assigned bool, err error) {
	var placement *db.RunnerPlacementDecision
	var requestedTags *db.StringArrayField
	var matchMode db.RunnerTagMatchMode
	var placementReason string
	if len(placements) > 0 {
		placement = &placements[0]
		requestedTags = (*db.StringArrayField)(&placement.RequestedTags)
		matchMode = placement.MatchMode
		placementReason = placement.Reason
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return task, false, err
	}
	// Lock the runner row before checking capacity. This serializes assignments
	// to different task rows on every SQL dialect and prevents write skew.
	if _, err = tx.Exec(d.PrepareQuery(
		"update runner set current_load=current_load "+
			"where id=? and active=true and token != '' and (project_id=? or project_id is null)"),
		runnerID, projectID,
	); err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	type capacityRow struct {
		MaxParallelTasks int                   `db:"max_parallel_tasks"`
		Assignments      int                   `db:"assignments"`
		ExecutorType     db.RunnerExecutorType `db:"executor_type"`
	}
	var capacity capacityRow
	capacityErr := tx.SelectOne(&capacity, d.PrepareQuery(
		"select r.max_parallel_tasks, r.executor_type, "+
			"(select count(*) from task assigned where assigned.runner_id=r.id "+
			"and assigned.status in (?, ?, ?, ?, ?, ?, ?)) as assignments "+
			"from runner r where r.id=? and r.active=true and r.token != '' "+
			"and (r.project_id=? or r.project_id is null)"),
		append(unfinishedRunnerStatusArgs(), runnerID, projectID)...,
	)
	if capacityErr != nil {
		_ = tx.Rollback()
		if capacityErr == sql.ErrNoRows {
			return task, false, nil
		}
		return task, false, capacityErr
	}
	if capacity.MaxParallelTasks > 0 && capacity.Assignments >= capacity.MaxParallelTasks {
		_ = tx.Rollback()
		return task, false, nil
	}

	result, err := tx.Exec(d.PrepareQuery(
		"update task set runner_id=?, runner_id_snapshot=?, runner_name=?, runner_assigned_at=?, "+
			"assignment_generation=assignment_generation+1, placement_decision=? "+
			"where id=? and project_id=? and runner_id is null and status in (?, ?)"),
		runnerID, runnerID, runnerName, assignedAt, placement,
		taskID, projectID, task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus,
	)
	if err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if rows != 1 {
		_ = tx.Rollback()
		return task, false, nil
	}
	if err = tx.SelectOne(&task, d.PrepareQuery("select * from task where id=? and project_id=?"), taskID, projectID); err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"insert into task__runner_attempt "+
			"(project_id, task_id, generation, runner_id, runner_name, assigned_at, outcome, "+
			"requested_tags, match_mode, placement_reason, requested_executor_image, resolved_executor_image, executor_type) "+
			"values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"),
		projectID, taskID, task.AssignmentGeneration, runnerID, runnerName, assignedAt,
		db.RunnerAttemptActive, requestedTags, matchMode, placementReason,
		task.RequestedExecutorImage, task.ResolvedExecutorImage, capacity.ExecutorType,
	); err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if err = tx.Commit(); err != nil {
		return task, false, err
	}
	return task, true, nil
}

func (d *SqlDb) UpdateTaskRunnerAttemptMetadata(
	projectID int,
	taskID int,
	generation int,
	runnerID int,
	metadata db.RunnerExecutorMetadata,
) (bool, error) {
	result, err := d.exec(
		"update task__runner_attempt set executor_type=?, container_id=?, container_name=?, docker_requested_image=?, docker_resolved_image=?, docker_policy_revision=?, docker_policy_hash=?, docker_nano_cpus=?, docker_memory_bytes=?, docker_pids_limit=?, denial_rule_id=?, "+
			"k8s_cluster_alias=?, k8s_namespace=?, k8s_job_name=?, k8s_job_uid=?, k8s_pod_name=?, k8s_pod_uid=?, k8s_container_name=?, k8s_lifecycle=?, k8s_terminal_reason=?, k8s_policy_revision=?, k8s_policy_hash=?, k8s_denial_rule_id=?, k8s_service_account=?, k8s_runtime_class=?, k8s_resource_policy_id=?, k8s_resource_policy_hash=?, k8s_network_profile=?, k8s_network_enforcement=?, k8s_secret_name=?, k8s_secret_uid=?, k8s_network_policy_name=?, k8s_network_policy_uid=?, k8s_retention_deadline=?, k8s_retention_state=? "+
			"where project_id=? and task_id=? and generation=? and runner_id=? and ended_at is null",
		metadata.ExecutorType, metadata.ContainerID, metadata.ContainerName, metadata.RequestedImage, metadata.ResolvedImage, metadata.PolicyRevision, metadata.PolicyHash, metadata.NanoCPUs, metadata.MemoryBytes, metadata.PidsLimit, metadata.DenialRuleID,
		metadata.K8sClusterAlias, metadata.K8sNamespace, metadata.K8sJobName, metadata.K8sJobUID, metadata.K8sPodName, metadata.K8sPodUID, metadata.K8sContainerName, metadata.K8sLifecycle, metadata.K8sTerminalReason,
		metadata.K8sPolicyRevision, metadata.K8sPolicyHash, metadata.K8sDenialRuleID, metadata.K8sServiceAccount, metadata.K8sRuntimeClass, metadata.K8sResourcePolicyID, metadata.K8sResourcePolicyHash, metadata.K8sNetworkProfile, metadata.K8sNetworkEnforcement, metadata.K8sSecretName, metadata.K8sSecretUID, metadata.K8sNetworkPolicyName, metadata.K8sNetworkPolicyUID, metadata.K8sRetentionDeadline, metadata.K8sRetentionState,
		projectID, taskID, generation, runnerID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) SetTaskRunnerPlacement(
	projectID int,
	taskID int,
	decision db.RunnerPlacementDecision,
) (bool, error) {
	result, err := d.exec(
		"update task set placement_decision=? where id=? and project_id=? "+
			"and runner_id is null and status in (?, ?)",
		&decision, taskID, projectID, task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) UpdateTaskRunner(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
) (bool, error) {
	return d.updateTaskRunner(task, expectedStatus, expectedRunnerID, expectedGeneration,
		outcome, attemptReason, transitionedAt, nil)
}

// UpdateTaskRunnerFenced applies the task and attempt transition only while
// the task row still carries the caller's current HA task-control fence.
func (d *SqlDb) UpdateTaskRunnerFenced(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	expectedFencingToken int64,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
) (bool, error) {
	if expectedFencingToken <= 0 {
		return false, errors.New("task recovery fencing token is invalid")
	}
	return d.updateTaskRunner(task, expectedStatus, expectedRunnerID, expectedGeneration,
		outcome, attemptReason, transitionedAt, &expectedFencingToken)
}

func (d *SqlDb) updateTaskRunner(
	task db.Task,
	expectedStatus task_logger.TaskStatus,
	expectedRunnerID int,
	expectedGeneration int,
	outcome db.RunnerAttemptOutcome,
	attemptReason string,
	transitionedAt time.Time,
	expectedFencingToken *int64,
) (bool, error) {
	if err := task.PreUpdate(d.Sql()); err != nil {
		return false, err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return false, err
	}
	var current db.Task
	if err = tx.SelectOne(&current, d.PrepareQuery("select * from task where id=? and project_id=?"), task.ID, task.ProjectID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	notify := current.Status != task.Status && task.Status.IsFinished()
	query := "update task set status=?, start=?, `end`=?, commit_hash=?, commit_message=?, runner_id=?, " +
		"runner_id_snapshot=?, runner_name=?, runner_assigned_at=?, message=?, recovery_reason=?, placement_decision=?"
	if notify {
		query += ", notification_revision=notification_revision+1"
	}
	query += " where id=? and project_id=? and status=? and assignment_generation=? " +
		"and (runner_id=? or (runner_id is null and (runner_id_snapshot=? or assignment_generation=0)))"
	args := []any{
		task.Status, task.Start, task.End, task.CommitHash, task.CommitMessage, task.RunnerID,
		task.RunnerSnapshotID, task.RunnerName, task.RunnerAssignedAt, task.Message, task.RecoveryReason,
		task.PlacementDecision,
		task.ID, task.ProjectID, expectedStatus, expectedGeneration, expectedRunnerID, expectedRunnerID,
	}
	if expectedFencingToken != nil {
		query += " and task_control_fencing_token=?"
		args = append(args, *expectedFencingToken)
	}
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if rows != 1 {
		_ = tx.Rollback()
		return false, nil
	}
	if outcome != "" && outcome != db.RunnerAttemptActive {
		attemptResult, attemptErr := tx.Exec(d.PrepareQuery(
			"update task__runner_attempt set ended_at=?, outcome=?, reason=? "+
				"where project_id=? and task_id=? and generation=? and runner_id=? and ended_at is null"),
			transitionedAt, outcome, attemptReason,
			task.ProjectID, task.ID, expectedGeneration, expectedRunnerID,
		)
		if attemptErr != nil {
			_ = tx.Rollback()
			return false, attemptErr
		}
		attemptRows, rowsErr := attemptResult.RowsAffected()
		if rowsErr != nil {
			_ = tx.Rollback()
			return false, rowsErr
		}
		if expectedGeneration > 0 && attemptRows != 1 {
			_ = tx.Rollback()
			return false, fmt.Errorf(
				"runner attempt %d for task %d was not active", expectedGeneration, task.ID,
			)
		}
	}
	if notify {
		task.NotificationRevision = current.NotificationRevision + 1
		if err = d.routeNotificationTx(tx, taskTerminalNotification(task)); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (d *SqlDb) GetTaskRunnerAttempts(projectID int, taskID int) ([]db.RunnerAttempt, error) {
	attempts := make([]db.RunnerAttempt, 0)
	_, err := d.selectAll(&attempts,
		"select * from task__runner_attempt where project_id=? and task_id=? order by generation",
		projectID, taskID,
	)
	return attempts, err
}
