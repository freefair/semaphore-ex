package sql

import (
	"errors"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type TaskControlStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.TaskControlLeaseRepository = (*TaskControlStore)(nil)
var _ pro_interfaces.TaskControlRecoveryRepository = (*TaskControlStore)(nil)
var _ coredb.TaskExecutionEvidenceRecorder = (*TaskControlStore)(nil)

func NewTaskControlStore(connection *coresql.SqlDbConnection) *TaskControlStore {
	return &TaskControlStore{connection: connection}
}

func (s *TaskControlStore) ClaimTaskControl(taskID int, execution pro_interfaces.TaskExecutionIdentity, ownerBootID string, ttl time.Duration) (pro_interfaces.TaskControlLease, bool, error) {
	if s.connection == nil {
		return pro_interfaces.TaskControlLease{}, false, errors.New("task control database connection is required")
	}
	if taskID <= 0 || execution.RunnerID <= 0 || execution.Generation <= 0 || strings.TrimSpace(execution.StableID) == "" {
		return pro_interfaces.TaskControlLease{}, false, errors.New("task control execution identity is invalid")
	}
	ownerBootID = strings.TrimSpace(ownerBootID)
	if ownerBootID == "" || ttl <= 0 {
		return pro_interfaces.TaskControlLease{}, false, errors.New("task control owner and ttl are required")
	}
	expiresAt, ttlSeconds, err := s.leaseExpiryExpression(ttl)
	if err != nil {
		return pro_interfaces.TaskControlLease{}, false, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		record, found, err := s.get(taskID)
		if err != nil {
			return pro_interfaces.TaskControlLease{}, false, err
		}
		if !found {
			tx, beginErr := s.connection.Begin()
			if beginErr != nil {
				return pro_interfaces.TaskControlLease{}, false, beginErr
			}
			_, err = s.connection.ExecTx(tx,
				"insert into cluster__task_control(task_id, owner_boot_id, fencing_token, lease_expires_at, runner_id, assignment_generation, execution_stable_id, last_observed_at, created, updated) values (?, ?, ?, "+expiresAt+", ?, ?, ?, null, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
				taskID, ownerBootID, 1, ttlSeconds, execution.RunnerID, execution.Generation, execution.StableID,
			)
			if err == nil {
				if err = s.installTaskFenceTx(tx, taskID, 1); err != nil {
					_ = tx.Rollback()
					return pro_interfaces.TaskControlLease{}, false, err
				}
				if err = tx.Commit(); err != nil {
					return pro_interfaces.TaskControlLease{}, false, err
				}
				claimed, found, err := s.get(taskID)
				if err != nil || !found {
					return pro_interfaces.TaskControlLease{}, false, err
				}
				return taskControlLeaseFromRecord(claimed), true, nil
			}
			_ = tx.Rollback()
			continue
		}
		if record.RunnerID != execution.RunnerID || record.AssignmentGeneration != execution.Generation || record.ExecutionStableID != execution.StableID {
			tx, beginErr := s.connection.Begin()
			if beginErr != nil {
				return pro_interfaces.TaskControlLease{}, false, beginErr
			}
			nextFence := record.FencingToken + 1
			result, updateErr := s.connection.ExecTx(tx,
				"update cluster__task_control set previous_owner_boot_id=owner_boot_id, ownership_transferred_at=CURRENT_TIMESTAMP, owner_boot_id=?, fencing_token=?, lease_expires_at="+expiresAt+", runner_id=?, assignment_generation=?, execution_stable_id=?, last_evidence_state=null, last_evidence_status=null, last_observed_at=null, assignment_revoked_at=null, last_recovery_decision=null, last_recovery_reason=null, last_recovery_safe_replacement=0, last_recovery_at=null, updated=CURRENT_TIMESTAMP where task_id=? and fencing_token=? and lease_expires_at<=CURRENT_TIMESTAMP",
				ownerBootID, nextFence, ttlSeconds, execution.RunnerID, execution.Generation, execution.StableID, taskID, record.FencingToken,
			)
			if updateErr != nil {
				_ = tx.Rollback()
				return pro_interfaces.TaskControlLease{}, false, updateErr
			}
			updated, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				_ = tx.Rollback()
				return pro_interfaces.TaskControlLease{}, false, rowsErr
			}
			if updated != 1 {
				_ = tx.Rollback()
				return taskControlLeaseFromRecord(record), false, errors.New("task control execution identity changed")
			}
			if err = s.installTaskFenceTx(tx, taskID, nextFence); err != nil {
				_ = tx.Rollback()
				return pro_interfaces.TaskControlLease{}, false, err
			}
			if err = tx.Commit(); err != nil {
				return pro_interfaces.TaskControlLease{}, false, err
			}
			claimed, found, getErr := s.get(taskID)
			if getErr != nil || !found {
				return pro_interfaces.TaskControlLease{}, false, getErr
			}
			return taskControlLeaseFromRecord(claimed), true, nil
		}
		if record.OwnerBootID == ownerBootID {
			updated, err := s.renew(record, expiresAt, ttlSeconds)
			if err != nil {
				return pro_interfaces.TaskControlLease{}, false, err
			}
			if updated {
				renewed, found, err := s.get(taskID)
				if err != nil || !found {
					return pro_interfaces.TaskControlLease{}, false, err
				}
				return taskControlLeaseFromRecord(renewed), true, nil
			}
			continue
		}
		tx, beginErr := s.connection.Begin()
		if beginErr != nil {
			return pro_interfaces.TaskControlLease{}, false, beginErr
		}
		nextFence := record.FencingToken + 1
		result, err := s.connection.ExecTx(tx,
			"update cluster__task_control set previous_owner_boot_id=owner_boot_id, ownership_transferred_at=CURRENT_TIMESTAMP, owner_boot_id=?, fencing_token=?, lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where task_id=? and fencing_token=? and lease_expires_at<=CURRENT_TIMESTAMP",
			ownerBootID, nextFence, ttlSeconds, taskID, record.FencingToken,
		)
		if err != nil {
			_ = tx.Rollback()
			return pro_interfaces.TaskControlLease{}, false, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return pro_interfaces.TaskControlLease{}, false, err
		}
		if updated == 1 {
			if err = s.installTaskFenceTx(tx, taskID, nextFence); err != nil {
				_ = tx.Rollback()
				return pro_interfaces.TaskControlLease{}, false, err
			}
			if err = tx.Commit(); err != nil {
				return pro_interfaces.TaskControlLease{}, false, err
			}
			claimed, found, err := s.get(taskID)
			if err != nil || !found {
				return pro_interfaces.TaskControlLease{}, false, err
			}
			return taskControlLeaseFromRecord(claimed), true, nil
		}
		_ = tx.Rollback()
		return taskControlLeaseFromRecord(record), false, nil
	}
	return pro_interfaces.TaskControlLease{}, false, errors.New("task control claim conflicted repeatedly")
}

