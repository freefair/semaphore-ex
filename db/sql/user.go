package sql

import (
	stdsql "database/sql"
	"errors"
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"golang.org/x/crypto/bcrypt"
)

func (d *SqlDb) CreateUserWithoutPassword(user db.User) (newUser db.User, err error) {

	err = db.ValidateUser(user)
	if err != nil {
		return
	}

	user.Password = ""
	user.Created = db.GetParsedTime(tz.Now())

	err = d.Sql().Insert(&user)

	if err != nil {
		return
	}

	newUser = user
	return
}

func (d *SqlDb) CreateUser(user db.UserWithPwd) (newUser db.User, err error) {

	err = db.ValidateUser(user.User)
	if err != nil {
		return
	}

	pwdHash, err := bcrypt.GenerateFromPassword([]byte(user.Pwd), 11)

	if err != nil {
		return
	}

	user.Password = string(pwdHash)
	user.Created = db.GetParsedTime(tz.Now())

	err = d.Sql().Insert(&user.User)

	if err != nil {
		return
	}

	newUser = user.User
	return
}

func (d *SqlDb) ImportUser(user db.UserWithPwd) (newUser db.User, err error) {
	err = db.ValidateUser(user.User)
	if err != nil {
		return
	}

	user.Created = db.GetParsedTime(tz.Now())

	err = d.Sql().Insert(&user.User)

	if err != nil {
		return
	}

	newUser = user.User
	return
}

func (d *SqlDb) DeleteUser(userID int) error {
	hasGlobalRoles, err := d.IsMigrationApplied(db.Migration{Version: "2.20.30"})
	if err != nil {
		return err
	}
	if !hasGlobalRoles {
		res, deleteErr := d.exec("delete from `user` where id=?", userID)
		return validateMutationResult(res, deleteErr)
	}
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

func (d *SqlDb) UpdateUser(user db.UserWithPwd) error {
	var pwdHash []byte
	var err error
	if user.Pwd != "" {
		pwdHash, err = bcrypt.GenerateFromPassword([]byte(user.Pwd), 11)
		if err != nil {
			return err
		}
	}

	hasGlobalRoles, err := d.IsMigrationApplied(db.Migration{Version: "2.20.30"})
	if err != nil {
		return err
	}
	if !hasGlobalRoles {
		return d.updateUserFields(nil, user, pwdHash)
	}
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

func (d *SqlDb) SetUserPassword(userID int, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 11)
	if err != nil {
		return err
	}
	_, err = d.exec(
		"update `user` set password=? where id=?",
		string(hash), userID)
	return err
}

func (d *SqlDb) CreateProjectUser(projectUser db.ProjectUser) (newProjectUser db.ProjectUser, err error) {
	hasProjectRoleIdentity, migrationErr := d.IsMigrationApplied(db.Migration{Version: "2.20.29"})
	if migrationErr != nil {
		return newProjectUser, migrationErr
	}
	if hasProjectRoleIdentity {
		tx, beginErr := d.Sql().Begin()
		if beginErr != nil {
			return newProjectUser, beginErr
		}
		defer func() { _ = tx.Rollback() }()
		if err = lockProjectRoleMutations(tx, d, projectUser.ProjectID); err != nil {
			return newProjectUser, err
		}
		projectUser, err = normalizeProjectRoleAssignmentTx(tx, d, projectUser)
		if err != nil {
			return newProjectUser, err
		}
		_, err = tx.Exec(d.PrepareQuery(
			"insert into project__user (project_id, user_id, `role`, role_id, revision) values (?, ?, ?, ?, ?)"),
			projectUser.ProjectID,
			projectUser.UserID,
			projectUser.Role,
			projectUser.RoleID,
			projectUser.Revision)
		if err == nil {
			err = tx.Commit()
		}
	} else {
		_, err = d.exec(
			"insert into project__user (project_id, user_id, `role`) values (?, ?, ?)",
			projectUser.ProjectID,
			projectUser.UserID,
			projectUser.Role)
	}

	if err != nil {
		return
	}

	newProjectUser = projectUser
	return
}

func (d *SqlDb) GetProjectUser(projectID, userID int) (db.ProjectUser, error) {
	var user db.ProjectUser

	err := d.selectOne(&user,
		"select * from project__user where project_id=? and user_id=?",
		projectID,
		userID)

	return user, err
}

func (d *SqlDb) GetProjectUsers(projectID int, params db.RetrieveQueryParams) (users []db.UserWithProjectRole, err error) {

	pp, err := params.Validate(db.UserProps)
	if err != nil {
		return
	}

	q := squirrel.Select("u.*").
		Column("pu.role").
		From("project__user as pu").
		LeftJoin("`user` as u on pu.user_id=u.id").
		Where("pu.project_id=?", projectID)
	hasProjectRoleIdentity, migrationErr := d.IsMigrationApplied(db.Migration{Version: "2.20.29"})
	if migrationErr != nil {
		err = migrationErr
		return
	}
	if hasProjectRoleIdentity {
		q = q.Columns("pu.role_id", "pu.revision")
	}

	sortDirection := "ASC"
	if pp.SortInverted {
		sortDirection = "DESC"
	}

	switch pp.SortBy {
	case "name", "username", "email":
		q = q.OrderBy("u." + pp.SortBy + " " + sortDirection)
	case "role":
		q = q.OrderBy("pu.role " + sortDirection)
	default:
		q = q.OrderBy("u.name " + sortDirection)
	}

	query, args, err := q.ToSql()

	if err != nil {
		return
	}

	_, err = d.selectAll(&users, query, args...)

	return
}

func (d *SqlDb) UpdateProjectUser(projectUser db.ProjectUser) error {
	hasProjectRoleIdentity, err := d.IsMigrationApplied(db.Migration{Version: "2.20.29"})
	if err != nil {
		return err
	}
	if !hasProjectRoleIdentity {
		_, err = d.exec(
			"update `project__user` set role=? where user_id=? and project_id = ?",
			projectUser.Role,
			projectUser.UserID,
			projectUser.ProjectID)
		return err
	}

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
		"select * from project__user where project_id=? and user_id=?"),
		projectUser.ProjectID, projectUser.UserID)
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

