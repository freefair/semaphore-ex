package sql

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"strings"
)

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
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
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
	SelectOne(any, string, ...any) error
}, d *SqlDb, projectID int) error {
	if d.GetDialect() != util.DbDriverSQLite {
		var project struct {
			ID int `db:"id"`
		}
		err := tx.SelectOne(&project, d.PrepareQuery("select id from project where id=? for update"), projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrNotFound
		}
		return err
	}
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

// ResolveProjectWorkflowRoleIdentity returns exactly one live project role.
// It never follows a global role or legacy slug, and treats stale/malformed
// directory ownership as unavailable instead of falling back to a broad bit.
func (d *SqlDb) ResolveProjectWorkflowRoleIdentity(
	projectID int,
	userID int,
) (db.ProjectWorkflowRoleIdentity, error) {
	member, err := d.GetProjectUser(projectID, userID)
	if errors.Is(err, db.ErrNotFound) {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, err
	}
	if member.LDAPGroupManagedAssignmentID != nil && member.OIDCGroupManagedAssignmentID != nil {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	identity, roleIdentifier, builtIn, err := d.projectWorkflowRoleIdentityBase(member)
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, err
	}
	switch {
	case member.LDAPGroupManagedAssignmentID != nil:
		return d.resolveLDAPProjectWorkflowRoleIdentity(identity, member, roleIdentifier, *member.LDAPGroupManagedAssignmentID)
	case member.OIDCGroupManagedAssignmentID != nil:
		return d.resolveOIDCProjectWorkflowRoleIdentity(identity, member, roleIdentifier, *member.OIDCGroupManagedAssignmentID)
	default:
		if builtIn {
			identity.Origin = db.ProjectWorkflowRoleOriginBuiltIn
		} else {
			identity.Origin = db.ProjectWorkflowRoleOriginManual
		}
		if err = identity.Validate(); err != nil {
			return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
		}
		return identity, nil
	}
}

func (d *SqlDb) projectWorkflowRoleIdentityBase(
	member db.ProjectUser,
) (db.ProjectWorkflowRoleIdentity, string, bool, error) {
	if member.RoleID == nil {
		reference, ok := db.ProjectRoleReferenceForBuiltInRole(member.Role)
		if !ok || member.Revision < 1 {
			return db.ProjectWorkflowRoleIdentity{}, "", false, db.ErrProjectWorkflowRoleIdentityUnavailable
		}
		return db.ProjectWorkflowRoleIdentity{
			Reference: reference, Permissions: member.Role.GetPermissions(), Revision: member.Revision,
		}, string(member.Role), true, nil
	}
	role, err := d.GetProjectRoleByID(member.ProjectID, *member.RoleID)
	if errors.Is(err, db.ErrNotFound) || err == nil &&
		(role.ProjectID == nil || *role.ProjectID != member.ProjectID || role.Revision < 1) {
		return db.ProjectWorkflowRoleIdentity{}, "", false, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, "", false, err
	}
	return db.ProjectWorkflowRoleIdentity{
		Reference: db.ProjectRoleReferenceForCustomRole(role.ID), Permissions: role.Permissions, Revision: role.Revision,
	}, string(role.ID), false, nil
}

type workflowRoleDirectoryProvenance struct {
	ProviderID        string `db:"provider_id"`
	MappingID         string `db:"mapping_id"`
	MappingRevision   int    `db:"mapping_revision"`
	DirectoryRevision string `db:"directory_revision"`
}

func (d *SqlDb) resolveLDAPProjectWorkflowRoleIdentity(
	identity db.ProjectWorkflowRoleIdentity,
	member db.ProjectUser,
	roleIdentifier string,
	ledgerID int,
) (db.ProjectWorkflowRoleIdentity, error) {
	var provenance workflowRoleDirectoryProvenance
	err := d.selectOne(&provenance, `
		select assignment.provider_id, assignment.mapping_id, mapping.revision as mapping_revision,
		       reconciliation.directory_revision
		from ldap_group_managed_assignment assignment
		join ldap_group_mapping mapping on mapping.provider_id=assignment.provider_id and mapping.id=assignment.mapping_id
		join ldap_group_mapping_state state on state.provider_id=assignment.provider_id
		join ldap_group_reconciliation reconciliation on reconciliation.id=(
			select latest.id from ldap_group_reconciliation latest
			where latest.provider_id=assignment.provider_id and latest.mapping_revision=state.revision
			  and latest.status='applied' and latest.applied_at is not null
			order by latest.id desc limit 1
		)
		where assignment.id=? and assignment.user_id=? and assignment.project_id=?
		  and assignment.target_scope='project' and assignment.role_id=?
		  and mapping.target_scope='project' and mapping.project_id=? and mapping.role_id=? and mapping.enabled=true`,
		ledgerID, member.UserID, member.ProjectID, roleIdentifier, member.ProjectID, roleIdentifier)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, err
	}
	return directoryWorkflowRoleIdentity(identity, db.ProjectWorkflowRoleOriginLDAP, provenance)
}

