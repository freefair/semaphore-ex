package sql

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

func (d *SqlDb) GetRunnerByToken(token string) (runner db.Runner, err error) {

	runners := make([]db.Runner, 0)

	err = d.getObjects(0, db.GlobalRunnerProps, db.RetrieveQueryParams{}, func(builder squirrel.SelectBuilder) squirrel.SelectBuilder {
		return builder.Where("token=?", token)
	}, &runners)

	if err != nil {
		return
	}

	if len(runners) == 0 {
		err = db.ErrNotFound
		return
	}

	runner = runners[0]
	err = d.loadRunnerTagsSingle(&runner)
	return
}

func (d *SqlDb) GetGlobalRunner(runnerID int) (runner db.Runner, err error) {
	err = d.getObject(0, db.GlobalRunnerProps, runnerID, &runner)
	if err != nil {
		return
	}
	err = d.loadRunnerTagsSingle(&runner)
	return
}

func (d *SqlDb) GetAllRunners(activeAndRegisteredOnly bool, globalOnly bool, tagFilterMode db.RunnerTagFilterMode, tag *string) (runners []db.Runner, err error) {
	if tag == nil && tagFilterMode == db.RunnerFilterTagCompleteMatch {
		err = fmt.Errorf("tag filter mode is complete match but no tag was provided")
		return
	}

	err = d.getObjects(0, db.GlobalRunnerProps, db.RetrieveQueryParams{}, func(builder squirrel.SelectBuilder) squirrel.SelectBuilder {

		if globalOnly {
			builder = builder.Where("project_id is null")
		}

		if activeAndRegisteredOnly {
			builder = builder.Where("active=true and token != ''")
		}

		switch tagFilterMode {
		case db.RunnerFilterHasAnyTag:
			builder = builder.Where(runnerHasAnyTagExpr())
		case db.RunnerFilterIsDefault:
			builder = builder.Where(runnerIsDefaultExpr())
		case db.RunnerFilterIgnoreTags:
			// No tag filtering applied.
		case db.RunnerFilterTagCompleteMatch:
			builder = builder.Where(runnerHasTagExpr(*tag))
		default:
			panic("invalid tag filter mode: " + tagFilterMode)
		}

		return builder
	}, &runners)
	if err != nil {
		return
	}
	err = d.loadRunnerTags(runners)
	return
}

func (d *SqlDb) GetGlobalRunnerTags() (res []db.RunnerTag, err error) {
	query, args, err := squirrel.Select("lower(trim(rt.tag)) as tag", "count(distinct rt.runner_id) as cnt").
		From("runner__tag rt").
		Join("runner r on r.id = rt.runner_id").
		Where("r.project_id is null").
		GroupBy("lower(trim(rt.tag))").
		OrderBy("tag").
		ToSql()

	if err != nil {
		return
	}

	type row struct {
		Tag string `db:"tag"`
		Cnt int    `db:"cnt"`
	}

	rows := make([]row, 0)
	_, err = d.selectAll(&rows, query, args...)
	if err != nil {
		return
	}

	res = make([]db.RunnerTag, 0, len(rows))
	for _, r := range rows {
		res = append(res, db.RunnerTag{
			Tag:             r.Tag,
			NumberOfRunners: r.Cnt,
		})
	}

	return
}

func (d *SqlDb) DeleteGlobalRunner(runnerID int) (err error) {
	err = d.deleteObject(0, db.GlobalRunnerProps, runnerID)
	return
}

func (d *SqlDb) ClearRunnerCache(runner db.Runner) (err error) {
	if runner.ProjectID == nil {
		_, err = d.exec(
			"update `runner` set `cleaning_requested`=? where id=?",
			tz.Now(),
			runner.ID)
		return
	}

	_, err = d.exec(
		"update `runner` set `cleaning_requested`=? where id=? and project_id=?",
		tz.Now(),
		runner.ID,
		runner.ProjectID)

	return
}

func (d *SqlDb) TouchRunner(runner db.Runner) (err error) {
	touchedAt := tz.Now()
	if runner.IsCacheClearPending() {
		// MySQL/MariaDB DATETIME columns can round both the request and the
		// preceding heartbeat to the same second. Persist the acknowledgement
		// strictly after the request so a repeated poll does not clear twice.
		acknowledgedAt := runner.CleaningRequested.Add(time.Second)
		if touchedAt.Before(acknowledgedAt) {
			touchedAt = acknowledgedAt
		}
	}
	if runner.ProjectID == nil {
		_, err = d.exec(
			"update `runner` set `touched`=?, `started_at`=?, `version`=?, `platform`=?, `current_load`=? where id=?",
			touchedAt,
			runner.StartedAt,
			runner.Version,
			runner.Platform,
			runner.CurrentLoad,
			runner.ID)
		return
	}

	_, err = d.exec(
		"update `runner` set `touched`=?, `started_at`=?, `version`=?, `platform`=?, `current_load`=? where id=? and project_id=?",
		touchedAt,
		runner.StartedAt,
		runner.Version,
		runner.Platform,
		runner.CurrentLoad,
		runner.ID,
		runner.ProjectID)

	return
}

