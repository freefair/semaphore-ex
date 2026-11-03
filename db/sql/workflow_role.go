package sql

import (
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

func (d *SqlDb) GetWorkflowRole(projectID int, workflowID int, id int) (perm db.WorkflowRolePerm, err error) {
	query, args, err := sq.Select("pwr.*", "coalesce(r.slug, substr(pwr.role_id, 9)) role_slug").
		From("project__workflow_role pwr").
		LeftJoin("`role` r ON r.role_id = pwr.role_id").
		Where("pwr.project_id = ?", projectID).
		Where("pwr.workflow_id = ?", workflowID).
		Where("pwr.id = ?", id).
		ToSql()
	if err != nil {
		return
	}
	err = d.selectOne(&perm, query, args...)
	return
}

// GetWorkflowPermission returns the OR of the workflow grants of the member's roles.
func (d *SqlDb) GetWorkflowPermission(projectID int, workflowID int, userID int) (db.ProjectUserPermission, error) {
	return d.workflowGrants(`
		select
		  pwr.permissions
		from project__workflow_role pwr
		where pwr.project_id = ? and pwr.workflow_id = ?
		  and pwr.role_id in (
		    select pur.role_id from project__user_role pur where pur.project_id = ? and pur.user_id = ?
		  )
	`, projectID, workflowID, projectID, userID)
}

// GetWorkflowRunPermission is GetWorkflowPermission for the workflow of a run.
func (d *SqlDb) GetWorkflowRunPermission(projectID int, runID int, userID int) (db.ProjectUserPermission, error) {
	return d.workflowGrants(`
		select
		  pwr.permissions
		from project__workflow_role pwr
		join project__workflow_run wr on wr.workflow_template_id = pwr.workflow_id
		where pwr.project_id = ? and wr.id = ?
		  and pwr.role_id in (
		    select pur.role_id from project__user_role pur where pur.project_id = ? and pur.user_id = ?
		  )
	`, projectID, runID, projectID, userID)
}

// Per workflow of the project, the OR of the grants of the member's roles; no grant, no entry.
func (d *SqlDb) GetWorkflowGrantsForUser(projectID int, userID int) (map[int]db.ProjectUserPermission, error) {
	var rows []db.WorkflowRolePerm
	_, err := d.selectAll(&rows, `
		select
		  pwr.workflow_id, pwr.permissions
		from project__workflow_role pwr
		where pwr.project_id = ?
		  and pwr.role_id in (
		    select pur.role_id from project__user_role pur where pur.project_id = ? and pur.user_id = ?
		  )
	`, projectID, projectID, userID)
	if err != nil {
		return nil, err
	}
	grants := map[int]db.ProjectUserPermission{}
	for _, row := range rows {
		grants[row.WorkflowID] |= row.Permissions
	}
	return grants, nil
}

func (d *SqlDb) workflowGrants(query string, args ...any) (db.ProjectUserPermission, error) {
	var rows []db.WorkflowRolePerm
	if _, err := d.selectAll(&rows, query, args...); err != nil {
		return 0, err
	}
	var permissions db.ProjectUserPermission
	for _, row := range rows {
		permissions |= row.Permissions
	}
	return permissions, nil
}

func (d *SqlDb) GetWorkflowRoles(projectID int, workflowID int) (roles []db.WorkflowRolePerm, err error) {
	query, args, err := sq.Select("pwr.*", "coalesce(r.slug, substr(pwr.role_id, 9)) role_slug").
		From("project__workflow_role pwr").
		LeftJoin("`role` r ON r.role_id = pwr.role_id").
		Where("pwr.project_id = ?", projectID).
		Where("pwr.workflow_id = ?", workflowID).
		ToSql()
	if err != nil {
		return
	}
	_, err = d.selectAll(&roles, query, args...)
	return
}

func (d *SqlDb) CreateWorkflowRole(role db.WorkflowRolePerm) (newRole db.WorkflowRolePerm, err error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return newRole, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return newRole, err
	}
	if err = lockProjectRoleMutations(tx, d, role.ProjectID); err != nil {
		return newRole, err
	}
	if role.RoleID == "" {
		return newRole, &common_errors.ValidationError{Message: "Role ID is required"}
	}
	if strings.HasPrefix(string(role.RoleID), "builtin:") {
		if !db.ProjectUserRole(strings.TrimPrefix(string(role.RoleID), "builtin:")).IsValid() {
			return newRole, &common_errors.ValidationError{Message: "Unknown built-in role"}
		}
	} else {
		available, lookupErr := tx.SelectInt(d.PrepareQuery(
			"select count(*) from `role` where role_id=? and (project_id is null or project_id=?)"), role.RoleID, role.ProjectID)
		if lookupErr != nil {
			return newRole, lookupErr
		}
		if available == 0 {
			return newRole, &common_errors.ValidationError{Message: "Role is not available in this project"}
		}
	}

	workflowCount, err := tx.SelectInt(d.PrepareQuery("select count(*) from project__workflow_template where id=? and project_id=?"), role.WorkflowID, role.ProjectID)
	if err != nil {
		return newRole, err
	}
	if workflowCount != 1 {
		return newRole, &common_errors.ValidationError{Message: "Workflow is not available in this project"}
	}
	if role.Permissions & ^db.WorkflowGrantPermissions != 0 {
		return newRole, &common_errors.ValidationError{Message: "Unknown workflow permissions"}
	}
	granted, err := tx.SelectInt(d.PrepareQuery(
		"select count(*) from project__workflow_role where workflow_id=? and role_id=?"), role.WorkflowID, role.RoleID)
	if err != nil {
		return
	}
	if granted > 0 {
		err = &common_errors.ValidationError{Message: "Role already has permissions on this workflow"}
		return
	}

	insertID, err := d.insertTemplateTx(
		tx,
		"insert into project__workflow_role (project_id, workflow_id, role_id, permissions) values (?, ?, ?, ?)",
		role.ProjectID,
		role.WorkflowID,
		role.RoleID,
		role.Permissions)
	if err != nil {
		return
	}

	if err = tx.Commit(); err != nil {
		return newRole, err
	}
	newRole = role
	newRole.ID = insertID
	return
}

func (d *SqlDb) DeleteWorkflowRole(projectID int, workflowID int, id int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return err
	}
	res, err := d.execTx(tx, "delete from project__workflow_role where project_id=? and workflow_id=? and id=?", projectID, workflowID, id)
	if err = requireDeletedRow(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) UpdateWorkflowRole(role db.WorkflowRolePerm) error {
	if role.Permissions & ^db.WorkflowGrantPermissions != 0 {
		return &common_errors.ValidationError{Message: "Unknown workflow permissions"}
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	if err = lockProjectRoleMutations(tx, d, role.ProjectID); err != nil {
		return err
	}
	count, err := tx.SelectInt(d.PrepareQuery("select count(*) from project__workflow_role where project_id=? and workflow_id=? and id=?"), role.ProjectID, role.WorkflowID, role.ID)
	if err != nil {
		return err
	}
	if count == 0 {
		return db.ErrNotFound
	}
	_, err = d.execTx(tx, "update project__workflow_role set permissions=? where project_id=? and workflow_id=? and id=?", role.Permissions, role.ProjectID, role.WorkflowID, role.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