func (s *TaskControlStore) installTaskFenceTx(tx *gorp.Transaction, taskID int, fencingToken int64) error {
	result, err := s.connection.ExecTx(tx,
		"update task set task_control_fencing_token=? where id=?", fencingToken, taskID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 1 {
		return errors.New("task control fence updated more than one task")
	}
	return nil
}

func (s *TaskControlStore) IsCurrentTaskControlLease(lease pro_interfaces.TaskControlLease) (bool, error) {
	if s.connection == nil {
		return false, errors.New("task control database connection is required")
	}
	var row struct {
		Count int `db:"count"`
	}
	err := s.connection.SelectOne(&row,
		"select count(1) as count from cluster__task_control where task_id=? and owner_boot_id=? and fencing_token=? and runner_id=? and assignment_generation=? and execution_stable_id=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.TaskID, lease.OwnerBootID, lease.FencingToken, lease.Execution.RunnerID, lease.Execution.Generation, lease.Execution.StableID,
	)
	return row.Count == 1, err
}

func (s *TaskControlStore) ReleaseTaskControlLease(lease pro_interfaces.TaskControlLease) (bool, error) {
	if s.connection == nil {
		return false, errors.New("task control database connection is required")
	}
	result, err := s.connection.Exec(
		"update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where task_id=? and owner_boot_id=? and fencing_token=? and runner_id=? and assignment_generation=? and execution_stable_id=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.TaskID, lease.OwnerBootID, lease.FencingToken, lease.Execution.RunnerID, lease.Execution.Generation, lease.Execution.StableID,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

// RecordTaskExecutionSnapshot persists a complete, value-free runner snapshot.
// Every controlled execution omitted by the runner is marked absent in the
// same transaction, so recovery never observes a partially applied snapshot.
func (s *TaskControlStore) RecordTaskExecutionSnapshot(runnerID int, evidence []coredb.TaskExecutionEvidence) error {
	if s.connection == nil {
		return errors.New("task control database connection is required")
	}
	if runnerID <= 0 {
		return errors.New("runner identity is invalid")
	}
	if evidence == nil {
		return errors.New("runner execution snapshot must be complete")
	}
	seen := make(map[[2]int]struct{}, len(evidence))
	for _, item := range evidence {
		if item.TaskID <= 0 || item.Generation <= 0 {
			return errors.New("runner execution evidence identity is invalid")
		}
		if item.State != coredb.TaskExecutionEvidenceRunning && item.State != coredb.TaskExecutionEvidenceTerminal {
			return errors.New("runner execution evidence state is invalid")
		}
		key := [2]int{item.TaskID, item.Generation}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("runner execution evidence is duplicated")
		}
		seen[key] = struct{}{}
	}

	tx, err := s.connection.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err = s.connection.ExecTx(tx,
		"update cluster__task_control set last_evidence_state=?, last_evidence_status=null, last_observed_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where runner_id=?",
		coredb.TaskExecutionEvidenceAbsent, runnerID,
	); err != nil {
		return err
	}
	for _, item := range evidence {
		if _, err = s.connection.ExecTx(tx,
			"update cluster__task_control set last_evidence_state=?, last_evidence_status=?, last_observed_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where task_id=? and runner_id=? and assignment_generation=?",
			item.State, item.Status, item.TaskID, runnerID, item.Generation,
		); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *TaskControlStore) ListExpiredTaskControls(limit int) ([]pro_interfaces.TaskControlRecoveryRecord, error) {
	if s.connection == nil {
		return nil, errors.New("task control database connection is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("task control recovery limit is invalid")
	}
	databaseNow, err := s.databaseNowProjection()
	if err != nil {
		return nil, err
	}
	records := make([]taskControlRecord, 0)
	_, err = s.connection.SelectAll(&records,
		"select cluster__task_control.*, "+databaseNow+" as database_now from cluster__task_control where lease_expires_at<=CURRENT_TIMESTAMP order by lease_expires_at, task_id limit ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	result := make([]pro_interfaces.TaskControlRecoveryRecord, 0, len(records))
	for _, record := range records {
		recovery, conversionErr := taskControlRecoveryFromRecord(record)
		if conversionErr != nil {
			return nil, conversionErr
		}
		result = append(result, recovery)
	}
	return result, nil
}

func (s *TaskControlStore) GetTaskControlRecovery(taskID int) (pro_interfaces.TaskControlRecoveryRecord, bool, error) {
	if s.connection == nil {
		return pro_interfaces.TaskControlRecoveryRecord{}, false, errors.New("task control database connection is required")
	}
	if taskID <= 0 {
		return pro_interfaces.TaskControlRecoveryRecord{}, false, errors.New("task control task identity is invalid")
	}
	databaseNow, err := s.databaseNowProjection()
	if err != nil {
		return pro_interfaces.TaskControlRecoveryRecord{}, false, err
	}
	var record taskControlRecord
	err = s.connection.SelectOne(&record,
		"select cluster__task_control.*, "+databaseNow+" as database_now from cluster__task_control where task_id=?", taskID,
	)
	if err == nil {
		recovery, conversionErr := taskControlRecoveryFromRecord(record)
		return recovery, conversionErr == nil, conversionErr
	}
	if errors.Is(err, coredb.ErrNotFound) {
		return pro_interfaces.TaskControlRecoveryRecord{}, false, nil
	}
	return pro_interfaces.TaskControlRecoveryRecord{}, false, err
}

func (s *TaskControlStore) databaseNowProjection() (string, error) {
	switch s.connection.GetDialect() {
	case "mysql":
		return "cast(CURRENT_TIMESTAMP as char(64))", nil
	case "sqlite", "postgres":
		return "cast(CURRENT_TIMESTAMP as varchar(64))", nil
	default:
		return "", errors.New("task control database dialect is unsupported")
	}
}

func (s *TaskControlStore) RecordTaskAssignmentRevoked(lease pro_interfaces.TaskControlLease) (bool, error) {
	if s.connection == nil {
		return false, errors.New("task control database connection is required")
	}
	result, err := s.connection.Exec(
		"update cluster__task_control set assignment_revoked_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where task_id=? and owner_boot_id=? and fencing_token=? and runner_id=? and assignment_generation=? and execution_stable_id=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.TaskID, lease.OwnerBootID, lease.FencingToken, lease.Execution.RunnerID, lease.Execution.Generation, lease.Execution.StableID,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *TaskControlStore) RecordTaskRecoveryDecision(lease pro_interfaces.TaskControlLease, assessment pro_interfaces.TaskRecoveryAssessment) (bool, error) {
	if s.connection == nil {
		return false, errors.New("task control database connection is required")
	}
	reason := strings.TrimSpace(assessment.Reason)
	if reason == "" || len(reason) > 512 {
		return false, errors.New("task recovery reason is invalid")
	}
	switch assessment.Decision {
	case pro_interfaces.TaskRecoveryObserve, pro_interfaces.TaskRecoveryRecover, pro_interfaces.TaskRecoveryQuarantine:
	default:
		return false, errors.New("task recovery decision is invalid")
	}
	if len(assessment.TerminalStatus) > 32 {
		return false, errors.New("task recovery terminal status is invalid")
	}
	safeReplacement := 0
	if assessment.SafeReplacement {
		safeReplacement = 1
	}
	result, err := s.connection.Exec(
		"update cluster__task_control set last_recovery_decision=?, last_recovery_reason=?, last_recovery_safe_replacement=?, last_recovery_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where task_id=? and owner_boot_id=? and fencing_token=? and runner_id=? and assignment_generation=? and execution_stable_id=? and lease_expires_at>CURRENT_TIMESTAMP",
		assessment.Decision, reason, safeReplacement,
		lease.TaskID, lease.OwnerBootID, lease.FencingToken, lease.Execution.RunnerID, lease.Execution.Generation, lease.Execution.StableID,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *TaskControlStore) renew(record taskControlRecord, expiresAt string, ttlSeconds int64) (bool, error) {
	result, err := s.connection.Exec(
		"update cluster__task_control set lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where task_id=? and owner_boot_id=? and fencing_token=? and runner_id=? and assignment_generation=? and execution_stable_id=? and lease_expires_at>CURRENT_TIMESTAMP",
		ttlSeconds, record.TaskID, record.OwnerBootID, record.FencingToken, record.RunnerID, record.AssignmentGeneration, record.ExecutionStableID,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *TaskControlStore) leaseExpiryExpression(ttl time.Duration) (string, int64, error) {
	seconds := int64(ttl.Round(time.Second) / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	switch s.connection.GetDialect() {
	case "sqlite":
		return "DATETIME(CURRENT_TIMESTAMP, '+' || ? || ' seconds')", seconds, nil
	case "mysql":
		return "DATE_ADD(CURRENT_TIMESTAMP, INTERVAL ? SECOND)", seconds, nil
	case "postgres":
		return "CURRENT_TIMESTAMP + (? * INTERVAL '1 second')", seconds, nil
	default:
		return "", 0, errors.New("task control database dialect is unsupported")
	}
}

func (s *TaskControlStore) get(taskID int) (taskControlRecord, bool, error) {
	var record taskControlRecord
	err := s.connection.SelectOne(&record, "select * from cluster__task_control where task_id=?", taskID)
	if err == nil {
		return record, true, nil
	}
	if errors.Is(err, coredb.ErrNotFound) {
		return taskControlRecord{}, false, nil
	}
	return taskControlRecord{}, false, err
}

type taskControlRecord struct {
	TaskID                 int        `db:"task_id"`
	OwnerBootID            string     `db:"owner_boot_id"`
	FencingToken           int64      `db:"fencing_token"`
	LeaseExpiresAt         time.Time  `db:"lease_expires_at"`
	RunnerID               int        `db:"runner_id"`
	AssignmentGeneration   int        `db:"assignment_generation"`
	ExecutionStableID      string     `db:"execution_stable_id"`
	LastEvidenceState      *string    `db:"last_evidence_state"`
	LastEvidenceStatus     *string    `db:"last_evidence_status"`
	LastObservedAt         *time.Time `db:"last_observed_at"`
	PreviousOwnerBootID    *string    `db:"previous_owner_boot_id"`
	OwnershipTransferredAt *time.Time `db:"ownership_transferred_at"`
	AssignmentRevokedAt    *time.Time `db:"assignment_revoked_at"`
	LastRecoveryDecision   *string    `db:"last_recovery_decision"`
	LastRecoveryReason     *string    `db:"last_recovery_reason"`
	LastRecoverySafe       int        `db:"last_recovery_safe_replacement"`
	LastRecoveryAt         *time.Time `db:"last_recovery_at"`
	DatabaseNow            string     `db:"database_now"`
	Created                time.Time  `db:"created"`
	Updated                time.Time  `db:"updated"`
}

func taskControlRecoveryFromRecord(record taskControlRecord) (pro_interfaces.TaskControlRecoveryRecord, error) {
	databaseNow, err := parseTaskControlDatabaseTime(record.DatabaseNow)
	if err != nil {
		return pro_interfaces.TaskControlRecoveryRecord{}, err
	}
	recovery := pro_interfaces.TaskControlRecoveryRecord{
		Lease: taskControlLeaseFromRecord(record), EvidenceObservedAt: record.LastObservedAt,
		OwnershipTransferredAt: record.OwnershipTransferredAt,
		AssignmentRevokedAt:    record.AssignmentRevokedAt, RecoveryDecidedAt: record.LastRecoveryAt,
		DatabaseNow: databaseNow,
		Evidence:    pro_interfaces.TaskExecutionEvidence{State: pro_interfaces.TaskExecutionUnknown},
	}
	if record.PreviousOwnerBootID != nil {
		recovery.PreviousOwnerBootID = *record.PreviousOwnerBootID
	}
	if record.LastEvidenceState != nil {
		recovery.Evidence.State = pro_interfaces.TaskExecutionState(*record.LastEvidenceState)
	}
	if record.LastEvidenceStatus != nil && recovery.Evidence.State == pro_interfaces.TaskExecutionTerminal {
		recovery.Evidence.TerminalStatus = *record.LastEvidenceStatus
	}
	if record.LastRecoveryDecision != nil {
		assessment := pro_interfaces.TaskRecoveryAssessment{
			Decision:        pro_interfaces.TaskRecoveryDecision(*record.LastRecoveryDecision),
			SafeReplacement: record.LastRecoverySafe == 1,
			EvidenceState:   recovery.Evidence.State,
			TerminalStatus:  recovery.Evidence.TerminalStatus,
		}
		if record.LastRecoveryReason != nil {
			assessment.Reason = *record.LastRecoveryReason
		}
		recovery.LastAssessment = &assessment
	}
	return recovery, nil
}

func parseTaskControlDatabaseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("task control database time is invalid")
}

func taskControlLeaseFromRecord(record taskControlRecord) pro_interfaces.TaskControlLease {
	return pro_interfaces.TaskControlLease{
		TaskID: record.TaskID, OwnerBootID: record.OwnerBootID, FencingToken: record.FencingToken, ExpiresAt: record.LeaseExpiresAt,
		Execution: pro_interfaces.TaskExecutionIdentity{RunnerID: record.RunnerID, Generation: record.AssignmentGeneration, StableID: record.ExecutionStableID},
	}
}