func (d *SqlDb) DeleteProjectUser(projectID, userID int) error {
	hasProjectRoleIdentity, err := d.IsMigrationApplied(db.Migration{Version: "2.20.29"})
	if err != nil {
		return err
	}
	if !hasProjectRoleIdentity {
		_, err = d.exec("delete from project__user where user_id=? and project_id=?", userID, projectID)
		return err
	}
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

// GetUser retrieves a user from the database by ID
func (d *SqlDb) GetUser(userID int) (user db.User, err error) {

	err = d.selectOne(&user, "select * from `user` where id=?", userID)

	if err != nil {
		return
	}

	var totp db.UserTotp
	err = d.selectOne(&totp, "select * from `user__totp` where user_id=?", user.ID)

	if err == nil {
		user.Totp = &totp
	}

	if errors.Is(err, db.ErrNotFound) {
		err = nil
	}

	var emailOtp db.UserEmailOtp
	err = d.selectOne(&emailOtp, "select * from `user__email_otp` where user_id=?", user.ID)

	if err == nil {
		user.EmailOtp = &emailOtp
	}

	if errors.Is(err, db.ErrNotFound) {
		err = nil
	}

	return
}

func (d *SqlDb) GetProUserCount() (count int, err error) {

	cnt, err := d.Sql().SelectInt(d.PrepareQuery("select count(*) from `user` where pro"))

	count = int(cnt)

	return
}

func (d *SqlDb) GetUserCount() (count int, err error) {

	cnt, err := d.Sql().SelectInt(d.PrepareQuery("select count(*) from `user`"))

	count = int(cnt)

	return
}

func escapeLike(s string) string {
	// Order matters: escape \ first
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func (d *SqlDb) GetUsers(params db.RetrieveQueryParams) (users []db.User, err error) {
	q := squirrel.Select("*").From("`user`")

	q, err = getQueryForParams(q, "", db.UserProps, params)

	if err != nil {
		return
	}

	if params.Filter != "" {
		q = q.Where(squirrel.Like{"username": escapeLike(params.Filter) + "%"})
	}

	query, args, err := q.ToSql()

	if err != nil {
		return
	}

	_, err = d.selectAll(&users, query, args...)

	return
}

func (d *SqlDb) GetUserByLoginOrEmail(login string, email string) (user db.User, err error) {
	err = d.selectOne(
		&user,
		d.PrepareQuery("select * from `user` where email=? or username=?"),
		email, login)

	if err != nil {
		return
	}

	var totp db.UserTotp
	err = d.selectOne(&totp, "select * from `user__totp` where user_id=?", user.ID)

	if err == nil {
		user.Totp = &totp
	}

	if errors.Is(err, db.ErrNotFound) {
		err = nil
	}

	var emailOtp db.UserEmailOtp
	err = d.selectOne(&emailOtp, "select * from `user__email_otp` where user_id=?", user.ID)

	if err == nil && !emailOtp.IsExpired() {
		user.EmailOtp = &emailOtp
	}

	if errors.Is(err, db.ErrNotFound) {
		err = nil
	}

	return
}

func (d *SqlDb) GetAllAdmins() (users []db.User, err error) {
	_, err = d.selectAll(&users, "select * from `user` where `admin` = true")

	return
}

func (d *SqlDb) insertEmailOtp(userID int, code string) (totp db.UserEmailOtp, err error) {

	totp.UserID = userID
	totp.Code = code
	totp.Created = db.GetParsedTime(tz.Now())

	res, err := d.exec(
		"insert into user__email_otp (user_id, code, created) values (?, ?, ?)",
		totp.UserID,
		totp.Code,
		totp.Created)

	if err != nil {
		return
	}

	id, err := res.LastInsertId()
	if err != nil {
		return
	}

	totp.ID = int(id)

	return
}

func (d *SqlDb) AddEmailOtpVerification(userID int, code string) (res db.UserEmailOtp, err error) {

	var emailOtp db.UserEmailOtp
	err = d.selectOne(&emailOtp, "select * from `user__email_otp` where user_id=?", userID)

	if err == nil {
		now := db.GetParsedTime(tz.Now())
		_, err = d.exec("update user__email_otp set code=?, created=?, attempts=0 where user_id=?", code, now, userID)
	} else if errors.Is(err, db.ErrNotFound) {
		err = nil
		res, err = d.insertEmailOtp(userID, code)
	} else {
		return
	}

	return
}

func (d *SqlDb) IncrementEmailOtpAttempts(userID int) error {
	_, err := d.exec("update user__email_otp set attempts = attempts + 1 where user_id=?", userID)
	return err
}

func (d *SqlDb) DeleteEmailOtpVerification(userID int, totpID int) error {
	_, err := d.exec("delete from user__email_otp where user_id=? and id = ?", userID, totpID)
	return err
}