func (d *SqlDb) UpdateRunner(runner db.Runner) (err error) {
	if err = db.ValidateRunnerTags(runner.Tags); err != nil {
		return
	}
	runner.Tags = db.NormalizeRunnerTags(runner.Tags)

	_, err = d.exec(
		"update `runner` set `name`=?, `active`=?, `is_default`=?, webhook=?, max_parallel_tasks=? where id=?",
		runner.Name,
		runner.Active,
		runner.IsDefault,
		runner.Webhook,
		runner.MaxParallelTasks,
		runner.ID)

	if err != nil {
		return
	}

	err = d.replaceRunnerTags(runner.ID, runner.Tags)
	return
}

func (d *SqlDb) RegisterRunner(registrationTokenHash string, publicKey *string) (runner db.Runner, err error) {
	runners := make([]db.Runner, 0)

	err = d.getObjects(0, db.GlobalRunnerProps, db.RetrieveQueryParams{}, func(builder squirrel.SelectBuilder) squirrel.SelectBuilder {
		return builder.Where("registration_token=?", registrationTokenHash)
	}, &runners)

	if err != nil {
		return
	}

	if len(runners) == 0 {
		err = db.ErrNotFound
		return
	}

	runner = runners[0]

	if runner.IsRegistered() {
		err = fmt.Errorf("runner is already registered")
		return
	}

	if runner.RegistrationTokenExpiresAt == nil || !runner.RegistrationTokenExpiresAt.After(tz.Now()) {
		err = fmt.Errorf("registration token expired")
		return
	}

	token := db.GenerateRunnerToken()

	var result sql.Result
	result, err = d.exec(
		"update `runner` set `token`=?, `active`=?, `public_key`=?, `registration_token`=null, `registration_token_expires_at`=null "+
			"where id=? and `token`='' and `registration_token`=? and `registration_token_expires_at` > CURRENT_TIMESTAMP",
		token,
		true,
		publicKey,
		runner.ID,
		registrationTokenHash)

	if err != nil {
		return
	}
	var updated int64
	updated, err = result.RowsAffected()
	if err != nil {
		return
	}
	if updated != 1 {
		err = db.ErrNotFound
		return
	}

	runner.Token = token
	runner.Active = true
	runner.PublicKey = publicKey
	runner.RegistrationTokenHash = nil
	runner.RegistrationTokenExpiresAt = nil

	err = d.loadRunnerTagsSingle(&runner)
	return
}

func (d *SqlDb) ResetRunnerRegistration(runnerID int, registrationTokenHash string, expiresAt time.Time) (err error) {
	_, err = d.exec(
		"update `runner` set `token`='', `active`=false, `public_key`=null, `registration_token`=?, `registration_token_expires_at`=? where id=?",
		registrationTokenHash,
		expiresAt,
		runnerID)
	return
}

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

func (d *SqlDb) CreateRunner(runner db.Runner) (newRunner db.Runner, err error) {
	if err = db.ValidateRunnerTags(runner.Tags); err != nil {
		return
	}
	runner.Tags = db.NormalizeRunnerTags(runner.Tags)

	insertID, err := d.insert(
		"id",
		"insert into `runner` (project_id, token, webhook, max_parallel_tasks, `name`, `active`, `is_default`, public_key, registration_token, registration_token_expires_at, `version`, `platform`, current_load) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		runner.ProjectID,
		runner.Token,
		runner.Webhook,
		runner.MaxParallelTasks,
		runner.Name,
		runner.Active,
		runner.IsDefault,
		runner.PublicKey,
		runner.RegistrationTokenHash,
		runner.RegistrationTokenExpiresAt,
		runner.Version,
		runner.Platform,
		runner.CurrentLoad)

	if err != nil {
		return
	}

	newRunner = runner
	newRunner.ID = insertID
	newRunner.Tags = runner.Tags

	if err = d.replaceRunnerTags(newRunner.ID, newRunner.Tags); err != nil {
		return
	}

	return
}
