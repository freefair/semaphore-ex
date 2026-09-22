package db

import (
	"slices"
	"strconv"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

// RequireFinishedTaskGroups protects group occupancy from cascading deletion.
// Callers hold this transaction through deletion; the same guard serializes
// admission, so no grouped process can start between the check and deletion.
// A zero template ID checks every task owned by the project.
func RequireFinishedTaskGroups(tx *gorp.Transaction, prepare func(string) string, projectID, templateID int) error {
	if _, err := tx.Exec("update task_group_dispatch_guard set id=id where id=1"); err != nil {
		return err
	}
	query := "select count(*) from task where project_id=? and task_group_keys is not null and status not in (?, ?, ?, ?)"
	args := []any{projectID, task_logger.TaskStoppedStatus, task_logger.TaskBlockedStatus, task_logger.TaskSuccessStatus, task_logger.TaskFailStatus}
	if templateID != 0 {
		query += " and template_id=?"
		args = append(args, templateID)
	}
	count, err := tx.SelectInt(prepare(query), args...)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrInvalidOperation
	}
	return nil
}

// RequireNoExternalTaskGroupReferences prevents deleting a group owner from
// leaving another project's template or nonterminal task with an unresolved
// group ID. The caller must already hold the task-group dispatch guard for the
// duration of its deletion transaction.
func RequireNoExternalTaskGroupReferences(tx *gorp.Transaction, prepare func(string) string, ownerProjectID int) error {
	var groups []TaskGroup
	if _, err := tx.Select(&groups, prepare("select * from project__task_group where project_id=?"), ownerProjectID); err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	groupIDs := make([]int, 0, len(groups))
	groupKeys := make([]string, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
		groupKeys = append(groupKeys, "group/"+strconv.Itoa(group.ID))
	}

	var templates []Template
	if _, err := tx.Select(&templates, prepare("select * from project__template where project_id<>? and task_groups is not null"), ownerProjectID); err != nil {
		return err
	}
	for _, template := range templates {
		if slices.ContainsFunc(template.TaskGroups, func(groupID int) bool {
			return slices.Contains(groupIDs, groupID)
		}) {
			return ErrInvalidOperation
		}
	}

	var tasks []Task
	query := "select * from task where project_id<>? and task_group_keys is not null and status not in (?, ?, ?, ?)"
	if _, err := tx.Select(&tasks, prepare(query), ownerProjectID,
		task_logger.TaskStoppedStatus, task_logger.TaskBlockedStatus,
		task_logger.TaskSuccessStatus, task_logger.TaskFailStatus); err != nil {
		return err
	}
	for _, task := range tasks {
		if slices.ContainsFunc(task.TaskGroupKeys, func(groupKey string) bool {
			return slices.Contains(groupKeys, groupKey)
		}) {
			return ErrInvalidOperation
		}
	}
	return nil
}
