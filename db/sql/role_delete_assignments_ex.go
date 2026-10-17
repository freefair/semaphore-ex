package sql

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
)

// ForceDeleteProjectRole removes only manual references to a project role.
// Directory ownership is an authorization boundary: callers must remove its
// mapping through reconciliation, never by deleting the role underneath it.
func (d *SqlDb) ForceDeleteProjectRole(projectID int, roleID db.ProjectRoleID, expectedRevision int) (db.RoleImpact, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.RoleImpact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockProjectRoleMutations(tx, d, projectID); err != nil {
		return db.RoleImpact{}, err
	}
	var role db.Role
	err = tx.SelectOne(&role, d.PrepareQuery("select * from role where project_id=? and role_id=?"), projectID, roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.RoleImpact{}, db.ErrNotFound
	}
	if err != nil {
		return db.RoleImpact{}, err
	}
	if role.Revision != expectedRevision {
		return db.RoleImpact{}, db.ErrProjectRoleRevisionConflict
	}
	if err = rejectForceRoleProtection(tx, d, projectID, roleID); err != nil {
		return db.RoleImpact{}, err
	}
	assigned, err := d.roleAssignments(tx, roleID)
	if err != nil {
		return db.RoleImpact{}, err
	}
	impact := assigned.Impact()
	if _, err = tx.Exec(d.PrepareQuery("delete from project__user_role where project_id=? and role_id=? and ldap_group_managed_assignment_id is null and oidc_group_managed_assignment_id is null"), projectID, roleID); err != nil {
		return db.RoleImpact{}, err
	}
	for _, member := range assigned.Members {
		var remaining db.ProjectRoleID
		err = tx.SelectOne(&remaining, d.PrepareQuery("select role_id from project__user_role where project_id=? and user_id=? order by role_id limit 1"), projectID, member.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return db.RoleImpact{}, err
		}
		legacyRole, legacyRoleID, projectionErr := d.projectRoleProjection(tx, projectID, remaining)
		if projectionErr != nil {
			return db.RoleImpact{}, projectionErr
		}
		if _, err = tx.Exec(d.PrepareQuery("update project__user set role=?, role_id=nullif(?, ''), revision=revision+1 where project_id=? and user_id=?"), legacyRole, legacyRoleID, projectID, member.UserID); err != nil {
			return db.RoleImpact{}, err
		}
	}
	if role.Permissions.Can(db.CanManageProjectUsers) {
		administrators, countErr := d.countEffectiveProjectAdministrators(tx, projectID)
		if countErr != nil {
			return db.RoleImpact{}, countErr
		}
		if administrators == 0 {
			return db.RoleImpact{}, db.ErrLastProjectAdministrator
		}
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from project__template_role where project_id=? and role_id=?"), projectID, roleID); err != nil {
		return db.RoleImpact{}, err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from project__user where project_id=? and not exists (select 1 from project__user_role pur where pur.project_id=project__user.project_id and pur.user_id=project__user.user_id)"), projectID); err != nil {
		return db.RoleImpact{}, err
	}
	result, err := tx.Exec(d.PrepareQuery("delete from role where project_id=? and role_id=? and revision=?"), projectID, roleID, expectedRevision)
	if err != nil {
		return db.RoleImpact{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.RoleImpact{}, err
	}
	if rows != 1 {
		return db.RoleImpact{}, db.ErrProjectRoleRevisionConflict
	}
	return impact, tx.Commit()
}

func (d *SqlDb) projectRoleProjection(tx *gorp.Transaction, projectID int, roleID db.ProjectRoleID) (string, db.ProjectRoleID, error) {
	if strings.HasPrefix(string(roleID), "builtin:") {
		role := db.ProjectUserRole(strings.TrimPrefix(string(roleID), "builtin:"))
		if !role.IsValid() {
			return "", "", db.ErrNotFound
		}
		return string(role), "", nil
	}
	var role db.Role
	err := tx.SelectOne(&role, d.PrepareQuery("select * from role where role_id=? and (project_id=? or project_id is null)"), roleID, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", db.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if role.ProjectID == nil {
		return role.Slug, "", nil
	}
	return "", role.ID, nil
}

func rejectForceRoleProtection(tx *gorp.Transaction, d *SqlDb, projectID int, roleID db.ProjectRoleID) error {
	queries := []string{
		"select count(*) from project__user_role where project_id=? and role_id=? and (ldap_group_managed_assignment_id is not null or oidc_group_managed_assignment_id is not null)",
		"select count(*) from ldap_group_mapping where target_scope='project' and project_id=? and role_id=?",
		"select count(*) from oidc_group_mapping where target_scope='project' and project_id=? and role_id=?",
	}
	for _, query := range queries {
		count, err := tx.SelectInt(d.PrepareQuery(query), projectID, roleID)
		if err != nil {
			return err
		}
		if count > 0 {
			return db.ErrProjectRoleForceProtected
		}
	}
	return nil
}

// ForceDeleteGlobalRole is deliberately conservative: a global role may be
// projected into many projects, so any project or directory reference remains
// protected until it is explicitly reconciled away.
func (d *SqlDb) ForceDeleteGlobalRole(roleID db.ProjectRoleID, expectedRevision int) (db.RoleImpact, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.RoleImpact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGlobalRoleMutations(tx, d); err != nil {
		return db.RoleImpact{}, err
	}
	var role db.Role
	err = tx.SelectOne(&role, d.PrepareQuery("select * from role where role_id=? and project_id is null"), roleID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.RoleImpact{}, db.ErrNotFound
	}
	if err != nil {
		return db.RoleImpact{}, err
	}
	if role.Revision != expectedRevision {
		return db.RoleImpact{}, db.ErrGlobalRoleRevisionConflict
	}
	for _, query := range []string{
		"select count(*) from project__user_role where role_id=?",
		"select count(*) from ldap_group_mapping where role_id=?",
		"select count(*) from oidc_group_mapping where role_id=?",
	} {
		count, queryErr := tx.SelectInt(d.PrepareQuery(query), roleID)
		if queryErr != nil {
			return db.RoleImpact{}, queryErr
		}
		if count > 0 {
			return db.RoleImpact{}, db.ErrGlobalRoleForceProtected
		}
	}
	if role.GlobalPermissions.Can(db.CanManageGlobalRoles) {
		if err = requireGlobalAdministratorWithoutRoleTx(tx, d, roleID); err != nil {
			return db.RoleImpact{}, err
		}
	}
	assigned, err := d.roleAssignments(tx, roleID)
	if err != nil {
		return db.RoleImpact{}, err
	}
	impact := assigned.Impact()
	if _, err = tx.Exec(d.PrepareQuery("delete from project__template_role where role_id=?"), roleID); err != nil {
		return db.RoleImpact{}, err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from user__global_role where role_id=?"), roleID); err != nil {
		return db.RoleImpact{}, err
	}
	result, err := tx.Exec(d.PrepareQuery("delete from role where role_id=? and project_id is null and revision=?"), roleID, expectedRevision)
	if err != nil {
		return db.RoleImpact{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.RoleImpact{}, err
	}
	if rows != 1 {
		return db.RoleImpact{}, db.ErrGlobalRoleRevisionConflict
	}
	return impact, tx.Commit()
}
