package sql

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

var _ db.TaskCredentialPrincipalRepository = (*SqlDb)(nil)

// credentialPrincipalClaimLifetime bounds how long a crashed request can
// keep a trigger claimed; the trigger has no principal meanwhile.
const credentialPrincipalClaimLifetime = time.Minute

// errLostCredentialPrincipalClaim reports a change whose claim another
// change took over; the trigger is left without principal.
const errLostCredentialPrincipalClaim = credentialPrincipalError("the credential principal claim was lost")

type credentialPrincipalError string

func (e credentialPrincipalError) Error() string { return string(e) }

func credentialPrincipalTable(kind db.CredentialPrincipalKind) (string, string, error) {
	switch kind {
	case db.CredentialPrincipalSchedule:
		return "project__schedule_credential_principal", "schedule_id", nil
	case db.CredentialPrincipalIntegration:
		return "project__integration_credential_principal", "integration_id", nil
	default:
		return "", "", db.ErrInvalidOperation
	}
}

func (d *SqlDb) SetScheduleCredentialPrincipal(scheduleID int, userID int) error {
	return d.setCredentialPrincipal(db.CredentialPrincipalSchedule, scheduleID, userID)
}

func (d *SqlDb) SetIntegrationCredentialPrincipal(integrationID int, userID int) error {
	return d.setCredentialPrincipal(db.CredentialPrincipalIntegration, integrationID, userID)
}

// setCredentialPrincipal records the principal of a new trigger. It fails
// closed: the previous row is removed on its own before the new one is
// written, so a failed write leaves the trigger without principal.
func (d *SqlDb) setCredentialPrincipal(kind db.CredentialPrincipalKind, triggerID int, userID int) error {
	table, column, err := credentialPrincipalTable(kind)
	if err != nil || triggerID <= 0 || userID <= 0 {
		return db.ErrInvalidOperation
	}
	if _, err = d.exec("delete from "+table+" where "+column+"=?", triggerID); err != nil {
		return err
	}
	_, err = d.exec("insert into "+table+" ("+column+", user_id) values (?, ?)", triggerID, userID)
	return err
}

// BeginCredentialPrincipalChange claims a trigger for one change. While the
// claim holds, the trigger has no principal, so no node can dispatch with
// the previous one. A second change during a fresh claim is refused, which
// serializes changes across nodes.
func (d *SqlDb) BeginCredentialPrincipalChange(kind db.CredentialPrincipalKind, triggerID int) (string, bool, error) {
	table, column, err := credentialPrincipalTable(kind)
	if err != nil || triggerID <= 0 {
		return "", false, db.ErrInvalidOperation
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return "", false, err
	}
	token := hex.EncodeToString(raw)
	now := tz.Now()
	result, err := d.exec(
		"update "+table+" set user_id=NULL, pending_token=?, pending_since=? where "+column+"=? and (pending_token is null or pending_since < ?)",
		token, now, triggerID, now.Add(-credentialPrincipalClaimLifetime))
	if err != nil {
		return "", false, err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr == nil && affected == 1 {
		return token, true, nil
	}
	var existing int
	if err = d.selectOne(&existing, "select count(*) from "+table+" where "+column+"=?", triggerID); err != nil {
		return "", false, err
	}
	if existing > 0 {
		return "", false, nil
	}
	if _, err = d.exec("insert into "+table+" ("+column+", user_id, pending_token, pending_since) values (?, NULL, ?, ?)",
		triggerID, token, now); err != nil {
		// A concurrent change inserted its claim first.
		return "", false, nil
	}
	return token, true, nil
}

// FinishCredentialPrincipalChange makes userID the principal if the claim
// is still this change's. A lost claim means another change took the
// trigger over while this one ran, so neither can tell whose configuration
// was written last: the trigger is invalidated instead.
func (d *SqlDb) FinishCredentialPrincipalChange(kind db.CredentialPrincipalKind, triggerID int, token string, userID int) error {
	table, column, err := credentialPrincipalTable(kind)
	if err != nil || token == "" || userID <= 0 {
		return db.ErrInvalidOperation
	}
	result, err := d.exec(
		"update "+table+" set user_id=?, pending_token=NULL, pending_since=NULL where "+column+"=? and pending_token=?",
		userID, triggerID, token)
	if err != nil {
		return err
	}
	if requireOneRow(result, errLostCredentialPrincipalClaim) == nil {
		return nil
	}
	if err = d.invalidateCredentialPrincipal(table, column, triggerID); err != nil {
		return err
	}
	return errLostCredentialPrincipalClaim
}

// AbortCredentialPrincipalChange releases a claim without a principal. A
// lost claim invalidates the trigger, as in FinishCredentialPrincipalChange.
func (d *SqlDb) AbortCredentialPrincipalChange(kind db.CredentialPrincipalKind, triggerID int, token string) error {
	table, column, err := credentialPrincipalTable(kind)
	if err != nil || token == "" {
		return db.ErrInvalidOperation
	}
	result, err := d.exec("update "+table+" set pending_token=NULL, pending_since=NULL where "+column+"=? and pending_token=?", triggerID, token)
	if err != nil {
		return err
	}
	if requireOneRow(result, errLostCredentialPrincipalClaim) == nil {
		return nil
	}
	return d.invalidateCredentialPrincipal(table, column, triggerID)
}

// invalidateCredentialPrincipal removes the principal and replaces any claim
// with a fresh token nobody holds, so a change that took the trigger over
// cannot finish either. The trigger has no principal until a later change
// completes without interference.
func (d *SqlDb) invalidateCredentialPrincipal(table string, column string, triggerID int) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	_, err := d.exec("update "+table+" set user_id=NULL, pending_token=?, pending_since=? where "+column+"=?",
		hex.EncodeToString(raw), tz.Now(), triggerID)
	return err
}

func (d *SqlDb) GetTaskCredentialPrincipal(task db.Task) (int, bool, error) {
	kind := db.CredentialPrincipalKind("")
	triggerID := 0
	switch {
	case task.ScheduleID != nil:
		kind, triggerID = db.CredentialPrincipalSchedule, *task.ScheduleID
	case task.IntegrationID != nil:
		kind, triggerID = db.CredentialPrincipalIntegration, *task.IntegrationID
	default:
		return 0, false, nil
	}
	table, column, err := credentialPrincipalTable(kind)
	if err != nil {
		return 0, false, err
	}
	var rows []struct {
		UserID *int `db:"user_id"`
	}
	if _, err = d.selectAll(&rows, "select user_id from "+table+" where "+column+"=? and pending_token is null", triggerID); err != nil {
		return 0, false, err
	}
	if len(rows) != 1 || rows[0].UserID == nil {
		return 0, false, nil
	}
	return *rows[0].UserID, true, nil
}
