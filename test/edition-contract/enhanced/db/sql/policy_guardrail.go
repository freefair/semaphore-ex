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
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const defaultPolicyGuardrailSource = "version: 1\nrules: []\n"

// PolicyGuardrailStore persists compiler output and immutable admission
// provenance. It intentionally does not parse YAML or invoke a compiler.
type PolicyGuardrailStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.PolicyGuardrailGovernanceRepository = (*PolicyGuardrailStore)(nil)
var _ pro_interfaces.PolicyGuardrailAdmissionRepository = (*PolicyGuardrailStore)(nil)

func NewPolicyGuardrailStore(connection *coresql.SqlDbConnection) *PolicyGuardrailStore {
	return &PolicyGuardrailStore{connection: connection}
}

func (s *PolicyGuardrailStore) GetPolicyGuardrailDraft(scope pro_interfaces.PolicyGuardrailScope, projectID *int) (db.PolicyGuardrailDraft, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil {
		return db.PolicyGuardrailDraft{}, db.ErrInvalidOperation
	}
	if err = s.ensurePolicyProject(normalizedProjectID); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	draft, found, err := s.getDraft(nil, scopeKey, false)
	if err != nil || found {
		return draft, err
	}
	// A missing draft is a virtual, unpersisted first revision. The caller must
	// save it with an actor before it becomes shared mutable state. Use the same
	// database clock as persisted governance state so a DTO never invents time.
	tx, err := s.connection.Begin()
	if err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now, err := s.policyDatabaseNow(tx)
	if err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	return db.PolicyGuardrailDraft{ScopeKey: scopeKey, Scope: db.PolicyGuardrailScope(scope), ProjectID: normalizedProjectID, SourceYAML: defaultPolicyGuardrailSource, Revision: 1, Created: now, Updated: now}, nil
}

func (s *PolicyGuardrailStore) SavePolicyGuardrailDraft(scope pro_interfaces.PolicyGuardrailScope, projectID *int, sourceYAML string, expectedRevision int, actorID int) (db.PolicyGuardrailDraft, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil || expectedRevision <= 0 || actorID <= 0 || len(sourceYAML) > db.MaxPolicyGuardrailSourceBytes {
		return db.PolicyGuardrailDraft{}, db.ErrInvalidOperation
	}
	tx, err := s.connection.Begin()
	if err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockPolicyScope(tx, scopeKey, normalizedProjectID); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	if err = s.ensurePolicyProjectTx(tx, normalizedProjectID); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	if err = s.ensurePolicyActor(tx, actorID); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	draft, found, err := s.getDraft(tx, scopeKey, true)
	if err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	currentRevision := 1
	if found {
		currentRevision = draft.Revision
	}
	if currentRevision != expectedRevision {
		return db.PolicyGuardrailDraft{}, db.ErrPolicyGuardrailDraftRevisionConflict
	}
	nextRevision := currentRevision + 1
	if found {
		result, updateErr := tx.Exec(s.connection.PrepareQuery("update policy_guardrail_draft set source_yaml=?, revision=?, updated_by=?, updated="+policyGuardrailTimestamp(s.connection)+" where scope_key=? and revision=?"), sourceYAML, nextRevision, actorID, scopeKey, currentRevision)
		if updateErr != nil {
			return db.PolicyGuardrailDraft{}, updateErr
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return db.PolicyGuardrailDraft{}, rowsErr
		}
		if affected != 1 {
			return db.PolicyGuardrailDraft{}, db.ErrPolicyGuardrailDraftRevisionConflict
		}
	} else {
		if _, err = tx.Exec(s.connection.PrepareQuery("insert into policy_guardrail_draft(scope_key, scope, project_id, source_yaml, revision, active_revision, updated_by, created, updated) values (?, ?, ?, ?, ?, null, ?, "+policyGuardrailTimestamp(s.connection)+", "+policyGuardrailTimestamp(s.connection)+")"), scopeKey, scope, normalizedProjectID, sourceYAML, nextRevision, actorID); err != nil {
			return db.PolicyGuardrailDraft{}, db.ErrPolicyGuardrailDraftRevisionConflict
		}
	}
	draft, _, err = s.getDraft(tx, scopeKey, false)
	if err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.PolicyGuardrailDraft{}, err
	}
	return draft, nil
}

