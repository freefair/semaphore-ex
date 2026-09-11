package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"time"
)

// prepareRunnerForCreate normalizes the runner configuration and records the
// registration-policy decision before the shared persistence path inserts it.
func prepareRunnerForCreate(runner db.Runner) (db.Runner, error) {
	if err := db.ValidateRunnerTags(runner.Tags); err != nil {
		return db.Runner{}, err
	}
	runner.Tags = db.NormalizeRunnerTags(runner.Tags)
	var err error
	runner.ExecutorType, err = db.NormalizeRunnerExecutorType(runner.ExecutorType)
	if err != nil {
		return db.Runner{}, err
	}
	runner.RegistrationPolicy, err = db.NormalizeRunnerRegistrationPolicy(runner.RegistrationPolicy)
	if err != nil {
		return db.Runner{}, err
	}
	if runner.TransportTrust == "" {
		runner.TransportTrust = db.RunnerTransportPlaintext
	}
	if runner.RegistrationTokenHash != nil {
		runner.RegistrationKind = db.RunnerRegistrationOneTime
	} else if runner.RegistrationKind == "" {
		runner.RegistrationKind = db.RunnerRegistrationShared
	}
	decision := db.EvaluateRunnerRegistrationPolicy(runner.RegistrationPolicy, db.RunnerSecurityReport{
		RegistrationKind: runner.RegistrationKind,
		TransportTrust:   runner.TransportTrust,
		RunnerVersion:    runner.Version,
		ProtocolVersion:  runner.SecurityProtocolVersion,
		ExecutorType:     runner.ExecutorType,
	})
	runner.SecurityCompliant = decision.Compliant
	runner.SecurityReason = decision.Reason
	runner.SecurityRemediation = decision.Remediation
	if runner.IsRegistered() && runner.RegistrationPolicy == db.RunnerRegistrationSecure {
		return db.Runner{}, db.RunnerSecurityViolationError{Decision: decision}
	}
	return runner, nil
}

// prepareRunnerForUpdate normalizes mutable runner policy data and verifies
// the change against the persisted registration policy before shared SQL runs.
func (d *SqlDb) prepareRunnerForUpdate(runner db.Runner) (db.Runner, error) {
	if err := db.ValidateRunnerTags(runner.Tags); err != nil {
		return db.Runner{}, err
	}
	runner.Tags = db.NormalizeRunnerTags(runner.Tags)
	policy, err := db.NormalizeRunnerRegistrationPolicy(runner.RegistrationPolicy)
	if err != nil {
		return db.Runner{}, err
	}
	runner.RegistrationPolicy = policy
	if runner.TransportTrust == "" {
		runner.TransportTrust = db.RunnerTransportPlaintext
	}
	var current db.Runner
	if runner.ProjectID == nil {
		current, err = d.GetGlobalRunner(runner.ID)
	} else {
		current, err = d.GetRunner(*runner.ProjectID, runner.ID)
	}
	if err != nil {
		return db.Runner{}, err
	}
	if err = db.ValidateRunnerRegistrationPolicyChange(current, runner.RegistrationPolicy); err != nil {
		return db.Runner{}, err
	}
	return runner, nil
}

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
