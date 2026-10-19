package sql

import (
	"errors"
	"github.com/Masterminds/squirrel"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"golang.org/x/crypto/bcrypt"
	"strings"
)

const projectMemberMappedRolesQuery = `
	select pur.project_id, pur.user_id, u.username, assignment.id as mapping_id,
	       assignment.provider_id as provider, mapping.claim_value as group_name,
	       pur.role_id, coalesce(role.slug, '') as role_slug, coalesce(role.name, '') as role_name,
	       coalesce(role.permissions, 0) as role_permissions
	from project__user_role pur
	join oidc_group_managed_assignment assignment on assignment.id=pur.oidc_group_managed_assignment_id
	join oidc_group_mapping mapping on mapping.provider_id=assignment.provider_id and mapping.id=assignment.mapping_id
	join ` + "`user`" + ` u on u.id=pur.user_id
	left join role on role.role_id=pur.role_id
	where pur.project_id=? and pur.user_id=? order by pur.role_id`

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
	hasGlobalRoles, err := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.29"})
	if err != nil {
		return err
	}
	if !hasGlobalRoles {
		res, deleteErr := d.exec("delete from `user` where id=?", userID)
		return validateMutationResult(res, deleteErr)
	}
	return d.deleteUserWithGlobalRoleProtection(userID)
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

	hasGlobalRoles, err := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.29"})
	if err != nil {
		return err
	}
	if !hasGlobalRoles {
		return d.updateUserFields(nil, user, pwdHash)
	}
	return d.updateUserWithGlobalRoleProtection(user, pwdHash)
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
	hasRelations, migrationErr := d.IsMigrationApplied(db.Migration{Version: "2.20.12"})
	if migrationErr != nil {
		return newProjectUser, migrationErr
	}
	if hasRelations {
		roleID, resolveErr := d.projectUserRoleRefForCreate(projectUser)
		if resolveErr != nil {
			return newProjectUser, resolveErr
		}
		return d.CreateProjectUserWithRoleRefs(projectUser, []db.ProjectRoleID{roleID})
	}

	hasProjectRoleIdentity, migrationErr := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.28"})
	if migrationErr != nil {
		return newProjectUser, migrationErr
	}
	if hasProjectRoleIdentity {
		projectUser, err = d.createProjectUserWithRoleIdentity(projectUser)
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
	return projectUser, nil
}

