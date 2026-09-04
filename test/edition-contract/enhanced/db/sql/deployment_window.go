package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

// DeploymentWindowStore owns the narrow transactional boundary for enhanced
// admission. It intentionally has no enqueue capability: a later start path
// must bind the returned immutable decision to its own task/run transaction.
type DeploymentWindowStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.DeploymentWindowPolicyRepository = (*DeploymentWindowStore)(nil)
var _ pro_interfaces.DeploymentWindowGovernanceRepository = (*DeploymentWindowStore)(nil)

func NewDeploymentWindowStore(connection *coresql.SqlDbConnection) *DeploymentWindowStore {
	return &DeploymentWindowStore{connection: connection}
}

func (d *DeploymentWindowStore) GetDeploymentWindowPolicy(projectID int) (db.DeploymentWindowPolicy, error) {
	if d == nil || d.connection == nil || projectID <= 0 {
		return db.DeploymentWindowPolicy{}, db.ErrInvalidOperation
	}
	if err := d.ensureProject(projectID); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	policy, found, err := d.getPolicy(nil, projectID, false)
	if err != nil || !found {
		if err != nil {
			return db.DeploymentWindowPolicy{}, err
		}
		return defaultDeploymentWindowPolicy(projectID), nil
	}
	return policy, nil
}

// GetDeploymentWindowDatabaseTime returns the authoritative clock used by
// admission/status evaluation. Governance callers must not substitute a web
// server timestamp, which could disagree with the transactional start path.
func (d *DeploymentWindowStore) GetDeploymentWindowDatabaseTime() (time.Time, error) {
	if d == nil || d.connection == nil {
		return time.Time{}, db.ErrInvalidOperation
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now, err := d.databaseNow(tx)
	if err != nil {
		return time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return now, nil
}

// GetDeploymentWindowDecisionHistory returns only immutable decisions for one
// project. The query has a fixed id-desc order and bounded page size so a
// controller cannot turn the governance history into an unbounded export.
func (d *DeploymentWindowStore) GetDeploymentWindowDecisionHistory(projectID int, params db.RetrieveQueryParams) ([]db.DeploymentWindowDecisionRecord, error) {
	if d == nil || d.connection == nil || projectID <= 0 || params.Offset < 0 || params.Count < 0 || params.BeforeID < 0 {
		return nil, db.ErrInvalidOperation
	}
	if err := d.ensureProject(projectID); err != nil {
		return nil, err
	}
	query := "select * from project__deployment_window_decision where project_id=?"
	args := []any{projectID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	count := params.Count
	if count <= 0 || count > db.MaxDeploymentWindowHistoryPage {
		count = db.MaxDeploymentWindowHistoryPage
	}
	args = append(args, count)
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	var records []db.DeploymentWindowDecisionRecord
	if _, err := d.connection.SelectAll(&records, query, args...); err != nil {
		return nil, err
	}
	return records, nil
}

// EvaluateDeploymentWindowStatus keeps policy load and database time together
// under the project lock, matching the clock semantics of an admission claim
// while deliberately avoiding creation of an execution decision.
func (d *DeploymentWindowStore) EvaluateDeploymentWindowStatus(
	request pro_interfaces.DeploymentWindowStatusRequest,
	evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error),
) (pro_interfaces.DeploymentWindowDecision, error) {
	if d == nil || d.connection == nil || evaluate == nil || request.Validate() != nil {
		return pro_interfaces.DeploymentWindowDecision{}, db.ErrInvalidOperation
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockProject(tx, request.ProjectID); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if err = d.validateStatusTenants(tx, request); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	now, err := d.databaseNow(tx)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	policy, found, err := d.getPolicy(tx, request.ProjectID, true)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if !found {
		policy = defaultDeploymentWindowPolicy(request.ProjectID)
	}
	decision, err := evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: request.ProjectID, TemplateID: dereferenceDeploymentWindowID(request.TemplateID), WorkflowID: dereferenceDeploymentWindowID(request.WorkflowID),
		Source: pro_interfaces.DeploymentWindowSourceManual, At: now,
	})
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if err = tx.Commit(); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	return decision, nil
}

// PreviewDeploymentWindowPolicy evaluates an unpersisted draft under the same
// project lock and database clock as status/admission. Draft rules and the
// requested target are tenant-checked here so an API caller cannot use a
// numerically colliding target from another project to influence a preview.
func (d *DeploymentWindowStore) PreviewDeploymentWindowPolicy(
	policy db.DeploymentWindowPolicy,
	request pro_interfaces.DeploymentWindowStatusRequest,
	evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error),
) (pro_interfaces.DeploymentWindowDecision, error) {
	if d == nil || d.connection == nil || evaluate == nil || policy.ProjectID != request.ProjectID ||
		policy.ValidateDraft(validateDeploymentWindowTimezone) != nil || request.Validate() != nil {
		return pro_interfaces.DeploymentWindowDecision{}, db.ErrInvalidOperation
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockProject(tx, request.ProjectID); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if err = d.validateRuleTenants(tx, policy); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if err = d.validateStatusTenants(tx, request); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	now, err := d.databaseNow(tx)
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	decision, err := evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: request.ProjectID, TemplateID: dereferenceDeploymentWindowID(request.TemplateID), WorkflowID: dereferenceDeploymentWindowID(request.WorkflowID),
		Source: pro_interfaces.DeploymentWindowSourceManual, At: now,
	})
	if err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	if err = tx.Commit(); err != nil {
		return pro_interfaces.DeploymentWindowDecision{}, err
	}
	return decision, nil
}

