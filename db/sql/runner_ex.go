package sql

import (
	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

func (d *SqlDb) SetProjectRunnerActive(projectID int, runnerID int, active bool) error {
	query := "update runner set active=? where id=? and project_id=?"
	args := []any{active, runnerID, projectID}
	if !active {
		query += " and not exists (select 1 from task where task.runner_id=runner.id and task.status in (?,?,?,?,?,?,?))"
		args = append(args, unfinishedRunnerStatusArgs()...)
	}
	result, err := d.exec(query, args...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	if _, err = d.GetRunner(projectID, runnerID); err != nil {
		return err
	}
	return d.runnerLifecycleConflict(projectID, runnerID)
}

func unfinishedRunnerStatusArgs() []any {
	statuses := task_logger.UnfinishedTaskStatuses()
	args := make([]any, len(statuses))
	for i := range statuses {
		args[i] = statuses[i]
	}
	return args
}

func (d *SqlDb) runnerLifecycleConflict(projectID int, runnerID int) error {
	query, args, err := squirrel.Select("id as task_id", "status").
		From("task").
		Where(squirrel.Eq{
			"project_id": projectID,
			"runner_id":  runnerID,
			"status":     task_logger.UnfinishedTaskStatuses(),
		}).
		OrderBy("id").
		ToSql()
	if err != nil {
		return err
	}
	assignments := make([]db.RunnerTaskAssignment, 0)
	if _, err = d.selectAll(&assignments, query, args...); err != nil {
		return err
	}
	return &db.RunnerLifecycleConflictError{RunnerID: runnerID, Assignments: assignments}
}