func (d *SqlDb) resolveOIDCProjectWorkflowRoleIdentity(
	identity db.ProjectWorkflowRoleIdentity,
	member db.ProjectUser,
	roleIdentifier string,
	ledgerID int,
) (db.ProjectWorkflowRoleIdentity, error) {
	var provenance workflowRoleDirectoryProvenance
	err := d.selectOne(&provenance, `
		select assignment.provider_id, assignment.mapping_id, mapping.revision as mapping_revision,
		       reconciliation.claim_revision as directory_revision
		from oidc_group_managed_assignment assignment
		join oidc_group_mapping mapping on mapping.provider_id=assignment.provider_id and mapping.id=assignment.mapping_id
		join oidc_group_mapping_state state on state.provider_id=assignment.provider_id
		join oidc_group_reconciliation reconciliation on reconciliation.id=(
			select latest.id from oidc_group_reconciliation latest
			where latest.provider_id=assignment.provider_id and latest.user_id=assignment.user_id
			  and latest.mapping_revision=state.revision and latest.status='applied' and latest.applied_at is not null
			order by latest.id desc limit 1
		)
		where assignment.id=? and assignment.user_id=? and assignment.project_id=?
		  and assignment.target_scope='project' and assignment.role_id=?
		  and mapping.target_scope='project' and mapping.project_id=? and mapping.role_id=? and mapping.enabled=true`,
		ledgerID, member.UserID, member.ProjectID, roleIdentifier, member.ProjectID, roleIdentifier)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	if err != nil {
		return db.ProjectWorkflowRoleIdentity{}, err
	}
	return directoryWorkflowRoleIdentity(identity, db.ProjectWorkflowRoleOriginOIDC, provenance)
}

func directoryWorkflowRoleIdentity(
	identity db.ProjectWorkflowRoleIdentity,
	origin db.ProjectWorkflowRoleOrigin,
	provenance workflowRoleDirectoryProvenance,
) (db.ProjectWorkflowRoleIdentity, error) {
	if provenance.ProviderID == "" || provenance.MappingID == "" || provenance.MappingRevision < 1 ||
		provenance.DirectoryRevision == "" {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	fingerprint := sha256.Sum256([]byte(provenance.DirectoryRevision))
	identity.Origin = origin
	identity.DirectoryProviderID = provenance.ProviderID
	identity.DirectoryMappingID = provenance.MappingID
	identity.DirectoryMappingRevision = provenance.MappingRevision
	identity.DirectoryRevisionFingerprint = hex.EncodeToString(fingerprint[:])
	if err := identity.Validate(); err != nil {
		return db.ProjectWorkflowRoleIdentity{}, db.ErrProjectWorkflowRoleIdentityUnavailable
	}
	return identity, nil
}
