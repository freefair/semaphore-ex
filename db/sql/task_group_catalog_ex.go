package sql

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

func (d *SqlDb) GetTaskGroups(projectID int) ([]db.TaskGroup, error) {
	groups := make([]db.TaskGroup, 0)
	_, err := d.selectAll(&groups, "select g.* from project__task_group g where g.project_id=? or exists(select 1 from project__task_group_grant s where s.group_id=g.id and s.project_id=?) order by g.name,g.id", projectID, projectID)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].ProjectID == projectID {
			_, err = d.selectAll(&groups[i].SharedProjectIDs, "select project_id from project__task_group_grant where group_id=? order by project_id", groups[i].ID)
			if err != nil {
				return nil, err
			}
		}
	}
	return groups, nil
}
func (d *SqlDb) GetTaskGroup(projectID, id int) (db.TaskGroup, error) {
	var group db.TaskGroup
	err := d.selectOne(&group, "select g.* from project__task_group g where g.id=? and (g.project_id=? or exists(select 1 from project__task_group_grant s where s.group_id=g.id and s.project_id=?))", id, projectID, projectID)
	if err != nil {
		return group, err
	}
	if group.ProjectID == projectID {
		_, err = d.selectAll(&group.SharedProjectIDs, "select project_id from project__task_group_grant where group_id=? order by project_id", id)
	}
	return group, err
}
func (d *SqlDb) ResolveTaskGroups(projectID int, ids db.TaskGroupBindings) ([]db.TaskGroup, error) {
	normalized, err := db.NormalizeTaskGroups(ids)
	if err != nil {
		return nil, common_errors.NewValidationError(err.Error())
	}
	groups := make([]db.TaskGroup, 0, len(normalized))
	for _, id := range normalized {
		group, e := d.GetTaskGroup(projectID, id)
		if e != nil {
			return nil, common_errors.NewValidationError(fmt.Sprintf("task group #%d is not available to this project", id))
		}
		groups = append(groups, group)
	}
	allowed, err := db.IntersectTaskGroupRunners(groups)
	if err != nil {
		return nil, common_errors.NewValidationError(err.Error())
	}
	if len(allowed) > 0 {
		visible := false
		for _, id := range allowed {
			var count int64
			err = d.selectOne(&count, "select count(*) from runner where id=? and (project_id=? or project_id is null)", id, projectID)
			if err != nil {
				return nil, err
			}
			visible = visible || count > 0
		}
		if !visible {
			return nil, common_errors.NewValidationError("task group runner requirements have no common runner accessible to this project")
		}
	}
	return groups, nil
}
func (d *SqlDb) CreateTaskGroup(group db.TaskGroup) (db.TaskGroup, error) {
	group.ID = 0
	group.Revision = 1
	if err := group.Validate(); err != nil {
		return group, common_errors.NewValidationError(err.Error())
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return group, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockTaskGroupDispatchTx(tx); err != nil {
		return group, err
	}
	if err = d.validateTaskGroupRunnersTx(tx, group); err != nil {
		return group, err
	}
	if err = tx.Insert(&group); err != nil {
		return group, err
	}
	if err = d.writeTaskGroupGrantsTx(tx, group); err != nil {
		return group, err
	}
	return group, tx.Commit()
}
func (d *SqlDb) UpdateTaskGroup(group db.TaskGroup) (db.TaskGroup, error) {
	if err := group.Validate(); err != nil {
		return group, common_errors.NewValidationError(err.Error())
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return group, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockTaskGroupDispatchTx(tx); err != nil {
		return group, err
	}
	var current db.TaskGroup
	if err = tx.SelectOne(&current, d.PrepareQuery("select * from project__task_group where id=? and project_id=?"), group.ID, group.ProjectID); err != nil {
		return group, db.ErrNotFound
	}
	if current.Revision != group.Revision {
		return group, common_errors.NewValidationError("task group changed; reload before saving")
	}
	active, err := d.taskGroupActiveCountTx(tx, group.ID)
	if err != nil {
		return group, err
	}
	if active > group.MaxParallelTasks {
		return group, common_errors.NewValidationError("concurrent executions cannot be reduced below the number of active group tasks")
	}
	if active > 0 && !slices.Equal(current.RunnerIDs, group.RunnerIDs) {
		return group, common_errors.NewValidationError("runner requirements cannot change while group tasks are active")
	}
	var previous db.TaskGroupBindings
	if _, err = tx.Select(&previous, d.PrepareQuery("select project_id from project__task_group_grant where group_id=?"), group.ID); err != nil {
		return group, err
	}
	for _, project := range previous {
		if !slices.Contains(group.SharedProjectIDs, project) {
			if err = d.requireTaskGroupUnreferencedTx(tx, group.ID, project); err != nil {
				return group, err
			}
		}
	}
	if err = d.validateTaskGroupRunnersTx(tx, group); err != nil {
		return group, err
	}
	// Policy updates also validate all current template combinations while the
	// admission guard prevents a task from starting with an intermediate policy.
	group.Revision++
	if _, err = tx.Exec(d.PrepareQuery("update project__task_group set name=?,description=?,max_parallel_tasks=?,runner_ids=?,revision=? where id=? and project_id=?"), group.Name, group.Description, group.MaxParallelTasks, group.RunnerIDs, group.Revision, group.ID, group.ProjectID); err != nil {
		return group, err
	}
	if err = d.validateGroupTemplateIntersectionsTx(tx, group.ID); err != nil {
		return group, err
	}
	if err = d.writeTaskGroupGrantsTx(tx, group); err != nil {
		return group, err
	}
	return group, tx.Commit()
}
func (d *SqlDb) DeleteTaskGroup(projectID, id, revision int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockTaskGroupDispatchTx(tx); err != nil {
		return err
	}
	var current db.TaskGroup
	if err = tx.SelectOne(&current, d.PrepareQuery("select * from project__task_group where id=? and project_id=?"), id, projectID); err != nil {
		return db.ErrNotFound
	}
	if current.Revision != revision {
		return common_errors.NewValidationError("task group changed; reload before deleting")
	}
	if err = d.requireTaskGroupUnreferencedTx(tx, id, 0); err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from project__task_group where id=? and project_id=?"), id, projectID); err != nil {
		return err
	}
	return tx.Commit()
}
func (d *SqlDb) lockTaskGroupDispatchTx(tx *gorp.Transaction) error {
	if _, err := tx.Exec("update task_group_dispatch_guard set id=id where id=1"); err != nil {
		return err
	}
	var id int
	return tx.SelectOne(&id, "select id from task_group_dispatch_guard where id=1")
}
func (d *SqlDb) writeTaskGroupGrantsTx(tx *gorp.Transaction, group db.TaskGroup) error {
	if _, err := tx.Exec(d.PrepareQuery("delete from project__task_group_grant where group_id=?"), group.ID); err != nil {
		return err
	}
	for _, project := range group.SharedProjectIDs {
		if _, err := tx.Exec(d.PrepareQuery("insert into project__task_group_grant(group_id,project_id) values (?,?)"), group.ID, project); err != nil {
			return err
		}
	}
	return nil
}
func (d *SqlDb) validateTaskGroupRunnersTx(tx *gorp.Transaction, group db.TaskGroup) error {
	if len(group.RunnerIDs) == 0 {
		return nil
	}
	for _, id := range group.RunnerIDs {
		var runner db.Runner
		if err := tx.SelectOne(&runner, d.PrepareQuery("select * from runner where id=? and (project_id=? or project_id is null)"), id, group.ProjectID); err != nil {
			return common_errors.NewValidationError("group runner is not available to the owning project")
		}
	}
	for _, project := range group.SharedProjectIDs {
		visible := false
		for _, id := range group.RunnerIDs {
			n, err := tx.SelectInt(d.PrepareQuery("select count(*) from runner where id=? and (project_id=? or project_id is null)"), id, project)
			if err != nil {
				return err
			}
			visible = visible || n > 0
		}
		if !visible {
			return common_errors.NewValidationError(fmt.Sprintf("group runner requirements have no runner accessible to shared project #%d", project))
		}
	}
	return nil
}
func (d *SqlDb) taskGroupActiveCountTx(tx *gorp.Transaction, id int) (int, error) {
	var tasks []db.Task
	_, err := tx.Select(&tasks, d.PrepareQuery("select * from task where task_group_keys is not null and (status<>? or runner_id is not null) and status not in (?,?,?,?)"), task_logger.TaskWaitingStatus, task_logger.TaskStoppedStatus, task_logger.TaskBlockedStatus, task_logger.TaskSuccessStatus, task_logger.TaskFailStatus)
	if err != nil {
		return 0, err
	}
	count := 0
	key := fmt.Sprintf("group/%d", id)
	for _, task := range tasks {
		if slices.Contains(task.TaskGroupKeys, key) {
			count++
		}
	}
	return count, nil
}
func (d *SqlDb) requireTaskGroupUnreferencedTx(tx *gorp.Transaction, id, projectID int) error {
	var templates []db.Template
	query := "select * from project__template where task_groups is not null"
	args := []any{}
	if projectID > 0 {
		query += " and project_id=?"
		args = append(args, projectID)
	}
	if _, err := tx.Select(&templates, d.PrepareQuery(query), args...); err != nil {
		return err
	}
	for _, template := range templates {
		if slices.Contains(template.TaskGroups, id) {
			return common_errors.NewValidationError("task group is still selected by a template")
		}
	}
	var tasks []db.Task
	query = "select * from task where task_group_keys is not null and status not in (?,?,?,?)"
	args = []any{task_logger.TaskStoppedStatus, task_logger.TaskBlockedStatus, task_logger.TaskSuccessStatus, task_logger.TaskFailStatus}
	if projectID > 0 {
		query += " and project_id=?"
		args = append(args, projectID)
	}
	if _, err := tx.Select(&tasks, d.PrepareQuery(query), args...); err != nil {
		return err
	}
	for _, task := range tasks {
		if slices.Contains(task.TaskGroupKeys, fmt.Sprintf("group/%d", id)) {
			return common_errors.NewValidationError("task group is still required by queued or active tasks")
		}
	}
	return nil
}
func (d *SqlDb) validateGroupTemplateIntersectionsTx(tx *gorp.Transaction, changedID int) error {
	var templates []db.Template
	if _, err := tx.Select(&templates, "select * from project__template where task_groups is not null"); err != nil {
		return err
	}
	for _, template := range templates {
		if !slices.Contains(template.TaskGroups, changedID) {
			continue
		}
		if err := d.validateTemplateTaskGroupsTx(tx, &template); err != nil {
			return err
		}
	}
	return nil
}

func taskGroupID(key string) (int, error) {
	raw, ok := strings.CutPrefix(key, "group/")
	id, err := strconv.Atoi(raw)
	if !ok || err != nil || id <= 0 || strconv.Itoa(id) != raw {
		return 0, common_errors.NewValidationError("invalid task group membership")
	}
	return id, nil
}
