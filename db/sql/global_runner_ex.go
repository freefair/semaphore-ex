package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"time"
)

func (d *SqlDb) UpdateRunnerSecurity(runner db.Runner) error {
	_, err := d.exec(
		"update `runner` set `security_compliant`=?, `security_reason`=?, `security_remediation`=?, `transport_trust`=?, `security_protocol_version`=?, `security_checked_at`=? where id=?",
		runner.SecurityCompliant,
		runner.SecurityReason,
		runner.SecurityRemediation,
		runner.TransportTrust,
		runner.SecurityProtocolVersion,
		runner.SecurityCheckedAt,
		runner.ID,
	)
	return err
}

func (d *SqlDb) ResetProjectRunnerRegistration(
	runnerID int,
	projectID int,
	registrationTokenHash string,
	expiresAt time.Time,
) error {
	query := "update `runner` set `token`='', `active`=false, `public_key`=null, `registration_kind`='one_time', `security_compliant`=case when `registration_policy`='standard' then true else false end, `security_reason`='registration required', `security_remediation`='Register with the new one-time token.', `transport_trust`='plaintext', `security_protocol_version`=0, `security_checked_at`=null, `registration_token`=?, `registration_token_expires_at`=? " +
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
