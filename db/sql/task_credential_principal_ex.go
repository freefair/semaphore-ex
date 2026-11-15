package sql

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

var _ db.TaskCredentialPrincipalRepository = (*SqlDb)(nil)

func (d *SqlDb) SetScheduleCredentialPrincipal(scheduleID int, userID int) error {
	return d.setCredentialPrincipal("project__schedule_credential_principal", "schedule_id", scheduleID, userID)
}

func (d *SqlDb) SetIntegrationCredentialPrincipal(integrationID int, userID int) error {
	return d.setCredentialPrincipal("project__integration_credential_principal", "integration_id", integrationID, userID)
}

// setCredentialPrincipal fails closed: the previous principal is removed on
// its own before the new one is written, so a failed write leaves the trigger
// without principal (its unattended use blocked) rather than with the
// previous user still answering for a change someone else made.
func (d *SqlDb) setCredentialPrincipal(table, column string, triggerID int, userID int) error {
	if triggerID <= 0 || userID <= 0 {
		return db.ErrInvalidOperation
	}
	if _, err := d.exec("delete from "+table+" where "+column+"=?", triggerID); err != nil {
		return err
	}
	_, err := d.exec("insert into "+table+" ("+column+", user_id) values (?, ?)", triggerID, userID)
	return err
}

func (d *SqlDb) ClearScheduleCredentialPrincipal(scheduleID int) error {
	_, err := d.exec("delete from project__schedule_credential_principal where schedule_id=?", scheduleID)
	return err
}

func (d *SqlDb) ClearIntegrationCredentialPrincipal(integrationID int) error {
	_, err := d.exec("delete from project__integration_credential_principal where integration_id=?", integrationID)
	return err
}

func (d *SqlDb) GetTaskCredentialPrincipal(task db.Task) (int, bool, error) {
	var table, column string
	var triggerID int
	switch {
	case task.ScheduleID != nil:
		table, column, triggerID = "project__schedule_credential_principal", "schedule_id", *task.ScheduleID
	case task.IntegrationID != nil:
		table, column, triggerID = "project__integration_credential_principal", "integration_id", *task.IntegrationID
	default:
		return 0, false, nil
	}
	var userID int
	err := d.selectOne(&userID, "select user_id from "+table+" where "+column+"=?", triggerID)
	if errors.Is(err, db.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return userID, true, nil
}
