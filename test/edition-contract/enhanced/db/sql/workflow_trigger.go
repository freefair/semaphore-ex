package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

func (d *WorkflowStoreImpl) GetWorkflowTriggers(projectID int, workflowTemplateID int, params db.RetrieveQueryParams) ([]db.WorkflowTrigger, error) {
	query := "select * from project__workflow_trigger where project_id=? and workflow_template_id=?"
	args := []any{projectID, workflowTemplateID}
	if params.Filter != "" {
		query += " and lower(name) like lower(?)"
		args = append(args, "%"+params.Filter+"%")
	}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc"
	count := params.Count
	if count <= 0 || count > db.MaxWorkflowTriggers {
		count = db.MaxWorkflowTriggers
	}
	query += " limit ?"
	args = append(args, count)
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	var triggers []db.WorkflowTrigger
	if _, err := d.connection.SelectAll(&triggers, query, args...); err != nil {
		return nil, err
	}
	for index := range triggers {
		if err := decodeWorkflowTrigger(&triggers[index]); err != nil {
			return nil, err
		}
	}
	return triggers, nil
}

func (d *WorkflowStoreImpl) GetWorkflowTrigger(projectID int, workflowTemplateID int, triggerID int) (db.WorkflowTrigger, error) {
	var trigger db.WorkflowTrigger
	err := d.connection.SelectOne(&trigger,
		"select * from project__workflow_trigger where project_id=? and workflow_template_id=? and id=?",
		projectID, workflowTemplateID, triggerID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowTrigger{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	if err = decodeWorkflowTrigger(&trigger); err != nil {
		return db.WorkflowTrigger{}, err
	}
	return trigger, nil
}

func (d *WorkflowStoreImpl) CreateWorkflowTrigger(trigger db.WorkflowTrigger) (db.WorkflowTrigger, error) {
	if d.connection == nil {
		return db.WorkflowTrigger{}, errors.New("workflow database connection is unavailable")
	}
	if err := encodeWorkflowTrigger(&trigger); err != nil {
		return db.WorkflowTrigger{}, err
	}
	trigger.Revision = 1
	if trigger.Created.IsZero() {
		trigger.Created = time.Now().UTC()
	}
	if trigger.Updated.IsZero() {
		trigger.Updated = trigger.Created
	}
	result, err := d.connection.Exec(
		"insert into project__workflow_trigger(project_id, workflow_template_id, revision, name, type, owner_user_id, enabled, cron_format, input_mappings, credential_hash, credential_generation, created, updated, last_fired, last_result) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		trigger.ProjectID, trigger.WorkflowTemplateID, trigger.Revision, trigger.Name, trigger.Type,
		trigger.OwnerUserID, trigger.Enabled, trigger.CronFormat, trigger.InputMappingsJSON,
		trigger.CredentialHash, trigger.CredentialGeneration, trigger.Created, trigger.Updated,
		trigger.LastFired, trigger.LastResult,
	)
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	trigger.ID = int(id)
	return trigger, nil
}

func (d *WorkflowStoreImpl) UpdateWorkflowTrigger(trigger db.WorkflowTrigger, expectedRevision int) (db.WorkflowTrigger, error) {
	if err := encodeWorkflowTrigger(&trigger); err != nil {
		return db.WorkflowTrigger{}, err
	}
	trigger.Updated = time.Now().UTC()
	result, err := d.connection.Exec(
		"update project__workflow_trigger set name=?, type=?, owner_user_id=?, enabled=?, cron_format=?, input_mappings=?, credential_hash=?, credential_generation=?, updated=?, revision=revision+1 where project_id=? and workflow_template_id=? and id=? and revision=?",
		trigger.Name, trigger.Type, trigger.OwnerUserID, trigger.Enabled, trigger.CronFormat,
		trigger.InputMappingsJSON, trigger.CredentialHash, trigger.CredentialGeneration, trigger.Updated,
		trigger.ProjectID, trigger.WorkflowTemplateID, trigger.ID, expectedRevision,
	)
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	if updated != 1 {
		return db.WorkflowTrigger{}, db.ErrWorkflowTriggerRevisionConflict
	}
	trigger.Revision = expectedRevision + 1
	return trigger, nil
}

func (d *WorkflowStoreImpl) DeleteWorkflowTrigger(projectID int, workflowTemplateID int, triggerID int) error {
	result, err := d.connection.Exec(
		"delete from project__workflow_trigger where project_id=? and workflow_template_id=? and id=?",
		projectID, workflowTemplateID, triggerID,
	)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return db.ErrNotFound
	}
	return nil
}

func (d *WorkflowStoreImpl) GetWorkflowTriggerInvocations(projectID int, triggerID int, params db.RetrieveQueryParams) ([]db.WorkflowTriggerInvocation, error) {
	query := "select * from project__workflow_trigger_invocation where project_id=? and workflow_trigger_id=?"
	args := []any{projectID, triggerID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	count := params.Count
	if count <= 0 || count > db.MaxWorkflowTriggerHistoryPage {
		count = db.MaxWorkflowTriggerHistoryPage
	}
	args = append(args, count)
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	var invocations []db.WorkflowTriggerInvocation
	if _, err := d.connection.SelectAll(&invocations, query, args...); err != nil {
		return nil, err
	}
	for index := range invocations {
		if err := decodeWorkflowTriggerInvocation(&invocations[index]); err != nil {
			return nil, err
		}
	}
	return invocations, nil
}

func (d *WorkflowStoreImpl) ClaimWorkflowTriggerInvocation(invocation db.WorkflowTriggerInvocation, now time.Time) (db.WorkflowTriggerInvocation, bool, error) {
	if err := db.ValidateWorkflowTriggerInvocation(invocation); err != nil {
		return db.WorkflowTriggerInvocation{}, false, err
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.WorkflowTriggerInvocation{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if invocation.RequestKeyHash != nil {
		if _, err = tx.Exec(d.connection.PrepareQuery(
			"delete from project__workflow_trigger_invocation where workflow_trigger_id=? and credential_generation=? and request_key_hash=? and expires_at is not null and expires_at<=?"),
			invocation.WorkflowTriggerID, invocation.CredentialGeneration, *invocation.RequestKeyHash, now,
		); err != nil {
			return db.WorkflowTriggerInvocation{}, false, err
		}
	}

	invocation.ID, err = d.insertWorkflowTriggerInvocationIfCurrent(tx, invocation)
	if err != nil {
		if errors.Is(err, db.ErrWorkflowTriggerStateChanged) {
			return db.WorkflowTriggerInvocation{}, false, err
		}
		_ = tx.Rollback()
		existing, getErr := d.selectWorkflowTriggerInvocationByIdentity(invocation)
		if getErr == nil {
			return existing, false, nil
		}
		return db.WorkflowTriggerInvocation{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowTriggerInvocation{}, false, err
	}
	return invocation, true, nil
}

func (d *WorkflowStoreImpl) insertWorkflowTriggerInvocationIfCurrent(
	tx *gorp.Transaction,
	invocation db.WorkflowTriggerInvocation,
) (int, error) {
	query := `insert into project__workflow_trigger_invocation(
		project_id, workflow_trigger_id, workflow_template_id, trigger_revision,
		credential_generation, definition_revision, request_key_hash, occurrence_identity,
		status, run_id, actor_user_id, trigger_snapshot, input_snapshot, result, reason,
		created, updated, expires_at
	) select ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	where exists (
		select 1 from project__workflow_trigger
		where project_id=? and workflow_template_id=? and id=?
			and revision=? and enabled=? and credential_generation=?
	)`
	args := []any{
		invocation.ProjectID, invocation.WorkflowTriggerID, invocation.WorkflowTemplateID,
		invocation.TriggerRevision, invocation.CredentialGeneration, invocation.DefinitionRevision,
		invocation.RequestKeyHash, invocation.OccurrenceIdentity, invocation.Status, invocation.RunID,
		invocation.ActorUserID, invocation.TriggerSnapshotJSON, invocation.InputSnapshotJSON,
		invocation.Result, invocation.Reason, invocation.Created, invocation.Updated, invocation.ExpiresAt,
		invocation.ProjectID, invocation.WorkflowTemplateID, invocation.WorkflowTriggerID,
		invocation.TriggerRevision, true, invocation.CredentialGeneration,
	}
	prepared := d.connection.PrepareQuery(query)
	if d.connection.GetDialect() == util.DbDriverPostgres {
		id, err := tx.SelectInt(prepared+" returning id", args...)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, db.ErrWorkflowTriggerStateChanged
		}
		return int(id), err
	}
	result, err := tx.Exec(prepared, args...)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rows != 1 {
		return 0, db.ErrWorkflowTriggerStateChanged
	}
	id, err := result.LastInsertId()
	return int(id), err
}

func (d *WorkflowStoreImpl) UpdateWorkflowTriggerInvocation(invocation db.WorkflowTriggerInvocation) error {
	result, err := d.connection.Exec(
		"update project__workflow_trigger_invocation set status=?, run_id=?, result=?, reason=?, updated=? where project_id=? and workflow_trigger_id=? and id=?",
		invocation.Status, invocation.RunID, invocation.Result, invocation.Reason, invocation.Updated,
		invocation.ProjectID, invocation.WorkflowTriggerID, invocation.ID,
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return db.ErrNotFound
	}
	return nil
}

func (d *WorkflowStoreImpl) DeleteExpiredWorkflowTriggerInvocations(before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > db.MaxWorkflowTriggerHistoryPage {
		limit = db.MaxWorkflowTriggerHistoryPage
	}
	var ids []int
	if _, err := d.connection.SelectAll(&ids,
		"select id from project__workflow_trigger_invocation where expires_at is not null and expires_at<=? order by id limit ?",
		before, limit,
	); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := d.connection.Exec("delete from project__workflow_trigger_invocation where id in ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *WorkflowStoreImpl) GetActiveWorkflowScheduleTriggers() ([]db.WorkflowTrigger, error) {
	var triggers []db.WorkflowTrigger
	if _, err := d.connection.SelectAll(&triggers,
		"select * from project__workflow_trigger where type=? and enabled=? order by id",
		db.WorkflowTriggerSchedule, true,
	); err != nil {
		return nil, err
	}
	for index := range triggers {
		if err := decodeWorkflowTrigger(&triggers[index]); err != nil {
			return nil, err
		}
	}
	return triggers, nil
}

func (d *WorkflowStoreImpl) RecordWorkflowTriggerResult(projectID int, triggerID int, firedAt time.Time, result string) error {
	resultRow, err := d.connection.Exec(
		"update project__workflow_trigger set last_fired=?, last_result=?, updated=? where project_id=? and id=?",
		firedAt, result, firedAt, projectID, triggerID,
	)
	if err != nil {
		return err
	}
	updated, err := resultRow.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return db.ErrNotFound
	}
	return nil
}

func encodeWorkflowTrigger(trigger *db.WorkflowTrigger) error {
	if trigger.InputMappingsJSON == "" {
		encoded, err := json.Marshal(trigger.InputMappings)
		if err != nil {
			return fmt.Errorf("encode workflow trigger input mappings: %w", err)
		}
		trigger.InputMappingsJSON = string(encoded)
	}
	if !json.Valid([]byte(trigger.InputMappingsJSON)) {
		return errors.New("workflow trigger input mappings are invalid")
	}
	return nil
}

func decodeWorkflowTrigger(trigger *db.WorkflowTrigger) error {
	if trigger.InputMappingsJSON == "" {
		trigger.InputMappingsJSON = "[]"
	}
	if err := json.Unmarshal([]byte(trigger.InputMappingsJSON), &trigger.InputMappings); err != nil {
		return fmt.Errorf("decode workflow trigger input mappings: %w", err)
	}
	return nil
}

func decodeWorkflowTriggerInvocation(invocation *db.WorkflowTriggerInvocation) error {
	if err := json.Unmarshal([]byte(invocation.TriggerSnapshotJSON), &invocation.TriggerSnapshot); err != nil {
		return fmt.Errorf("decode workflow trigger snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(invocation.InputSnapshotJSON), &invocation.InputSnapshot); err != nil {
		return fmt.Errorf("decode workflow trigger input snapshot: %w", err)
	}
	invocation.TriggerSnapshotJSON = ""
	invocation.InputSnapshotJSON = ""
	return nil
}

func (d *WorkflowStoreImpl) selectWorkflowTriggerInvocationByIdentity(wanted db.WorkflowTriggerInvocation) (db.WorkflowTriggerInvocation, error) {
	var invocation db.WorkflowTriggerInvocation
	var err error
	if wanted.RequestKeyHash != nil {
		err = d.connection.SelectOne(&invocation,
			"select * from project__workflow_trigger_invocation where workflow_trigger_id=? and credential_generation=? and request_key_hash=?",
			wanted.WorkflowTriggerID, wanted.CredentialGeneration, *wanted.RequestKeyHash,
		)
	} else if wanted.OccurrenceIdentity != nil {
		err = d.connection.SelectOne(&invocation,
			"select * from project__workflow_trigger_invocation where occurrence_identity=?", *wanted.OccurrenceIdentity)
	} else {
		return db.WorkflowTriggerInvocation{}, db.ErrNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowTriggerInvocation{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowTriggerInvocation{}, err
	}
	if err = decodeWorkflowTriggerInvocation(&invocation); err != nil {
		return db.WorkflowTriggerInvocation{}, err
	}
	return invocation, nil
}
