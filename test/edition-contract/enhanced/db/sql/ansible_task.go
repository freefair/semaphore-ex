package sql

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
)

const (
	defaultSummaryPageSize = 50
	maxSummaryPageSize     = 200
)

var errTaskSummaryRepositoryUnavailable = errors.New("task summary repository is unavailable")

type AnsibleTaskStoreImpl struct {
	connection *coresql.SqlDbConnection
}

var _ db.AnsibleTaskRepository = (*AnsibleTaskStoreImpl)(nil)

func NewAnsibleTask(connection *coresql.SqlDbConnection) db.AnsibleTaskRepository {
	return &AnsibleTaskStoreImpl{connection: connection}
}

func (d *AnsibleTaskStoreImpl) CreateAnsibleTaskHost(host db.AnsibleTaskHost) error {
	event := db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventHost,
		EventID: fmt.Sprintf("legacy-host:%s", host.Host), Host: host.Host,
		Status: hostStatus(host.Failed, host.Unreachable), Changed: host.Changed,
		Failed: host.Failed, Ignored: host.Ignored, Ok: host.Ok, Rescued: host.Rescued,
		Skipped: host.Skipped, Unreachable: host.Unreachable,
	}
	return d.IngestTaskSummaryEvent(host.ProjectID, host.TaskID, event, host.Created)
}

func (d *AnsibleTaskStoreImpl) CreateAnsibleTaskError(taskError db.AnsibleTaskError) error {
	event := db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventResult,
		EventID: fmt.Sprintf("legacy-error:%s:%s:%d", taskError.Host, taskError.Task, taskError.ID),
		Host:    taskError.Host, Stage: taskError.Task, StageID: taskError.Task,
		Status: "failed", Error: taskError.Error,
	}
	return d.IngestTaskSummaryEvent(taskError.ProjectID, taskError.TaskID, event, taskError.Created)
}

func (d *AnsibleTaskStoreImpl) GetAnsibleTaskHosts(projectID int, taskID int) ([]db.AnsibleTaskHost, error) {
	page, err := d.GetTaskSummaryHosts(projectID, taskID, db.RetrieveQueryParams{Count: maxSummaryPageSize})
	if err != nil {
		return nil, err
	}
	hosts := make([]db.AnsibleTaskHost, 0, len(page.Items))
	for _, host := range page.Items {
		hosts = append(hosts, db.AnsibleTaskHost{
			ID: host.ID, TaskID: taskID, ProjectID: projectID, Host: host.Host,
			Changed: host.Changed, Failed: host.Failed, Ignored: host.Ignored, Ok: host.Ok,
			Rescued: host.Rescued, Skipped: host.Skipped, Unreachable: host.Unreachable,
		})
	}
	return hosts, nil
}

func (d *AnsibleTaskStoreImpl) GetAnsibleTaskErrors(projectID int, taskID int) ([]db.AnsibleTaskError, error) {
	page, err := d.GetTaskSummaryErrors(projectID, taskID, db.RetrieveQueryParams{Count: maxSummaryPageSize})
	if err != nil {
		return nil, err
	}
	result := make([]db.AnsibleTaskError, 0, len(page.Items))
	for _, taskError := range page.Items {
		result = append(result, db.AnsibleTaskError{
			ID: taskError.ID, TaskID: taskID, ProjectID: projectID, Host: taskError.Host,
			Task: taskError.Stage, Error: taskError.Error,
		})
	}
	return result, nil
}

