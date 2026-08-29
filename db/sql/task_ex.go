package sql

import (
	databaseSQL "database/sql"
	"errors"
	"github.com/semaphoreui/semaphore/db"
)

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
