package sql

import (
	stdsql "database/sql"
	"errors"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"golang.org/x/crypto/bcrypt"
)

// SetUserPasswordAndRevokeSessions updates the password and expires every
// session except the caller's current session in one transaction. A zero
// currentSessionID intentionally expires all sessions for an administrator reset.
func (d *SqlDb) SetUserPasswordAndRevokeSessions(userID int, password string, currentSessionID int) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 11)
	if err != nil {
		return err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(d.PrepareQuery("update `user` set password=? where id=?"), string(hash), userID)
	if err = validateMutationResult(result, err); err != nil {
		return err
	}
	if currentSessionID > 0 {
		_, err = tx.Exec(d.PrepareQuery("update session set expired=true where user_id=? and id<>?"), userID, currentSessionID)
	} else {
		_, err = tx.Exec(d.PrepareQuery("update session set expired=true where user_id=?"), userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateUserAndRevokeSessions is the profile-route equivalent of
// SetUserPasswordAndRevokeSessions. It preserves the one current session only
// when a user changes their own password.
func (d *SqlDb) UpdateUserAndRevokeSessions(user db.UserWithPwd, currentSessionID int) error {
	pwdHash, err := bcrypt.GenerateFromPassword([]byte(user.Pwd), 11)
	if err != nil {
		return err
	}
	hasGlobalRoles, err := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.29"})
	if err != nil {
		return err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if hasGlobalRoles {
		if err = lockGlobalRoleMutations(tx, d); err != nil {
			return err
		}
		var current db.User
		if err = tx.SelectOne(&current, d.PrepareQuery("select * from `user` where id=?"), user.ID); err != nil {
			return err
		}
		if current.Admin && !user.Admin {
			if err = requireGlobalAdministratorWithoutUserTx(tx, d, user.ID); err != nil {
				return err
			}
		}
	}
	if err = d.updateUserFields(tx, user, pwdHash); err != nil {
		return err
	}
	if currentSessionID > 0 {
		_, err = tx.Exec(d.PrepareQuery("update session set expired=true where user_id=? and id<>?"), user.ID, currentSessionID)
	} else {
		_, err = tx.Exec(d.PrepareQuery("update session set expired=true where user_id=?"), user.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserDisabledAndRevokeCredentials changes lifecycle state, expires all
// browser sessions and API tokens, and protects the final effective global
// administrator while the global-role lock serializes concurrent changes.
func (d *SqlDb) SetUserDisabledAndRevokeCredentials(userID int, disabled bool) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	var current db.User
	if err = tx.SelectOne(&current, d.PrepareQuery("select * from `user` where id=?"), userID); err != nil {
		return err
	}
	if current.Disabled == disabled {
		return tx.Commit()
	}
	if disabled {
		effectiveAdmin, adminErr := isEffectiveGlobalAdministratorTx(tx, d, userID)
		if adminErr != nil {
			return adminErr
		}
		if effectiveAdmin {
			if err = requireActiveGlobalAdministratorWithoutUserTx(tx, d, userID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(d.PrepareQuery("update `user` set disabled=? where id=?"), disabled, userID); err != nil {
		return err
	}
	if disabled {
		if _, err = tx.Exec(d.PrepareQuery("update session set expired=true where user_id=?"), userID); err != nil {
			return err
		}
		if _, err = tx.Exec(d.PrepareQuery("update user__token set expired=true where user_id=?"), userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func requireActiveGlobalAdministratorWithoutUserTx(tx *gorp.Transaction, d *SqlDb, excludedUserID int) error {
	count, err := tx.SelectInt(d.PrepareQuery(
		"select count(distinct u.id) from `user` u where u.id<>? and u.disabled=false and (u.admin=true or exists ("+
			"select 1 from user__global_role a join role r on r.role_id=a.role_id and r.project_id is null "+
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

func (d *SqlDb) updateUserFields(
	tx interface {
		Exec(string, ...any) (stdsql.Result, error)
	},
	user db.UserWithPwd,
	pwdHash []byte,
) error {
	exec := d.exec
	if tx != nil {
		exec = func(query string, args ...any) (stdsql.Result, error) {
			return tx.Exec(d.PrepareQuery(query), args...)
		}
	}
	if len(pwdHash) > 0 {
		_, err := exec(
			"update `user` set name=?, username=?, email=?, alert=?, admin=?, pro=?, password=? where id=?",
			user.Name, user.Username, user.Email, user.Alert, user.Admin, user.Pro,
			string(pwdHash), user.ID)
		return err
	}
	_, err := exec(
		"update `user` set name=?, username=?, email=?, alert=?, admin=?, pro=? where id=?",
		user.Name, user.Username, user.Email, user.Alert, user.Admin, user.Pro, user.ID)
	return err
}

// deleteUserWithGlobalRoleProtection keeps the global-role lock and the
// last-effective-administrator invariant in one transaction.
func (d *SqlDb) deleteUserWithGlobalRoleProtection(userID int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	effectiveAdmin, err := isEffectiveGlobalAdministratorTx(tx, d, userID)
	if err != nil {
		return err
	}
	if effectiveAdmin {
		if err = requireGlobalAdministratorWithoutUserTx(tx, d, userID); err != nil {
			return err
		}
	}
	res, err := tx.Exec(d.PrepareQuery("delete from `user` where id=?"), userID)
	if err = validateMutationResult(res, err); err != nil {
		return err
	}
	return tx.Commit()
}

// updateUserWithGlobalRoleProtection serializes administrator demotion with
// global-role mutations before updating the shared user fields.
func (d *SqlDb) updateUserWithGlobalRoleProtection(user db.UserWithPwd, pwdHash []byte) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return err
	}
	var current db.User
	err = tx.SelectOne(&current, d.PrepareQuery("select * from `user` where id=?"), user.ID)
	if errors.Is(err, stdsql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if current.Admin && !user.Admin {
		if err = requireGlobalAdministratorWithoutUserTx(tx, d, user.ID); err != nil {
			return err
		}
	}
	if err = d.updateUserFields(tx, user, pwdHash); err != nil {
		return err
	}
	return tx.Commit()
}

// createProjectUserWithRoleIdentity resolves a custom role and writes the
// membership while the project-role lock prevents concurrent ambiguity.
func (d *SqlDb) createProjectUserWithRoleIdentity(projectUser db.ProjectUser) (db.ProjectUser, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.ProjectUser{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockProjectRoleMutations(tx, d, projectUser.ProjectID); err != nil {
		return db.ProjectUser{}, err
	}
	projectUser, err = normalizeProjectRoleAssignmentTx(tx, d, projectUser)
	if err != nil {
		return db.ProjectUser{}, err
	}
	_, err = tx.Exec(d.PrepareQuery(
		"insert into project__user (project_id, user_id, `role`, role_id, revision) values (?, ?, ?, ?, ?)"),
		projectUser.ProjectID, projectUser.UserID, projectUser.Role, projectUser.RoleID, projectUser.Revision)
	if err != nil {
		return db.ProjectUser{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.ProjectUser{}, err
	}
	return projectUser, nil
}

// updateProjectUserWithRoleIdentity makes a revision-checked membership
// change without allowing the last project administrator to be removed.
func (d *SqlDb) updateProjectUserWithRoleIdentity(projectUser db.ProjectUser) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockProjectRoleMutations(tx, d, projectUser.ProjectID); err != nil {
		return err
	}
	var current db.ProjectUser
	err = tx.SelectOne(&current, d.PrepareQuery(
		"select * from project__user where project_id=? and user_id=?"), projectUser.ProjectID, projectUser.UserID)
	if errors.Is(err, stdsql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if projectUser.Revision <= 0 || projectUser.Revision != current.Revision {
		return db.ErrProjectMembershipRevisionConflict
	}
	projectUser.Revision = current.Revision
	projectUser, err = normalizeProjectRoleAssignmentTx(tx, d, projectUser)
	if err != nil {
		return err
	}
	currentPermissions, err := projectRoleAssignmentPermissionsTx(tx, d, current)
	if err != nil {
		return err
	}
	updatedPermissions, err := projectRoleAssignmentPermissionsTx(tx, d, projectUser)
	if err != nil {
		return err
	}
	if currentPermissions.Can(db.CanManageProjectUsers) && !updatedPermissions.Can(db.CanManageProjectUsers) {
		if err = requireAnotherProjectAdministratorTx(tx, d, projectUser.ProjectID, projectUser.UserID); err != nil {
			return err
		}
	}
	result, err := tx.Exec(d.PrepareQuery(
		`update project__user set role=?, role_id=?, revision=revision+1
		 where project_id=? and user_id=? and revision=?`),
		projectUser.Role, projectUser.RoleID, projectUser.ProjectID, projectUser.UserID, current.Revision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrProjectMembershipRevisionConflict
	}
	return tx.Commit()
}

// deleteProjectUserWithRoleIdentity serializes deletion with role changes and
// checks that a project administrator remains before committing it.
func (d *SqlDb) deleteProjectUserWithRoleIdentity(projectID int, userID int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return err
	}
	var current db.ProjectUser
	err = tx.SelectOne(&current, d.PrepareQuery(
		"select * from project__user where project_id=? and user_id=?"), projectID, userID)
	if errors.Is(err, stdsql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	permissions, err := projectRoleAssignmentPermissionsTx(tx, d, current)
	if err != nil {
		return err
	}
	if permissions.Can(db.CanManageProjectUsers) {
		if err = requireAnotherProjectAdministratorTx(tx, d, projectID, userID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"delete from project__user where user_id=? and project_id=?"), userID, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

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