func (d *AnsibleTaskStoreImpl) IngestTaskSummaryEvent(projectID int, taskID int, event db.TaskSummaryEvent, outputTime time.Time) error {
	if d.connection == nil {
		return errTaskSummaryRepositoryUnavailable
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = requireTask(tx, d.connection, projectID, taskID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err = ensureSummary(tx, d.connection, projectID, taskID, event.Version, now); err != nil {
		return err
	}
	if event.Version != db.TaskSummarySchemaVersion {
		_, err = tx.Exec(d.connection.PrepareQuery(
			"update task__summary set runner_result_version=?, state=?, diagnostic=?, updated=? where project_id=? and task_id=?"),
			event.Version, db.TaskSummaryUnsupported,
			fmt.Sprintf("runner result version %d is not supported", event.Version), now, projectID, taskID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}

	_, err = tx.Exec(insertIgnoreQuery(d.connection, `
			insert into task__summary_event
			(project_id, task_id, schema_version, event_id, kind, play_id, stage_id, stage, host, status,
			 changed, failed, ignored, ok, rescued, skipped, unreachable, expected_hosts,
			 started_at, ended_at, duration_ms, error, output_time, created)
			values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"task_id, event_id"),
		projectID, taskID, event.Version, event.EventID, event.Kind, event.PlayID, event.StageID,
		event.Stage, event.Host, event.Status, event.Changed, event.Failed, event.Ignored, event.Ok,
		event.Rescued, event.Skipped, event.Unreachable, event.ExpectedHosts, event.Started, event.Ended,
		event.DurationMS, event.Error, nullableTime(outputTime), now)
	if err != nil {
		return err
	}
	status, err := taskStatus(tx, d.connection, projectID, taskID)
	if err != nil {
		return err
	}
	if err = recomputeSummary(tx, d.connection, projectID, taskID, status, nil, nil, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *AnsibleTaskStoreImpl) FinalizeTaskSummary(projectID int, taskID int, status task_logger.TaskStatus, started *time.Time, ended *time.Time) error {
	return d.recompute(projectID, taskID, status, started, ended)
}

func (d *AnsibleTaskStoreImpl) RepairTaskSummary(projectID int, taskID int, status task_logger.TaskStatus) error {
	return d.recompute(projectID, taskID, status, nil, nil)
}

func (d *AnsibleTaskStoreImpl) recompute(projectID int, taskID int, status task_logger.TaskStatus, started *time.Time, ended *time.Time) error {
	if d.connection == nil {
		return errTaskSummaryRepositoryUnavailable
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = requireTask(tx, d.connection, projectID, taskID); err != nil {
		return err
	}
	persistedStatus, err := taskStatus(tx, d.connection, projectID, taskID)
	if err != nil {
		return err
	}
	status = persistedStatus
	now := time.Now().UTC()
	if err = ensureSummary(tx, d.connection, projectID, taskID, db.TaskSummarySchemaVersion, now); err != nil {
		return err
	}
	if err = recomputeSummary(tx, d.connection, projectID, taskID, status, started, ended, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *AnsibleTaskStoreImpl) GetTaskSummary(projectID int, taskID int) (db.TaskSummary, error) {
	if d.connection == nil {
		return db.TaskSummary{}, errTaskSummaryRepositoryUnavailable
	}
	if err := d.requireTask(projectID, taskID); err != nil {
		return db.TaskSummary{}, err
	}
	var summary db.TaskSummary
	err := d.connection.SelectOne(&summary, `
		select s.schema_version, s.task_id, s.project_id, s.runner_result_version, s.state,
		       s.task_status, s.expected_hosts, s.diagnostic, s.started_at, s.ended_at, s.updated,
		       (select count(1) from task__summary_event e where e.project_id=s.project_id and e.task_id=s.task_id) as event_count,
		       (select count(1) from task__summary_event e where e.project_id=s.project_id and e.task_id=s.task_id and e.kind='host_summary') as total_hosts,
		       (select count(1) from task__summary_event e where e.project_id=s.project_id and e.task_id=s.task_id and e.kind='host_summary' and e.status='success') as ok_hosts,
		       (select count(1) from task__summary_event e where e.project_id=s.project_id and e.task_id=s.task_id and e.kind='host_summary' and e.status='failed') as failed_hosts
		from task__summary s where s.project_id=? and s.task_id=?`, projectID, taskID)
	if err != nil {
		return db.TaskSummary{}, err
	}
	return summary, nil
}

func (d *AnsibleTaskStoreImpl) GetTaskSummaryHosts(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryHost], error) {
	page := db.TaskSummaryPage[db.TaskSummaryHost]{Items: []db.TaskSummaryHost{}}
	if d.connection == nil {
		return page, errTaskSummaryRepositoryUnavailable
	}
	if err := d.requireTask(projectID, taskID); err != nil {
		return page, err
	}
	count := summaryPageSize(params.Count)
	query := `select id, host, status, changed, failed, ignored, ok, rescued, skipped, unreachable,
	                 started_at, ended_at, duration_ms
	          from task__summary_event
	          where project_id=? and task_id=? and kind='host_summary'`
	args := []any{projectID, taskID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	args = append(args, count+1)
	if _, err := d.connection.SelectAll(&page.Items, query, args...); err != nil {
		return page, err
	}
	page.Items, page.NextCursor = trimPage(page.Items, count, func(item db.TaskSummaryHost) int { return item.ID })
	return page, nil
}

func (d *AnsibleTaskStoreImpl) GetTaskSummaryStages(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryStage], error) {
	page := db.TaskSummaryPage[db.TaskSummaryStage]{Items: []db.TaskSummaryStage{}}
	if d.connection == nil {
		return page, errTaskSummaryRepositoryUnavailable
	}
	if err := d.requireTask(projectID, taskID); err != nil {
		return page, err
	}
	count := summaryPageSize(params.Count)
	query := `select max(id) as id, stage_id, stage,
	                 sum(case when status='ok' then 1 else 0 end) as ok,
	                 sum(case when status='changed' then 1 else 0 end) as changed,
	                 sum(case when status='failed' then 1 else 0 end) as failed,
	                 sum(case when status='ignored' then 1 else 0 end) as ignored,
	                 sum(case when status='rescued' then 1 else 0 end) as rescued,
	                 sum(case when status='skipped' then 1 else 0 end) as skipped,
	                 sum(case when status='unreachable' then 1 else 0 end) as unreachable,
	                 min(started_at) as started_at, max(ended_at) as ended_at,
	                 sum(duration_ms) as duration_ms
	          from task__summary_event
	          where project_id=? and task_id=? and kind='task_result'
	          group by stage_id, stage`
	args := []any{projectID, taskID}
	if params.BeforeID > 0 {
		query += " having max(id) < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by max(id) desc limit ?"
	args = append(args, count+1)
	if _, err := d.connection.SelectAll(&page.Items, query, args...); err != nil {
		return page, err
	}
	page.Items, page.NextCursor = trimPage(page.Items, count, func(item db.TaskSummaryStage) int { return item.ID })
	return page, nil
}

func (d *AnsibleTaskStoreImpl) GetTaskSummaryErrors(projectID int, taskID int, params db.RetrieveQueryParams) (db.TaskSummaryPage[db.TaskSummaryError], error) {
	page := db.TaskSummaryPage[db.TaskSummaryError]{Items: []db.TaskSummaryError{}}
	if d.connection == nil {
		return page, errTaskSummaryRepositoryUnavailable
	}
	if err := d.requireTask(projectID, taskID); err != nil {
		return page, err
	}
	count := summaryPageSize(params.Count)
	query := `select id, event_id, host, stage, status, error, output_time
	          from task__summary_event
	          where project_id=? and task_id=? and kind='task_result'
	            and status in ('failed','unreachable')`
	args := []any{projectID, taskID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	args = append(args, count+1)
	if _, err := d.connection.SelectAll(&page.Items, query, args...); err != nil {
		return page, err
	}
	page.Items, page.NextCursor = trimPage(page.Items, count, func(item db.TaskSummaryError) int { return item.ID })
	return page, nil
}

func (d *AnsibleTaskStoreImpl) requireTask(projectID int, taskID int) error {
	var count int
	if err := d.connection.SelectOne(&count, "select count(1) from task where project_id=? and id=?", projectID, taskID); err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func requireTask(tx *gorp.Transaction, connection *coresql.SqlDbConnection, projectID int, taskID int) error {
	count, err := tx.SelectInt(connection.PrepareQuery("select count(1) from task where project_id=? and id=?"), projectID, taskID)
	if err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func ensureSummary(tx *gorp.Transaction, connection *coresql.SqlDbConnection, projectID int, taskID int, version int, now time.Time) error {
	_, err := tx.Exec(insertIgnoreQuery(connection, `
		insert into task__summary
		(task_id, project_id, schema_version, runner_result_version, state, task_status,
		 expected_hosts, diagnostic, updated)
		values (?, ?, ?, ?, ?, '', 0, '', ?)`, "task_id"),
		taskID, projectID, db.TaskSummarySchemaVersion, version, db.TaskSummaryCollecting, now)
	return err
}

func insertIgnoreQuery(connection *coresql.SqlDbConnection, query string, conflictColumns string) string {
	if connection.GetDialect() == util.DbDriverMySQL {
		query = strings.Replace(query, "insert into", "insert ignore into", 1)
	} else {
		query += " on conflict (" + conflictColumns + ") do nothing"
	}
	return connection.PrepareQuery(query)
}

func recomputeSummary(tx *gorp.Transaction, connection *coresql.SqlDbConnection, projectID int, taskID int, status task_logger.TaskStatus, started *time.Time, ended *time.Time, now time.Time) error {
	var runnerVersion int
	var currentState string
	if err := tx.SelectOne(&runnerVersion, connection.PrepareQuery(
		"select runner_result_version from task__summary where project_id=? and task_id=?"), projectID, taskID); err != nil {
		return err
	}
	if err := tx.SelectOne(&currentState, connection.PrepareQuery(
		"select state from task__summary where project_id=? and task_id=?"), projectID, taskID); err != nil {
		return err
	}
	if runnerVersion != 0 && runnerVersion != db.TaskSummarySchemaVersion || currentState == string(db.TaskSummaryUnsupported) {
		_, err := tx.Exec(connection.PrepareQuery(
			"update task__summary set task_status=?, started_at=coalesce(?, started_at), ended_at=coalesce(?, ended_at), updated=? where project_id=? and task_id=?"),
			status, started, ended, now, projectID, taskID)
		return err
	}

	eventCount, err := tx.SelectInt(connection.PrepareQuery(
		"select count(1) from task__summary_event where project_id=? and task_id=?"), projectID, taskID)
	if err != nil {
		return err
	}
	hostCount, err := tx.SelectInt(connection.PrepareQuery(
		"select count(1) from task__summary_event where project_id=? and task_id=? and kind='host_summary'"), projectID, taskID)
	if err != nil {
		return err
	}
	completeCount, err := tx.SelectInt(connection.PrepareQuery(
		"select count(1) from task__summary_event where project_id=? and task_id=? and kind='run_complete'"), projectID, taskID)
	if err != nil {
		return err
	}
	expectedHosts, err := tx.SelectInt(connection.PrepareQuery(
		"select coalesce(max(expected_hosts), 0) from task__summary_event where project_id=? and task_id=? and kind='run_complete'"), projectID, taskID)
	if err != nil {
		return err
	}
	var collectionError string
	if err := tx.SelectOne(&collectionError, connection.PrepareQuery(
		"select coalesce(max(error), '') from task__summary_event where project_id=? and task_id=? and kind='collection_error'"),
		projectID, taskID); err != nil {
		return err
	}

	state := db.TaskSummaryCollecting
	diagnostic := ""
	if status.IsFinished() {
		switch {
		case eventCount == 0:
			state = db.TaskSummaryEmpty
		case completeCount == 0:
			state = db.TaskSummaryPartial
			if collectionError != "" {
				diagnostic = "task summary collection failed: " + collectionError
			} else {
				diagnostic = "runner result stream ended without a completion event"
			}
		case int64(expectedHosts) != hostCount:
			state = db.TaskSummaryPartial
			diagnostic = fmt.Sprintf("received %d of %d expected host summaries", hostCount, expectedHosts)
		default:
			state = db.TaskSummaryComplete
		}
	}
	_, err = tx.Exec(connection.PrepareQuery(`
		update task__summary
		set runner_result_version=?, state=?, task_status=?, expected_hosts=?, diagnostic=?,
		    started_at=coalesce(?, started_at), ended_at=coalesce(?, ended_at), updated=?
		where project_id=? and task_id=?`),
		db.TaskSummarySchemaVersion, state, status, expectedHosts, diagnostic,
		started, ended, now, projectID, taskID)
	return err
}

func taskStatus(tx *gorp.Transaction, connection *coresql.SqlDbConnection, projectID int, taskID int) (task_logger.TaskStatus, error) {
	var status string
	err := tx.SelectOne(&status, connection.PrepareQuery(
		"select status from task where project_id=? and id=?"), projectID, taskID)
	return task_logger.TaskStatus(status), err
}

func summaryPageSize(requested int) int {
	if requested <= 0 {
		return defaultSummaryPageSize
	}
	if requested > maxSummaryPageSize {
		return maxSummaryPageSize
	}
	return requested
}

func trimPage[T any](items []T, count int, id func(T) int) ([]T, *int) {
	if len(items) <= count {
		return items, nil
	}
	items = items[:count]
	cursor := id(items[len(items)-1])
	return items, &cursor
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func hostStatus(failed int, unreachable int) string {
	if failed > 0 || unreachable > 0 {
		return "failed"
	}
	return "success"
}
