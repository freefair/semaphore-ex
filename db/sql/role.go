package sql

import (
	"database/sql"
	"errors"
	"strings"

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
	_, err := d.insert(
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

func (d *SqlDb) GetProjectRoleByID(projectID int, roleID db.ProjectRoleID) (db.Role, error) {
	var role db.Role
	err := d.selectOne(&role, "select * from `role` where role_id=? and project_id=?", roleID, projectID)
	return role, err
}

func (d *SqlDb) CreateProjectRole(role db.Role) (db.Role, error) {
	if err := db.ValidateProjectRole(role); err != nil {
		return db.Role{}, err
	}
	role.Name = strings.TrimSpace(role.Name)
	if role.Slug == "" {
		role.Slug = string(role.ID)
	}
	_, err := d.insert(
		"",
		"insert into `role` (role_id, slug, name, permissions, project_id, revision) values (?, ?, ?, ?, ?, ?)",
		role.ID,
		role.Slug,
		role.Name,
		role.Permissions,
		role.ProjectID,
		role.Revision,
	)
	if err != nil {
		return db.Role{}, err
	}
	return role, nil
}

func (d *SqlDb) UpdateProjectRole(
	projectID int,
	role db.Role,
	expectedRevision int,
) (db.Role, error) {
	if role.ProjectID == nil || *role.ProjectID != projectID {
		return db.Role{}, db.ErrNotFound
	}
	if role.Revision != expectedRevision {
		return db.Role{}, db.ErrProjectRoleRevisionConflict
	}
	if err := db.ValidateProjectRole(role); err != nil {
		return db.Role{}, err
	}
	result, err := d.exec(
		"update `role` set name=?, permissions=?, revision=revision+1 "+
			"where project_id=? and role_id=? and revision=?",
		strings.TrimSpace(role.Name), role.Permissions, projectID, role.ID, expectedRevision,
	)
	if err != nil {
		return db.Role{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.Role{}, err
	}
	if rows != 1 {
		if _, findErr := d.GetProjectRoleByID(projectID, role.ID); errors.Is(findErr, db.ErrNotFound) {
			return db.Role{}, db.ErrNotFound
		} else if findErr != nil {
			return db.Role{}, findErr
		}
		return db.Role{}, db.ErrProjectRoleRevisionConflict
	}
	return d.GetProjectRoleByID(projectID, role.ID)
}

func (d *SqlDb) DeleteProjectRole(projectID int, roleID db.ProjectRoleID, expectedRevision int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return err
	}
	var role db.Role
	err = tx.SelectOne(&role, d.PrepareQuery(
		"select * from `role` where project_id=? and role_id=?"), projectID, roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if role.Revision != expectedRevision {
		return db.ErrProjectRoleRevisionConflict
	}
	assigned, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from project__user where project_id=? and role_id=?"), projectID, roleID)
	if err != nil {
		return err
	}
	if assigned > 0 {
		return db.ErrProjectRoleAssigned
	}
	result, err := tx.Exec(d.PrepareQuery(
		"delete from `role` where project_id=? and role_id=? and revision=?"),
		projectID, roleID, expectedRevision)
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
	return tx.Commit()
}

func lockProjectRoleMutations(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, d *SqlDb, projectID int) error {
	result, err := tx.Exec(d.PrepareQuery("update project set id=id where id=?"), projectID)
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

func (d *SqlDb) GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error) {
	var role db.Role
	err := d.selectOne(
		&role,
		"select * from `role` where slug=? and (project_id=? or project_id is null)",
		slug,
		projectID)
	return role, err
}