func (d *DeploymentWindowStore) SaveDeploymentWindowPolicy(policy db.DeploymentWindowPolicy, expectedRevision int) (db.DeploymentWindowPolicy, error) {
	if d == nil || d.connection == nil || policy.ProjectID <= 0 || expectedRevision <= 0 || policy.ValidateDraft(validateDeploymentWindowTimezone) != nil {
		return db.DeploymentWindowPolicy{}, db.ErrInvalidOperation
	}
	if policy.Revision != expectedRevision {
		return db.DeploymentWindowPolicy{}, db.ErrDeploymentWindowRevisionConflict
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockProject(tx, policy.ProjectID); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	current, found, err := d.getPolicy(tx, policy.ProjectID, true)
	if err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	currentRevision := 1
	if found {
		currentRevision = current.Revision
	}
	if expectedRevision != currentRevision {
		return db.DeploymentWindowPolicy{}, db.ErrDeploymentWindowRevisionConflict
	}
	nextRevision := currentRevision + 1
	policy.Revision = nextRevision
	for index := range policy.Rules {
		policy.Rules[index].Revision = nextRevision
	}
	if err = d.validateRuleTenants(tx, policy); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	for _, rule := range policy.Rules {
		if rule.ID <= 0 {
			continue
		}
		count, countErr := tx.SelectInt(d.connection.PrepareQuery("select count(*) from project__deployment_window_rule where project_id=? and id=?"), policy.ProjectID, rule.ID)
		if countErr != nil || count != 1 {
			return db.DeploymentWindowPolicy{}, db.ErrDeploymentWindowTenantMismatch
		}
	}
	if found {
		result, updateErr := tx.Exec(d.connection.PrepareQuery("update project__deployment_window_policy set revision=?, timezone=?, default_decision=?, updated="+databaseCurrentTimestamp(d.connection)+" where project_id=? and revision=?"), nextRevision, policy.Timezone, policy.Default, policy.ProjectID, currentRevision)
		if updateErr != nil {
			return db.DeploymentWindowPolicy{}, updateErr
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return db.DeploymentWindowPolicy{}, affectedErr
		}
		if affected != 1 {
			return db.DeploymentWindowPolicy{}, db.ErrDeploymentWindowRevisionConflict
		}
		if _, err = tx.Exec(d.connection.PrepareQuery("delete from project__deployment_window_rule where project_id=?"), policy.ProjectID); err != nil {
			return db.DeploymentWindowPolicy{}, err
		}
	} else {
		if _, err = tx.Exec(d.connection.PrepareQuery("insert into project__deployment_window_policy(project_id, revision, timezone, default_decision, created, updated) values (?, ?, ?, ?, "+databaseCurrentTimestamp(d.connection)+", "+databaseCurrentTimestamp(d.connection)+")"), policy.ProjectID, nextRevision, policy.Timezone, policy.Default); err != nil {
			return db.DeploymentWindowPolicy{}, err
		}
	}
	for index := range policy.Rules {
		rule := &policy.Rules[index]
		id, insertErr := d.insertRule(tx, policy.ProjectID, *rule)
		if insertErr != nil {
			return db.DeploymentWindowPolicy{}, insertErr
		}
		rule.ID = id
	}
	if err = tx.Commit(); err != nil {
		return db.DeploymentWindowPolicy{}, err
	}
	return d.GetDeploymentWindowPolicy(policy.ProjectID)
}

func (d *DeploymentWindowStore) DeleteDeploymentWindowPolicy(projectID int, expectedRevision int) error {
	if d == nil || d.connection == nil || projectID <= 0 || expectedRevision <= 0 {
		return db.ErrInvalidOperation
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockProject(tx, projectID); err != nil {
		return err
	}
	current, found, err := d.getPolicy(tx, projectID, true)
	if err != nil {
		return err
	}
	if !found || expectedRevision != current.Revision {
		return db.ErrDeploymentWindowRevisionConflict
	}
	if _, err = tx.Exec(d.connection.PrepareQuery("delete from project__deployment_window_rule where project_id=?"), projectID); err != nil {
		return err
	}
	result, err := tx.Exec(d.connection.PrepareQuery("update project__deployment_window_policy set revision=?, timezone='UTC', default_decision=?, updated="+databaseCurrentTimestamp(d.connection)+" where project_id=? and revision=?"), expectedRevision+1, db.DeploymentWindowDefaultAllow, projectID, expectedRevision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return db.ErrDeploymentWindowRevisionConflict
	}
	return tx.Commit()
}

func (d *DeploymentWindowStore) ClaimDeploymentWindowAdmission(
	request pro_interfaces.DeploymentWindowAdmissionRequest,
	evaluate func(db.DeploymentWindowPolicy, pro_interfaces.DeploymentWindowEvaluationRequest) (pro_interfaces.DeploymentWindowDecision, error),
) (pro_interfaces.DeploymentWindowAdmissionClaim, error) {
	if d == nil || d.connection == nil || evaluate == nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, db.ErrInvalidOperation
	}
	if request.Override != nil && (request.Source != pro_interfaces.DeploymentWindowSourceManual || request.Origin != pro_interfaces.DeploymentWindowOriginUser) {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, pro_interfaces.ErrDeploymentWindowOverrideForbidden
	}
	if request.Validate() != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, db.ErrInvalidOperation
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = d.lockProject(tx, request.ProjectID); err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	existing, found, err := d.getDecision(tx, request)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	if found {
		// A bound decision is a completed idempotent operation. No new work can
		// result from returning it, so preserve retry semantics after a role is
		// later revoked. An unbound overridden decision, however, must recheck
		// the current permission before it can authorize first persistence.
		if existing.State == string(pro_interfaces.DeploymentWindowDecisionOverridden) &&
			(existing.TaskID != nil || existing.WorkflowRunID != nil) {
			if !deploymentWindowDecisionMatchesRequest(existing, request) {
				return pro_interfaces.DeploymentWindowAdmissionClaim{}, pro_interfaces.ErrDeploymentWindowOverrideForbidden
			}
			if err = tx.Commit(); err != nil {
				return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
			}
			return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: existing}, nil
		}
		if request.Override != nil {
			override, overrideErr := d.currentOverride(tx, request)
			if overrideErr != nil {
				return pro_interfaces.DeploymentWindowAdmissionClaim{}, overrideErr
			}
			request.Override = override
		}
		if !deploymentWindowDecisionMatchesRequest(existing, request) {
			if existing.State == string(pro_interfaces.DeploymentWindowDecisionOverridden) && request.Override == nil {
				return pro_interfaces.DeploymentWindowAdmissionClaim{}, pro_interfaces.ErrDeploymentWindowOverrideForbidden
			}
			return pro_interfaces.DeploymentWindowAdmissionClaim{}, db.ErrInvalidOperation
		}
		if err = tx.Commit(); err != nil {
			return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
		}
		return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: existing}, nil
	}
	if err = d.validateDecisionTenants(tx, request); err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	now, err := d.databaseNow(tx)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	policy, policyFound, err := d.getPolicy(tx, request.ProjectID, true)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	if !policyFound {
		policy = defaultDeploymentWindowPolicy(request.ProjectID)
	}
	var override *pro_interfaces.DeploymentWindowOverrideRequest
	if request.Override != nil {
		override, err = d.currentOverride(tx, request)
		if err != nil {
			return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
		}
		request.Override = override
	}
	decision, err := evaluate(policy, pro_interfaces.DeploymentWindowEvaluationRequest{
		ProjectID: request.ProjectID, TemplateID: dereferenceDeploymentWindowID(request.TemplateID), WorkflowID: dereferenceDeploymentWindowID(request.WorkflowID),
		Source: request.Source, At: now, Override: override,
	})
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	record, err := deploymentWindowDecisionRecord(request, decision, now)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	id, err := d.insertDecision(tx, record)
	if err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	record.ID = id
	if err = tx.Commit(); err != nil {
		return pro_interfaces.DeploymentWindowAdmissionClaim{}, err
	}
	return pro_interfaces.DeploymentWindowAdmissionClaim{Decision: record, Inserted: true}, nil
}

