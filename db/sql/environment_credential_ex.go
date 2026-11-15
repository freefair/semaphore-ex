package sql

import (
	"sort"

	"github.com/semaphoreui/semaphore/db"
)

type environmentCredentialRow struct {
	Name         string `db:"name"`
	Target       string `db:"target"`
	CredentialID int    `db:"credential_id"`
}

// fillEnvironmentCredentials loads the value-free global credential references
// of a Variable Group, ordered by name for stable API output and snapshots.
func (d *SqlDb) fillEnvironmentCredentials(env *db.Environment) error {
	var rows []environmentCredentialRow
	if _, err := d.selectAll(&rows,
		"select name, target, credential_id from project__environment_credential where project_id=? and environment_id=?",
		env.ProjectID, env.ID); err != nil {
		return err
	}
	env.Credentials = make([]db.EnvironmentCredential, 0, len(rows))
	for _, row := range rows {
		env.Credentials = append(env.Credentials, db.EnvironmentCredential{
			Name: row.Name, Target: db.EnvironmentCredentialTarget(row.Target), CredentialID: row.CredentialID,
		})
	}
	sort.Slice(env.Credentials, func(left, right int) bool { return env.Credentials[left].Name < env.Credentials[right].Name })
	return nil
}

// saveEnvironmentCredentials replaces the references in one transaction. An
// update that omits the field keeps the stored references.
func (d *SqlDb) saveEnvironmentCredentials(env db.Environment, replace bool) error {
	if !replace {
		return nil
	}
	if err := db.ValidateEnvironmentCredentials(env.Credentials); err != nil {
		return err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(d.PrepareQuery(
		"delete from project__environment_credential where project_id=? and environment_id=?"),
		env.ProjectID, env.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, credential := range env.Credentials {
		if _, err = tx.Exec(d.PrepareQuery(
			"insert into project__environment_credential (project_id, environment_id, credential_id, name, target) values (?, ?, ?, ?, ?)"),
			env.ProjectID, env.ID, credential.CredentialID, credential.Name, string(credential.Target)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
