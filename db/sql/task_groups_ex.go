package sql

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

type taskGroupsBusyError struct{}

func (taskGroupsBusyError) Error() string        { return "waiting for task group capacity" }
func (taskGroupsBusyError) TaskGroupsBusy() bool { return true }

// claimTaskGroupsTx admits all groups together with waiting-to-starting. The
// transaction never reserves a subset while waiting for another group's capacity.
func (d *SqlDb) claimTaskGroupsTx(tx *gorp.Transaction, projectID, taskID, generation int) error {
	if err := d.lockTaskGroupDispatchTx(tx); err != nil {
		return err
	}
	var candidate db.Task
	err := tx.SelectOne(&candidate, d.PrepareQuery("select * from task where id=? and project_id=? and status=? and assignment_generation=? and runner_id is null and `end` is null"), taskID, projectID, task_logger.TaskWaitingStatus, generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || len(candidate.TaskGroupKeys) == 0 {
		return err
	}
	groups := make([]db.TaskGroup, 0, len(candidate.TaskGroupKeys))
	for _, key := range candidate.TaskGroupKeys {
		id, e := taskGroupID(key)
		if e != nil {
			return e
		}
		var group db.TaskGroup
		e = tx.SelectOne(&group, d.PrepareQuery("select g.* from project__task_group g where g.id=? and (g.project_id=? or exists(select 1 from project__task_group_grant s where s.group_id=g.id and s.project_id=?))"), id, projectID, projectID)
		if e != nil {
			return common_errors.NewValidationError("a required task group is no longer available to this project")
		}
		active, e := d.taskGroupActiveCountTx(tx, id)
		if e != nil {
			return e
		}
		if active >= group.MaxParallelTasks {
			return taskGroupsBusyError{}
		}
		groups = append(groups, group)
	}
	allowed, err := db.IntersectTaskGroupRunners(groups)
	if err != nil {
		return common_errors.NewValidationError(err.Error())
	}
	// Liveness and capacity do not determine whether policy definitions conflict.
	// They are checked later by the ordinary runner placement queue.
	if len(allowed) > 0 {
		visible := make(db.TaskGroupBindings, 0, len(allowed))
		for _, id := range allowed {
			var runner db.Runner
			e := tx.SelectOne(&runner, d.PrepareQuery("select * from runner where id=? and (project_id=? or project_id is null)"), id, projectID)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			visible = append(visible, runner.ID)
		}
		if len(visible) == 0 {
			return common_errors.NewValidationError("task group runner requirements have no common runner accessible to this project")
		}
		allowed = visible
	}
	_, err = tx.Exec(d.PrepareQuery("update task set task_group_runner_ids=? where id=?"), allowed, taskID)
	return err
}

func (d *SqlDb) validateTaskGroupRunnerAssignmentTx(tx *gorp.Transaction, taskID, runnerID int) error {
	var task db.Task
	if err := tx.SelectOne(&task, d.PrepareQuery("select * from task where id=?"), taskID); err != nil {
		return err
	}
	if len(task.TaskGroupRunnerIDs) > 0 && !slices.Contains(task.TaskGroupRunnerIDs, runnerID) {
		return common_errors.NewValidationError("runner does not satisfy every task group's requirements")
	}
	return nil
}