func defaultDeploymentWindowPolicy(projectID int) db.DeploymentWindowPolicy {
	return db.DeploymentWindowPolicy{ProjectID: projectID, Revision: 1, Timezone: "UTC", Default: db.DeploymentWindowDefaultAllow, Rules: []db.DeploymentWindowRule{}}
}

func validateDeploymentWindowTimezone(value string) error {
	_, err := time.LoadLocation(value)
	return err
}

func (d *DeploymentWindowStore) ensureProject(projectID int) error {
	var count int
	if err := d.connection.SelectOne(&count, "select count(*) from project where id=?", projectID); err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func (d *DeploymentWindowStore) lockProject(tx *gorp.Transaction, projectID int) error {
	query := "select id from project where id=?"
	if d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var id int
	err := tx.SelectOne(&id, d.connection.PrepareQuery(query), projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	return err
}

func (d *DeploymentWindowStore) getPolicy(tx *gorp.Transaction, projectID int, lock bool) (db.DeploymentWindowPolicy, bool, error) {
	query := "select project_id, revision, timezone, default_decision from project__deployment_window_policy where project_id=?"
	if lock && d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var policy db.DeploymentWindowPolicy
	var err error
	if tx == nil {
		err = d.connection.SelectOne(&policy, query, projectID)
	} else {
		err = tx.SelectOne(&policy, d.connection.PrepareQuery(query), projectID)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.DeploymentWindowPolicy{}, false, nil
	}
	if err != nil {
		return db.DeploymentWindowPolicy{}, false, err
	}
	ruleQuery := "select id, policy_revision, name, active, kind, scope, template_id, workflow_template_id, recurrence, duration_minutes, effective_from, effective_until from project__deployment_window_rule where project_id=? order by id"
	if tx == nil {
		_, err = d.connection.SelectAll(&policy.Rules, ruleQuery, projectID)
	} else {
		_, err = tx.Select(&policy.Rules, d.connection.PrepareQuery(ruleQuery), projectID)
	}
	if err != nil {
		return db.DeploymentWindowPolicy{}, false, err
	}
	if policy.Validate(validateDeploymentWindowTimezone) != nil {
		return db.DeploymentWindowPolicy{}, false, db.ErrInvalidOperation
	}
	return policy, true, nil
}

func (d *DeploymentWindowStore) validateRuleTenants(tx *gorp.Transaction, policy db.DeploymentWindowPolicy) error {
	for _, rule := range policy.Rules {
		if rule.TemplateID != nil && !d.tenantReferenceExists(tx, "project__template", "id", policy.ProjectID, *rule.TemplateID) {
			return db.ErrDeploymentWindowTenantMismatch
		}
		if rule.WorkflowID != nil && !d.tenantReferenceExists(tx, "project__workflow_template", "id", policy.ProjectID, *rule.WorkflowID) {
			return db.ErrDeploymentWindowTenantMismatch
		}
	}
	return nil
}

func (d *DeploymentWindowStore) tenantReferenceExists(tx *gorp.Transaction, table, idColumn string, projectID, id int) bool {
	count, err := tx.SelectInt(d.connection.PrepareQuery("select count(*) from "+table+" where project_id=? and "+idColumn+"=?"), projectID, id)
	return err == nil && count == 1
}

func (d *DeploymentWindowStore) insertRule(tx *gorp.Transaction, projectID int, rule db.DeploymentWindowRule) (int, error) {
	if rule.ID > 0 {
		_, err := tx.Exec(d.connection.PrepareQuery("insert into project__deployment_window_rule(id, project_id, policy_revision, name, active, kind, scope, template_id, workflow_template_id, recurrence, duration_minutes, effective_from, effective_until) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"), rule.ID, projectID, rule.Revision, rule.Name, rule.Active, rule.Kind, rule.Scope, rule.TemplateID, rule.WorkflowID, rule.Recurrence, rule.DurationMinutes, rule.EffectiveFrom, rule.EffectiveUntil)
		return rule.ID, err
	}
	query := "insert into project__deployment_window_rule(project_id, policy_revision, name, active, kind, scope, template_id, workflow_template_id, recurrence, duration_minutes, effective_from, effective_until) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	args := []any{projectID, rule.Revision, rule.Name, rule.Active, rule.Kind, rule.Scope, rule.TemplateID, rule.WorkflowID, rule.Recurrence, rule.DurationMinutes, rule.EffectiveFrom, rule.EffectiveUntil}
	return insertDeploymentWindowTx(tx, d.connection, query, args...)
}

func (d *DeploymentWindowStore) validateDecisionTenants(tx *gorp.Transaction, request pro_interfaces.DeploymentWindowAdmissionRequest) error {
	if request.TemplateID != nil && !d.tenantReferenceExists(tx, "project__template", "id", request.ProjectID, *request.TemplateID) {
		return db.ErrDeploymentWindowTenantMismatch
	}
	if request.WorkflowID != nil && !d.tenantReferenceExists(tx, "project__workflow_template", "id", request.ProjectID, *request.WorkflowID) {
		return db.ErrDeploymentWindowTenantMismatch
	}
	for _, reference := range []struct {
		table, column string
		id            *int
	}{
		{"project__schedule", "id", request.ScheduleID}, {"project__workflow_run", "id", request.WorkflowRunID}, {"project__workflow_run_node", "id", request.WorkflowRunNodeID},
	} {
		if reference.id != nil && !d.tenantReferenceExists(tx, reference.table, reference.column, request.ProjectID, *reference.id) {
			return db.ErrDeploymentWindowTenantMismatch
		}
	}
	if request.TaskID != nil {
		count, err := tx.SelectInt(d.connection.PrepareQuery("select count(*) from task join project__template on project__template.id=task.template_id where project__template.project_id=? and task.id=?"), request.ProjectID, *request.TaskID)
		if err != nil || count != 1 {
			return db.ErrDeploymentWindowTenantMismatch
		}
	}
	return nil
}

func (d *DeploymentWindowStore) validateStatusTenants(tx *gorp.Transaction, request pro_interfaces.DeploymentWindowStatusRequest) error {
	if request.TemplateID != nil && !d.tenantReferenceExists(tx, "project__template", "id", request.ProjectID, *request.TemplateID) {
		return db.ErrDeploymentWindowTenantMismatch
	}
	if request.WorkflowID != nil && !d.tenantReferenceExists(tx, "project__workflow_template", "id", request.ProjectID, *request.WorkflowID) {
		return db.ErrDeploymentWindowTenantMismatch
	}
	return nil
}

func (d *DeploymentWindowStore) databaseNow(tx *gorp.Transaction) (time.Time, error) {
	value, err := tx.SelectStr(d.connection.PrepareQuery("select " + databaseCurrentTimestamp(d.connection)))
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if parsed, parseErr := time.Parse(layout, value); parseErr == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid database timestamp")
}

func databaseCurrentTimestamp(connection *coresql.SqlDbConnection) string {
	switch connection.GetDialect() {
	case util.DbDriverPostgres:
		return "(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"
	case util.DbDriverMySQL:
		return "UTC_TIMESTAMP()"
	default:
		return "CURRENT_TIMESTAMP"
	}
}

func (d *DeploymentWindowStore) currentOverride(tx *gorp.Transaction, request pro_interfaces.DeploymentWindowAdmissionRequest) (*pro_interfaces.DeploymentWindowOverrideRequest, error) {
	if request.Override == nil {
		return nil, nil
	}
	// The project row is locked by ClaimDeploymentWindowAdmission before this
	// method runs. Authorization rows follow in a single order (user,
	// membership, role) so an override cannot be admitted from a permission
	// snapshot that a concurrent revocation is changing. PostgreSQL and MySQL
	// lock the rows read here; SQLite serializes writes for its single
	// connection. Keep this order aligned with any future authorization lookup.
	var actor struct {
		Admin bool `db:"admin"`
	}
	if err := tx.SelectOne(&actor, d.connection.PrepareQuery(d.lockAuthorizationQuery("select admin from `user` where id=?")), *request.ActorUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pro_interfaces.ErrDeploymentWindowOverrideForbidden
		}
		return nil, err
	}
	if actor.Admin {
		return &pro_interfaces.DeploymentWindowOverrideRequest{ActorID: *request.ActorUserID, Authenticated: true, Authorized: true, Category: request.Override.Category, Reference: request.Override.Reference}, nil
	}
	var membership db.ProjectUser
	err := tx.SelectOne(&membership, d.connection.PrepareQuery(d.lockAuthorizationQuery("select project_id, user_id, role, role_id, revision from project__user where project_id=? and user_id=?")), request.ProjectID, *request.ActorUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, pro_interfaces.ErrDeploymentWindowOverrideForbidden
	}
	if err != nil {
		return nil, err
	}
	permissions := membership.Role.GetPermissions()
	if membership.RoleID != nil {
		var role db.Role
		err = tx.SelectOne(&role, d.connection.PrepareQuery(d.lockAuthorizationQuery("select role_id, slug, name, permissions, global_permissions, project_id, revision from role where project_id=? and role_id=?")), request.ProjectID, *membership.RoleID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pro_interfaces.ErrDeploymentWindowOverrideForbidden
		}
		if err != nil {
			return nil, err
		}
		permissions = role.Permissions
	} else if !membership.Role.IsValid() {
		var role db.Role
		err = tx.SelectOne(&role, d.connection.PrepareQuery(d.lockAuthorizationQuery("select role_id, slug, name, permissions, global_permissions, project_id, revision from role where slug=? and (project_id=? or project_id is null) order by case when project_id=? then 0 else 1 end limit 1")), membership.Role, request.ProjectID, request.ProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, pro_interfaces.ErrDeploymentWindowOverrideForbidden
		}
		if err != nil {
			return nil, err
		}
		permissions = role.Permissions
	}
	if !permissions.Can(db.CanOverrideDeploymentWindow) {
		return nil, pro_interfaces.ErrDeploymentWindowOverrideForbidden
	}
	return &pro_interfaces.DeploymentWindowOverrideRequest{ActorID: *request.ActorUserID, Authenticated: true, Authorized: true, Category: request.Override.Category, Reference: request.Override.Reference}, nil
}

func (d *DeploymentWindowStore) lockAuthorizationQuery(query string) string {
	return deploymentWindowLockAuthorizationQuery(d.connection.GetDialect(), query)
}

func deploymentWindowLockAuthorizationQuery(dialect string, query string) string {
	if dialect == util.DbDriverSQLite {
		return query
	}
	return query + " for update"
}

func (d *DeploymentWindowStore) getDecision(tx *gorp.Transaction, request pro_interfaces.DeploymentWindowAdmissionRequest) (db.DeploymentWindowDecisionRecord, bool, error) {
	var record db.DeploymentWindowDecisionRecord
	err := tx.SelectOne(&record, d.connection.PrepareQuery("select * from project__deployment_window_decision where project_id=? and source=? and origin=? and decision_key=?"), request.ProjectID, request.Source, request.Origin, request.DecisionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return db.DeploymentWindowDecisionRecord{}, false, nil
	}
	return record, err == nil, err
}

func deploymentWindowDecisionRecord(request pro_interfaces.DeploymentWindowAdmissionRequest, decision pro_interfaces.DeploymentWindowDecision, now time.Time) (db.DeploymentWindowDecisionRecord, error) {
	matched, err := json.Marshal(decision.Provenance.MatchedRules)
	if err != nil {
		return db.DeploymentWindowDecisionRecord{}, err
	}
	record := db.DeploymentWindowDecisionRecord{
		ProjectID: request.ProjectID, DecisionKey: request.DecisionKey, Source: string(request.Source), Origin: string(request.Origin),
		TemplateID: request.TemplateID, WorkflowTemplateID: request.WorkflowID, ScheduleID: request.ScheduleID, TaskID: request.TaskID,
		WorkflowRunID: request.WorkflowRunID, WorkflowRunNodeID: request.WorkflowRunNodeID, ActorUserID: request.ActorUserID,
		PolicyRevision: decision.Provenance.PolicyRevision, EffectiveTimezone: decision.Provenance.EffectiveTimezone, EvaluatedAt: now,
		State: string(decision.State), Reason: string(decision.Reason), NextEligibleAt: decision.NextEligibleAt, NextEligibleKnown: decision.NextEligibleKnown,
		MatchedRulesJSON: string(matched), Created: now,
	}
	if request.Override != nil && decision.State == pro_interfaces.DeploymentWindowDecisionOverridden {
		category := string(request.Override.Category)
		reference := request.Override.Reference
		record.OverrideActorID, record.OverrideCategory, record.OverrideReference = request.ActorUserID, &category, &reference
	}
	return record, nil
}

func (d *DeploymentWindowStore) insertDecision(tx *gorp.Transaction, record db.DeploymentWindowDecisionRecord) (int, error) {
	return insertDeploymentWindowTx(tx, d.connection, "insert into project__deployment_window_decision(project_id, decision_key, source, origin, template_id, workflow_template_id, schedule_id, task_id, workflow_run_id, workflow_run_node_id, actor_user_id, policy_revision, effective_timezone, evaluated_at, state, reason, next_eligible_at, next_eligible_known, override_actor_user_id, override_category, override_reference, matched_rules, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		deploymentWindowDecisionArgs(record)...)
}

func deploymentWindowDecisionArgs(record db.DeploymentWindowDecisionRecord) []any {
	return []any{
		record.ProjectID, record.DecisionKey, record.Source, record.Origin, record.TemplateID,
		record.WorkflowTemplateID, record.ScheduleID, record.TaskID, record.WorkflowRunID,
		record.WorkflowRunNodeID, record.ActorUserID, record.PolicyRevision, record.EffectiveTimezone,
		record.EvaluatedAt, record.State, record.Reason, record.NextEligibleAt,
		sqlBool(record.NextEligibleKnown), record.OverrideActorID, record.OverrideCategory,
		record.OverrideReference, record.MatchedRulesJSON, record.Created,
	}
}

func insertDeploymentWindowTx(tx *gorp.Transaction, connection *coresql.SqlDbConnection, query string, args ...any) (int, error) {
	prepared := connection.PrepareQuery(query)
	if connection.GetDialect() == util.DbDriverPostgres {
		returnValue, err := tx.SelectInt(prepared+" returning id", args...)
		return int(returnValue), err
	}
	result, err := tx.Exec(prepared, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

func dereferenceDeploymentWindowID(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// deploymentWindowDecisionMatchesRequest prevents a replayed origin key from
// being rebound to another target in the same project. The unique key makes a
// delivery idempotent only for the original admission payload.
func deploymentWindowDecisionMatchesRequest(record db.DeploymentWindowDecisionRecord, request pro_interfaces.DeploymentWindowAdmissionRequest) bool {
	return record.ProjectID == request.ProjectID &&
		deploymentWindowIDsEqual(record.TemplateID, request.TemplateID) &&
		deploymentWindowIDsEqual(record.WorkflowTemplateID, request.WorkflowID) &&
		deploymentWindowIDsEqual(record.ScheduleID, request.ScheduleID) &&
		deploymentWindowIDsEqual(record.TaskID, request.TaskID) &&
		deploymentWindowIDsEqual(record.WorkflowRunID, request.WorkflowRunID) &&
		deploymentWindowIDsEqual(record.WorkflowRunNodeID, request.WorkflowRunNodeID) &&
		deploymentWindowIDsEqual(record.ActorUserID, request.ActorUserID) &&
		deploymentWindowOverrideMatchesRequest(record, request)
}

func deploymentWindowIDsEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func deploymentWindowOverrideMatchesRequest(record db.DeploymentWindowDecisionRecord, request pro_interfaces.DeploymentWindowAdmissionRequest) bool {
	if record.State != string(pro_interfaces.DeploymentWindowDecisionOverridden) {
		return true
	}
	return request.Override != nil && record.OverrideActorID != nil && record.OverrideCategory != nil && record.OverrideReference != nil &&
		*record.OverrideActorID == request.Override.ActorID && *record.OverrideCategory == string(request.Override.Category) &&
		*record.OverrideReference == request.Override.Reference
}
