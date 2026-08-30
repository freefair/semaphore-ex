package sql

import (
	"github.com/semaphoreui/semaphore/db"
)

func (d *SqlDb) GetGlobalRoleBySlug(slug string) (db.Role, error) {
	var role db.Role
	err := d.selectOne(&role, "select * from `role` where slug=? and project_id is null", slug)
	return role, err
}

func (d *SqlDb) GetProjectRoles(projectID int) ([]db.Role, error) {
	var roles []db.Role
	_, err := d.selectAll(&roles, "select * from `role` where project_id=? order by name", projectID)
	return roles, err
}

func (d *SqlDb) GetGlobalRoles() ([]db.Role, error) {
	var roles []db.Role
	_, err := d.selectAll(&roles, "select * from `role` where project_id is null order by name")
	return roles, err
}

func (d *SqlDb) UpdateRole(role db.Role) error {
	_, err := d.exec(
		"update `role` set name=?, permissions=? where slug=?",
		role.Name,
		role.Permissions,
		role.Slug)
	return err
}

func (d *SqlDb) CreateRole(role db.Role) (db.Role, error) {
	hasScopedPermissions, err := d.IsMigrationApplied(db.Migration{Version: "2.20.30"})
	if err != nil {
		return role, err
	}
	hasRoleIdentity, err := d.IsMigrationApplied(db.Migration{Version: "2.20.29"})
	if err != nil {
		return role, err
	}
	if hasRoleIdentity {
		if role.ID == "" {
			role.ID = db.ProjectRoleID(role.Slug)
		}
		if role.Revision <= 0 {
			role.Revision = 1
		}
		if hasScopedPermissions {
			_, err = d.insert(
				"",
				"insert into `role` (role_id, slug, name, permissions, global_permissions, project_id, revision) values (?, ?, ?, ?, ?, ?, ?)",
				role.ID,
				role.Slug,
				role.Name,
				role.Permissions,
				role.GlobalPermissions,
				role.ProjectID,
				role.Revision,
			)
			return role, err
		}
		_, err = d.insert(
			"",
			"insert into `role` (role_id, slug, name, permissions, project_id, revision) values (?, ?, ?, ?, ?, ?)",
			role.ID,
			role.Slug,
			role.Name,
			role.Permissions,
			role.ProjectID,
			role.Revision,
		)
		return role, err
	}

	_, err = d.insert(
		"",
		"insert into `role` (slug, name, permissions, project_id) values (?, ?, ?, ?)",
		role.Slug,
		role.Name,
		role.Permissions,
		role.ProjectID)

	if err != nil {
		return role, err
	}

	return role, nil
}

func (d *SqlDb) DeleteRole(slug string) error {
	res, err := d.exec("delete from `role` where slug=?", slug)
	return validateMutationResult(res, err)
}

func (d *SqlDb) GetProjectRole(projectID int, slug string) (db.Role, error) {
	var role db.Role
	err := d.selectOne(&role, "select * from `role` where slug=? and project_id=?", slug, projectID)
	return role, err
}

func (d *SqlDb) GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error) {
	var role db.Role
	err := d.selectOne(
		&role,
		"select * from `role` where slug=? and (project_id=? or project_id is null)",
		slug,
		projectID)
	return role, err
}
