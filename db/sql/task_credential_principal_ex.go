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

func (d *SqlDb) setCredentialPrincipal(table, column string, triggerID int, userID int) error {
	if triggerID <= 0 || userID <= 0 {
		return db.ErrInvalidOperation
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("delete from "+table+" where "+column+"=?"), triggerID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery("insert into "+table+" ("+column+", user_id) values (?, ?)"), triggerID, userID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
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
