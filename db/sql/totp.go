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

func (d *SqlDb) GetTOTP(userID int) (enrollment db.UserTotp, err error) {
	err = d.selectOne(&enrollment, "select * from user__totp where user_id=?", userID)
	return
}

func (d *SqlDb) GetLegacyTOTPs() (enrollments []db.UserTotp, err error) {
	enrollments = make([]db.UserTotp, 0)
	_, err = d.selectAll(&enrollments,
		"select * from user__totp where encrypted_secret='' and url<>''")
	return
}

func (d *SqlDb) UpdateLegacyTOTPSecret(
	userID int,
	totpID int,
	encryptedSecret string,
	migratedAt time.Time,
) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var enrollment db.UserTotp
	if err = tx.SelectOne(&enrollment, d.PrepareQuery(
		"select * from user__totp where user_id=? and id=? and encrypted_secret=''"), userID, totpID); err != nil {
		return err
	}
	if enrollment.RecoveryHash != "" {
		if _, err = tx.Exec(d.PrepareQuery(
			"insert into user__totp_recovery_code (totp_id, code_hash, created, consumed_at) values (?, ?, ?, null)"),
			totpID, enrollment.RecoveryHash, migratedAt,
		); err != nil {
			return err
		}
	}
	result, err := tx.Exec(d.PrepareQuery(
		"update user__totp set encrypted_secret=?, url='', recovery_hash='', confirmed_at=?, recovery_acknowledged_at=? where user_id=? and id=? and encrypted_secret=''"),
		encryptedSecret, migratedAt, migratedAt, userID, totpID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("legacy TOTP enrollment changed concurrently")
	}
	return tx.Commit()
}

