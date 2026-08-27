package sql

import (
	"time"
)

func (d *SqlDb) ResetProjectRunnerRegistration(
	runnerID int,
	projectID int,
	registrationTokenHash string,
	expiresAt time.Time,
) error {
	query := "update `runner` set `token`='', `active`=false, `public_key`=null, `registration_token`=?, `registration_token_expires_at`=? " +
		"where id=? and project_id=? and not exists " +
		"(select 1 from task where task.runner_id=runner.id and task.status in (?,?,?,?,?,?,?))"
	args := []any{registrationTokenHash, expiresAt, runnerID, projectID}
	args = append(args, unfinishedRunnerStatusArgs()...)
	result, err := d.exec(query, args...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	if _, err = d.GetRunner(projectID, runnerID); err != nil {
		return err
	}
	return d.runnerLifecycleConflict(projectID, runnerID)
}
