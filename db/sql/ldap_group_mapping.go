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

func (d *SqlDb) GetLDAPGroupMappings(providerID string) ([]db.LDAPGroupMapping, error) {
	mappings := make([]db.LDAPGroupMapping, 0)
	_, err := d.selectAll(&mappings,
		`select * from ldap_group_mapping where provider_id=? order by id`, providerID)
	return mappings, err
}

func (d *SqlDb) SaveLDAPGroupMapping(
	mapping db.LDAPGroupMapping,
	expectedRevision int,
) (db.LDAPGroupMapping, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.LDAPGroupMapping{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureLDAPGroupMappingStateTx(tx, d, mapping.ProviderID); err != nil {
		return db.LDAPGroupMapping{}, err
	}
	if err = requireLDAPGroupMappingRoleTx(tx, d, mapping); err != nil {
		return db.LDAPGroupMapping{}, err
	}

	var current db.LDAPGroupMapping
	err = tx.SelectOne(&current, d.PrepareQuery(
		"select * from ldap_group_mapping where provider_id=? and id=?"), mapping.ProviderID, mapping.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows) && expectedRevision == 0:
		if mapping.Revision <= 0 {
			mapping.Revision = 1
		}
		_, err = tx.Exec(d.PrepareQuery(
			`insert into ldap_group_mapping
			 (id, provider_id, group_external_id, target_scope, project_id, role_id,
			  enabled, revision, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			mapping.ID, mapping.ProviderID, mapping.GroupExternalID, mapping.TargetScope,
			mapping.ProjectID, mapping.RoleID, mapping.Enabled, mapping.Revision,
			mapping.Created, mapping.Updated,
		)
	case errors.Is(err, sql.ErrNoRows):
		return db.LDAPGroupMapping{}, db.ErrLDAPGroupMappingRevisionConflict
	case err != nil:
		return db.LDAPGroupMapping{}, err
	case expectedRevision <= 0 || current.Revision != expectedRevision:
		return db.LDAPGroupMapping{}, db.ErrLDAPGroupMappingRevisionConflict
	default:
		result, updateErr := tx.Exec(d.PrepareQuery(
			`update ldap_group_mapping set group_external_id=?, target_scope=?, project_id=?,
			 role_id=?, enabled=?, revision=revision+1, updated=?
			 where provider_id=? and id=? and revision=?`),
			mapping.GroupExternalID, mapping.TargetScope, mapping.ProjectID, mapping.RoleID,
			mapping.Enabled, mapping.Updated, mapping.ProviderID, mapping.ID, expectedRevision,
		)
		if updateErr != nil {
			return db.LDAPGroupMapping{}, updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return db.LDAPGroupMapping{}, rowsErr
		}
		if rows != 1 {
			return db.LDAPGroupMapping{}, db.ErrLDAPGroupMappingRevisionConflict
		}
	}
	if err != nil {
		return db.LDAPGroupMapping{}, err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update ldap_group_mapping_state set revision=revision+1 where provider_id=?"), mapping.ProviderID); err != nil {
		return db.LDAPGroupMapping{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.LDAPGroupMapping{}, err
	}
	return d.getLDAPGroupMapping(mapping.ProviderID, mapping.ID)
}

func (d *SqlDb) DeleteLDAPGroupMapping(providerID string, mappingID string, expectedRevision int) error {
	if expectedRevision <= 0 {
		return db.ErrLDAPGroupMappingRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureLDAPGroupMappingStateTx(tx, d, providerID); err != nil {
		return err
	}
	result, err := tx.Exec(d.PrepareQuery(
		"delete from ldap_group_mapping where provider_id=? and id=? and revision=?"),
		providerID, mappingID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrLDAPGroupMappingRevisionConflict
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update ldap_group_mapping_state set revision=revision+1 where provider_id=?"), providerID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) GetLDAPGroupMappingRevision(providerID string) (int, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureLDAPGroupMappingStateTx(tx, d, providerID); err != nil {
		return 0, err
	}
	revision, err := tx.SelectInt(d.PrepareQuery(
		"select revision from ldap_group_mapping_state where provider_id=?"), providerID)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int(revision), nil
}

func (d *SqlDb) GetLDAPLinkedUsers(providerID string) ([]db.LDAPLinkedUser, error) {
	users := make([]db.LDAPLinkedUser, 0)
	_, err := d.selectAll(&users,
		`select external_uid, user_id from user__external_identity
		 where type='ldap' and provider=? order by external_uid, user_id`, providerID)
	return users, err
}

func (d *SqlDb) GetLDAPGroupRoleAssignments(providerID string) ([]db.LDAPGroupRoleAssignment, error) {
	type ledgerRow struct {
		ID                 int    `db:"id"`
		MappingID          string `db:"mapping_id"`
		UserID             int    `db:"user_id"`
		TargetScope        string `db:"target_scope"`
		ProjectID          *int   `db:"project_id"`
		RoleID             string `db:"role_id"`
		GlobalAssignmentID *int   `db:"global_assignment_id"`
	}
	ledger := make([]ledgerRow, 0)
	_, err := d.selectAll(&ledger,
		`select id, mapping_id, user_id, target_scope, project_id, role_id, global_assignment_id
		 from ldap_group_managed_assignment where provider_id=?`, providerID)
	if err != nil {
		return nil, err
	}
	ledgerByGlobalID := make(map[int]ledgerRow)
	ledgerByID := make(map[int]ledgerRow)
	for _, row := range ledger {
		ledgerByID[row.ID] = row
		if row.GlobalAssignmentID != nil {
			ledgerByGlobalID[*row.GlobalAssignmentID] = row
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
		`select a.id, a.user_id, a.role_id, r.global_permissions
		 from user__global_role a
		 join role r on r.role_id=a.role_id and r.project_id is null
		 join user__external_identity i on i.user_id=a.user_id and i.type='ldap' and i.provider=?
		 order by a.user_id, a.id`, providerID)
	if err != nil {
		return nil, err
	}
	assignments := make([]db.LDAPGroupRoleAssignment, 0, len(globalRows))
	for _, row := range globalRows {
		managedBy := ""
		if owner, ok := ledgerByGlobalID[row.ID]; ok {
			managedBy = owner.MappingID
		}
		protected := false
		if row.GlobalPermissions.Can(db.CanManageGlobalRoles) {
			count, countErr := d.Sql().SelectInt(d.PrepareQuery(
				"select count(distinct u.id) from `user` u where u.admin=true or exists ("+
					"select 1 from user__global_role a join role r on r.role_id=a.role_id and r.project_id is null "+
					"where a.user_id=u.id and a.id<>? and (r.global_permissions & ?) = ?)"),
				row.ID, db.CanManageGlobalRoles, db.CanManageGlobalRoles)
			if countErr != nil {
				return nil, countErr
			}
			protected = count == 0
		}
		assignments = append(assignments, db.LDAPGroupRoleAssignment{
			UserID: row.UserID, TargetScope: "global", RoleID: string(row.RoleID),
			ManagedByMappingID: managedBy, ProtectedAdministrator: protected,
		})
	}

	type projectRow struct {
		UserID                       int                      `db:"user_id"`
		ProjectID                    int                      `db:"project_id"`
		Role                         db.ProjectUserRole       `db:"role"`
		RoleID                       *db.ProjectRoleID        `db:"role_id"`
		LDAPGroupManagedAssignmentID *int                     `db:"ldap_group_managed_assignment_id"`
		Permissions                  db.ProjectUserPermission `db:"permissions"`
	}
	projectRows := make([]projectRow, 0)
	_, err = d.selectAll(&projectRows,
		`select pu.user_id, pu.project_id, pu.role, pu.role_id, pu.ldap_group_managed_assignment_id,
		 case when pu.role='owner' then ?
		      when pu.role='manager' then ?
		      when pu.role='task_runner' then ?
		      when pu.role='guest' then ?
		      else coalesce(r.permissions, 0) end permissions
		 from project__user pu
		 join user__external_identity i on i.user_id=pu.user_id and i.type='ldap' and i.provider=?
		 left join role r on ((pu.role_id is not null and r.role_id=pu.role_id and r.project_id=pu.project_id)
		                    or (pu.role_id is null and r.slug=pu.role and (r.project_id=pu.project_id or r.project_id is null)))
		 order by pu.user_id, pu.project_id`,
		db.ProjectOwner.GetPermissions(), db.ProjectManager.GetPermissions(),
		db.ProjectTaskRunner.GetPermissions(), db.ProjectGuest.GetPermissions(), providerID)
	if err != nil {
		return nil, err
	}
	for _, row := range projectRows {
		roleID := string(row.Role)
		if row.RoleID != nil {
			roleID = string(*row.RoleID)
		}
		managedBy := ""
		if row.LDAPGroupManagedAssignmentID != nil {
			if owner, ok := ledgerByID[*row.LDAPGroupManagedAssignmentID]; ok &&
				owner.TargetScope == "project" && owner.UserID == row.UserID &&
				owner.ProjectID != nil && *owner.ProjectID == row.ProjectID && owner.RoleID == roleID {
				managedBy = owner.MappingID
			}
		}
		protected := false
		if row.Permissions.Can(db.CanManageProjectUsers) {
			count, countErr := d.Sql().SelectInt(d.PrepareQuery(
				"select count(1) from project__user pu where pu.project_id=? and pu.user_id<>? and ("+
					"pu.role='owner' or exists (select 1 from `role` r "+
					"where (r.project_id=pu.project_id or r.project_id is null) "+
					"and ((pu.role_id is not null and r.role_id=pu.role_id) or (pu.role_id is null and r.slug=pu.role)) "+
					"and (r.permissions & ?) = ?))"),
				row.ProjectID, row.UserID, db.CanManageProjectUsers, db.CanManageProjectUsers)
			if countErr != nil {
				return nil, countErr
			}
			protected = count == 0
		}
		projectID := row.ProjectID
		assignments = append(assignments, db.LDAPGroupRoleAssignment{
			UserID: row.UserID, TargetScope: "project", ProjectID: &projectID, RoleID: roleID,
			ManagedByMappingID: managedBy, ProtectedAdministrator: protected,
		})
	}
	return assignments, nil
}

func (d *SqlDb) SaveLDAPGroupReconciliation(
	reconciliation db.LDAPGroupReconciliation,
) (db.LDAPGroupReconciliation, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertLDAPGroupReconciliationTx(tx, d, &reconciliation); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	return reconciliation, nil
}

func (d *SqlDb) GetLDAPGroupPreview(providerID string, token string) (db.LDAPGroupReconciliation, error) {
	var reconciliation db.LDAPGroupReconciliation
	err := d.selectOne(&reconciliation,
		`select * from ldap_group_reconciliation
		 where provider_id=? and token=? and status='preview' order by id desc limit 1`,
		providerID, token)
	return reconciliation, err
}

func (d *SqlDb) ApplyLDAPGroupPreview(
	reconciliation db.LDAPGroupReconciliation,
	additions []db.LDAPGroupAssignmentChange,
	removals []db.LDAPGroupAssignmentChange,
) (db.LDAPGroupReconciliation, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockLDAPGroupMappingStateTx(tx, d, reconciliation.ProviderID, reconciliation.MappingRevision); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	var preview db.LDAPGroupReconciliation
	err = tx.SelectOne(&preview, d.PrepareQuery(
		`select * from ldap_group_reconciliation
		 where provider_id=? and token=? and status='preview' order by id desc limit 1`),
		reconciliation.ProviderID, reconciliation.Token)
	if errors.Is(err, sql.ErrNoRows) || (err == nil &&
		(preview.MappingRevision != reconciliation.MappingRevision ||
			preview.DirectoryRevision != reconciliation.DirectoryRevision)) {
		return db.LDAPGroupReconciliation{}, db.ErrLDAPGroupPreviewStale
	}
	if err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	if err = lockLDAPGroupRemovalTargetsTx(tx, d, removals); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	for _, change := range removals {
		if err = removeLDAPManagedAssignmentTx(tx, d, reconciliation.ProviderID, change); err != nil {
			return db.LDAPGroupReconciliation{}, err
		}
	}
	for _, change := range additions {
		if err = addLDAPManagedAssignmentTx(tx, d, reconciliation.ProviderID, change, reconciliation.Created); err != nil {
			return db.LDAPGroupReconciliation{}, err
		}
	}
	if err = insertLDAPGroupReconciliationTx(tx, d, &reconciliation); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.LDAPGroupReconciliation{}, err
	}
	return reconciliation, nil
}

func lockLDAPGroupRemovalTargetsTx(
	tx *gorp.Transaction,
	d *SqlDb,
	removals []db.LDAPGroupAssignmentChange,
) error {
	needsGlobalLock := false
	projectIDs := make(map[int]bool)
	for _, change := range removals {
		switch change.TargetScope {
		case "global":
			needsGlobalLock = true
		case "project":
			if change.ProjectID == nil {
				return db.ErrLDAPGroupMappingCollision
			}
			projectIDs[*change.ProjectID] = true
		default:
			return db.ErrLDAPGroupMappingCollision
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

func (d *SqlDb) GetLDAPGroupReconciliationHistory(
	providerID string,
	limit int,
) ([]db.LDAPGroupReconciliation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	result := make([]db.LDAPGroupReconciliation, 0)
	_, err := d.selectAll(&result,
		`select * from ldap_group_reconciliation where provider_id=? order by id desc limit ?`,
		providerID, limit)
	return result, err
}

func (d *SqlDb) getLDAPGroupMapping(providerID string, mappingID string) (db.LDAPGroupMapping, error) {
	var mapping db.LDAPGroupMapping
	err := d.selectOne(&mapping,
		"select * from ldap_group_mapping where provider_id=? and id=?", providerID, mappingID)
	return mapping, err
}

func ensureLDAPGroupMappingStateTx(tx *gorp.Transaction, d *SqlDb, providerID string) error {
	var revision int64
	err := tx.SelectOne(&revision, d.PrepareQuery(
		"select revision from ldap_group_mapping_state where provider_id=?"), providerID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	providerCount, err := tx.SelectInt(d.PrepareQuery(
		"select count(1) from ldap_provider where id=?"), providerID)
	if err != nil {
		return err
	}
	if providerCount != 1 {
		return db.ErrNotFound
	}
	_, err = tx.Exec(d.PrepareQuery(
		"insert into ldap_group_mapping_state (provider_id, revision) values (?, 1)"), providerID)
	return err
}

func lockLDAPGroupMappingStateTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	expectedRevision int,
) error {
	result, err := tx.Exec(d.PrepareQuery(
		"update ldap_group_mapping_state set revision=revision where provider_id=? and revision=?"),
		providerID, expectedRevision)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return db.ErrLDAPGroupPreviewStale
	}
	return nil
}

func requireLDAPGroupMappingRoleTx(tx *gorp.Transaction, d *SqlDb, mapping db.LDAPGroupMapping) error {
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

func insertLDAPGroupReconciliationTx(
	tx *gorp.Transaction,
	d *SqlDb,
	reconciliation *db.LDAPGroupReconciliation,
) error {
	query := `insert into ldap_group_reconciliation
	 (provider_id, source, status, token, mapping_revision, directory_revision, preview_json,
	  addition_count, removal_count, unresolved_count, collision_count, protected_admin_count,
	  error_code, actor_id, created, applied_at)
	 values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
		reconciliation.ProviderID, reconciliation.Source, reconciliation.Status, reconciliation.Token,
		reconciliation.MappingRevision, reconciliation.DirectoryRevision, reconciliation.PreviewJSON,
		reconciliation.AdditionCount, reconciliation.RemovalCount, reconciliation.UnresolvedCount,
		reconciliation.CollisionCount, reconciliation.ProtectedAdminCount, reconciliation.ErrorCode,
		reconciliation.ActorID, reconciliation.Created, reconciliation.AppliedAt,
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

func removeLDAPManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.LDAPGroupAssignmentChange,
) error {
	var ledger struct {
		ID                 int  `db:"id"`
		GlobalAssignmentID *int `db:"global_assignment_id"`
	}
	err := tx.SelectOne(&ledger, d.PrepareQuery(
		`select id, global_assignment_id from ldap_group_managed_assignment
		 where provider_id=? and mapping_id=? and user_id=? and target_scope=?
		   and role_id=? and ((project_id is null and ? is null) or project_id=?)`),
		providerID, change.MappingID, change.UserID, change.TargetScope, change.RoleID,
		change.ProjectID, change.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrLDAPGroupMappingCollision
	}
	if err != nil {
		return err
	}
	switch change.TargetScope {
	case "global":
		if ledger.GlobalAssignmentID == nil {
			return db.ErrLDAPGroupMappingCollision
		}
		var assignment db.GlobalRoleAssignment
		err = tx.SelectOne(&assignment, d.PrepareQuery(
			`select a.id, a.user_id, a.role_id, a.revision, r.global_permissions
			 from user__global_role a join role r on r.role_id=a.role_id and r.project_id is null
			 where a.id=? and a.user_id=? and a.role_id=?`),
			*ledger.GlobalAssignmentID, change.UserID, change.RoleID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrLDAPGroupMappingCollision
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
			return db.ErrLDAPGroupMappingCollision
		}
		var assignment db.ProjectUser
		err = tx.SelectOne(&assignment, d.PrepareQuery(
			`select * from project__user where project_id=? and user_id=? and role_id=?
			 and ldap_group_managed_assignment_id=?`),
			*change.ProjectID, change.UserID, change.RoleID, ledger.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.ErrLDAPGroupMappingCollision
		}
		if err != nil {
			return err
		}
		permissions, permissionErr := projectRoleAssignmentPermissionsTx(tx, d, assignment)
		if permissionErr != nil {
			return permissionErr
		}
		if permissions.Can(db.CanManageProjectUsers) {
			if err = requireAnotherProjectAdministratorTx(tx, d, *change.ProjectID, change.UserID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(d.PrepareQuery(
			`delete from project__user where project_id=? and user_id=? and role_id=?
			 and ldap_group_managed_assignment_id=?`),
			*change.ProjectID, change.UserID, change.RoleID, ledger.ID); err == nil {
			_, err = tx.Exec(d.PrepareQuery(
				"delete from ldap_group_managed_assignment where id=?"), ledger.ID)
		}
	default:
		return db.ErrLDAPGroupMappingCollision
	}
	return err
}

func addLDAPManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.LDAPGroupAssignmentChange,
	created time.Time,
) error {
	var globalAssignmentID *int
	var projectMembershipAssignmentID *int
	switch change.TargetScope {
	case "global":
		count, err := tx.SelectInt(d.PrepareQuery(
			"select count(1) from user__global_role where user_id=? and role_id=?"),
			change.UserID, change.RoleID)
		if err != nil {
			return err
		}
		if count != 0 {
			return db.ErrLDAPGroupMappingCollision
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
		mapping := db.LDAPGroupMapping{TargetScope: "project", ProjectID: change.ProjectID, RoleID: change.RoleID}
		if err := requireLDAPGroupMappingRoleTx(tx, d, mapping); err != nil {
			return err
		}
		count, err := tx.SelectInt(d.PrepareQuery(
			"select count(1) from project__user where project_id=? and user_id=?"),
			*change.ProjectID, change.UserID)
		if err != nil {
			return err
		}
		if count != 0 {
			return db.ErrLDAPGroupMappingCollision
		}
		ledgerID, insertErr := insertLDAPManagedAssignmentTx(tx, d, providerID, change, nil, created)
		if insertErr != nil {
			return insertErr
		}
		projectMembershipAssignmentID = &ledgerID
		_, err = tx.Exec(d.PrepareQuery(
			`insert into project__user
			 (project_id, user_id, role, role_id, revision, ldap_group_managed_assignment_id)
			 values (?, ?, '', ?, 1, ?)`),
			*change.ProjectID, change.UserID, change.RoleID, ledgerID)
		if err != nil {
			return err
		}
	default:
		return db.ErrNotFound
	}
	if projectMembershipAssignmentID != nil {
		return nil
	}
	_, err := insertLDAPManagedAssignmentTx(tx, d, providerID, change, globalAssignmentID, created)
	return err
}

func insertLDAPManagedAssignmentTx(
	tx *gorp.Transaction,
	d *SqlDb,
	providerID string,
	change db.LDAPGroupAssignmentChange,
	globalAssignmentID *int,
	created time.Time,
) (int, error) {
	query := d.PrepareQuery(
		`insert into ldap_group_managed_assignment
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

var _ db.LDAPRepository = (*SqlDb)(nil)
