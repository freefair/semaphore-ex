package sql

import (
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

func validateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag cannot be empty")
	}

	return nil
}

func makePropsNonGlobal(props db.ObjectProps) (res db.ObjectProps) {
	res = props
	res.IsGlobal = false
	return
}

var runnerProps = makePropsNonGlobal(db.GlobalRunnerProps)

// runnerHasTagExpr renders a parameterised EXISTS clause that checks
// whether the runner row identified by `pe.id` has the given tag.
// runner__tag.tag is indexed, so this is O(log N) per row.
func runnerHasTagExpr(tag string) squirrel.Sqlizer {
	normalized := db.NormalizeRunnerTags([]string{tag})
	if len(normalized) == 0 {
		return squirrel.Expr("1=0")
	}
	return squirrel.Expr(
		"exists (select 1 from runner__tag rt where rt.runner_id = pe.id and lower(trim(rt.tag)) = ?)",
		normalized[0],
	)
}

func runnerIsDefaultExpr() squirrel.Sqlizer {
	return squirrel.Expr("pe.is_default = true")
}

// runnerHasAnyTagExpr matches runners whose tag set is non-empty.
func runnerHasAnyTagExpr() squirrel.Sqlizer {
	return squirrel.Expr(
		"exists (select 1 from runner__tag rt where rt.runner_id = pe.id)",
	)
}

func (d *SqlDb) GetRunner(projectID int, runnerID int) (runner db.Runner, err error) {
	err = d.getObject(projectID, runnerProps, runnerID, &runner)
	if err != nil {
		return
	}
	err = d.loadRunnerTagsSingle(&runner)
	return
}

func (d *SqlDb) GetRunners(projectID int, activeOnly bool, tagFilterMode db.RunnerTagFilterMode, tag *string) (runners []db.Runner, err error) {
	if tag == nil && tagFilterMode == db.RunnerFilterTagCompleteMatch {
		err = fmt.Errorf("tag filter mode is complete match but no tag was provided")
		return
	}

	err = d.getObjects(projectID, runnerProps, db.RetrieveQueryParams{}, func(builder squirrel.SelectBuilder) squirrel.SelectBuilder {
		switch tagFilterMode {
		case db.RunnerFilterTagCompleteMatch:
			builder = builder.Where(runnerHasTagExpr(*tag))
		case db.RunnerFilterIsDefault:
			builder = builder.Where(runnerIsDefaultExpr())
		case db.RunnerFilterIgnoreTags:
			// No tag filtering applied.
		default:
			panic("invalid tag filter mode for GetRunners: " + tagFilterMode)
		}

		if activeOnly {
			builder = builder.Where("active=true and token != ''")
		}

		return builder
	}, &runners)
	if err != nil {
		return
	}
	err = d.loadRunnerTags(runners)
	return
}

func (d *SqlDb) DeleteRunner(projectID int, runnerID int) (err error) {
	runner, err := d.GetRunner(projectID, runnerID)
	if err != nil {
		return err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update task set runner_name=?, runner_id_snapshot=coalesce(runner_id_snapshot, runner_id) where project_id=? and runner_id=?"),
		runner.Name, projectID, runnerID); err != nil {
		_ = tx.Rollback()
		return err
	}
	query, args, err := squirrel.Delete("runner").
		Where(squirrel.Eq{"id": runnerID, "project_id": projectID}).
		Where("not exists (select 1 from task where task.runner_id=runner.id and task.status in (?,?,?,?,?,?,?))",
			unfinishedRunnerStatusArgs()...).
		ToSql()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if deleted != 1 {
		_ = tx.Rollback()
		return d.runnerLifecycleConflict(projectID, runnerID)
	}
	return tx.Commit()
}

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

func (d *SqlDb) GetRunnerCount() (res int, err error) {
	query, args, err := squirrel.Select("count(*)").
		From("runner").
		Where(squirrel.NotEq{"project_id": nil}).
		ToSql()

	if err != nil {
		return
	}

	cnt, err := d.Sql().SelectInt(query, args...)

	res = int(cnt)

	return
}

func (d *SqlDb) GetRunnerTags(projectID int) (res []db.RunnerTag, err error) {
	// Project runners (scoped to this project) plus global runners (project_id IS NULL)
	// both contribute tags here so the template/inventory tag autocomplete sees every
	// runner that could be selected for this project's tasks.
	query, args, err := squirrel.Select("lower(trim(rt.tag)) as tag", "count(distinct rt.runner_id) as cnt").
		From("runner__tag rt").
		Join("runner r on r.id = rt.runner_id").
		Where(squirrel.Or{
			squirrel.Eq{"r.project_id": projectID},
			squirrel.Eq{"r.project_id": nil},
		}).
		GroupBy("lower(trim(rt.tag))").
		OrderBy("tag").
		ToSql()

	if err != nil {
		return
	}

	type row struct {
		Tag string `db:"tag"`
		Cnt int    `db:"cnt"`
	}

	rows := make([]row, 0)
	_, err = d.selectAll(&rows, query, args...)
	if err != nil {
		return
	}

	res = make([]db.RunnerTag, 0, len(rows))
	for _, r := range rows {
		res = append(res, db.RunnerTag{
			Tag:             r.Tag,
			NumberOfRunners: r.Cnt,
		})
	}

	return
}