func (s *PolicyGuardrailStore) PublishPolicyGuardrailRevision(scope pro_interfaces.PolicyGuardrailScope, projectID *int, expectedDraftRevision int, actorID int, sourceYAML, compiledJSON, fingerprint string, compilerVersion int) (db.PolicyGuardrailRevision, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil || expectedDraftRevision <= 0 || actorID <= 0 || compilerVersion <= 0 || len(sourceYAML) == 0 || len(sourceYAML) > db.MaxPolicyGuardrailSourceBytes || len(compiledJSON) == 0 || len(compiledJSON) > db.MaxPolicyGuardrailCompiledBytes || !policyGuardrailFingerprintValid(fingerprint) {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	tx, err := s.connection.Begin()
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockPolicyScope(tx, scopeKey, normalizedProjectID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = s.ensurePolicyProjectTx(tx, normalizedProjectID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = s.ensurePolicyActor(tx, actorID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	draft, found, err := s.getDraft(tx, scopeKey, true)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if !found || draft.Revision != expectedDraftRevision || draft.SourceYAML != sourceYAML {
		return db.PolicyGuardrailRevision{}, db.ErrPolicyGuardrailPublishConflict
	}
	latest, hasLatest, err := s.getLatestRevision(tx, scopeKey, true)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	nextRevision := 1
	var parent *int
	if hasLatest {
		nextRevision, parent = latest.Revision+1, policyGuardrailPointer(latest.Revision)
	}
	now, err := s.policyDatabaseNow(tx)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	revision := db.PolicyGuardrailRevision{ScopeKey: scopeKey, Scope: db.PolicyGuardrailScope(scope), ProjectID: normalizedProjectID, Revision: nextRevision, ParentRevision: parent, SourceYAML: sourceYAML, CompiledJSON: compiledJSON, Fingerprint: fingerprint, CompilerVersion: compilerVersion, PublishedBy: actorID, Created: now}
	id, err := s.insertPolicyRevision(tx, revision)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	revision.ID = id
	if err = s.advanceDraft(tx, scopeKey, expectedDraftRevision, nextRevision, actorID, sourceYAML); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	return revision, nil
}

func (s *PolicyGuardrailStore) RollbackPolicyGuardrailRevision(scope pro_interfaces.PolicyGuardrailScope, projectID *int, rollbackRevision, expectedDraftRevision, actorID int, reason string) (db.PolicyGuardrailRevision, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil || rollbackRevision <= 0 || expectedDraftRevision <= 0 || actorID <= 0 || !policyGuardrailRollbackReasonValid(reason) {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	tx, err := s.connection.Begin()
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockPolicyScope(tx, scopeKey, normalizedProjectID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = s.ensurePolicyProjectTx(tx, normalizedProjectID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = s.ensurePolicyActor(tx, actorID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	draft, found, err := s.getDraft(tx, scopeKey, true)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if !found || draft.Revision != expectedDraftRevision {
		return db.PolicyGuardrailRevision{}, db.ErrPolicyGuardrailPublishConflict
	}
	target, found, err := s.getRevision(tx, scopeKey, rollbackRevision, true)
	if err != nil || !found {
		if err != nil {
			return db.PolicyGuardrailRevision{}, err
		}
		return db.PolicyGuardrailRevision{}, db.ErrNotFound
	}
	latest, hasLatest, err := s.getLatestRevision(tx, scopeKey, true)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if !hasLatest {
		return db.PolicyGuardrailRevision{}, db.ErrPolicyGuardrailPublishConflict
	}
	now, err := s.policyDatabaseNow(tx)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	nextRevision := latest.Revision + 1
	revision := db.PolicyGuardrailRevision{ScopeKey: scopeKey, Scope: db.PolicyGuardrailScope(scope), ProjectID: normalizedProjectID, Revision: nextRevision, ParentRevision: policyGuardrailPointer(latest.Revision), RollbackOfRevision: policyGuardrailPointer(target.Revision), RollbackReason: reason, SourceYAML: target.SourceYAML, CompiledJSON: target.CompiledJSON, Fingerprint: target.Fingerprint, CompilerVersion: target.CompilerVersion, PublishedBy: actorID, Created: now}
	id, err := s.insertPolicyRevision(tx, revision)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	revision.ID = id
	if err = s.advanceDraft(tx, scopeKey, expectedDraftRevision, nextRevision, actorID, target.SourceYAML); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	return revision, nil
}

func (s *PolicyGuardrailStore) GetPolicyGuardrailRevision(scope pro_interfaces.PolicyGuardrailScope, projectID *int, revision int) (db.PolicyGuardrailRevision, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil || revision <= 0 {
		return db.PolicyGuardrailRevision{}, db.ErrInvalidOperation
	}
	if err = s.ensurePolicyProject(normalizedProjectID); err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	value, found, err := s.getRevision(nil, scopeKey, revision, false)
	if err != nil {
		return db.PolicyGuardrailRevision{}, err
	}
	if !found {
		return db.PolicyGuardrailRevision{}, db.ErrNotFound
	}
	return value, nil
}

func (s *PolicyGuardrailStore) GetPolicyGuardrailRevisions(scope pro_interfaces.PolicyGuardrailScope, projectID *int, params db.RetrieveQueryParams) ([]db.PolicyGuardrailRevision, error) {
	scopeKey, normalizedProjectID, err := s.policyScope(scope, projectID)
	if err != nil || s == nil || s.connection == nil || !policyGuardrailPageValid(params) {
		return nil, db.ErrInvalidOperation
	}
	if err = s.ensurePolicyProject(normalizedProjectID); err != nil {
		return nil, err
	}
	query, args := policyGuardrailHistoryQuery("select * from policy_guardrail_revision where scope_key=?", []any{scopeKey}, params)
	var values []db.PolicyGuardrailRevision
	if _, err = s.connection.SelectAll(&values, query, args...); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *PolicyGuardrailStore) GetPolicyGuardrailEvaluationHistory(projectID *int, params db.RetrieveQueryParams) ([]db.PolicyGuardrailEvaluationRecord, error) {
	if s == nil || s.connection == nil || projectID == nil || *projectID <= 0 || !policyGuardrailPageValid(params) {
		return nil, db.ErrInvalidOperation
	}
	if err := s.ensurePolicyProject(projectID); err != nil {
		return nil, err
	}
	query, args := policyGuardrailHistoryQuery("select * from policy_guardrail_evaluation where project_id=?", []any{*projectID}, params)
	var values []db.PolicyGuardrailEvaluationRecord
	if _, err := s.connection.SelectAll(&values, query, args...); err != nil {
		return nil, err
	}
	for _, value := range values {
		if _, err := policyGuardrailEvaluationFromRecord(value); err != nil {
			return nil, db.ErrInvalidOperation
		}
	}
	return values, nil
}

func (s *PolicyGuardrailStore) PreviewPolicyGuardrails(input pro_interfaces.PolicyGuardrailEvaluationInput, evaluate func([]db.PolicyGuardrailRevision, pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error)) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if s == nil || s.connection == nil || evaluate == nil || !policyGuardrailInputTargetValid(input) {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	tx, err := s.connection.Begin()
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockPolicyScope(tx, "global", nil); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	if err = s.lockPolicyScope(tx, policyGuardrailProjectKey(input.ProjectID), policyGuardrailPointer(input.ProjectID)); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	revisions, now, err := s.activePolicyGuardrailRevisions(tx, input.ProjectID)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	input.EvaluatedAt = now
	evaluation, err := evaluate(revisions, input)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	if err = validatePolicyGuardrailEvaluation(revisions, input, evaluation); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	if err = tx.Commit(); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, err
	}
	return evaluation, nil
}

func (s *PolicyGuardrailStore) ClaimPolicyGuardrailEvaluation(request pro_interfaces.PolicyGuardrailAdmissionRequest, evaluate func([]db.PolicyGuardrailRevision, pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error)) (pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	claims, err := s.ClaimPolicyGuardrailEvaluations([]pro_interfaces.PolicyGuardrailAdmissionRequest{request}, evaluate)
	if err != nil {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, err
	}
	if len(claims) != 1 {
		return pro_interfaces.PolicyGuardrailEvaluationClaim{}, db.ErrInvalidOperation
	}
	return claims[0], nil
}

// ClaimPolicyGuardrailEvaluations captures one active revision set and database
// timestamp for a single workflow admission. A batch is either a complete
// replay, or entirely new; mixed replay/new batches are rejected so a retry
// cannot create only a suffix of its original admission decision.
func (s *PolicyGuardrailStore) ClaimPolicyGuardrailEvaluations(requests []pro_interfaces.PolicyGuardrailAdmissionRequest, evaluate func([]db.PolicyGuardrailRevision, pro_interfaces.PolicyGuardrailEvaluationInput) (pro_interfaces.PolicyGuardrailEvaluation, error)) ([]pro_interfaces.PolicyGuardrailEvaluationClaim, error) {
	if s == nil || s.connection == nil || evaluate == nil || len(requests) == 0 || len(requests) > pro_interfaces.MaxPolicyGuardrailAdmissionBatch {
		return nil, db.ErrInvalidOperation
	}
	projectID := requests[0].Input.ProjectID
	fingerprints := make([]string, len(requests))
	keys := make(map[string]struct{}, len(requests))
	for index, request := range requests {
		if !policyGuardrailInputTargetValid(request.Input) || !policyGuardrailAdmissionRequestValid(request) || request.Input.ProjectID != projectID {
			return nil, db.ErrInvalidOperation
		}
		key := request.Source + "\x00" + request.DecisionKey
		if _, duplicate := keys[key]; duplicate {
			return nil, db.ErrInvalidOperation
		}
		keys[key] = struct{}{}
		fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(request.Input)
		if err != nil {
			return nil, err
		}
		fingerprints[index] = fingerprint
	}
	tx, err := s.connection.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.lockPolicyScope(tx, "global", nil); err != nil {
		return nil, err
	}
	if err = s.lockPolicyScope(tx, policyGuardrailProjectKey(projectID), policyGuardrailPointer(projectID)); err != nil {
		return nil, err
	}
	revisions, now, err := s.activePolicyGuardrailRevisions(tx, projectID)
	if err != nil {
		return nil, err
	}

	claims := make([]pro_interfaces.PolicyGuardrailEvaluationClaim, len(requests))
	existingCount := 0
	for index, request := range requests {
		record, found, getErr := s.getPolicyEvaluation(tx, projectID, request.Source, request.DecisionKey)
		if getErr != nil {
			return nil, getErr
		}
		if !found {
			continue
		}
		existingCount++
		if !policyGuardrailRecordMatchesRequest(record, request, fingerprints[index]) {
			return nil, db.ErrInvalidOperation
		}
		evaluation, decodeErr := policyGuardrailEvaluationFromRecord(record)
		if decodeErr != nil || !policyGuardrailRevisionRefsMatchActive(evaluation.Revisions, revisions) {
			return nil, db.ErrInvalidOperation
		}
		claims[index] = pro_interfaces.PolicyGuardrailEvaluationClaim{Record: record, Evaluation: evaluation}
	}
	if existingCount != 0 {
		if existingCount != len(requests) {
			return nil, db.ErrInvalidOperation
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return claims, nil
	}

	actors := make(map[int]struct{}, len(requests))
	for _, request := range requests {
		if request.ActorUserID != nil {
			actors[*request.ActorUserID] = struct{}{}
		}
	}
	for actorID := range actors {
		if err = s.ensurePolicyActor(tx, actorID); err != nil {
			return nil, err
		}
	}
	for index, request := range requests {
		input := request.Input
		input.EvaluatedAt = now
		evaluation, evaluateErr := evaluate(revisions, input)
		if evaluateErr != nil {
			return nil, evaluateErr
		}
		if validateErr := validatePolicyGuardrailEvaluation(revisions, input, evaluation); validateErr != nil {
			return nil, validateErr
		}
		record, recordErr := policyGuardrailRecord(request, fingerprints[index], evaluation, now)
		if recordErr != nil {
			return nil, recordErr
		}
		claims[index] = pro_interfaces.PolicyGuardrailEvaluationClaim{Record: record, Evaluation: evaluation, Inserted: true}
	}
	for index := range claims {
		id, insertErr := s.insertPolicyEvaluation(tx, claims[index].Record)
		if insertErr != nil {
			return nil, insertErr
		}
		claims[index].Record.ID = id
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *PolicyGuardrailStore) policyScope(scope pro_interfaces.PolicyGuardrailScope, projectID *int) (string, *int, error) {
	if scope != pro_interfaces.PolicyGuardrailScopeGlobal && scope != pro_interfaces.PolicyGuardrailScopeProject {
		return "", nil, db.ErrInvalidOperation
	}
	if err := db.ValidatePolicyGuardrailScope(db.PolicyGuardrailScope(scope), projectID); err != nil {
		return "", nil, err
	}
	if projectID == nil {
		return "global", nil, nil
	}
	value := *projectID
	return policyGuardrailProjectKey(value), &value, nil
}

func policyGuardrailProjectKey(projectID int) string { return fmt.Sprintf("project:%d", projectID) }
func policyGuardrailPointer(value int) *int          { return &value }

func (s *PolicyGuardrailStore) ensurePolicyProject(projectID *int) error {
	if projectID == nil {
		return nil
	}
	var count int
	if err := s.connection.SelectOne(&count, "select count(*) from project where id=?", *projectID); err != nil {
		return err
	}
	if count != 1 {
		return db.ErrNotFound
	}
	return nil
}

func (s *PolicyGuardrailStore) ensurePolicyProjectTx(tx *gorp.Transaction, projectID *int) error {
	if projectID == nil {
		return nil
	}
	query := "select id from project where id=?"
	if s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var id int
	if err := tx.SelectOne(&id, s.connection.PrepareQuery(query), *projectID); errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	} else {
		return err
	}
}

func (s *PolicyGuardrailStore) ensurePolicyActor(tx *gorp.Transaction, actorID int) error {
	if actorID <= 0 {
		return db.ErrInvalidOperation
	}
	var id int
	if err := tx.SelectOne(&id, s.connection.PrepareQuery("select id from `user` where id=?"), actorID); errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	} else {
		return err
	}
}

// lockPolicyScope locks project rows before their matching draft row. For the
// global scope the draft row is the serialization point once governance has
// been initialized; SQLite serializes the write transaction itself.
func (s *PolicyGuardrailStore) lockPolicyScope(tx *gorp.Transaction, scopeKey string, projectID *int) error {
	if err := s.ensurePolicyProjectTx(tx, projectID); err != nil {
		return err
	}
	query := "select scope_key from policy_guardrail_draft where scope_key=?"
	if s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var value string
	err := tx.SelectOne(&value, s.connection.PrepareQuery(query), scopeKey)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return nil
	}
	return err
}

func (s *PolicyGuardrailStore) getDraft(tx *gorp.Transaction, scopeKey string, lock bool) (db.PolicyGuardrailDraft, bool, error) {
	query := "select * from policy_guardrail_draft where scope_key=?"
	if lock && s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var draft db.PolicyGuardrailDraft
	var err error
	if tx == nil {
		err = s.connection.SelectOne(&draft, query, scopeKey)
	} else {
		err = tx.SelectOne(&draft, s.connection.PrepareQuery(query), scopeKey)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.PolicyGuardrailDraft{}, false, nil
	}
	if err != nil {
		return db.PolicyGuardrailDraft{}, false, err
	}
	if draft.Validate() != nil {
		return db.PolicyGuardrailDraft{}, false, db.ErrInvalidOperation
	}
	return draft, true, nil
}

func (s *PolicyGuardrailStore) getRevision(tx *gorp.Transaction, scopeKey string, revision int, lock bool) (db.PolicyGuardrailRevision, bool, error) {
	query := "select * from policy_guardrail_revision where scope_key=? and revision=?"
	if lock && s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var value db.PolicyGuardrailRevision
	var err error
	if tx == nil {
		err = s.connection.SelectOne(&value, query, scopeKey, revision)
	} else {
		err = tx.SelectOne(&value, s.connection.PrepareQuery(query), scopeKey, revision)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.PolicyGuardrailRevision{}, false, nil
	}
	if err != nil {
		return db.PolicyGuardrailRevision{}, false, err
	}
	if value.Validate() != nil {
		return db.PolicyGuardrailRevision{}, false, db.ErrInvalidOperation
	}
	return value, true, nil
}

func (s *PolicyGuardrailStore) getLatestRevision(tx *gorp.Transaction, scopeKey string, lock bool) (db.PolicyGuardrailRevision, bool, error) {
	query := "select * from policy_guardrail_revision where scope_key=? order by revision desc limit 1"
	if lock && s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var value db.PolicyGuardrailRevision
	err := tx.SelectOne(&value, s.connection.PrepareQuery(query), scopeKey)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return db.PolicyGuardrailRevision{}, false, nil
	}
	if err != nil {
		return db.PolicyGuardrailRevision{}, false, err
	}
	if value.Validate() != nil {
		return db.PolicyGuardrailRevision{}, false, db.ErrInvalidOperation
	}
	return value, true, nil
}

func (s *PolicyGuardrailStore) advanceDraft(tx *gorp.Transaction, scopeKey string, expectedRevision, activeRevision, actorID int, sourceYAML string) error {
	result, err := tx.Exec(s.connection.PrepareQuery("update policy_guardrail_draft set source_yaml=?, revision=?, active_revision=?, updated_by=?, updated="+policyGuardrailTimestamp(s.connection)+" where scope_key=? and revision=?"), sourceYAML, expectedRevision+1, activeRevision, actorID, scopeKey, expectedRevision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return db.ErrPolicyGuardrailPublishConflict
	}
	return nil
}

func (s *PolicyGuardrailStore) insertPolicyRevision(tx *gorp.Transaction, revision db.PolicyGuardrailRevision) (int, error) {
	query := "insert into policy_guardrail_revision(scope_key, scope, project_id, revision, parent_revision, rollback_of_revision, rollback_reason, source_yaml, compiled_json, fingerprint, compiler_version, published_by, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	return policyGuardrailInsertID(tx, s.connection, query, revision.ScopeKey, revision.Scope, revision.ProjectID, revision.Revision, revision.ParentRevision, revision.RollbackOfRevision, revision.RollbackReason, revision.SourceYAML, revision.CompiledJSON, revision.Fingerprint, revision.CompilerVersion, revision.PublishedBy, revision.Created)
}

func (s *PolicyGuardrailStore) activePolicyGuardrailRevisions(tx *gorp.Transaction, projectID int) ([]db.PolicyGuardrailRevision, time.Time, error) {
	if err := s.ensurePolicyProjectTx(tx, policyGuardrailPointer(projectID)); err != nil {
		return nil, time.Time{}, err
	}
	now, err := s.policyDatabaseNow(tx)
	if err != nil {
		return nil, time.Time{}, err
	}
	values := make([]db.PolicyGuardrailRevision, 0, 2)
	for _, scope := range []struct {
		key       string
		projectID *int
	}{{"global", nil}, {policyGuardrailProjectKey(projectID), policyGuardrailPointer(projectID)}} {
		draft, found, draftErr := s.getDraft(tx, scope.key, true)
		if draftErr != nil {
			return nil, time.Time{}, draftErr
		}
		if !found || draft.ActiveRevision == nil {
			continue
		}
		revision, revisionFound, revisionErr := s.getRevision(tx, scope.key, *draft.ActiveRevision, true)
		if revisionErr != nil {
			return nil, time.Time{}, revisionErr
		}
		if !revisionFound {
			return nil, time.Time{}, db.ErrInvalidOperation
		}
		values = append(values, revision)
	}
	return values, now, nil
}

func (s *PolicyGuardrailStore) policyDatabaseNow(tx *gorp.Transaction) (time.Time, error) {
	value, err := tx.SelectStr(s.connection.PrepareQuery("select " + policyGuardrailTimestamp(s.connection)))
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if parsed, parseErr := time.Parse(layout, value); parseErr == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid database timestamp")
}

func policyGuardrailTimestamp(connection *coresql.SqlDbConnection) string {
	switch connection.GetDialect() {
	case util.DbDriverPostgres:
		return "(CURRENT_TIMESTAMP AT TIME ZONE 'UTC')"
	case util.DbDriverMySQL:
		return "UTC_TIMESTAMP()"
	default:
		return "CURRENT_TIMESTAMP"
	}
}

func policyGuardrailInsertID(tx *gorp.Transaction, connection *coresql.SqlDbConnection, query string, args ...any) (int, error) {
	prepared := connection.PrepareQuery(query)
	if connection.GetDialect() == util.DbDriverPostgres {
		value, err := tx.SelectInt(prepared+" returning id", args...)
		return int(value), err
	}
	result, err := tx.Exec(prepared, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

func policyGuardrailHistoryQuery(base string, args []any, params db.RetrieveQueryParams) (string, []any) {
	if params.BeforeID > 0 {
		base += " and id < ?"
		args = append(args, params.BeforeID)
	}
	base += " order by id desc limit ?"
	count := params.Count
	if count <= 0 || count > db.MaxPolicyGuardrailHistoryPage {
		count = db.MaxPolicyGuardrailHistoryPage
	}
	args = append(args, count)
	if params.Offset > 0 {
		base += " offset ?"
		args = append(args, params.Offset)
	}
	return base, args
}

func policyGuardrailPageValid(params db.RetrieveQueryParams) bool {
	return params.Offset >= 0 && params.BeforeID >= 0 && params.Count >= 0
}
func policyGuardrailFingerprintValid(value string) bool {
	return len(value) == len("sha256:")+64 && strings.HasPrefix(value, "sha256:") && strings.Trim(value[7:], "0123456789abcdef") == ""
}
func policyGuardrailRollbackReasonValid(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value && len(value) <= db.MaxPolicyGuardrailRollbackReasonBytes && !strings.ContainsAny(value, "\x00\r\n")
}

func policyGuardrailAdmissionRequestValid(request pro_interfaces.PolicyGuardrailAdmissionRequest) bool {
	return strings.TrimSpace(request.DecisionKey) != "" && len(request.DecisionKey) <= db.MaxPolicyGuardrailDecisionKeyBytes &&
		strings.TrimSpace(request.Source) != "" && len(request.Source) <= 32 && !strings.ContainsAny(request.DecisionKey+request.Source, "\x00\r\n") &&
		(request.ActorUserID == nil || *request.ActorUserID > 0)
}

func policyGuardrailInputTargetValid(input pro_interfaces.PolicyGuardrailEvaluationInput) bool {
	if input.Validate() != nil {
		return false
	}
	if input.Intent == pro_interfaces.ExecutionPreflightTask {
		return input.Template != nil
	}
	if input.Intent == pro_interfaces.ExecutionPreflightWorkflow {
		return input.Workflow != nil
	}
	return false
}

func validatePolicyGuardrailEvaluation(revisions []db.PolicyGuardrailRevision, input pro_interfaces.PolicyGuardrailEvaluationInput, evaluation pro_interfaces.PolicyGuardrailEvaluation) error {
	fingerprint, err := pro_interfaces.FingerprintPolicyGuardrailInput(input)
	if err != nil || evaluation.InputFingerprint != fingerprint || !evaluation.EvaluatedAt.Equal(input.EvaluatedAt) || len(evaluation.Revisions) != len(revisions) || len(evaluation.Findings) > pro_interfaces.MaxPolicyGuardrailFindings {
		return db.ErrInvalidOperation
	}
	for index, revision := range revisions {
		ref := evaluation.Revisions[index]
		if ref.Scope != pro_interfaces.PolicyGuardrailScope(revision.Scope) || ref.Revision != revision.Revision || ref.Fingerprint != revision.Fingerprint ||
			(ref.Scope == pro_interfaces.PolicyGuardrailScopeGlobal && ref.ProjectID != nil) ||
			(ref.Scope == pro_interfaces.PolicyGuardrailScopeProject && (ref.ProjectID == nil || revision.ProjectID == nil || *ref.ProjectID != *revision.ProjectID)) {
			return db.ErrInvalidOperation
		}
	}
	denied := false
	for _, finding := range evaluation.Findings {
		if !policyGuardrailFindingMatchesRevision(finding, evaluation.Revisions) {
			return db.ErrInvalidOperation
		}
		if finding.Effect == pro_interfaces.PolicyGuardrailEffectDeny {
			denied = true
		}
	}
	if evaluation.Allowed == denied {
		return db.ErrInvalidOperation
	}
	return nil
}

func policyGuardrailFindingMatchesRevision(finding pro_interfaces.PolicyGuardrailFinding, revisions []pro_interfaces.PolicyGuardrailRevisionRef) bool {
	if finding.RuleID == "" || len(finding.RuleID) > 63 || strings.TrimSpace(finding.Message) == "" || len(finding.Message) > pro_interfaces.MaxPolicyGuardrailMessageBytes ||
		(finding.Effect != pro_interfaces.PolicyGuardrailEffectAllow && finding.Effect != pro_interfaces.PolicyGuardrailEffectWarn && finding.Effect != pro_interfaces.PolicyGuardrailEffectDeny) ||
		(finding.Severity != pro_interfaces.PolicyGuardrailSeverityInfo && finding.Severity != pro_interfaces.PolicyGuardrailSeverityLow && finding.Severity != pro_interfaces.PolicyGuardrailSeverityMedium && finding.Severity != pro_interfaces.PolicyGuardrailSeverityHigh && finding.Severity != pro_interfaces.PolicyGuardrailSeverityCritical) ||
		len(finding.RemediationURL) > pro_interfaces.MaxPolicyGuardrailRemediationBytes || strings.ContainsAny(finding.Message+finding.RemediationURL, "\x00\r\n") {
		return false
	}
	for _, revision := range revisions {
		if finding.Scope == revision.Scope && finding.Revision == revision.Revision {
			return true
		}
	}
	return false
}

func policyGuardrailRecord(request pro_interfaces.PolicyGuardrailAdmissionRequest, inputFingerprint string, evaluation pro_interfaces.PolicyGuardrailEvaluation, now time.Time) (db.PolicyGuardrailEvaluationRecord, error) {
	revisions, err := json.Marshal(evaluation.Revisions)
	if err != nil {
		return db.PolicyGuardrailEvaluationRecord{}, err
	}
	findings, err := json.Marshal(evaluation.Findings)
	if err != nil {
		return db.PolicyGuardrailEvaluationRecord{}, err
	}
	if len(revisions) > db.MaxPolicyGuardrailEvaluationJSONBytes || len(findings) > db.MaxPolicyGuardrailEvaluationJSONBytes {
		return db.PolicyGuardrailEvaluationRecord{}, db.ErrInvalidOperation
	}
	record := db.PolicyGuardrailEvaluationRecord{ProjectID: request.Input.ProjectID, DecisionKey: request.DecisionKey, Intent: string(request.Input.Intent), Source: request.Source, ActorUserID: request.ActorUserID, InputFingerprint: inputFingerprint, RevisionsJSON: string(revisions), FindingsJSON: string(findings), EvaluatedAt: now, Created: now, Decision: db.PolicyGuardrailDecisionAllow}
	if request.Input.Intent == pro_interfaces.ExecutionPreflightTask {
		record.TemplateID = policyGuardrailPointer(request.Input.Template.ID)
	} else {
		record.WorkflowTemplateID = policyGuardrailPointer(request.Input.Workflow.ID)
	}
	if !evaluation.Allowed {
		record.Decision = db.PolicyGuardrailDecisionDeny
	}
	return record, nil
}

func (s *PolicyGuardrailStore) insertPolicyEvaluation(tx *gorp.Transaction, record db.PolicyGuardrailEvaluationRecord) (int, error) {
	query := "insert into policy_guardrail_evaluation(project_id, decision_key, intent, source, template_id, workflow_template_id, workflow_run_id, workflow_run_node_id, task_id, actor_user_id, input_fingerprint, revisions_json, findings_json, decision, evaluated_at, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	return policyGuardrailInsertID(tx, s.connection, query, record.ProjectID, record.DecisionKey, record.Intent, record.Source, record.TemplateID, record.WorkflowTemplateID, record.WorkflowRunID, record.WorkflowRunNodeID, record.TaskID, record.ActorUserID, record.InputFingerprint, record.RevisionsJSON, record.FindingsJSON, record.Decision, record.EvaluatedAt, record.Created)
}

func (s *PolicyGuardrailStore) getPolicyEvaluation(tx *gorp.Transaction, projectID int, source, decisionKey string) (db.PolicyGuardrailEvaluationRecord, bool, error) {
	query := "select * from policy_guardrail_evaluation where project_id=? and source=? and decision_key=?"
	if s.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	var record db.PolicyGuardrailEvaluationRecord
	err := tx.SelectOne(&record, s.connection.PrepareQuery(query), projectID, source, decisionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return db.PolicyGuardrailEvaluationRecord{}, false, nil
	}
	if err != nil {
		return db.PolicyGuardrailEvaluationRecord{}, false, err
	}
	if record.Validate() != nil {
		return db.PolicyGuardrailEvaluationRecord{}, false, db.ErrInvalidOperation
	}
	return record, true, nil
}

func policyGuardrailRecordMatchesRequest(record db.PolicyGuardrailEvaluationRecord, request pro_interfaces.PolicyGuardrailAdmissionRequest, inputFingerprint string) bool {
	if record.ProjectID != request.Input.ProjectID || record.Intent != string(request.Input.Intent) || record.Source != request.Source || record.InputFingerprint != inputFingerprint || !policyGuardrailIDsEqual(record.ActorUserID, request.ActorUserID) {
		return false
	}
	if request.Input.Intent == pro_interfaces.ExecutionPreflightTask {
		return request.Input.Template != nil && policyGuardrailIDsEqual(record.TemplateID, policyGuardrailPointer(request.Input.Template.ID))
	}
	return request.Input.Workflow != nil && policyGuardrailIDsEqual(record.WorkflowTemplateID, policyGuardrailPointer(request.Input.Workflow.ID))
}

func policyGuardrailIDsEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func policyGuardrailEvaluationFromRecord(record db.PolicyGuardrailEvaluationRecord) (pro_interfaces.PolicyGuardrailEvaluation, error) {
	if record.Validate() != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	var revisions []pro_interfaces.PolicyGuardrailRevisionRef
	var findings []pro_interfaces.PolicyGuardrailFinding
	if err := json.Unmarshal([]byte(record.RevisionsJSON), &revisions); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	if err := json.Unmarshal([]byte(record.FindingsJSON), &findings); err != nil {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	if !policyGuardrailStoredRevisionRefsValid(revisions) {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	evaluation := pro_interfaces.PolicyGuardrailEvaluation{Revisions: revisions, Findings: findings, Allowed: record.Decision == db.PolicyGuardrailDecisionAllow, InputFingerprint: record.InputFingerprint, EvaluatedAt: record.EvaluatedAt}
	denied := false
	for _, finding := range findings {
		if !policyGuardrailFindingMatchesRevision(finding, revisions) {
			return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
		}
		denied = denied || finding.Effect == pro_interfaces.PolicyGuardrailEffectDeny
	}
	if evaluation.Allowed == denied {
		return pro_interfaces.PolicyGuardrailEvaluation{}, db.ErrInvalidOperation
	}
	return evaluation, nil
}

func policyGuardrailStoredRevisionRefsValid(revisions []pro_interfaces.PolicyGuardrailRevisionRef) bool {
	seenGlobal, seenProject := false, false
	for _, revision := range revisions {
		if revision.Revision <= 0 || !policyGuardrailFingerprintValid(revision.Fingerprint) {
			return false
		}
		switch revision.Scope {
		case pro_interfaces.PolicyGuardrailScopeGlobal:
			if seenGlobal || seenProject || revision.ProjectID != nil {
				return false
			}
			seenGlobal = true
		case pro_interfaces.PolicyGuardrailScopeProject:
			if seenProject || revision.ProjectID == nil || *revision.ProjectID <= 0 {
				return false
			}
			seenProject = true
		default:
			return false
		}
	}
	return true
}

func policyGuardrailRevisionRefsMatchActive(refs []pro_interfaces.PolicyGuardrailRevisionRef, active []db.PolicyGuardrailRevision) bool {
	if len(refs) != len(active) {
		return false
	}
	for index, revision := range active {
		ref := refs[index]
		if ref.Scope != pro_interfaces.PolicyGuardrailScope(revision.Scope) || ref.Revision != revision.Revision || ref.Fingerprint != revision.Fingerprint {
			return false
		}
		if revision.ProjectID == nil {
			if ref.ProjectID != nil {
				return false
			}
		} else if ref.ProjectID == nil || *ref.ProjectID != *revision.ProjectID {
			return false
		}
	}
	return true
}