// CreateProjectUserWithRoleRefs creates the compatibility projection and the
// complete effective role set in one transaction. Relation rows are the
// authorization source of truth from 2.20.12 onward.
func (d *SqlDb) CreateProjectUserWithRoleRefs(projectUser db.ProjectUser, roleIDs []db.ProjectRoleID) (db.ProjectUser, error) {
	hasRelations, err := d.IsMigrationApplied(db.Migration{Version: "2.20.12"})
	if err != nil {
		return db.ProjectUser{}, err
	}
	if !hasRelations {
		if len(roleIDs) != 1 {
			return db.ProjectUser{}, &common_errors.ValidationError{Message: "Multiple project roles require migration 2.20.12"}
		}
		ref := roleIDs[0]
		if strings.HasPrefix(string(ref), "builtin:") {
			role := db.ProjectUserRole(strings.TrimPrefix(string(ref), "builtin:"))
			if !role.IsValid() {
				return db.ProjectUser{}, db.ErrNotFound
			}
			projectUser.Role, projectUser.RoleID = role, nil
		} else if role, lookupErr := d.GetProjectRoleByID(projectUser.ProjectID, ref); lookupErr == nil {
			projectUser.Role = db.ProjectNone
			projectUser.RoleID = &role.ID
		} else if role, globalErr := d.GetGlobalRoleByID(ref); globalErr == nil {
			projectUser.Role, projectUser.RoleID = db.ProjectUserRole(role.Slug), nil
		} else {
			return db.ProjectUser{}, lookupErr
		}
		return d.CreateProjectUser(projectUser)
	}
	if len(roleIDs) == 0 {
		return db.ProjectUser{}, &common_errors.ValidationError{Message: "A member needs at least one role"}
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.ProjectUser{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return db.ProjectUser{}, err
	}
	if err = lockProjectRoleMutations(tx, d, projectUser.ProjectID); err != nil {
		return db.ProjectUser{}, err
	}
	if err = d.validateProjectRoleRefs(tx, projectUser.ProjectID, roleIDs); err != nil {
		return db.ProjectUser{}, err
	}
	existing, err := tx.SelectInt(d.PrepareQuery(
		"select count(*) from project__user where project_id=? and user_id=?"), projectUser.ProjectID, projectUser.UserID)
	if err != nil {
		return db.ProjectUser{}, err
	}
	if existing > 0 {
		return db.ProjectUser{}, db.ErrProjectMembershipConflict
	}
	role, roleID, err := d.projectRoleProjectionForRefs(tx, projectUser.ProjectID, roleIDs)
	if err != nil {
		return db.ProjectUser{}, err
	}
	if projectUser.Revision <= 0 {
		projectUser.Revision = 1
	}
	projectUser.Role = db.ProjectUserRole(role)
	if roleID == "" {
		projectUser.RoleID = nil
	} else {
		projectUser.RoleID = &roleID
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"insert into project__user (project_id, user_id, `role`, role_id, revision) values (?, ?, ?, ?, ?)"),
		projectUser.ProjectID, projectUser.UserID, projectUser.Role, projectUser.RoleID, projectUser.Revision); err != nil {
		return db.ProjectUser{}, err
	}
	for _, ref := range roleIDs {
		if _, err = tx.Exec(d.PrepareQuery("insert into project__user_role (project_id,user_id,role_id) values (?,?,?)"), projectUser.ProjectID, projectUser.UserID, ref); err != nil {
			return db.ProjectUser{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.ProjectUser{}, err
	}
	return projectUser, nil
}

// SetProjectUserRoleRefs replaces every effective role atomically. The
// legacy project__user row remains the CAS-protected membership state.
func (d *SqlDb) SetProjectUserRoleRefs(projectID, userID int, roleIDs []db.ProjectRoleID, expectedRevision int) error {
	if len(roleIDs) == 0 {
		return &common_errors.ValidationError{Message: "A member needs at least one role"}
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
	}
	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return err
	}
	if err = d.validateProjectRoleRefs(tx, projectID, roleIDs); err != nil {
		return err
	}
	var managed int64
	if managed, err = tx.SelectInt(d.PrepareQuery("select count(*) from project__user_role where project_id=? and user_id=? and (ldap_group_managed_assignment_id is not null or oidc_group_managed_assignment_id is not null)"), projectID, userID); err != nil {
		return err
	}
	if managed > 0 {
		return &common_errors.ValidationError{Message: "Directory-managed roles cannot be replaced manually"}
	}
	oldCanManage, err := d.memberCanManageProjectUsers(tx, projectID, userID)
	if err != nil {
		return err
	}
	newCanManage, err := d.roleRefsCanManageProjectUsers(tx, projectID, roleIDs)
	if err != nil {
		return err
	}
	if oldCanManage && !newCanManage {
		admins, countErr := d.countEffectiveProjectAdministrators(tx, projectID)
		if countErr != nil {
			return countErr
		}
		if admins <= 1 {
			return db.ErrLastProjectAdministrator
		}
	}
	res, err := tx.Exec(d.PrepareQuery("update project__user set revision=revision+1 where project_id=? and user_id=? and revision=?"), projectID, userID, expectedRevision)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return db.ErrProjectMembershipRevisionConflict
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from project__user_role where project_id=? and user_id=?"), projectID, userID); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if _, err = tx.Exec(d.PrepareQuery("insert into project__user_role (project_id,user_id,role_id) values (?,?,?)"), projectID, userID, roleID); err != nil {
			return err
		}
	}
	projection, projectionRoleID, err := d.projectRoleProjectionForRefs(tx, projectID, roleIDs)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("update project__user set role=?, role_id=nullif(?, '') where project_id=? and user_id=?"), projection, projectionRoleID, projectID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) memberCanManageProjectUsers(tx *gorp.Transaction, projectID, userID int) (bool, error) {
	refs, err := d.memberRoleRefs(tx, projectID, userID)
	if err != nil {
		return false, err
	}
	return d.roleRefsCanManageProjectUsers(tx, projectID, refs)
}

