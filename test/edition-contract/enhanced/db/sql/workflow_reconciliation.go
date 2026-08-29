package sql

import (
	"errors"
	"strings"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// WorkflowReconciliationStore is the SQL source of truth for workflow
// progression ownership. Redis and process-local events are deliberately not
// part of this correctness boundary.
type WorkflowReconciliationStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.WorkflowReconciliationRepository = (*WorkflowReconciliationStore)(nil)

func NewWorkflowReconciliationStore(connection *coresql.SqlDbConnection) *WorkflowReconciliationStore {
	return &WorkflowReconciliationStore{connection: connection}
}

func (s *WorkflowReconciliationStore) ClaimWorkflowReconciliation(projectID int, runID int, ownerBootID string, ttl time.Duration) (pro_interfaces.WorkflowReconciliationLease, bool, error) {
	if err := validateWorkflowReconciliationClaim(s.connection, projectID, runID, ownerBootID, ttl); err != nil {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	expiresAt, ttlSeconds, err := s.leaseExpiryExpression(ttl)
	if err != nil {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	ownerBootID = strings.TrimSpace(ownerBootID)
	for attempt := 0; attempt < 3; attempt++ {
		record, found, getErr := s.get(projectID, runID)
		if getErr != nil {
			return pro_interfaces.WorkflowReconciliationLease{}, false, getErr
		}
		if !found {
			_, insertErr := s.connection.Exec(
				"insert into cluster__workflow_reconciliation(project_id, workflow_run_id, owner_boot_id, fencing_token, lease_expires_at, acquired_at, transfer_count, created, updated) values (?, ?, ?, 1, "+expiresAt+", CURRENT_TIMESTAMP, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
				projectID, runID, ownerBootID, ttlSeconds,
			)
			if insertErr != nil {
				continue
			}
			claimed, ok, readErr := s.get(projectID, runID)
			if readErr != nil || !ok {
				return pro_interfaces.WorkflowReconciliationLease{}, false, readErr
			}
			return workflowReconciliationLeaseFromRecord(claimed), true, nil
		}

		if record.OwnerBootID == ownerBootID {
			renewed, ok, renewErr := s.RenewWorkflowReconciliation(workflowReconciliationLeaseFromRecord(record), ttl)
			if renewErr != nil {
				return pro_interfaces.WorkflowReconciliationLease{}, false, renewErr
			}
			if ok {
				return renewed, true, nil
			}
		}

		nextFence := record.FencingToken + 1
		result, updateErr := s.connection.Exec(
			"update cluster__workflow_reconciliation set previous_owner_boot_id=case when owner_boot_id<>? then owner_boot_id else previous_owner_boot_id end, ownership_transferred_at=case when owner_boot_id<>? then CURRENT_TIMESTAMP else ownership_transferred_at end, transfer_count=transfer_count+case when owner_boot_id<>? then 1 else 0 end, owner_boot_id=?, fencing_token=?, lease_expires_at="+expiresAt+", acquired_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and fencing_token=? and lease_expires_at<=CURRENT_TIMESTAMP",
			ownerBootID, ownerBootID, ownerBootID, ownerBootID, nextFence, ttlSeconds, projectID, runID, record.FencingToken,
		)
		if updateErr != nil {
			return pro_interfaces.WorkflowReconciliationLease{}, false, updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return pro_interfaces.WorkflowReconciliationLease{}, false, rowsErr
		}
		if updated == 1 {
			claimed, ok, readErr := s.get(projectID, runID)
			if readErr != nil || !ok {
				return pro_interfaces.WorkflowReconciliationLease{}, false, readErr
			}
			return workflowReconciliationLeaseFromRecord(claimed), true, nil
		}
		current, ok, readErr := s.get(projectID, runID)
		if readErr != nil {
			return pro_interfaces.WorkflowReconciliationLease{}, false, readErr
		}
		if ok {
			return workflowReconciliationLeaseFromRecord(current), false, nil
		}
	}
	return pro_interfaces.WorkflowReconciliationLease{}, false, errors.New("workflow reconciliation claim conflicted repeatedly")
}

func (s *WorkflowReconciliationStore) RenewWorkflowReconciliation(lease pro_interfaces.WorkflowReconciliationLease, ttl time.Duration) (pro_interfaces.WorkflowReconciliationLease, bool, error) {
	if err := validateWorkflowReconciliationLease(s.connection, lease); err != nil {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	expiresAt, ttlSeconds, err := s.leaseExpiryExpression(ttl)
	if err != nil {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	result, err := s.connection.Exec(
		"update cluster__workflow_reconciliation set lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		ttlSeconds, lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return lease, false, err
	}
	record, found, err := s.get(lease.ProjectID, lease.WorkflowRunID)
	if err != nil || !found {
		return pro_interfaces.WorkflowReconciliationLease{}, false, err
	}
	return workflowReconciliationLeaseFromRecord(record), true, nil
}

func (s *WorkflowReconciliationStore) IsCurrentWorkflowReconciliation(lease pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if err := validateWorkflowReconciliationLease(s.connection, lease); err != nil {
		return false, err
	}
	var count int
	err := s.connection.SelectOne(&count,
		"select count(1) from cluster__workflow_reconciliation where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	return count == 1, err
}

func (s *WorkflowReconciliationStore) ReleaseWorkflowReconciliation(lease pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if err := validateWorkflowReconciliationLease(s.connection, lease); err != nil {
		return false, err
	}
	result, err := s.connection.Exec(
		"update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *WorkflowReconciliationStore) RecordWorkflowReconciled(lease pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if err := validateWorkflowReconciliationLease(s.connection, lease); err != nil {
		return false, err
	}
	result, err := s.connection.Exec(
		"update cluster__workflow_reconciliation set last_reconciled_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and owner_boot_id=? and fencing_token=? and lease_expires_at>CURRENT_TIMESTAMP",
		lease.ProjectID, lease.WorkflowRunID, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *WorkflowReconciliationStore) GetWorkflowReconciliationDiagnostics(projectID int, runID int) (coredb.WorkflowReconciliationDiagnostics, bool, error) {
	projection, err := s.databaseNowProjection()
	if err != nil {
		return coredb.WorkflowReconciliationDiagnostics{}, false, err
	}
	var record workflowReconciliationRecord
	err = s.connection.SelectOne(&record,
		"select cluster__workflow_reconciliation.*, "+projection+" as database_now from cluster__workflow_reconciliation where project_id=? and workflow_run_id=?",
		projectID, runID,
	)
	if errors.Is(err, coredb.ErrNotFound) {
		return coredb.WorkflowReconciliationDiagnostics{}, false, nil
	}
	if err != nil {
		return coredb.WorkflowReconciliationDiagnostics{}, false, err
	}
	return workflowReconciliationDiagnosticsFromRecord(record)
}

func (s *WorkflowReconciliationStore) WorkflowProgressionHealth() (pro_interfaces.WorkflowProgressionHealth, error) {
	projection, err := s.databaseNowProjection()
	if err != nil {
		return pro_interfaces.WorkflowProgressionHealth{}, err
	}
	var records []workflowReconciliationRecord
	if _, err = s.connection.SelectAll(&records,
		"select cluster__workflow_reconciliation.*, "+projection+" as database_now from cluster__workflow_reconciliation join project__workflow_run on project__workflow_run.project_id=cluster__workflow_reconciliation.project_id and project__workflow_run.id=cluster__workflow_reconciliation.workflow_run_id where project__workflow_run.status not in (?, ?, ?, ?, ?, ?) order by cluster__workflow_reconciliation.project_id, cluster__workflow_reconciliation.workflow_run_id",
		coredb.WorkflowRunSucceeded, coredb.WorkflowRunSuccess, coredb.WorkflowRunFailed, coredb.WorkflowRunStopped, coredb.WorkflowRunCanceled, coredb.WorkflowRunBlocked,
	); err != nil {
		return pro_interfaces.WorkflowProgressionHealth{}, err
	}
	health := pro_interfaces.WorkflowProgressionHealth{}
	for _, record := range records {
		diagnostics, _, decodeErr := workflowReconciliationDiagnosticsFromRecord(record)
		if decodeErr != nil {
			return pro_interfaces.WorkflowProgressionHealth{}, decodeErr
		}
		health.ObservedAt, _ = parseTaskControlDatabaseTime(record.DatabaseNow)
		health.TransferCount += record.TransferCount
		if diagnostics.Owned {
			health.CurrentOwnerships++
		} else {
			health.ExpiredOwnerships++
		}
		if diagnostics.ReconciliationLagSeconds > health.MaxLagSeconds {
			health.MaxLagSeconds = diagnostics.ReconciliationLagSeconds
		}
	}
	if len(records) == 0 {
		var databaseNow string
		if err = s.connection.SelectOne(&databaseNow, "select "+projection); err != nil {
			return pro_interfaces.WorkflowProgressionHealth{}, err
		}
		health.ObservedAt, err = parseTaskControlDatabaseTime(databaseNow)
	}
	return health, err
}

func (s *WorkflowReconciliationStore) databaseNowProjection() (string, error) {
	switch s.connection.GetDialect() {
	case "sqlite", "postgres":
		return "cast(CURRENT_TIMESTAMP as varchar(64))", nil
	case "mysql":
		return "cast(CURRENT_TIMESTAMP as char(64))", nil
	default:
		return "", errors.New("workflow reconciliation database dialect is unsupported")
	}
}

func (s *WorkflowReconciliationStore) get(projectID int, runID int) (workflowReconciliationRecord, bool, error) {
	var record workflowReconciliationRecord
	err := s.connection.SelectOne(&record,
		"select * from cluster__workflow_reconciliation where project_id=? and workflow_run_id=?",
		projectID, runID,
	)
	if err == nil {
		return record, true, nil
	}
	if errors.Is(err, coredb.ErrNotFound) {
		return workflowReconciliationRecord{}, false, nil
	}
	return workflowReconciliationRecord{}, false, err
}

func (s *WorkflowReconciliationStore) leaseExpiryExpression(ttl time.Duration) (string, int64, error) {
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
		return "", 0, errors.New("workflow reconciliation database dialect is unsupported")
	}
}

func validateWorkflowReconciliationClaim(connection *coresql.SqlDbConnection, projectID int, runID int, ownerBootID string, ttl time.Duration) error {
	if connection == nil {
		return errors.New("workflow reconciliation database connection is required")
	}
	if projectID <= 0 || runID <= 0 || strings.TrimSpace(ownerBootID) == "" || ttl <= 0 {
		return errors.New("workflow reconciliation ownership is invalid")
	}
	return nil
}

func validateWorkflowReconciliationLease(connection *coresql.SqlDbConnection, lease pro_interfaces.WorkflowReconciliationLease) error {
	if connection == nil {
		return errors.New("workflow reconciliation database connection is required")
	}
	if lease.ProjectID <= 0 || lease.WorkflowRunID <= 0 || strings.TrimSpace(lease.OwnerBootID) == "" || lease.FencingToken <= 0 {
		return errors.New("workflow reconciliation lease is invalid")
	}
	return nil
}

type workflowReconciliationRecord struct {
	ProjectID              int        `db:"project_id"`
	WorkflowRunID          int        `db:"workflow_run_id"`
	OwnerBootID            string     `db:"owner_boot_id"`
	PreviousOwnerBootID    *string    `db:"previous_owner_boot_id"`
	FencingToken           int64      `db:"fencing_token"`
	LeaseExpiresAt         time.Time  `db:"lease_expires_at"`
	AcquiredAt             time.Time  `db:"acquired_at"`
	OwnershipTransferredAt *time.Time `db:"ownership_transferred_at"`
	TransferCount          int        `db:"transfer_count"`
	OperationSequence      int64      `db:"operation_sequence"`
	LastReconciledAt       *time.Time `db:"last_reconciled_at"`
	Created                time.Time  `db:"created"`
	Updated                time.Time  `db:"updated"`
	DatabaseNow            string     `db:"database_now"`
}

func workflowReconciliationLeaseFromRecord(record workflowReconciliationRecord) pro_interfaces.WorkflowReconciliationLease {
	lease := pro_interfaces.WorkflowReconciliationLease{
		ProjectID: record.ProjectID, WorkflowRunID: record.WorkflowRunID,
		OwnerBootID: record.OwnerBootID, FencingToken: record.FencingToken,
		LeaseExpiresAt: record.LeaseExpiresAt, AcquiredAt: record.AcquiredAt,
		OwnershipTransferredAt: record.OwnershipTransferredAt,
		TransferCount:          record.TransferCount, LastReconciledAt: record.LastReconciledAt,
	}
	if record.PreviousOwnerBootID != nil {
		lease.PreviousOwnerBootID = *record.PreviousOwnerBootID
	}
	return lease
}

func workflowReconciliationDiagnosticsFromRecord(record workflowReconciliationRecord) (coredb.WorkflowReconciliationDiagnostics, bool, error) {
	databaseNow, err := parseTaskControlDatabaseTime(record.DatabaseNow)
	if err != nil {
		return coredb.WorkflowReconciliationDiagnostics{}, false, err
	}
	leaseAge := databaseNow.Sub(record.AcquiredAt)
	if leaseAge < 0 {
		leaseAge = 0
	}
	lagFrom := record.AcquiredAt
	if record.LastReconciledAt != nil {
		lagFrom = *record.LastReconciledAt
	}
	lag := databaseNow.Sub(lagFrom)
	if lag < 0 {
		lag = 0
	}
	diagnostics := coredb.WorkflowReconciliationDiagnostics{
		Owned: record.LeaseExpiresAt.After(databaseNow), OwnerBootID: record.OwnerBootID,
		FencingToken: record.FencingToken, LeaseExpiresAt: record.LeaseExpiresAt, AcquiredAt: record.AcquiredAt,
		OwnershipTransferredAt: record.OwnershipTransferredAt, TransferCount: record.TransferCount,
		LastReconciledAt: record.LastReconciledAt, LeaseAgeSeconds: int64(leaseAge / time.Second),
		ReconciliationLagSeconds: int64(lag / time.Second), Recovered: record.TransferCount > 0,
	}
	if record.PreviousOwnerBootID != nil {
		diagnostics.PreviousOwnerBootID = *record.PreviousOwnerBootID
	}
	return diagnostics, true, nil
}
