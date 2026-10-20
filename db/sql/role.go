package sql

import (
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

func (d *SqlDb) GetGlobalRoleBySlug(slug string) (role db.Role, err error) {
	if builtin := db.ProjectUserRole(strings.ToLower(slug)); builtin.IsValid() {
		return builtinProjectRole(builtin), nil
	}
	err = d.selectOne(&role, "select * from `role` where lower(slug)=lower(?) and project_id is null", slug)
	return
}

func (d *SqlDb) GetSharedRoleBySlug(slug string) (db.Role, error) { return d.GetGlobalRoleBySlug(slug) }

func builtinProjectRole(role db.ProjectUserRole) db.Role {
	return db.Role{ID: db.ProjectRoleID("builtin:" + string(role)), Slug: string(role), Name: builtinProjectRoleName(role), Permissions: role.GetPermissions(), Builtin: true, Revision: 1}
}

func builtinProjectRoleName(role db.ProjectUserRole) string {
	switch role {
	case db.ProjectOwner:
		return "Owner"
	case db.ProjectManager:
		return "Manager"
	case db.ProjectTaskRunner:
		return "Task Runner"
	case db.ProjectGuest:
		return "Guest"
	default:
		return string(role)
	}
}

func (d *SqlDb) GetProjectRole(projectID int, slug string) (role db.Role, err error) {
	err = d.selectOne(&role, "select * from `role` where lower(slug)=lower(?) and project_id=?", slug, projectID)
	return
}

func (d *SqlDb) GetProjectOrGlobalRoleBySlug(projectID int, slug string) (role db.Role, err error) {
	if builtin := db.ProjectUserRole(strings.ToLower(slug)); builtin.IsValid() {
		return builtinProjectRole(builtin), nil
	}
	err = d.selectOne(&role, `select * from role where lower(slug)=lower(?) and (project_id=? or project_id is null)
		order by project_id is null limit 1`, slug, projectID)
	return
}

func (d *SqlDb) GetProjectOrSharedRoleBySlug(projectID int, slug string) (db.Role, error) {
	return d.GetProjectOrGlobalRoleBySlug(projectID, slug)
}

func (d *SqlDb) GetProjectRoles(projectID int) (roles []db.Role, err error) {
	_, err = d.selectAll(&roles, "select * from `role` where project_id=? order by name", projectID)
	return
}

func (d *SqlDb) GetSharedRoles() (roles []db.Role, err error) {
	_, err = d.selectAll(&roles, "select * from `role` where project_id is null order by name")
	if err != nil {
		return
	}
	for _, builtin := range []db.ProjectUserRole{db.ProjectOwner, db.ProjectManager, db.ProjectTaskRunner, db.ProjectGuest} {
		roles = append([]db.Role{builtinProjectRole(builtin)}, roles...)
	}
	return
}

func (d *SqlDb) CreateRole(role db.Role) (db.Role, error) {
	if err := db.ValidateRole(role, true); err != nil {
		return db.Role{}, err
	}
	if role.ID == "" {
		role.ID = db.ProjectRoleID(role.Slug)
	}
	if role.Revision <= 0 {
		role.Revision = 1
	}
	taken, err := d.Sql().SelectInt(d.PrepareQuery(
		"select count(*) from role where lower(slug)=lower(?) and (project_id is null or project_id=?)"), role.Slug, role.ProjectID)
	if err != nil {
		return db.Role{}, err
	}
	if taken != 0 {
		return db.Role{}, &common_errors.ValidationError{Message: "Role slug is taken"}
	}
	hasIdentity, err := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.28"})
	if err != nil {
		return db.Role{}, err
	}
	if !hasIdentity {
		_, err = d.insert("", "insert into role (slug,name,permissions,project_id) values (?,?,?,?)", role.Slug, role.Name, role.Permissions, role.ProjectID)
		return role, err
	}
	hasGlobal, err := d.IsMigrationApplied(db.Migration{Version: "2.20.1-ex1.29"})
	if err != nil {
		return db.Role{}, err
	}
	if !hasGlobal {
		_, err = d.insert("", "insert into role (role_id,slug,name,permissions,project_id,revision) values (?,?,?,?,?,?)", role.ID, role.Slug, role.Name, role.Permissions, role.ProjectID, role.Revision)
		return role, err
	}
	_, err = d.insert("", `insert into role (role_id, slug, name, permissions, global_permissions, project_id, revision)
		values (?, ?, ?, ?, ?, ?, ?)`, role.ID, role.Slug, role.Name, role.Permissions, role.GlobalPermissions, role.ProjectID, role.Revision)
	return role, err
}

func (d *SqlDb) UpdateRole(role db.Role) error {
	result, err := d.exec("update role set name=?, permissions=?, global_permissions=?, revision=revision+1 where role_id=? and revision=?",
		role.Name, role.Permissions, role.GlobalPermissions, role.ID, role.Revision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrProjectRoleRevisionConflict
	}
	return nil
}

// DeleteRole is the retained legacy unscoped deletion entry point. Scoped
// callers use DeleteProjectRole or DeleteGlobalRole, which enforce current
// assignment and revision rules.
func (d *SqlDb) DeleteRole(roleID db.ProjectRoleID) error {
	mappings, err := d.Sql().SelectInt(d.PrepareQuery("select count(1) from oidc_group_mapping where role_id=?"), roleID)
	if err != nil {
		return err
	}
	if mappings > 0 {
		return db.ErrInvalidOperation
	}
	res, err := d.exec("delete from role where role_id=?", roleID)
	return requireDeletedRow(res, err)
}

func (d *SqlDb) GetRoleAssignments(roleID db.ProjectRoleID) (db.RoleAssignments, error) {
	return d.roleAssignments(d.Sql(), roleID)
}

func (d *SqlDb) roleAssignments(q gorp.SqlExecutor, roleID db.ProjectRoleID) (assigned db.RoleAssignments, err error) {
	query, args, err := squirrel.Select("pur.project_id", "pur.user_id", "u.username", "pur.role_id", "coalesce(r.slug, '') as slug").
		From("project__user_role pur").Join("`user` u on u.id=pur.user_id").
		LeftJoin("role r on r.role_id=pur.role_id").
		Where(`exists (
			select 1 from project__user_role selected
			where selected.project_id=pur.project_id
			  and selected.user_id=pur.user_id
			  and selected.role_id=?
		)`, roleID).
		OrderBy("pur.project_id", "pur.user_id", "pur.role_id").ToSql()
	if err != nil {
		return
	}
	var rows []struct {
		ProjectID int              `db:"project_id"`
		UserID    int              `db:"user_id"`
		Username  string           `db:"username"`
		RoleID    db.ProjectRoleID `db:"role_id"`
		Slug      string           `db:"slug"`
	}
	if _, err = q.Select(&rows, d.PrepareQuery(query), args...); err != nil {
		return
	}
	for _, row := range rows {
		last := len(assigned.Members) - 1
		if last < 0 || assigned.Members[last].ProjectID != row.ProjectID || assigned.Members[last].UserID != row.UserID {
			assigned.Members = append(assigned.Members, db.RoleMember{ProjectID: row.ProjectID, UserID: row.UserID, Username: row.Username})
			last++
		}
		role := db.Role{ID: row.RoleID, Slug: row.Slug}
		if strings.HasPrefix(string(row.RoleID), "builtin:") {
			builtin := db.ProjectUserRole(strings.TrimPrefix(string(row.RoleID), "builtin:"))
			if builtin.IsValid() {
				role = builtinProjectRole(builtin)
			}
		}
		assigned.Members[last].Roles = append(assigned.Members[last].Roles, role)
	}
	_, err = q.Select(&assigned.Grants, d.PrepareQuery("select * from project__template_role where role_id=?"), roleID)
	if err != nil {
		return
	}
	globalMembers, err := q.SelectInt(d.PrepareQuery("select count(*) from user__global_role where role_id=?"), roleID)
	if err != nil {
		return assigned, err
	}
	assigned.GlobalMembers = int(globalMembers)
	count, countErr := q.SelectInt(d.PrepareQuery("select count(1) from oidc_group_mapping where role_id=?"), roleID)
	if countErr != nil {
		return assigned, countErr
	}
	assigned.MappingCount = int(count)
	var mappingProjects []struct {
		ProjectID *int `db:"project_id"`
	}
	if _, err = q.Select(&mappingProjects, d.PrepareQuery("select project_id from oidc_group_mapping where role_id=?"), roleID); err != nil {
		return assigned, err
	}
	for _, mapping := range mappingProjects {
		if mapping.ProjectID != nil {
			assigned.MappingProjects = append(assigned.MappingProjects, *mapping.ProjectID)
		}
	}
	return
}

var _ db.ProjectWorkflowRoleIdentityResolver = (*SqlDb)(nil)
