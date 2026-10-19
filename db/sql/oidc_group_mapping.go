package sql

import (
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func (d *SqlDb) GetOIDCGroupMappings(providerID string) ([]db.OIDCGroupMapping, error) {
	mappings := make([]db.OIDCGroupMapping, 0)
	_, err := d.selectAll(&mappings,
		`select * from oidc_group_mapping where provider_id=? order by id`, providerID)
	return mappings, err
}

func (d *SqlDb) SaveOIDCGroupMapping(
	mapping db.OIDCGroupMapping,
	expectedRevision int,
) (db.OIDCGroupMapping, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.OIDCGroupMapping{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureOIDCGroupMappingStateTx(tx, d, mapping.ProviderID); err != nil {
		return db.OIDCGroupMapping{}, err
	}
	if err = requireOIDCGroupMappingRoleTx(tx, d, mapping); err != nil {
		return db.OIDCGroupMapping{}, err
	}

	var current db.OIDCGroupMapping
	err = tx.SelectOne(&current, d.PrepareQuery(
		"select * from oidc_group_mapping where provider_id=? and id=?"), mapping.ProviderID, mapping.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows) && expectedRevision == 0:
		if mapping.Revision <= 0 {
			mapping.Revision = 1
		}
		_, err = tx.Exec(d.PrepareQuery(
			`insert into oidc_group_mapping
			 (id, provider_id, claim_value, target_scope, project_id, role_id,
			  enabled, revision, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			mapping.ID, mapping.ProviderID, mapping.ClaimValue, mapping.TargetScope,
			mapping.ProjectID, mapping.RoleID, mapping.Enabled, mapping.Revision,
			mapping.Created, mapping.Updated,
		)
	case errors.Is(err, sql.ErrNoRows):
		return db.OIDCGroupMapping{}, db.ErrOIDCGroupMappingRevisionConflict
	case err != nil:
		return db.OIDCGroupMapping{}, err
	case expectedRevision <= 0 || current.Revision != expectedRevision:
		return db.OIDCGroupMapping{}, db.ErrOIDCGroupMappingRevisionConflict
	default:
		result, updateErr := tx.Exec(d.PrepareQuery(
			`update oidc_group_mapping set claim_value=?, target_scope=?, project_id=?,
			 role_id=?, enabled=?, revision=revision+1, updated=?
			 where provider_id=? and id=? and revision=?`),
			mapping.ClaimValue, mapping.TargetScope, mapping.ProjectID, mapping.RoleID,
			mapping.Enabled, mapping.Updated, mapping.ProviderID, mapping.ID, expectedRevision,
		)
		if updateErr != nil {
			return db.OIDCGroupMapping{}, updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return db.OIDCGroupMapping{}, rowsErr
		}
		if rows != 1 {
			return db.OIDCGroupMapping{}, db.ErrOIDCGroupMappingRevisionConflict
		}
	}
	if err != nil {
		return db.OIDCGroupMapping{}, err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update oidc_group_mapping_state set revision=revision+1 where provider_id=?"), mapping.ProviderID); err != nil {
		return db.OIDCGroupMapping{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.OIDCGroupMapping{}, err
	}
	return d.getOIDCGroupMapping(mapping.ProviderID, mapping.ID)
}

func (d *SqlDb) DeleteOIDCGroupMapping(providerID string, mappingID string, expectedRevision int) error {
	return d.DeleteOIDCGroupMappingAndRevoke(providerID, mappingID, expectedRevision)
}

// DeleteOIDCGroupMappingAndRevoke removes every grant owned by this mapping
// and the mapping itself in one transaction. It uses the same removal path as
// reconciliation, including protected-administrator checks and membership
// reprojection, so deleting configuration cannot leave an orphaned grant.
func (d *SqlDb) DeleteOIDCGroupMappingAndRevoke(providerID string, mappingID string, expectedRevision int) error {
	if expectedRevision <= 0 {
		return db.ErrOIDCGroupMappingRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureOIDCGroupMappingStateTx(tx, d, providerID); err != nil {
		return err
	}
	var mapping db.OIDCGroupMapping
	if err = tx.SelectOne(&mapping, d.PrepareQuery("select * from oidc_group_mapping where provider_id=? and id=?"), providerID, mappingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrOIDCGroupMappingRevisionConflict
		}
		return err
	}
	if mapping.Revision != expectedRevision {
		return db.ErrOIDCGroupMappingRevisionConflict
	}
	var removals []db.OIDCGroupAssignmentChange
	if _, err = tx.Select(&removals, d.PrepareQuery(`
		select mapping_id, user_id, target_scope, project_id, role_id
		from oidc_group_managed_assignment
		where provider_id=? and mapping_id=?`), providerID, mappingID); err != nil {
		return err
	}
	if err = lockOIDCGroupTargetsTx(tx, d, nil, removals); err != nil {
		return err
	}
	for _, removal := range removals {
		if err = removeOIDCManagedAssignmentTx(tx, d, providerID, removal); err != nil {
			return err
		}
	}
	result, err := tx.Exec(d.PrepareQuery(
		"delete from oidc_group_mapping where provider_id=? and id=? and revision=?"),
		providerID, mappingID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrOIDCGroupMappingRevisionConflict
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update oidc_group_mapping_state set revision=revision+1 where provider_id=?"), providerID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) GetOIDCGroupMappingRevision(providerID string) (int, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureOIDCGroupMappingStateTx(tx, d, providerID); err != nil {
		return 0, err
	}
	revision, err := tx.SelectInt(d.PrepareQuery(
		"select revision from oidc_group_mapping_state where provider_id=?"), providerID)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int(revision), nil
}

func (d *SqlDb) GetOIDCGroupRoleAssignments(providerID string, userID int) ([]db.OIDCGroupRoleAssignment, error) {
	type ledgerRow struct {
		ID                 int    `db:"id"`
		ProviderID         string `db:"provider_id"`
		MappingID          string `db:"mapping_id"`
		UserID             int    `db:"user_id"`
		TargetScope        string `db:"target_scope"`
		ProjectID          *int   `db:"project_id"`
		RoleID             string `db:"role_id"`
		GlobalAssignmentID *int   `db:"global_assignment_id"`
	}
	oidcLedger := make([]ledgerRow, 0)
	_, err := d.selectAll(&oidcLedger,
		`select id, provider_id, mapping_id, user_id, target_scope, project_id, role_id, global_assignment_id
		 from oidc_group_managed_assignment`)
	if err != nil {
		return nil, err
	}
	oidcByID := make(map[int]ledgerRow)
	oidcByGlobalID := make(map[int]ledgerRow)
	for _, row := range oidcLedger {
		oidcByID[row.ID] = row
		if row.GlobalAssignmentID != nil {
			oidcByGlobalID[*row.GlobalAssignmentID] = row
		}
	}
	ldapLedger := make([]ledgerRow, 0)
	_, err = d.selectAll(&ldapLedger,
		`select id, provider_id, mapping_id, user_id, target_scope, project_id, role_id, global_assignment_id
		 from ldap_group_managed_assignment`)
	if err != nil {
		return nil, err
	}
	ldapByID := make(map[int]ledgerRow)
	ldapByGlobalID := make(map[int]ledgerRow)
	for _, row := range ldapLedger {
		ldapByID[row.ID] = row
		if row.GlobalAssignmentID != nil {
			ldapByGlobalID[*row.GlobalAssignmentID] = row
		}
	}

	type globalRow struct {
		ID                int                 `db:"id"`
		UserID            int                 `db:"user_id"`
		RoleID            db.ProjectRoleID    `db:"role_id"`
		GlobalPermissions db.GlobalPermission `db:"global_permissions"`
	}
	globalRows := make([]globalRow, 0)
	_, err = d.selectAll(&globalRows,
		`select distinct a.id, a.user_id, a.role_id, r.global_permissions
		 from user__global_role a
		 join role r on r.role_id=a.role_id and r.project_id is null
		 join user__external_identity i on i.user_id=a.user_id and i.type='oidc' and i.provider=?
		 where (?=0 or a.user_id=?) order by a.user_id, a.id`, providerID, userID, userID)
	if err != nil {
		return nil, err
	}
	assignments := make([]db.OIDCGroupRoleAssignment, 0, len(globalRows))
	for _, row := range globalRows {
		assignment := db.OIDCGroupRoleAssignment{
			UserID: row.UserID, TargetScope: "global", RoleID: string(row.RoleID), OwnerKind: "manual",
		}
		if owner, ok := oidcByGlobalID[row.ID]; ok {
			assignment.OwnerKind = "oidc"
			assignment.ManagedByProviderID = owner.ProviderID
			assignment.ManagedByMappingID = owner.MappingID
		} else if _, ok := ldapByGlobalID[row.ID]; ok {
			assignment.OwnerKind = "ldap"
		}
		if row.GlobalPermissions.Can(db.CanManageGlobalRoles) {
			count, countErr := d.Sql().SelectInt(d.PrepareQuery(
				"select count(distinct u.id) from `user` u where u.admin=true or exists ("+
					"select 1 from user__global_role a join role r on r.role_id=a.role_id and r.project_id is null "+
					"where a.user_id=u.id and a.id<>? and (r.global_permissions & ?) = ?)"),
				row.ID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
			if countErr != nil {
				return nil, countErr
			}
			assignment.ProtectedAdministrator = count == 0
		}
		assignments = append(assignments, assignment)
	}

	type projectRow struct {
		UserID                       int                      `db:"user_id"`
		ProjectID                    int                      `db:"project_id"`
		RoleID                       db.ProjectRoleID         `db:"role_id"`
		LDAPGroupManagedAssignmentID *int                     `db:"ldap_group_managed_assignment_id"`
		OIDCGroupManagedAssignmentID *int                     `db:"oidc_group_managed_assignment_id"`
		Permissions                  db.ProjectUserPermission `db:"permissions"`
	}
	projectRows := make([]projectRow, 0)
	_, err = d.selectAll(&projectRows,
		`select distinct pur.user_id, pur.project_id, pur.role_id,
		 pur.ldap_group_managed_assignment_id, pur.oidc_group_managed_assignment_id,
		 case when pur.role_id=? then ?
		      when pur.role_id=? then ?
		      when pur.role_id=? then ?
		      when pur.role_id=? then ?
		      else coalesce(r.permissions, 0) end permissions
		 from project__user_role pur
		 join user__external_identity i on i.user_id=pur.user_id and i.type='oidc' and i.provider=?
		 left join role r on r.role_id=pur.role_id and (r.project_id=pur.project_id or r.project_id is null)
		 where (?=0 or pur.user_id=?) order by pur.user_id, pur.project_id, pur.role_id`,
		db.BuiltinProjectRoleReferenceOwner, db.ProjectOwner.GetPermissions(),
		db.BuiltinProjectRoleReferenceManager, db.ProjectManager.GetPermissions(),
		db.BuiltinProjectRoleReferenceTaskRunner, db.ProjectTaskRunner.GetPermissions(),
		db.BuiltinProjectRoleReferenceGuest, db.ProjectGuest.GetPermissions(), providerID, userID, userID)
	if err != nil {
		return nil, err
	}
	for _, row := range projectRows {
		assignment := db.OIDCGroupRoleAssignment{
			UserID: row.UserID, TargetScope: "project", ProjectID: &row.ProjectID,
			RoleID: string(row.RoleID), OwnerKind: "manual",
		}
		if row.OIDCGroupManagedAssignmentID != nil {
			if owner, ok := oidcByID[*row.OIDCGroupManagedAssignmentID]; ok &&
				owner.TargetScope == "project" && owner.UserID == row.UserID && owner.ProjectID != nil &&
				*owner.ProjectID == row.ProjectID && owner.RoleID == string(row.RoleID) {
				assignment.OwnerKind = "oidc"
				assignment.ManagedByProviderID = owner.ProviderID
				assignment.ManagedByMappingID = owner.MappingID
			}
		} else if row.LDAPGroupManagedAssignmentID != nil {
			if _, ok := ldapByID[*row.LDAPGroupManagedAssignmentID]; ok {
				assignment.OwnerKind = "ldap"
			}
		}
		protected, protectedErr := d.projectRoleAssignmentProtectedAdministrator(row.ProjectID, row.UserID, row.RoleID, row.Permissions)
		if protectedErr != nil {
			return nil, protectedErr
		}
		assignment.ProtectedAdministrator = protected
		assignments = append(assignments, assignment)
	}
	return assignments, nil
}

func (d *SqlDb) SaveOIDCGroupReconciliation(
	reconciliation db.OIDCGroupReconciliation,
) (db.OIDCGroupReconciliation, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertOIDCGroupReconciliationTx(tx, d, &reconciliation); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	return reconciliation, nil
}

func (d *SqlDb) ApplyOIDCGroupReconciliation(
	reconciliation db.OIDCGroupReconciliation,
	additions []db.OIDCGroupAssignmentChange,
	removals []db.OIDCGroupAssignmentChange,
) (db.OIDCGroupReconciliation, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockOIDCGroupMappingStateTx(tx, d, reconciliation.ProviderID, reconciliation.MappingRevision); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	if err = lockOIDCGroupTargetsTx(tx, d, additions, removals); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	for _, change := range removals {
		if err = removeOIDCManagedAssignmentTx(tx, d, reconciliation.ProviderID, change); err != nil {
			return db.OIDCGroupReconciliation{}, err
		}
	}
	for _, change := range additions {
		if err = addOIDCManagedAssignmentTx(tx, d, reconciliation.ProviderID, change, reconciliation.Created); err != nil {
			return db.OIDCGroupReconciliation{}, err
		}
	}
	if err = insertOIDCGroupReconciliationTx(tx, d, &reconciliation); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.OIDCGroupReconciliation{}, err
	}
	return reconciliation, nil
}

func (d *SqlDb) GetOIDCGroupReconciliationHistory(
	providerID string,
	limit int,
) ([]db.OIDCGroupReconciliation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	result := make([]db.OIDCGroupReconciliation, 0)
	_, err := d.selectAll(&result,
		`select * from oidc_group_reconciliation where provider_id=? order by id desc limit ?`,
		providerID, limit)
	return result, err
}

func (d *SqlDb) getOIDCGroupMapping(providerID string, mappingID string) (db.OIDCGroupMapping, error) {
	var mapping db.OIDCGroupMapping
	err := d.selectOne(&mapping,
		"select * from oidc_group_mapping where provider_id=? and id=?", providerID, mappingID)
	return mapping, err
}

func ensureOIDCGroupMappingStateTx(tx *gorp.Transaction, d *SqlDb, providerID string) error {
	var revision int64
	err := tx.SelectOne(&revision, d.PrepareQuery(
		"select revision from oidc_group_mapping_state where provider_id=?"), providerID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(d.PrepareQuery(
		"insert into oidc_group_mapping_state (provider_id, revision) values (?, 1)"), providerID)
	return err
}

func lockOIDCGroupMappingStateTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	expectedRevision int,
) error {
	result, err := tx.Exec(d.PrepareQuery(
		"update oidc_group_mapping_state set revision=revision where provider_id=? and revision=?"),
		providerID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrOIDCGroupPreviewStale
	}
	return nil
}

func requireOIDCGroupMappingRoleTx(tx *gorp.Transaction, d *SqlDb, mapping db.OIDCGroupMapping) error {
	var count int64
	var err error
	switch mapping.TargetScope {
	case "global":
		if mapping.ProjectID != nil {
			return db.ErrNotFound
		}
		count, err = tx.SelectInt(d.PrepareQuery(
			"select count(1) from `role` where role_id=? and project_id is null"), mapping.RoleID)
	case "project":
		if mapping.ProjectID == nil {
			return db.ErrNotFound
		}
		if mapping.RoleID == "builtin:owner" || mapping.RoleID == "builtin:manager" ||
			mapping.RoleID == "builtin:task_runner" || mapping.RoleID == "builtin:guest" {
			return nil
		}
		count, err = tx.SelectInt(d.PrepareQuery(
			"select count(1) from `role` where role_id=? and project_id=?"), mapping.RoleID, *mapping.ProjectID)
	default:
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func lockOIDCGroupTargetsTx(
	tx *gorp.Transaction,
	d *SqlDb,
	additions []db.OIDCGroupAssignmentChange,
	removals []db.OIDCGroupAssignmentChange,
) error {
	needsGlobalLock := false
	projectIDs := make(map[int]bool)
	for _, change := range append(append([]db.OIDCGroupAssignmentChange{}, additions...), removals...) {
		switch change.TargetScope {
		case "global":
			needsGlobalLock = true
		case "project":
			if change.ProjectID == nil {
				return db.ErrOIDCGroupMappingCollision
			}
			projectIDs[*change.ProjectID] = true
		default:
			return db.ErrOIDCGroupMappingCollision
		}
	}
	if needsGlobalLock {
		if err := lockGlobalRoleMutations(tx, d); err != nil {
			return err
		}
	}
	orderedProjectIDs := make([]int, 0, len(projectIDs))
	for projectID := range projectIDs {
		orderedProjectIDs = append(orderedProjectIDs, projectID)
	}
	sort.Ints(orderedProjectIDs)
	for _, projectID := range orderedProjectIDs {
		if err := lockProjectRoleMutations(tx, d, projectID); err != nil {
			return err
		}
	}
	return nil
}

func insertOIDCGroupReconciliationTx(
	tx *gorp.Transaction,
	d *SqlDb,
	reconciliation *db.OIDCGroupReconciliation,
) error {
	query := `insert into oidc_group_reconciliation
	 (provider_id, user_id, source, status, token, mapping_revision, claim_revision, preview_json,
	  addition_count, removal_count, unknown_count, collision_count, protected_admin_count,
	  error_code, actor_id, created, applied_at)
	 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
		reconciliation.ProviderID, reconciliation.UserID, reconciliation.Source, reconciliation.Status,
		reconciliation.Token, reconciliation.MappingRevision, reconciliation.ClaimRevision,
		reconciliation.PreviewJSON, reconciliation.AdditionCount, reconciliation.RemovalCount,
		reconciliation.UnknownCount, reconciliation.CollisionCount, reconciliation.ProtectedAdminCount,
		reconciliation.ErrorCode, reconciliation.ActorID, reconciliation.Created, reconciliation.AppliedAt,
	}
	if d.GetDialect() == util.DbDriverPostgres {
		id, err := tx.SelectInt(d.PrepareQuery(query+" returning id"), args...)
		if err != nil {
			return err
		}
		reconciliation.ID = int(id)
		return nil
	}
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	reconciliation.ID = int(id)
	return nil
}

func removeOIDCManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.OIDCGroupAssignmentChange,
) error {
	var ledger struct {
		ID                 int  `db:"id"`
		GlobalAssignmentID *int `db:"global_assignment_id"`
	}
	err := tx.SelectOne(&ledger, d.PrepareQuery(
		`select id, global_assignment_id from oidc_group_managed_assignment
		 where provider_id=? and mapping_id=? and user_id=? and target_scope=?
		   and role_id=? and ((project_id is null and ? is null) or project_id=?)`),
		providerID, change.MappingID, change.UserID, change.TargetScope, change.RoleID,
		change.ProjectID, change.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrOIDCGroupMappingCollision
	}
	if err != nil {
		return err
	}
	switch change.TargetScope {
	case "global":
		if ledger.GlobalAssignmentID == nil {
			return db.ErrOIDCGroupMappingCollision
		}
		var assignment db.GlobalRoleAssignment
		err = tx.SelectOne(&assignment, d.PrepareQuery(
			`select a.id, a.user_id, a.role_id, a.revision, r.global_permissions
			 from user__global_role a join role r on r.role_id=a.role_id and r.project_id is null
			 where a.id=? and a.user_id=? and a.role_id=?`),
			*ledger.GlobalAssignmentID, change.UserID, change.RoleID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrOIDCGroupMappingCollision
		}
		if err != nil {
			return err
		}
		if assignment.GlobalPermissions.Can(db.CanManageGlobalRoles) {
			if err = requireGlobalAdministratorWithoutAssignmentTx(tx, d, assignment.ID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(d.PrepareQuery(
			"delete from user__global_role where id=? and user_id=?"), assignment.ID, change.UserID)
	case "project":
		if change.ProjectID == nil {
			return db.ErrOIDCGroupMappingCollision
		}
		assigned, err := tx.SelectInt(d.PrepareQuery(
			`select count(1) from project__user_role where project_id=? and user_id=? and role_id=?
			 and oidc_group_managed_assignment_id=?`),
			*change.ProjectID, change.UserID, change.RoleID, ledger.ID)
		if err != nil {
			return err
		}
		if assigned != 1 {
			return db.ErrOIDCGroupMappingCollision
		}
		if _, err = tx.Exec(d.PrepareQuery(
			`delete from project__user_role where project_id=? and user_id=? and role_id=?
			 and oidc_group_managed_assignment_id=?`),
			*change.ProjectID, change.UserID, change.RoleID, ledger.ID); err == nil {
			removedCanManage, permissionErr := d.roleRefsCanManageProjectUsers(tx, *change.ProjectID, []db.ProjectRoleID{db.ProjectRoleID(change.RoleID)})
			if permissionErr != nil {
				return permissionErr
			}
			if removedCanManage {
				administrators, countErr := d.countEffectiveProjectAdministrators(tx, *change.ProjectID)
				if countErr != nil {
					return countErr
				}
				if administrators == 0 {
					return db.ErrLastProjectAdministrator
				}
			}
			if err = reprojectProjectMembershipAfterOIDCRemoval(tx, d, *change.ProjectID, change.UserID, ledger.ID); err != nil {
				return err
			}
			_, err = tx.Exec(d.PrepareQuery(
				`delete from project__user where project_id=? and user_id=? and oidc_group_managed_assignment_id=?
				 and not exists (select 1 from project__user_role where project_id=? and user_id=?)`),
				*change.ProjectID, change.UserID, ledger.ID, *change.ProjectID, change.UserID)
			if err != nil {
				return err
			}
			_, err = tx.Exec(d.PrepareQuery(
				"delete from oidc_group_managed_assignment where id=?"), ledger.ID)
		}
	default:
		return db.ErrOIDCGroupMappingCollision
	}
	return err
}

func reprojectProjectMembershipAfterOIDCRemoval(tx *gorp.Transaction, d *SqlDb, projectID, userID, ledgerID int) error {
	var remaining struct {
		RoleID db.ProjectRoleID `db:"role_id"`
		LDAP   *int             `db:"ldap_group_managed_assignment_id"`
		OIDC   *int             `db:"oidc_group_managed_assignment_id"`
	}
	err := tx.SelectOne(&remaining, d.PrepareQuery("select role_id, ldap_group_managed_assignment_id, oidc_group_managed_assignment_id from project__user_role where project_id=? and user_id=? order by role_id limit 1"), projectID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	role, roleID, err := d.projectRoleProjection(tx, projectID, remaining.RoleID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("update project__user set revision=revision+1 where project_id=? and user_id=?"), projectID, userID); err != nil {
		return err
	}
	_, err = tx.Exec(d.PrepareQuery("update project__user set role=?, role_id=nullif(?, ''), ldap_group_managed_assignment_id=?, oidc_group_managed_assignment_id=? where project_id=? and user_id=? and oidc_group_managed_assignment_id=?"), role, roleID, remaining.LDAP, remaining.OIDC, projectID, userID, ledgerID)
	return err
}

func addOIDCManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.OIDCGroupAssignmentChange,
	created time.Time,
) error {
	var globalAssignmentID *int
	switch change.TargetScope {
	case "global":
		count, err := tx.SelectInt(d.PrepareQuery(
			"select count(1) from user__global_role where user_id=? and role_id=?"),
			change.UserID, change.RoleID)
		if err != nil {
			return err
		}
		if count != 0 {
			return db.ErrOIDCGroupMappingCollision
		}
		if err = requireGlobalRoleAndUserTx(tx, d, db.ProjectRoleID(change.RoleID), change.UserID); err != nil {
			return err
		}
		query := "insert into user__global_role (user_id, role_id, revision) values (?, ?, 1)"
		if d.GetDialect() == util.DbDriverPostgres {
			id, insertErr := tx.SelectInt(d.PrepareQuery(query+" returning id"), change.UserID, change.RoleID)
			if insertErr != nil {
				return insertErr
			}
			value := int(id)
			globalAssignmentID = &value
		} else {
			result, insertErr := tx.Exec(d.PrepareQuery(query), change.UserID, change.RoleID)
			if insertErr != nil {
				return insertErr
			}
			id, idErr := result.LastInsertId()
			if idErr != nil {
				return idErr
			}
			value := int(id)
			globalAssignmentID = &value
		}
	case "project":
		if change.ProjectID == nil {
			return db.ErrNotFound
		}
		mapping := db.OIDCGroupMapping{TargetScope: "project", ProjectID: change.ProjectID, RoleID: change.RoleID}
		if err := requireOIDCGroupMappingRoleTx(tx, d, mapping); err != nil {
			return err
		}
		count, err := tx.SelectInt(d.PrepareQuery(
			"select count(1) from project__user where project_id=? and user_id=?"),
			*change.ProjectID, change.UserID)
		if err != nil {
			return err
		}
		if count != 0 {
			if err = ensureOIDCProjectMembershipCompatibleForAdditionTx(tx, d, change); err != nil {
				return err
			}
		}
		ledgerID, insertErr := insertOIDCManagedAssignmentTx(tx, d, providerID, change, nil, created)
		if insertErr != nil {
			return insertErr
		}
		if count == 0 {
			_, err = tx.Exec(d.PrepareQuery(
				`insert into project__user
				 (project_id, user_id, role, role_id, revision, oidc_group_managed_assignment_id)
				 values (?, ?, '', ?, 1, ?)`),
				*change.ProjectID, change.UserID, change.RoleID, ledgerID)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(d.PrepareQuery(
			`insert into project__user_role
			 (project_id, user_id, role_id, oidc_group_managed_assignment_id)
			 values (?, ?, ?, ?)`),
			*change.ProjectID, change.UserID, change.RoleID, ledgerID)
		if err != nil {
			return err
		}
		if count != 0 {
			_, err = tx.Exec(d.PrepareQuery(
				"update project__user set revision=revision+1 where project_id=? and user_id=?"),
				*change.ProjectID, change.UserID)
		}
		return err
	default:
		return db.ErrNotFound
	}
	_, err := insertOIDCManagedAssignmentTx(tx, d, providerID, change, globalAssignmentID, created)
	return err
}

func ensureOIDCProjectMembershipCompatibleForAdditionTx(
	tx *gorp.Transaction,
	d *SqlDb,
	change db.OIDCGroupAssignmentChange,
) error {
	type relation struct {
		RoleID db.ProjectRoleID `db:"role_id"`
		LDAP   *int             `db:"ldap_group_managed_assignment_id"`
		OIDC   *int             `db:"oidc_group_managed_assignment_id"`
	}
	var relations []relation
	_, err := tx.Select(&relations, d.PrepareQuery(
		"select role_id, ldap_group_managed_assignment_id, oidc_group_managed_assignment_id from project__user_role where project_id=? and user_id=?"),
		*change.ProjectID, change.UserID)
	if err != nil {
		return err
	}
	if len(relations) == 0 {
		return db.ErrOIDCGroupMappingCollision
	}
	for _, relation := range relations {
		if string(relation.RoleID) == change.RoleID || (relation.LDAP == nil && relation.OIDC == nil) || (relation.LDAP != nil && relation.OIDC != nil) {
			return db.ErrOIDCGroupMappingCollision
		}
	}
	return nil
}

func insertOIDCManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.OIDCGroupAssignmentChange,
	globalAssignmentID *int,
	created time.Time,
) (int, error) {
	query := d.PrepareQuery(
		`insert into oidc_group_managed_assignment
		 (provider_id, mapping_id, user_id, target_scope, project_id, role_id,
		  global_assignment_id, created) values (?, ?, ?, ?, ?, ?, ?, ?)`)
	args := []any{providerID, change.MappingID, change.UserID, change.TargetScope, change.ProjectID,
		change.RoleID, globalAssignmentID, created}
	if d.GetDialect() == util.DbDriverPostgres {
		id, err := tx.SelectInt(query+" returning id", args...)
		return int(id), err
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

var _ db.OIDCGroupMappingRepository = (*SqlDb)(nil)
