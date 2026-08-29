package sql

import (
	stdsql "database/sql"
	"errors"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
)

func normalizeProjectRoleAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	assignment db.ProjectUser,
) (db.ProjectUser, error) {
	if assignment.Revision <= 0 {
		assignment.Revision = 1
	}
	if assignment.RoleID != nil {
		var role db.Role
		err := tx.SelectOne(&role, d.PrepareQuery(
			"select * from `role` where project_id=? and role_id=?"), assignment.ProjectID, *assignment.RoleID)
		if errors.Is(err, stdsql.ErrNoRows) {
			return db.ProjectUser{}, db.ErrNotFound
		}
		if err != nil {
			return db.ProjectUser{}, err
		}
		assignment.Role = db.ProjectNone
		return assignment, nil
	}
	if assignment.Role.IsValid() {
		return assignment, nil
	}
	var role db.Role
	err := tx.SelectOne(&role, d.PrepareQuery(
		"select * from `role` where slug=? and (project_id=? or project_id is null)"),
		assignment.Role, assignment.ProjectID)
	if errors.Is(err, stdsql.ErrNoRows) {
		return db.ProjectUser{}, db.ErrNotFound
	}
	if err != nil {
		return db.ProjectUser{}, err
	}
	if role.ProjectID != nil {
		assignment.Role = db.ProjectNone
		assignment.RoleID = &role.ID
	}
	return assignment, nil
}

func projectRoleAssignmentPermissionsTx(
	tx *gorp.Transaction,
	d *SqlDb,
	assignment db.ProjectUser,
) (db.ProjectUserPermission, error) {
	if assignment.RoleID == nil && assignment.Role.IsValid() {
		return assignment.Role.GetPermissions(), nil
	}
	var role db.Role
	var err error
	if assignment.RoleID != nil {
		err = tx.SelectOne(&role, d.PrepareQuery(
			"select * from `role` where project_id=? and role_id=?"), assignment.ProjectID, *assignment.RoleID)
	} else {
		err = tx.SelectOne(&role, d.PrepareQuery(
			"select * from `role` where slug=? and (project_id=? or project_id is null)"),
			assignment.Role, assignment.ProjectID)
	}
	if errors.Is(err, stdsql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return role.Permissions, nil
}

func requireAnotherProjectAdministratorTx(
	tx *gorp.Transaction,
	d *SqlDb,
	projectID int,
	excludedUserID int,
) error {
	query := "select count(1) " +
		"from project__user pu " +
		"where pu.project_id=? and pu.user_id<>? and (" +
		"pu.role='owner' or exists (" +
		"select 1 from `role` r " +
		"where (r.project_id=pu.project_id or r.project_id is null) " +
		"and ((pu.role_id is not null and r.role_id=pu.role_id) " +
		"or (pu.role_id is null and r.slug=pu.role)) " +
		"and (r.permissions & ?) = ?))"
	count, err := tx.SelectInt(
		d.PrepareQuery(query),
		projectID,
		excludedUserID,
		db.CanManageProjectUsers,
		db.CanManageProjectUsers,
	)
	if err != nil {
		return err
	}
	if count == 0 {
		return db.ErrLastProjectAdministrator
	}
	return nil
}
