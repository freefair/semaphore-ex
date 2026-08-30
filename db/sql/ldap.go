package sql

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func (d *SqlDb) GetLDAPProvider(providerID string) (provider db.LDAPProvider, err error) {
	err = d.selectOne(&provider, "select * from ldap_provider where id=?", providerID)
	return
}

func (d *SqlDb) GetLDAPProviders() (providers []db.LDAPProvider, err error) {
	providers = make([]db.LDAPProvider, 0)
	_, err = d.selectAll(&providers, "select * from ldap_provider order by display_name, id")
	return
}

func (d *SqlDb) SaveLDAPProvider(provider db.LDAPProvider) error {
	_, err := d.GetLDAPProvider(provider.ID)
	switch {
	case err == nil:
		_, err = d.exec(
			`update ldap_provider set display_name=?, state=?, server_url=?, tls_mode=?, trust_mode=?,
			 ca_pem=?, bind_dn=?, encrypted_bind_password=?, search_base_dn=?, user_filter=?,
			 identity_attribute=?, username_attribute=?, name_attribute=?, email_attribute=?,
			 group_search_base_dn=?, group_user_filter=?, group_filter=?, group_identity_attribute=?,
			 group_member_attribute=?, group_max_depth=?,
			 readiness_status=?, readiness_code=?, readiness_checked_at=?, recovery_admin_user_id=?,
			 recovery_checked_at=?, config_version=config_version+1, updated=? where id=?`,
			provider.DisplayName, provider.State, provider.ServerURL, provider.TLSMode, provider.TrustMode,
			provider.CAPEM, provider.BindDN, provider.EncryptedBindPassword, provider.SearchBaseDN,
			provider.UserFilter, provider.IdentityAttribute, provider.UsernameAttribute,
			provider.NameAttribute, provider.EmailAttribute,
			provider.GroupSearchBaseDN, provider.GroupUserFilter, provider.GroupFilter,
			provider.GroupIdentityAttribute, provider.GroupMemberAttribute, provider.GroupMaxDepth,
			provider.ReadinessStatus, provider.ReadinessCode, provider.ReadinessCheckedAt, provider.RecoveryAdminUserID,
			provider.RecoveryCheckedAt, provider.Updated, provider.ID,
		)
		return err
	case errors.Is(err, db.ErrNotFound):
		_, err = d.exec(
			`insert into ldap_provider
			 (id, display_name, state, server_url, tls_mode, trust_mode, ca_pem, bind_dn,
			 encrypted_bind_password, search_base_dn, user_filter, identity_attribute,
			 username_attribute, name_attribute, email_attribute, group_search_base_dn,
			 group_user_filter, group_filter, group_identity_attribute, group_member_attribute,
			 group_max_depth, readiness_status,
			 readiness_code, readiness_checked_at, recovery_admin_user_id, recovery_checked_at,
			 config_version, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			provider.ID, provider.DisplayName, provider.State, provider.ServerURL, provider.TLSMode,
			provider.TrustMode, provider.CAPEM, provider.BindDN, provider.EncryptedBindPassword,
			provider.SearchBaseDN, provider.UserFilter, provider.IdentityAttribute,
			provider.UsernameAttribute, provider.NameAttribute, provider.EmailAttribute,
			provider.GroupSearchBaseDN, provider.GroupUserFilter, provider.GroupFilter,
			provider.GroupIdentityAttribute, provider.GroupMemberAttribute, provider.GroupMaxDepth,
			provider.ReadinessStatus, provider.ReadinessCode, provider.ReadinessCheckedAt,
			provider.RecoveryAdminUserID, provider.RecoveryCheckedAt, provider.Created, provider.Updated,
		)
		return err
	default:
		return err
	}
}

func (d *SqlDb) SaveLDAPReadiness(
	providerID string,
	status string,
	code string,
	checkedAt time.Time,
	recoveryAdminUserID *int,
	expectedConfigVersion int,
) error {
	result, err := d.exec(
		`update ldap_provider set readiness_status=?, readiness_code=?, readiness_checked_at=?,
			 recovery_admin_user_id=?, recovery_checked_at=?, updated=? where id=? and config_version=?`,
		status, code, checkedAt, recoveryAdminUserID, checkedAt, checkedAt, providerID, expectedConfigVersion,
	)
	if err != nil {
		return validateMutationResult(result, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return db.ErrLDAPReadiness
	}
	return nil
}

func (d *SqlDb) ConfigureLDAPProvider(
	providerID string,
	state string,
	selectedUserIDs []int,
	actorID int,
	changedAt time.Time,
	readinessMaxAge time.Duration,
) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	provider, err := d.lockLDAPProviderTx(tx, providerID)
	if err != nil {
		return err
	}
	if state == "selected_users" || state == "active" {
		if provider.ReadinessStatus != "ready" || provider.ReadinessCheckedAt == nil ||
			provider.RecoveryCheckedAt == nil || provider.RecoveryAdminUserID == nil ||
			provider.ReadinessCheckedAt.After(changedAt) || provider.RecoveryCheckedAt.After(changedAt) ||
			changedAt.Sub(*provider.ReadinessCheckedAt) > readinessMaxAge ||
			changedAt.Sub(*provider.RecoveryCheckedAt) > readinessMaxAge {
			return db.ErrLDAPReadiness
		}
		readyAdmins, readinessErr := tx.SelectInt(d.PrepareQuery(
			"select count(1) from `user` where id=? and admin=true and external=false"),
			*provider.RecoveryAdminUserID,
		)
		if readinessErr != nil || readyAdmins != 1 {
			return db.ErrLDAPReadiness
		}
	}
	if state == "selected_users" && len(selectedUserIDs) == 0 {
		return db.ErrLDAPReadiness
	}

	if _, err = tx.Exec(d.PrepareQuery(
		"update ldap_provider set state=?, updated=? where id=?"), state, changedAt, providerID); err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"delete from ldap_provider_selected_user where provider_id=?"), providerID); err != nil {
		return err
	}
	for _, userID := range selectedUserIDs {
		if state != "selected_users" {
			continue
		}
		linked, linkErr := tx.SelectInt(d.PrepareQuery(
			`select count(1) from user__external_identity i join `+"`user`"+` u on u.id=i.user_id
			 where i.type='ldap' and i.provider=? and u.id=?`), providerID, userID)
		if linkErr != nil || linked != 1 {
			return db.ErrLDAPReadiness
		}
		if _, err = tx.Exec(d.PrepareQuery(
			"insert into ldap_provider_selected_user (provider_id, user_id, created) values (?, ?, ?)"),
			providerID, userID, changedAt,
		); err != nil {
			return err
		}
	}
	if provider.State != state {
		if _, err = tx.Exec(d.PrepareQuery(
			`insert into ldap_capability_transition
			 (provider_id, from_state, to_state, actor_id, created) values (?, ?, ?, ?, ?)`),
			providerID, provider.State, state, actorID, changedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *SqlDb) GetLDAPSelectedUsers(providerID string) ([]int, error) {
	var rows []struct {
		UserID int `db:"user_id"`
	}
	_, err := d.selectAll(&rows,
		"select user_id from ldap_provider_selected_user where provider_id=? order by user_id", providerID)
	if err != nil {
		return nil, err
	}
	result := make([]int, len(rows))
	for index, row := range rows {
		result[index] = row.UserID
	}
	return result, nil
}

func (d *SqlDb) GetLDAPLinkedUserIDs(providerID string) ([]int, error) {
	var rows []struct {
		UserID int `db:"user_id"`
	}
	_, err := d.selectAll(&rows,
		`select distinct user_id from user__external_identity
		 where type='ldap' and provider=? order by user_id`, providerID)
	if err != nil {
		return nil, err
	}
	result := make([]int, len(rows))
	for index, row := range rows {
		result[index] = row.UserID
	}
	return result, nil
}

func (d *SqlDb) IsLDAPUserSelected(providerID string, userID int) (bool, error) {
	count, err := d.Sql().SelectInt(d.PrepareQuery(
		"select count(1) from ldap_provider_selected_user where provider_id=? and user_id=?"),
		providerID, userID,
	)
	return count == 1, err
}

func (d *SqlDb) GetLDAPCapabilityTransitions(
	providerID string,
) (transitions []db.LDAPCapabilityTransition, err error) {
	transitions = make([]db.LDAPCapabilityTransition, 0)
	_, err = d.selectAll(&transitions,
		`select id, provider_id, from_state, to_state, actor_id, created
		 from ldap_capability_transition where provider_id=? order by id desc`, providerID)
	return
}

func (d *SqlDb) GetLDAPAuthAttempt(
	providerID string,
	subjectHash string,
) (attempt db.LDAPAuthAttempt, err error) {
	err = d.selectOne(&attempt,
		"select * from ldap_auth_attempt where provider_id=? and subject_hash=?", providerID, subjectHash)
	return
}

func (d *SqlDb) RecordLDAPAuthFailure(
	providerID string,
	subjectHash string,
	now time.Time,
	window time.Duration,
	maxFailures int,
	blockFor time.Duration,
) (db.LDAPAuthAttempt, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.LDAPAuthAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()

	query := "select * from ldap_auth_attempt where provider_id=? and subject_hash=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var attempt db.LDAPAuthAttempt
	err = tx.SelectOne(&attempt, d.PrepareQuery(query), providerID, subjectHash)
	if errors.Is(err, sql.ErrNoRows) {
		attempt = db.LDAPAuthAttempt{
			ProviderID: providerID, SubjectHash: subjectHash, FailureCount: 1,
			WindowStarted: now, Updated: now,
		}
		if maxFailures <= 1 {
			blockedUntil := now.Add(blockFor)
			attempt.BlockedUntil = &blockedUntil
		}
		_, err = tx.Exec(d.PrepareQuery(
			`insert into ldap_auth_attempt
			 (provider_id, subject_hash, failure_count, window_started, blocked_until, updated)
			 values (?, ?, ?, ?, ?, ?)`),
			attempt.ProviderID, attempt.SubjectHash, attempt.FailureCount, attempt.WindowStarted,
			attempt.BlockedUntil, attempt.Updated,
		)
	} else if err == nil {
		if attempt.BlockedUntil != nil && now.Before(*attempt.BlockedUntil) {
			return attempt, tx.Commit()
		}
		if now.Sub(attempt.WindowStarted) >= window {
			attempt.FailureCount = 1
			attempt.WindowStarted = now
			attempt.BlockedUntil = nil
		} else {
			attempt.FailureCount++
		}
		if attempt.FailureCount >= maxFailures {
			blockedUntil := now.Add(blockFor)
			attempt.BlockedUntil = &blockedUntil
		}
		attempt.Updated = now
		_, err = tx.Exec(d.PrepareQuery(
			`update ldap_auth_attempt set failure_count=?, window_started=?, blocked_until=?, updated=?
			 where provider_id=? and subject_hash=?`),
			attempt.FailureCount, attempt.WindowStarted, attempt.BlockedUntil, attempt.Updated,
			providerID, subjectHash,
		)
	}
	if err != nil {
		return db.LDAPAuthAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.LDAPAuthAttempt{}, err
	}
	return attempt, nil
}

func (d *SqlDb) ClearLDAPAuthFailures(providerID string, subjectHash string) error {
	_, err := d.exec(
		"delete from ldap_auth_attempt where provider_id=? and subject_hash=?", providerID, subjectHash)
	return err
}

func (d *SqlDb) lockLDAPProviderTx(tx *gorp.Transaction, providerID string) (db.LDAPProvider, error) {
	if d.GetDialect() == util.DbDriverSQLite {
		if _, err := tx.Exec(d.PrepareQuery(
			"update ldap_provider set updated=updated where id=?"), providerID); err != nil {
			return db.LDAPProvider{}, err
		}
	}
	query := "select * from ldap_provider where id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var provider db.LDAPProvider
	err := tx.SelectOne(&provider, d.PrepareQuery(query), providerID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.LDAPProvider{}, db.ErrNotFound
	}
	if err != nil {
		return db.LDAPProvider{}, fmt.Errorf("lock LDAP provider: %w", err)
	}
	return provider, nil
}

var _ db.LDAPRepository = (*SqlDb)(nil)
