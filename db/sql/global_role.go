package sql

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func (d *SqlDb) GetGlobalRoleByID(roleID db.ProjectRoleID) (db.Role, error) {
	var role db.Role
	err := d.selectOne(&role, "select * from `role` where role_id=? and project_id is null", roleID)
	return role, err
}

func (d *SqlDb) GetGlobalRoles() (roles []db.Role, err error) {
	_, err = d.selectAll(&roles, "select * from `role` where project_id is null and role_id is not null order by name")
	return
}

func (d *SqlDb) CreateGlobalRole(role db.Role) (db.Role, error) {
	role.ProjectID = nil
	role.Name = strings.TrimSpace(role.Name)
	if role.Slug == "" {
		role.Slug = string(role.ID)
	}
	if err := db.ValidateGlobalRole(role); err != nil {
		return db.Role{}, err
	}
	return d.CreateRole(role)
}

func (d *SqlDb) UpdateGlobalRole(
	role db.Role,
	expectedRevision int,
) (db.Role, error) {
	if expectedRevision <= 0 || role.Revision != expectedRevision {
		return db.Role{}, db.ErrGlobalRoleRevisionConflict
	}
	role.ProjectID = nil
	role.Name = strings.TrimSpace(role.Name)
	if err := db.ValidateGlobalRole(role); err != nil {
		return db.Role{}, err
	}

	tx, err := d.Sql().Begin()
	if err != nil {
		return db.Role{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return db.Role{}, err
	}

	var current db.Role
	err = tx.SelectOne(&current, d.PrepareQuery(
		"select * from `role` where role_id=? and project_id is null"), role.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Role{}, db.ErrNotFound
	}
	if err != nil {
		return db.Role{}, err
	}
	if current.Revision != expectedRevision {
		return db.Role{}, db.ErrGlobalRoleRevisionConflict
	}
	if current.GlobalPermissions.Can(db.CanManageGlobalRoles) &&
		!role.GlobalPermissions.Can(db.CanManageGlobalRoles) {
		if err = requireGlobalAdministratorWithoutRoleTx(tx, d, role.ID); err != nil {
			return db.Role{}, err
		}
	}
	if current.Permissions.Can(db.CanManageProjectUsers) &&
		!role.Permissions.Can(db.CanManageProjectUsers) {
		if err = lockProjectRoleMutationsForReferenceTx(tx, d, role.ID); err != nil {
			return db.Role{}, err
		}
		if err = requireProjectAdministratorsAfterGlobalRoleUpdateTx(tx, d, role.ID); err != nil {
			return db.Role{}, err
		}
	}

	result, err := tx.Exec(d.PrepareQuery(
		`update role set name=?, permissions=?, global_permissions=?, revision=revision+1
		 where role_id=? and project_id is null and revision=?`),
		role.Name, role.Permissions, role.GlobalPermissions, role.ID, expectedRevision)
	if err != nil {
		return db.Role{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.Role{}, err
	}
	if rows != 1 {
		return db.Role{}, db.ErrGlobalRoleRevisionConflict
	}
	if err = tx.Commit(); err != nil {
		return db.Role{}, err
	}
	return d.GetGlobalRoleByID(role.ID)
}

func (d *SqlDb) DeleteGlobalRole(roleID db.ProjectRoleID, expectedRevision int) error {
	if expectedRevision <= 0 {
		return db.ErrGlobalRoleRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	var role db.Role
	err = tx.SelectOne(&role, d.PrepareQuery(
		"select * from `role` where role_id=? and project_id is null"), roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if role.Revision != expectedRevision {
		return db.ErrGlobalRoleRevisionConflict
	}
	if err = lockProjectRoleMutationsForReferenceTx(tx, d, roleID); err != nil {
		return err
	}
	assigned, err := tx.SelectInt(d.PrepareQuery(
		"select (select count(1) from user__global_role where role_id=?) + "+
			"(select count(1) from project__user_role where role_id=?)"),
		roleID, roleID)
	if err != nil {
		return err
	}
	if assigned > 0 {
		return db.ErrGlobalRoleAssigned
	}
	mapped, err := tx.SelectInt(d.PrepareQuery("select count(1) from oidc_group_mapping where role_id=?"), roleID)
	if err != nil {
		return err
	}
	if mapped > 0 {
		return db.ErrGlobalRoleAssigned
	}
	result, err := tx.Exec(d.PrepareQuery(
		"delete from `role` where role_id=? and project_id is null and revision=?"),
		roleID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrGlobalRoleRevisionConflict
	}
	return tx.Commit()
}

func (d *SqlDb) GetGlobalRoleAssignments(userID int) ([]db.GlobalRoleAssignment, error) {
	assignments := make([]db.GlobalRoleAssignment, 0)
	_, err := d.selectAll(
		&assignments,
		`select a.id, a.user_id, a.role_id, a.revision,
		        r.name role_name, r.global_permissions
		 from user__global_role a
		 join role r on r.role_id=a.role_id and r.project_id is null
		 where a.user_id=?
		 order by r.name, a.id`,
		userID,
	)
	return assignments, err
}

func (d *SqlDb) CreateGlobalRoleAssignment(
	assignment db.GlobalRoleAssignment,
) (db.GlobalRoleAssignment, error) {
	if assignment.UserID <= 0 || strings.TrimSpace(string(assignment.RoleID)) == "" {
		return db.GlobalRoleAssignment{}, db.ErrNotFound
	}
	if assignment.Revision <= 0 {
		assignment.Revision = 1
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.GlobalRoleAssignment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return db.GlobalRoleAssignment{}, err
	}
	if err = requireGlobalRoleAndUserTx(tx, d, assignment.RoleID, assignment.UserID); err != nil {
		return db.GlobalRoleAssignment{}, err
	}
	query := "insert into user__global_role (user_id, role_id, revision) values (?, ?, ?)"
	args := []any{assignment.UserID, assignment.RoleID, assignment.Revision}
	if d.GetDialect() == util.DbDriverPostgres {
		id, insertErr := tx.SelectInt(d.PrepareQuery(query+" returning id"), args...)
		if insertErr != nil {
			return db.GlobalRoleAssignment{}, insertErr
		}
		assignment.ID = int(id)
	} else {
		result, insertErr := tx.Exec(d.PrepareQuery(query), args...)
		if insertErr != nil {
			return db.GlobalRoleAssignment{}, insertErr
		}
		id, idErr := result.LastInsertId()
		if idErr != nil {
			return db.GlobalRoleAssignment{}, idErr
		}
		assignment.ID = int(id)
	}
	if err = tx.Commit(); err != nil {
		return db.GlobalRoleAssignment{}, err
	}
	return d.getGlobalRoleAssignment(assignment.UserID, assignment.ID)
}

func (d *SqlDb) DeleteGlobalRoleAssignment(
	userID int,
	assignmentID int,
	expectedRevision int,
) error {
	if userID <= 0 || assignmentID <= 0 {
		return db.ErrNotFound
	}
	if expectedRevision <= 0 {
		return db.ErrGlobalRoleAssignmentConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	var assignment db.GlobalRoleAssignment
	err = tx.SelectOne(&assignment, d.PrepareQuery(
		`select a.id, a.user_id, a.role_id, a.revision,
		        r.name role_name, r.global_permissions
		 from user__global_role a
		 join role r on r.role_id=a.role_id and r.project_id is null
		 where a.user_id=? and a.id=?`), userID, assignmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if assignment.Revision != expectedRevision {
		return db.ErrGlobalRoleAssignmentConflict
	}
	if assignment.GlobalPermissions.Can(db.CanManageGlobalRoles) {
		if err = requireGlobalAdministratorWithoutAssignmentTx(tx, d, assignment.ID); err != nil {
			return err
		}
	}
	result, err := tx.Exec(d.PrepareQuery(
		"delete from user__global_role where user_id=? and id=? and revision=?"),
		userID, assignmentID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrGlobalRoleAssignmentConflict
	}
	return tx.Commit()
}

func (d *SqlDb) GetEffectiveGlobalPermissions(userID int) (db.GlobalPermission, error) {
	user, err := d.GetUser(userID)
	if err != nil {
		return 0, err
	}
	if user.Admin {
		return db.AllGlobalPermissions, nil
	}
	assignments, err := d.GetGlobalRoleAssignments(userID)
	if err != nil {
		return 0, err
	}
	var permissions db.GlobalPermission
	for _, assignment := range assignments {
		permissions |= assignment.GlobalPermissions
	}
	return permissions, nil
}

func (d *SqlDb) getGlobalRoleAssignment(userID int, assignmentID int) (db.GlobalRoleAssignment, error) {
	var assignment db.GlobalRoleAssignment
	err := d.selectOne(
		&assignment,
		`select a.id, a.user_id, a.role_id, a.revision,
		        r.name role_name, r.global_permissions
		 from user__global_role a
		 join role r on r.role_id=a.role_id and r.project_id is null
		 where a.user_id=? and a.id=?`,
		userID,
		assignmentID,
	)
	return assignment, err
}

func lockGlobalRoleMutations(tx *gorp.Transaction, d *SqlDb) error {
	result, err := tx.Exec(d.PrepareQuery(
		"update global_role_state set revision=revision+1 where id=1"))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrNotFound
	}
	return nil
}

func requireGlobalRoleAndUserTx(
	tx *gorp.Transaction,
	d *SqlDb,
	roleID db.ProjectRoleID,
	userID int,
) error {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from role r, `user` u "+
			"where r.role_id=? and r.project_id is null and u.id=?"), roleID, userID)
	if err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func requireGlobalAdministratorWithoutAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	excludedAssignmentID int,
) error {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(distinct u.id) from `user` u "+
			"where u.admin=true or exists ("+
			"select 1 from user__global_role a "+
			"join role r on r.role_id=a.role_id and r.project_id is null "+
			"where a.user_id=u.id and a.id<>? and (r.global_permissions & ?) = ?)"),
		excludedAssignmentID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
	if err != nil {
		return err
	}
	if count == 0 {
		return db.ErrLastGlobalAdministrator
	}
	return nil
}

func requireGlobalAdministratorWithoutRoleTx(
	tx *gorp.Transaction,
	d *SqlDb,
	excludedRoleID db.ProjectRoleID,
) error {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(distinct u.id) from `user` u "+
			"where u.admin=true or exists ("+
			"select 1 from user__global_role a "+
			"join role r on r.role_id=a.role_id and r.project_id is null "+
			"where a.user_id=u.id and a.role_id<>? and (r.global_permissions & ?) = ?)"),
		excludedRoleID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
	if err != nil {
		return err
	}
	if count == 0 {
		return db.ErrLastGlobalAdministrator
	}
	return nil
}

func requireProjectAdministratorsAfterGlobalRoleUpdateTx(
	tx *gorp.Transaction,
	d *SqlDb,
	roleID db.ProjectRoleID,
) error {
	var projects []int
	_, err := tx.Select(&projects, d.PrepareQuery(
		"select distinct project_id from project__user_role where role_id=? order by project_id"), roleID)
	if err != nil {
		return err
	}
	for _, projectID := range projects {
		var users []int
		_, err = tx.Select(&users, d.PrepareQuery(
			"select distinct user_id from project__user_role where project_id=? order by user_id"), projectID)
		if err != nil {
			return err
		}
		administrators := 0
		for _, userID := range users {
			refs, refsErr := d.memberRoleRefs(tx, projectID, userID)
			if refsErr != nil {
				return refsErr
			}
			remaining := refs[:0]
			for _, ref := range refs {
				if ref != roleID {
					remaining = append(remaining, ref)
				}
			}
			canManage, permissionsErr := d.roleRefsCanManageProjectUsers(tx, projectID, remaining)
			if permissionsErr != nil {
				return permissionsErr
			}
			if canManage {
				administrators++
			}
		}
		if administrators == 0 {
			return db.ErrLastProjectAdministrator
		}
	}
	return nil
}

// lockProjectRoleMutationsForReferenceTx follows the global-then-project lock
// order used by membership mutations before inspecting a global role's impact.
func lockProjectRoleMutationsForReferenceTx(tx *gorp.Transaction, d *SqlDb, roleID db.ProjectRoleID) error {
	var projectIDs []int
	_, err := tx.Select(&projectIDs, d.PrepareQuery(
		"select distinct project_id from project__user_role where role_id=? order by project_id"), roleID)
	if err != nil {
		return err
	}
	for _, projectID := range projectIDs {
		if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
			return err
		}
	}
	return nil
}

func isEffectiveGlobalAdministratorTx(
	tx *gorp.Transaction,
	d *SqlDb,
	userID int,
) (bool, error) {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from `user` u where u.id=? and (u.admin=true or exists ("+
			"select 1 from user__global_role a "+
			"join role r on r.role_id=a.role_id and r.project_id is null "+
			"where a.user_id=u.id and (r.global_permissions & ?) = ?))"),
		userID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
	return count > 0, err
}

func requireGlobalAdministratorWithoutUserTx(
	tx *gorp.Transaction,
	d *SqlDb,
	excludedUserID int,
) error {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(distinct u.id) from `user` u "+
			"where u.id<>? and (u.admin=true or exists ("+
			"select 1 from user__global_role a "+
			"join role r on r.role_id=a.role_id and r.project_id is null "+
			"where a.user_id=u.id and (r.global_permissions & ?) = ?))"),
		excludedUserID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
	if err != nil {
		return err
	}
	if count == 0 {
		return db.ErrLastGlobalAdministrator
	}
	return nil
}
