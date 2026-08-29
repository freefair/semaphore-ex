package sql

import (
	"errors"
	"strings"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type TaskControlStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.TaskControlLeaseRepository = (*TaskControlStore)(nil)

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
			_, err = s.connection.Exec(
				"insert into cluster__task_control(task_id, owner_boot_id, fencing_token, lease_expires_at, runner_id, assignment_generation, execution_stable_id, last_observed_at, created, updated) values (?, ?, ?, "+expiresAt+", ?, ?, ?, null, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
				taskID, ownerBootID, 1, ttlSeconds, execution.RunnerID, execution.Generation, execution.StableID,
			)
			if err == nil {
				claimed, found, err := s.get(taskID)
				if err != nil || !found {
					return pro_interfaces.TaskControlLease{}, false, err
				}
				return taskControlLeaseFromRecord(claimed), true, nil
			}
			continue
		}
		if record.RunnerID != execution.RunnerID || record.AssignmentGeneration != execution.Generation || record.ExecutionStableID != execution.StableID {
			return taskControlLeaseFromRecord(record), false, errors.New("task control execution identity changed")
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
		result, err := s.connection.Exec(
			"update cluster__task_control set owner_boot_id=?, fencing_token=fencing_token+1, lease_expires_at="+expiresAt+", updated=CURRENT_TIMESTAMP where task_id=? and lease_expires_at<=CURRENT_TIMESTAMP",
			ownerBootID, ttlSeconds, taskID,
		)
		if err != nil {
			return pro_interfaces.TaskControlLease{}, false, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return pro_interfaces.TaskControlLease{}, false, err
		}
		if updated == 1 {
			claimed, found, err := s.get(taskID)
			if err != nil || !found {
				return pro_interfaces.TaskControlLease{}, false, err
			}
			return taskControlLeaseFromRecord(claimed), true, nil
		}
		return taskControlLeaseFromRecord(record), false, nil
	}
	return pro_interfaces.TaskControlLease{}, false, errors.New("task control claim conflicted repeatedly")
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
	TaskID               int        `db:"task_id"`
	OwnerBootID          string     `db:"owner_boot_id"`
	FencingToken         int64      `db:"fencing_token"`
	LeaseExpiresAt       time.Time  `db:"lease_expires_at"`
	RunnerID             int        `db:"runner_id"`
	AssignmentGeneration int        `db:"assignment_generation"`
	ExecutionStableID    string     `db:"execution_stable_id"`
	LastObservedAt       *time.Time `db:"last_observed_at"`
	Created              time.Time  `db:"created"`
	Updated              time.Time  `db:"updated"`
}

func taskControlLeaseFromRecord(record taskControlRecord) pro_interfaces.TaskControlLease {
	return pro_interfaces.TaskControlLease{
		TaskID: record.TaskID, OwnerBootID: record.OwnerBootID, FencingToken: record.FencingToken, ExpiresAt: record.LeaseExpiresAt,
		Execution: pro_interfaces.TaskExecutionIdentity{RunnerID: record.RunnerID, Generation: record.AssignmentGeneration, StableID: record.ExecutionStableID},
	}
}