func (d *SqlDb) CreateTOTPEnrollment(
	enrollment db.UserTotp,
	recoveryCodeHashes []string,
) (db.UserTotp, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.UserTotp{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing db.UserTotp
	err = tx.SelectOne(&existing, d.PrepareQuery("select * from user__totp where user_id=?"), enrollment.UserID)
	if err == nil && existing.State == "active" {
		return db.UserTotp{}, fmt.Errorf("active TOTP enrollment already exists")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return db.UserTotp{}, err
	}
	if err == nil {
		if _, err = tx.Exec(d.PrepareQuery("delete from user__totp where user_id=?"), enrollment.UserID); err != nil {
			return db.UserTotp{}, err
		}
	}

	query := `insert into user__totp
		(user_id, url, recovery_hash, encrypted_secret, state, confirmed_at,
		 recovery_acknowledged_at, expires_at, last_used_step, created)
		values (?, '', '', ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
		enrollment.UserID, enrollment.EncryptedSecret, enrollment.State,
		enrollment.ConfirmedAt, enrollment.RecoveryAcknowledgedAt,
		enrollment.ExpiresAt, enrollment.LastUsedStep, enrollment.Created,
	}
	if d.GetDialect() == util.DbDriverPostgres {
		var resultID int64
		resultID, err = tx.SelectInt(d.PrepareQuery(query+" returning id"), args...)
		enrollment.ID = int(resultID)
	} else {
		var resultID int64
		result, execErr := tx.Exec(d.PrepareQuery(query), args...)
		if execErr == nil {
			resultID, execErr = result.LastInsertId()
		}
		err = execErr
		enrollment.ID = int(resultID)
	}
	if err != nil {
		return db.UserTotp{}, err
	}

	for _, codeHash := range recoveryCodeHashes {
		if _, err = tx.Exec(d.PrepareQuery(
			"insert into user__totp_recovery_code (totp_id, code_hash, created, consumed_at) values (?, ?, ?, null)"),
			enrollment.ID, codeHash, enrollment.Created,
		); err != nil {
			return db.UserTotp{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return db.UserTotp{}, err
	}
	return enrollment, nil
}

func (d *SqlDb) ConfirmTOTPEnrollment(
	userID int,
	totpID int,
	step int64,
	confirmedAt time.Time,
) (bool, error) {
	result, err := d.exec(
		`update user__totp set state='pending_recovery_ack', confirmed_at=?, last_used_step=?
		 where user_id=? and id=? and state='pending_confirmation' and expires_at>?`,
		confirmedAt, step, userID, totpID, confirmedAt,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) ActivateTOTPEnrollmentAndRevokeSessions(
	userID int,
	totpID int,
	currentSessionID int,
	acknowledgedAt time.Time,
) (bool, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(d.PrepareQuery(
		`update user__totp set state='active', recovery_acknowledged_at=?, expires_at=null
		 where user_id=? and id=? and state='pending_recovery_ack'`),
		acknowledgedAt, userID, totpID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update session set expired=true where user_id=? and id<>? and expired=false"),
		userID, currentSessionID,
	); err != nil {
		return false, err
	}
	result, err = tx.Exec(d.PrepareQuery(
		"update session set verified=true where id=? and user_id=? and expired=false"),
		currentSessionID, userID,
	)
	if err != nil {
		return false, err
	}
	rows, err = result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from user__totp_attempt where user_id=?"), userID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (d *SqlDb) ResetTOTPEnrollmentAndRevokeSessions(userID int, totpID int) error {
	return d.resetTOTPEnrollmentAndRevokeSessions(userID, totpID, true)
}

// ForceResetTOTPEnrollmentAndRevokeSessions is the host-local recovery path.
// It bypasses only the web policy readiness guard; enrollment removal and
// session revocation remain one transaction.
func (d *SqlDb) ForceResetTOTPEnrollmentAndRevokeSessions(userID int, totpID int) error {
	return d.resetTOTPEnrollmentAndRevokeSessions(userID, totpID, false)
}

func (d *SqlDb) resetTOTPEnrollmentAndRevokeSessions(
	userID int,
	totpID int,
	protectLastAdmin bool,
) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	configuration, err := d.lockTOTPConfigurationTx(tx)
	if err != nil {
		return err
	}
	protect := protectLastAdmin && configuration.State == "required"
	if protectLastAdmin && configuration.State == "required_selected" {
		selected, selectedErr := tx.SelectInt(d.PrepareQuery(
			"select count(1) from totp_capability_selected_user where user_id=?"), userID)
		if selectedErr != nil {
			return selectedErr
		}
		protect = selected == 1
	}
	if protect {
		targetIsActiveAdmin, readinessErr := tx.SelectInt(d.PrepareQuery(
			"select count(1) from `user` u join user__totp t on t.user_id=u.id "+
				"where u.id=? and u.admin=true and t.id=? and t.state='active'"),
			userID, totpID,
		)
		if readinessErr != nil {
			return readinessErr
		}
		if targetIsActiveAdmin == 1 {
			recoverable, readinessErr := d.countRecoverableTOTPAdminsTx(tx, userID)
			if readinessErr != nil {
				return readinessErr
			}
			if recoverable == 0 {
				return db.ErrTOTPReadiness
			}
		}
	}

	result, err := tx.Exec(d.PrepareQuery(
		"delete from user__totp where user_id=? and id=?"), userID, totpID)
	if err = validateMutationResult(result, err); err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"update session set expired=true where user_id=? and expired=false"), userID); err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from user__totp_attempt where user_id=?"), userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SqlDb) GetUnusedTOTPRecoveryCodes(
	userID int,
	totpID int,
) (codes []db.TOTPRecoveryCode, err error) {
	codes = make([]db.TOTPRecoveryCode, 0)
	_, err = d.selectAll(&codes,
		`select c.* from user__totp_recovery_code c
		 join user__totp t on t.id=c.totp_id
		 where t.user_id=? and t.id=? and c.consumed_at is null order by c.id`,
		userID, totpID,
	)
	return
}

func (d *SqlDb) ConsumeTOTPRecoveryCode(
	userID int,
	totpID int,
	recoveryCodeID int,
	consumedAt time.Time,
) (bool, error) {
	result, err := d.exec(
		`update user__totp_recovery_code set consumed_at=?
		 where id=? and totp_id=? and consumed_at is null
		 and exists (select 1 from user__totp where id=? and user_id=? and state='active')`,
		consumedAt, recoveryCodeID, totpID, totpID, userID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) ConsumeTOTPStep(userID int, totpID int, step int64) (bool, error) {
	result, err := d.exec(
		`update user__totp set last_used_step=?
		 where user_id=? and id=? and state='active'
		 and (last_used_step is null or last_used_step<?)`,
		step, userID, totpID, step,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (d *SqlDb) GetTOTPAttempt(userID int) (attempt db.TOTPAttempt, err error) {
	err = d.selectOne(&attempt, "select * from user__totp_attempt where user_id=?", userID)
	return
}

func (d *SqlDb) RecordTOTPFailure(
	userID int,
	now time.Time,
	window time.Duration,
	maxFailures int,
	blockFor time.Duration,
) (db.TOTPAttempt, error) {
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.TOTPAttempt{}, err
	}
	defer func() { _ = tx.Rollback() }()

	query := "select * from user__totp_attempt where user_id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var attempt db.TOTPAttempt
	err = tx.SelectOne(&attempt, d.PrepareQuery(query), userID)
	if errors.Is(err, sql.ErrNoRows) {
		attempt = db.TOTPAttempt{UserID: userID, FailureCount: 1, WindowStarted: now, Updated: now}
		if maxFailures <= 1 {
			blockedUntil := now.Add(blockFor)
			attempt.BlockedUntil = &blockedUntil
		}
		_, err = tx.Exec(d.PrepareQuery(
			`insert into user__totp_attempt
			 (user_id, failure_count, window_started, blocked_until, updated) values (?, ?, ?, ?, ?)`),
			attempt.UserID, attempt.FailureCount, attempt.WindowStarted, attempt.BlockedUntil, attempt.Updated,
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
			`update user__totp_attempt set failure_count=?, window_started=?, blocked_until=?, updated=?
			 where user_id=?`),
			attempt.FailureCount, attempt.WindowStarted, attempt.BlockedUntil, attempt.Updated, userID,
		)
	}
	if err != nil {
		return db.TOTPAttempt{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.TOTPAttempt{}, err
	}
	return attempt, nil
}

func (d *SqlDb) ClearTOTPFailures(userID int) error {
	_, err := d.exec("delete from user__totp_attempt where user_id=?", userID)
	return err
}

func (d *SqlDb) IsTOTPUserSelected(userID int) (bool, error) {
	count, err := d.Sql().SelectInt(d.PrepareQuery(
		"select count(1) from totp_capability_selected_user where user_id=?"), userID)
	return count == 1, err
}

func (d *SqlDb) GetTOTPSelectedUsers() ([]int, error) {
	var rows []struct {
		UserID int `db:"user_id"`
	}
	_, err := d.selectAll(&rows,
		"select user_id from totp_capability_selected_user order by user_id")
	if err != nil {
		return nil, err
	}
	result := make([]int, len(rows))
	for index, row := range rows {
		result[index] = row.UserID
	}
	return result, nil
}

func (d *SqlDb) ConfigureTOTP(
	state string,
	selectedUserIDs []int,
	actorID int,
	changedAt time.Time,
) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	config, err := d.lockTOTPConfigurationTx(tx)
	if err != nil {
		return err
	}
	fromState := config.State
	if state == "required" {
		externalUsers, readinessErr := tx.SelectInt(d.PrepareQuery(
			"select count(1) from `user` where external=true"))
		if readinessErr != nil {
			return readinessErr
		}
		recoverable, readinessErr := d.countRecoverableTOTPAdminsTx(tx, 0)
		if readinessErr != nil {
			return readinessErr
		}
		if externalUsers != 0 || recoverable == 0 {
			return db.ErrTOTPReadiness
		}
	}

	if config.CapabilityID == "totp" {
		_, err = tx.Exec(d.PrepareQuery(
			"update capability_config set state=?, expires_at=null, updated=? where capability_id='totp'"),
			state, changedAt,
		)
	} else {
		_, err = tx.Exec(d.PrepareQuery(
			"insert into capability_config (capability_id, state, expires_at, updated) values ('totp', ?, null, ?)"),
			state, changedAt,
		)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec("delete from totp_capability_selected_user"); err != nil {
		return err
	}
	for _, userID := range selectedUserIDs {
		if _, err = tx.Exec(d.PrepareQuery(
			"insert into totp_capability_selected_user (user_id) values (?)"), userID); err != nil {
			return err
		}
	}
	if fromState != state {
		if _, err = tx.Exec(d.PrepareQuery(
			`insert into totp_capability_transition
			 (from_state, to_state, actor_id, created) values (?, ?, ?, ?)`),
			fromState, state, actorID, changedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *SqlDb) GetTOTPCapabilityTransitions() (transitions []db.TOTPCapabilityTransition, err error) {
	transitions = make([]db.TOTPCapabilityTransition, 0)
	_, err = d.selectAll(&transitions,
		"select id, from_state, to_state, actor_id, created from totp_capability_transition order by id desc")
	return
}

func (d *SqlDb) CountRecoverableTOTPAdmins(excludeUserID int) (int, error) {
	count, err := d.Sql().SelectInt(d.PrepareQuery(recoverableTOTPAdminsQuery), excludeUserID)
	return int(count), err
}

const recoverableTOTPAdminsQuery = "select count(distinct u.id) from `user` u " +
	"join user__totp t on t.user_id=u.id " +
	"where u.admin=true and u.external=false and u.id<>? " +
	"and t.state='active' and t.recovery_acknowledged_at is not null " +
	"and exists (select 1 from user__totp_recovery_code c " +
	"where c.totp_id=t.id and c.consumed_at is null)"

func (d *SqlDb) countRecoverableTOTPAdminsTx(tx *gorp.Transaction, excludeUserID int) (int, error) {
	count, err := tx.SelectInt(d.PrepareQuery(recoverableTOTPAdminsQuery), excludeUserID)
	return int(count), err
}

func (d *SqlDb) lockTOTPConfigurationTx(tx *gorp.Transaction) (db.CapabilityConfig, error) {
	if d.GetDialect() == util.DbDriverSQLite {
		if _, err := tx.Exec(
			"update capability_config set updated=updated where capability_id='totp'",
		); err != nil {
			return db.CapabilityConfig{}, err
		}
	}
	query := "select capability_id, state, expires_at, updated from capability_config where capability_id=?"
	if d.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var config db.CapabilityConfig
	err := tx.SelectOne(&config, d.PrepareQuery(query), "totp")
	if errors.Is(err, sql.ErrNoRows) {
		return db.CapabilityConfig{State: "disabled"}, nil
	}
	return config, err
}

var _ db.TOTPRepository = (*SqlDb)(nil)
