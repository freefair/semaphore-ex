package sql

import (
	"fmt"
	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

func (d *SqlDb) GetRunnerTaskHistory(
	projectID int,
	runnerID int,
	params db.RetrieveQueryParams,
) (history []db.RunnerTaskHistoryItem, err error) {
	if params.Count < 0 || params.BeforeID < 0 {
		return nil, fmt.Errorf("runner history pagination values must not be negative")
	}
	query := squirrel.Select(
		"task.id as task_id",
		"task.template_id",
		"tpl.name as template_name",
		"task.status",
		"coalesce(task.runner_id_snapshot, task.runner_id) as runner_id",
		"coalesce(task.runner_name, runner.name, '') as runner_name",
		"task.created",
		"task.start",
		"task.end",
	).
		From("task").
		Join("project__template tpl on tpl.id=task.template_id and tpl.project_id=task.project_id").
		LeftJoin("runner on runner.id=task.runner_id").
		Where(squirrel.Eq{
			"task.project_id": projectID,
			"task.status": []task_logger.TaskStatus{
				task_logger.TaskStoppedStatus,
				task_logger.TaskSuccessStatus,
				task_logger.TaskFailStatus,
			},
		}).
		Where("coalesce(task.runner_id_snapshot, task.runner_id)=?", runnerID).
		OrderBy("task.id desc")
	if params.BeforeID > 0 {
		query = query.Where("task.id < ?", params.BeforeID)
	}
	if params.Count > 0 {
		query = query.Limit(uint64(params.Count))
	}
	sqlQuery, args, err := query.ToSql()
	if err != nil {
		return nil, err
	}
	history = make([]db.RunnerTaskHistoryItem, 0)
	_, err = d.selectAll(&history, sqlQuery, args...)
	return
}

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
