package sql

import (
	"slices"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

const taskGroupRunnerPolicyConflictMessage = "task group runner requirements have no common runner"

func (d *SqlDb) insertTemplateTx(tx *gorp.Transaction, query string, args ...any) (int, error) {
	query = d.PrepareQuery(query)
	if _, ok := d.Sql().Dialect.(gorp.PostgresDialect); ok {
		var id int
		if err := tx.QueryRow(query+" returning id", args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

// validateTemplateTaskGroupsTx checks a template's immutable group policy
// against its effective runner tag policy while the dispatch guard is held.
// Runner reachability and momentary capacity are deliberately excluded: they
// are runtime concerns, whereas an unavailable-but-compatible runner is still
// a valid policy definition.
func (d *SqlDb) validateTemplateTaskGroupsTx(tx *gorp.Transaction, tmpl *db.Template) error {
	normalized, err := db.NormalizeTaskGroups(tmpl.TaskGroups)
	if err != nil {
		return common_errors.NewValidationError(err.Error())
	}
	tmpl.TaskGroups = normalized
	if len(normalized) == 0 {
		return nil
	}

	groups := make([]db.TaskGroup, 0, len(normalized))
	for _, id := range normalized {
		var group db.TaskGroup
		if err = tx.SelectOne(&group, d.PrepareQuery(
			"select g.* from project__task_group g where g.id=? and (g.project_id=? or exists(select 1 from project__task_group_grant s where s.group_id=g.id and s.project_id=?))"),
			id, tmpl.ProjectID, tmpl.ProjectID); err != nil {
			return common_errors.NewValidationError("task group selection is invalid")
		}
		groups = append(groups, group)
	}

	allowed, err := db.IntersectTaskGroupRunners(groups)
	if err != nil {
		return common_errors.NewValidationError(taskGroupRunnerPolicyConflictMessage)
	}
	if len(allowed) == 0 {
		return nil
	}

	tags, matchMode, err := d.templateEffectiveRunnerTagsTx(tx, *tmpl)
	if err != nil {
		return err
	}
	for _, runnerID := range allowed {
		matches, matchErr := d.taskGroupRunnerMatchesTagsTx(tx, tmpl.ProjectID, runnerID, tags, matchMode)
		if matchErr != nil {
			return matchErr
		}
		if matches {
			return nil
		}
	}
	return common_errors.NewValidationError(taskGroupRunnerPolicyConflictMessage)
}

func (d *SqlDb) templateEffectiveRunnerTagsTx(tx *gorp.Transaction, tmpl db.Template) ([]string, db.RunnerTagMatchMode, error) {
	tags := tmpl.EffectiveRunnerTags()
	matchMode := tmpl.EffectiveRunnerTagMatchMode()
	if len(tags) != 0 || tmpl.RunnerTag != nil || tmpl.InventoryID == nil {
		return tags, matchMode, nil
	}
	var inventory db.Inventory
	if err := tx.SelectOne(&inventory, d.PrepareQuery(
		"select * from project__inventory where id=? and project_id=?"), *tmpl.InventoryID, tmpl.ProjectID); err != nil {
		return nil, matchMode, err
	}
	if inventory.RunnerTag == nil {
		return nil, matchMode, nil
	}
	return db.NormalizeRunnerTags([]string{*inventory.RunnerTag}), db.RunnerTagMatchAll, nil
}

func (d *SqlDb) taskGroupRunnerMatchesTagsTx(
	tx *gorp.Transaction,
	projectID, runnerID int,
	tags []string,
	matchMode db.RunnerTagMatchMode,
) (bool, error) {
	var count int
	if err := tx.SelectOne(&count, d.PrepareQuery(
		"select count(*) from runner where id=? and (project_id=? or project_id is null)"), runnerID, projectID); err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	var runnerTags []string
	if _, err := tx.Select(&runnerTags, d.PrepareQuery(
		"select lower(trim(tag)) from runner__tag where runner_id=?"), runnerID); err != nil {
		return false, err
	}
	runnerTags = db.NormalizeRunnerTags(runnerTags)
	matched := 0
	for _, tag := range tags {
		if slices.Contains(runnerTags, tag) {
			matched++
		}
	}
	if matchMode == db.RunnerTagMatchAny {
		return matched > 0, nil
	}
	return matched == len(tags), nil
}