func (d *SqlDb) memberRoleRefs(tx *gorp.Transaction, projectID, userID int) ([]db.ProjectRoleID, error) {
	var rows []struct {
		RoleID db.ProjectRoleID `db:"role_id"`
	}
	_, err := tx.Select(&rows, d.PrepareQuery("select role_id from project__user_role where project_id=? and user_id=?"), projectID, userID)
	if err != nil {
		return nil, err
	}
	refs := make([]db.ProjectRoleID, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.RoleID)
	}
	return refs, nil
}

func (d *SqlDb) roleRefsCanManageProjectUsers(tx *gorp.Transaction, projectID int, refs []db.ProjectRoleID) (bool, error) {
	for _, ref := range refs {
		if strings.HasPrefix(string(ref), "builtin:") && db.ProjectUserRole(strings.TrimPrefix(string(ref), "builtin:")).GetPermissions().Can(db.CanManageProjectUsers) {
			return true, nil
		}
		count, err := tx.SelectInt(d.PrepareQuery("select count(*) from role where role_id=? and (project_id is null or project_id=?) and (permissions & ?) = ?"), ref, projectID, db.CanManageProjectUsers, db.CanManageProjectUsers)
		if err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (d *SqlDb) countEffectiveProjectAdministrators(tx *gorp.Transaction, projectID int) (int, error) {
	var users []struct {
		UserID int `db:"user_id"`
	}
	_, err := tx.Select(&users, d.PrepareQuery("select distinct user_id from project__user_role where project_id=?"), projectID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, user := range users {
		ok, e := d.memberCanManageProjectUsers(tx, projectID, user.UserID)
		if e != nil {
			return 0, e
		}
		if ok {
			count++
		}
	}
	return count, nil
}

func (d *SqlDb) validateProjectRoleRefs(tx interface {
	SelectInt(string, ...any) (int64, error)
}, projectID int, refs []db.ProjectRoleID) error {
	seen := make(map[db.ProjectRoleID]bool, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			return &common_errors.ValidationError{Message: "Role is duplicated"}
		}
		seen[ref] = true
		if strings.HasPrefix(string(ref), "builtin:") {
			if !db.ProjectUserRole(strings.TrimPrefix(string(ref), "builtin:")).IsValid() {
				return &common_errors.ValidationError{Message: "Role is not available in this project"}
			}
			continue
		}
		count, err := tx.SelectInt(d.PrepareQuery("select count(*) from role where role_id=? and (project_id is null or project_id=?)"), ref, projectID)
		if err != nil {
			return err
		}
		if count != 1 {
			return &common_errors.ValidationError{Message: "Role is not available in this project"}
		}
	}
	return nil
}

// projectRoleProjectionForRefs produces the one-role representation retained
// for legacy readers. It never changes authorization, which reads every
// relation row. A built-in is preferred because it is representable without a
// custom ID; otherwise the first validated custom reference is used.
func (d *SqlDb) projectRoleProjectionForRefs(tx *gorp.Transaction, projectID int, refs []db.ProjectRoleID) (string, db.ProjectRoleID, error) {
	for _, ref := range refs {
		if strings.HasPrefix(string(ref), "builtin:") {
			role := db.ProjectUserRole(strings.TrimPrefix(string(ref), "builtin:"))
			if !role.IsValid() {
				return "", "", db.ErrNotFound
			}
			return string(role), "", nil
		}
	}
	if len(refs) == 0 {
		return "", "", db.ErrNotFound
	}
	return d.projectRoleProjection(tx, projectID, refs[0])
}

func (d *SqlDb) GetProjectUser(projectID, userID int) (user db.ProjectUser, err error) {
	err = d.selectOne(&user, "select * from project__user where project_id=? and user_id=?", projectID, userID)
	return
}

func (d *SqlDb) UpdateProjectUser(projectUser db.ProjectUser) error {
	hasRoleRelations, err := d.IsMigrationApplied(db.Migration{Version: "2.20.12"})
	if err != nil {
		return err
	}
	if hasRoleRelations {
		roleID, err := d.projectUserRoleRef(projectUser)
		if err != nil {
			return err
		}
		// The relation is authoritative from 2.20.12 onward. The plural setter
		// applies the revision fence and updates its legacy projection atomically.
		return d.SetProjectUserRoleRefs(projectUser.ProjectID, projectUser.UserID, []db.ProjectRoleID{roleID}, projectUser.Revision)
	}
	return d.updateProjectUserWithRoleIdentity(projectUser)
}

func (d *SqlDb) projectUserRoleRef(projectUser db.ProjectUser) (db.ProjectRoleID, error) {
	if projectUser.RoleID != nil {
		// Preserve the legacy RoleID contract: it is a project-local identity.
		// The plural setter subsequently validates the same ref under its lock.
		if _, err := d.GetProjectRoleByID(projectUser.ProjectID, *projectUser.RoleID); err != nil {
			return "", err
		}
		return *projectUser.RoleID, nil
	}
	if projectUser.Role.IsValid() {
		return db.ProjectRoleID("builtin:" + string(projectUser.Role)), nil
	}
	role, err := d.GetProjectOrGlobalRoleBySlug(projectUser.ProjectID, string(projectUser.Role))
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

func (d *SqlDb) projectUserRoleRefForCreate(projectUser db.ProjectUser) (db.ProjectRoleID, error) {
	if projectUser.RoleID != nil {
		return *projectUser.RoleID, nil
	}
	if projectUser.Role.IsValid() {
		return db.ProjectRoleID("builtin:" + string(projectUser.Role)), nil
	}
	role, err := d.GetProjectOrGlobalRoleBySlug(projectUser.ProjectID, string(projectUser.Role))
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

func (d *SqlDb) GetProjectUserRoles(projectID, userID int) (roles []db.Role, err error) {
	var refs []struct {
		RoleID db.ProjectRoleID `db:"role_id"`
	}
	_, err = d.selectAll(&refs, "select role_id from project__user_role where project_id=? and user_id=? order by role_id", projectID, userID)
	if err != nil {
		return
	}
	for _, ref := range refs {
		if strings.HasPrefix(string(ref.RoleID), "builtin:") {
			slug := db.ProjectUserRole(strings.TrimPrefix(string(ref.RoleID), "builtin:"))
			if !slug.IsValid() {
				return nil, &common_errors.ValidationError{Message: "Project membership contains an invalid built-in role"}
			}
			roles = append(roles, db.Role{ID: ref.RoleID, Slug: string(slug), Name: string(slug), Permissions: slug.GetPermissions(), Builtin: true})
			continue
		}
		var role db.Role
		if err = d.selectOne(&role, "select * from role where role_id=? and (project_id is null or project_id=?)", ref.RoleID, projectID); err != nil {
			return
		}
		roles = append(roles, role)
	}
	return
}

// GetProjectMember separates manual roles from directory-managed roles. Both
// live in project__user_role for authorization, while MappedRoles carries the
// immutable provider and group provenance required by the membership UI.
func (d *SqlDb) GetProjectMember(projectID, userID int) (db.ProjectMember, error) {
	roles, err := d.GetProjectUserRoles(projectID, userID)
	if err != nil {
		return db.ProjectMember{}, err
	}
	if len(roles) == 0 {
		return db.ProjectMember{}, db.ErrNotFound
	}
	member := db.ProjectMember{Roles: roles}
	_, err = d.selectAll(&member.MappedRoles, d.PrepareQuery(projectMemberMappedRolesQuery), projectID, userID)
	if err != nil {
		return db.ProjectMember{}, err
	}
	for index := range member.MappedRoles {
		mapped := &member.MappedRoles[index]
		if mapped.RoleSlug == "" && strings.HasPrefix(string(mapped.RoleID), "builtin:") {
			role := db.ProjectUserRole(strings.TrimPrefix(string(mapped.RoleID), "builtin:"))
			mapped.RoleSlug = string(role)
			mapped.RoleName = builtinProjectRoleName(role)
			mapped.RolePermissions = role.GetPermissions()
		}
	}
	// A managed relation is never a manual grant. Keep it out of Roles so
	// callers cannot mistake the effective authorization projection for a role
	// that may be changed through the team API.
	managed := make(map[db.ProjectRoleID]struct{}, len(member.MappedRoles))
	for _, mapped := range member.MappedRoles {
		managed[mapped.RoleID] = struct{}{}
	}
	manualRoles := member.Roles[:0]
	for _, role := range member.Roles {
		if _, isManaged := managed[role.ID]; !isManaged {
			manualRoles = append(manualRoles, role)
		}
	}
	member.Roles = manualRoles
	return member, nil
}

func (d *SqlDb) CountProjectOwners(projectID int) (int, error) {
	count, err := d.Sql().SelectInt(d.PrepareQuery("select count(*) from project__user_role where project_id=? and role_id=?"), projectID, "builtin:owner")
	return int(count), err
}

func (d *SqlDb) GetProjectUsers(projectID int, params db.RetrieveQueryParams) ([]db.UserWithProjectRoles, error) {

	pp, err := params.Validate(db.UserProps)
	if err != nil {
		return nil, err
	}

	q := squirrel.Select("u.*").
		From("`user` as u").
		Where("exists (select 1 from project__user_role pur where pur.project_id = ? and pur.user_id = u.id)", projectID)

	sortDirection := "ASC"
	if pp.SortInverted {
		sortDirection = "DESC"
	}

	switch pp.SortBy {
	case "name", "username", "email":
		q = q.OrderBy("u." + pp.SortBy + " " + sortDirection)
	case "role":
		q = q.OrderByClause(`(
			select
			  min(coalesce(r.name, substr(pur.role_id, 9)))
			from project__user_role pur
			left join role r on r.role_id = pur.role_id
			where pur.project_id = ? and pur.user_id = u.id
		) `+sortDirection, projectID)
	default:
		q = q.OrderBy("u.name " + sortDirection)
	}

	query, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	var users []db.User
	if _, err = d.selectAll(&users, query, args...); err != nil {
		return nil, err
	}

	result := make([]db.UserWithProjectRoles, 0, len(users))
	for _, u := range users {
		roles, roleErr := d.GetProjectUserRoles(projectID, u.ID)
		if roleErr != nil {
			return nil, roleErr
		}
		result = append(result, db.UserWithProjectRoles{User: u, Roles: roles})
	}

	return result, nil
}

func (d *SqlDb) DeleteProjectUser(projectID, userID int) error {
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
	}
	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return err
	}
	managed, err := tx.SelectInt(d.PrepareQuery(
		"select count(*) from project__user_role where project_id=? and user_id=? and (ldap_group_managed_assignment_id is not null or oidc_group_managed_assignment_id is not null)"), projectID, userID)
	if err != nil {
		return err
	}
	if managed > 0 {
		return &common_errors.ValidationError{Message: "Directory-managed roles must be removed through reconciliation"}
	}
	canManage, err := d.memberCanManageProjectUsers(tx, projectID, userID)
	if err != nil {
		return err
	}
	if canManage {
		admins, countErr := d.countEffectiveProjectAdministrators(tx, projectID)
		if countErr != nil {
			return countErr
		}
		if admins <= 1 {
			return db.ErrLastProjectAdministrator
		}
	}
	res, err := tx.Exec(d.PrepareQuery("delete from project__user where project_id=? and user_id=?"), projectID, userID)
	if err = requireDeletedRow(res, err); err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from project__user_role where project_id=? and user_id=?"), projectID, userID); err != nil {
		return err
	}
	return tx.Commit()
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

	if err != nil {
		return
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

	if err != nil {
		return
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
