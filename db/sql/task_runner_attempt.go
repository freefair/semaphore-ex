package sql

import (
	"fmt"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

func (d *SqlDb) AssignTaskRunner(
	projectID int,
	taskID int,
	runnerID int,
	runnerName string,
	assignedAt time.Time,
) (task db.Task, assigned bool, err error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return task, false, err
	}
	result, err := tx.Exec(d.PrepareQuery(
		"update task set runner_id=?, runner_id_snapshot=?, runner_name=?, runner_assigned_at=?, "+
			"assignment_generation=assignment_generation+1 "+
			"where id=? and project_id=? and runner_id is null and status in (?, ?)"),
		runnerID, runnerID, runnerName, assignedAt,
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
			"(project_id, task_id, generation, runner_id, runner_name, assigned_at, outcome) "+
			"values (?, ?, ?, ?, ?, ?, ?)"),
		projectID, taskID, task.AssignmentGeneration, runnerID, runnerName, assignedAt, db.RunnerAttemptActive,
	); err != nil {
		_ = tx.Rollback()
		return task, false, err
	}
	if err = tx.Commit(); err != nil {
		return task, false, err
	}
	return task, true, nil
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
	if err := task.PreUpdate(d.Sql()); err != nil {
		return false, err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(d.PrepareQuery(
		"update task set status=?, start=?, `end`=?, commit_hash=?, commit_message=?, runner_id=?, "+
			"runner_id_snapshot=?, runner_name=?, runner_assigned_at=?, message=?, recovery_reason=? "+
			"where id=? and project_id=? and status=? and assignment_generation=? "+
			"and (runner_id=? or (runner_id is null and (runner_id_snapshot=? or assignment_generation=0)))"),
		task.Status, task.Start, task.End, task.CommitHash, task.CommitMessage, task.RunnerID,
		task.RunnerSnapshotID, task.RunnerName, task.RunnerAssignedAt, task.Message, task.RecoveryReason,
		task.ID, task.ProjectID, expectedStatus, expectedGeneration, expectedRunnerID, expectedRunnerID,
	)
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
