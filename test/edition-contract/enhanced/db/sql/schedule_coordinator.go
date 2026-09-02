package sql

import (
	"errors"
	"strings"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type ScheduleCoordinatorStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.ScheduleOccurrenceLeaseRepository = (*ScheduleCoordinatorStore)(nil)

func NewScheduleCoordinatorStore(connection *coresql.SqlDbConnection) *ScheduleCoordinatorStore {
	return &ScheduleCoordinatorStore{connection: connection}
}

func (s *ScheduleCoordinatorStore) ClaimScheduleOccurrence(
	occurrence pro_interfaces.ScheduleOccurrence,
	ownerBootID string,
	ttl time.Duration,
) (pro_interfaces.ScheduleOccurrenceLease, bool, error) {
	if s.connection == nil {
		return pro_interfaces.ScheduleOccurrenceLease{}, false, errors.New("schedule coordinator database connection is required")
	}
	if occurrence.Key == "" || occurrence.ScheduleID <= 0 || occurrence.Revision == "" || occurrence.IntendedAt.IsZero() {
		return pro_interfaces.ScheduleOccurrenceLease{}, false, errors.New("schedule occurrence is invalid")
	}
	ownerBootID = strings.TrimSpace(ownerBootID)
	if ownerBootID == "" || ttl <= 0 {
		return pro_interfaces.ScheduleOccurrenceLease{}, false, errors.New("schedule lease owner and ttl are required")
	}
	expiresAt, ttlSeconds, err := s.leaseExpiryExpression(ttl)
	if err != nil {
		return pro_interfaces.ScheduleOccurrenceLease{}, false, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		if taskID, exists, err := s.taskForOccurrence(occurrence.Key); err != nil {
			return pro_interfaces.ScheduleOccurrenceLease{}, false, err
		} else if exists {
			if _, err = s.connection.Exec(
				"update cluster__schedule_occurrence set task_id=?, completed_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where occurrence_key=? and task_id is null",
				taskID, occurrence.Key,
			); err != nil {
				return pro_interfaces.ScheduleOccurrenceLease{}, false, err
			}
			record, found, err := s.get(occurrence.Key)
			if err != nil || !found {
				return pro_interfaces.ScheduleOccurrenceLease{}, false, err
			}
			return leaseFromRecord(record), false, nil
		}
		record, found, err := s.get(occurrence.Key)
		if err != nil {
			return pro_interfaces.ScheduleOccurrenceLease{}, false, err
		}
		if !found {
			_, err = s.connection.Exec(
				"insert into cluster__schedule_occurrence(occurrence_key, schedule_id, schedule_revision, intended_at, owner_boot_id, fencing_token, lease_expires_at, task_id, completed_at, created, updated) values (?, ?, ?, ?, ?, ?, "+expiresAt+", null, null, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
				occurrence.Key, occurrence.ScheduleID, occurrence.Revision, occurrence.IntendedAt, ownerBootID, 1, ttlSeconds,
			)
			if err == nil {
				claimed, found, err := s.get(occurrence.Key)
				if err != nil || !found {
					return pro_interfaces.ScheduleOccurrenceLease{}, false, err
				}
				return leaseFromRecord(claimed), true, nil
			}
			continue
		}
		if record.CompletedAt != nil {
			return leaseFromRecord(record), false, nil
		}
		if record.OwnerBootID == ownerBootID {
			updated, err := s.renew(record, expiresAt, ttlSeconds)
			if err != nil {
				return pro_interfaces.ScheduleOccurrenceLease{}, false, err
			}
			if updated {
				renewed, found, err := s.get(occurrence.Key)
				if err != nil || !found {
					return pro_interfaces.ScheduleOccurrenceLease{}, false, err
				}
				return leaseFromRecord(renewed), true, nil
			}
			continue
		}
		result, err := s.connection.Exec(
			"update cluster__schedule_occurrence set owner_boot_id=?, fencing_token=fencing_token+1, lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where occurrence_key=? and completed_at is null and lease_expires_at<=CURRENT_TIMESTAMP",
			ownerBootID, ttlSeconds, occurrence.Key,
		)
		if err != nil {
			return pro_interfaces.ScheduleOccurrenceLease{}, false, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return pro_interfaces.ScheduleOccurrenceLease{}, false, err
		}
		if updated == 1 {
			claimed, found, err := s.get(occurrence.Key)
			if err != nil || !found {
				return pro_interfaces.ScheduleOccurrenceLease{}, false, err
			}
			return leaseFromRecord(claimed), true, nil
		}
		return leaseFromRecord(record), false, nil
	}
	return pro_interfaces.ScheduleOccurrenceLease{}, false, errors.New("schedule occurrence claim conflicted repeatedly")
}

func (s *ScheduleCoordinatorStore) IsCurrentScheduleLease(lease pro_interfaces.ScheduleOccurrenceLease) (bool, error) {
	if s.connection == nil {
		return false, errors.New("schedule coordinator database connection is required")
	}
	var row struct {
		Count int `db:"count"`
	}
	err := s.connection.SelectOne(&row,
		"select count(1) as count from cluster__schedule_occurrence where occurrence_key=? and owner_boot_id=? and fencing_token=? and completed_at is null and lease_expires_at>CURRENT_TIMESTAMP",
		lease.Occurrence.Key, lease.OwnerBootID, lease.FencingToken,
	)
	return row.Count == 1, err
}

func (s *ScheduleCoordinatorStore) CompleteScheduleOccurrence(lease pro_interfaces.ScheduleOccurrenceLease, taskID int) (bool, error) {
	if s.connection == nil {
		return false, errors.New("schedule coordinator database connection is required")
	}
	if taskID <= 0 {
		return false, errors.New("schedule occurrence task id is required")
	}
	result, err := s.connection.Exec(
		"update cluster__schedule_occurrence set task_id=?, terminal_outcome='completed', completed_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where occurrence_key=? and owner_boot_id=? and fencing_token=? and task_id is null and completed_at is null and lease_expires_at>CURRENT_TIMESTAMP and exists(select 1 from task where id=? and schedule_occurrence_key=?)",
		taskID, lease.Occurrence.Key, lease.OwnerBootID, lease.FencingToken, taskID, lease.Occurrence.Key,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

// BlockScheduleOccurrence terminalizes one fenced occurrence without creating
// a task. The decision is re-derived from the lease's durable identity inside
// the CAS predicate, so neither a stale owner nor another occurrence of the
// same schedule can consume it.
func (s *ScheduleCoordinatorStore) BlockScheduleOccurrence(lease pro_interfaces.ScheduleOccurrenceLease, decisionID int) (bool, error) {
	if s.connection == nil {
		return false, errors.New("schedule coordinator database connection is required")
	}
	if decisionID <= 0 {
		return false, errors.New("blocked schedule decision is required")
	}
	decisionKey, err := pro_interfaces.DeploymentWindowScheduleDecisionKey(lease.Occurrence)
	if err != nil {
		return false, err
	}
	result, err := s.connection.Exec(
		`update cluster__schedule_occurrence
		 set terminal_outcome='blocked', deployment_window_decision_id=?,
		     next_eligible_at=(select next_eligible_at from project__deployment_window_decision where id=?),
		     next_eligible_known=(select next_eligible_known from project__deployment_window_decision where id=?),
		     blocked_at=CURRENT_TIMESTAMP, completed_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP
		 where occurrence_key=? and owner_boot_id=? and fencing_token=?
		   and task_id is null and completed_at is null and lease_expires_at>CURRENT_TIMESTAMP
		   and exists (
		     select 1 from project__deployment_window_decision d
		     join project__schedule schedule on schedule.id=?
		     where d.id=? and d.project_id=schedule.project_id and d.schedule_id=schedule.id
		       and d.source='schedule' and d.origin='schedule' and d.decision_key=?
		       and d.state='blocked' and d.task_id is null and d.workflow_run_id is null
		   )`,
		decisionID, decisionID, decisionID, lease.Occurrence.Key, lease.OwnerBootID, lease.FencingToken,
		lease.Occurrence.ScheduleID, decisionID, decisionKey,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated == 1 {
		return updated == 1, err
	}
	var record scheduleOccurrenceRecord
	lookupErr := s.connection.SelectOne(&record,
		"select * from cluster__schedule_occurrence where occurrence_key=? and owner_boot_id=? and fencing_token=?", lease.Occurrence.Key, lease.OwnerBootID, lease.FencingToken,
	)
	if lookupErr == nil && record.TerminalOutcome == "blocked" && record.DeploymentWindowDecisionID != nil && *record.DeploymentWindowDecisionID == decisionID {
		return true, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, coredb.ErrNotFound) {
		return false, lookupErr
	}
	return false, nil
}

func (s *ScheduleCoordinatorStore) ReleaseScheduleOccurrenceLease(lease pro_interfaces.ScheduleOccurrenceLease) (bool, error) {
	if s.connection == nil {
		return false, errors.New("schedule coordinator database connection is required")
	}
	result, err := s.connection.Exec(
		"update cluster__schedule_occurrence set lease_expires_at=CURRENT_TIMESTAMP, updated=CURRENT_TIMESTAMP where occurrence_key=? and owner_boot_id=? and fencing_token=? and completed_at is null and lease_expires_at>CURRENT_TIMESTAMP",
		lease.Occurrence.Key, lease.OwnerBootID, lease.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *ScheduleCoordinatorStore) renew(record scheduleOccurrenceRecord, expiresAt string, ttlSeconds int64) (bool, error) {
	result, err := s.connection.Exec(
		"update cluster__schedule_occurrence set lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where occurrence_key=? and owner_boot_id=? and fencing_token=? and completed_at is null and lease_expires_at>CURRENT_TIMESTAMP",
		ttlSeconds, record.OccurrenceKey, record.OwnerBootID, record.FencingToken,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated == 1, err
}

func (s *ScheduleCoordinatorStore) leaseExpiryExpression(ttl time.Duration) (string, int64, error) {
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
		return "", 0, errors.New("schedule coordinator database dialect is unsupported")
	}
}

func (s *ScheduleCoordinatorStore) get(key string) (scheduleOccurrenceRecord, bool, error) {
	var record scheduleOccurrenceRecord
	err := s.connection.SelectOne(&record, "select * from cluster__schedule_occurrence where occurrence_key=?", key)
	if err == nil {
		return record, true, nil
	}
	if errors.Is(err, coredb.ErrNotFound) {
		return scheduleOccurrenceRecord{}, false, nil
	}
	return scheduleOccurrenceRecord{}, false, err
}

func (s *ScheduleCoordinatorStore) taskForOccurrence(key string) (int, bool, error) {
	var row struct {
		ID int `db:"id"`
	}
	err := s.connection.SelectOne(&row, "select id from task where schedule_occurrence_key=?", key)
	if err == nil {
		return row.ID, true, nil
	}
	if errors.Is(err, coredb.ErrNotFound) {
		return 0, false, nil
	}
	return 0, false, err
}

type scheduleOccurrenceRecord struct {
	OccurrenceKey              string     `db:"occurrence_key"`
	ScheduleID                 int        `db:"schedule_id"`
	ScheduleRevision           string     `db:"schedule_revision"`
	IntendedAt                 time.Time  `db:"intended_at"`
	OwnerBootID                string     `db:"owner_boot_id"`
	FencingToken               int64      `db:"fencing_token"`
	LeaseExpiresAt             time.Time  `db:"lease_expires_at"`
	TaskID                     *int       `db:"task_id"`
	CompletedAt                *time.Time `db:"completed_at"`
	TerminalOutcome            string     `db:"terminal_outcome"`
	DeploymentWindowDecisionID *int       `db:"deployment_window_decision_id"`
	NextEligibleAt             *time.Time `db:"next_eligible_at"`
	NextEligibleKnown          bool       `db:"next_eligible_known"`
	BlockedAt                  *time.Time `db:"blocked_at"`
	Created                    time.Time  `db:"created"`
	Updated                    time.Time  `db:"updated"`
}

func leaseFromRecord(record scheduleOccurrenceRecord) pro_interfaces.ScheduleOccurrenceLease {
	return pro_interfaces.ScheduleOccurrenceLease{
		Occurrence: pro_interfaces.ScheduleOccurrence{
			Key: record.OccurrenceKey, ScheduleID: record.ScheduleID,
			Revision: record.ScheduleRevision, IntendedAt: record.IntendedAt,
		},
		OwnerBootID: record.OwnerBootID, FencingToken: record.FencingToken, ExpiresAt: record.LeaseExpiresAt,
	}
}
