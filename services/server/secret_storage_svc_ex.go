package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	pro "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"time"
)

const secretSyncOperationLease = 2 * time.Minute

func (s *SecretStorageServiceImpl) RequestSecretSync(
	ctx context.Context,
	sync db.SecretSync,
	requestID string,
	requestedBy *int,
	resolveOperationID *int,
) (db.SecretSyncOperation, error) {
	if s.secretSyncRepo == nil {
		return db.SecretSyncOperation{}, errors.New("secret sync repository unavailable")
	}
	if !secretSyncRequestIDPattern.MatchString(requestID) {
		return db.SecretSyncOperation{}, common_errors.NewValidationError("sync request id is invalid")
	}
	current, err := s.secretSyncRepo.GetSecretSync(sync.ID)
	if err != nil {
		return db.SecretSyncOperation{}, err
	}
	if current.ProjectID != sync.ProjectID || current.StorageID != sync.StorageID {
		return db.SecretSyncOperation{}, common_errors.NewValidationError(
			"secret sync does not match storage",
		)
	}
	if resolveOperationID != nil {
		resolved, resolveErr := s.secretSyncRepo.GetSecretSyncOperation(
			current.ProjectID, current.StorageID, *resolveOperationID,
		)
		if resolveErr != nil {
			return db.SecretSyncOperation{}, resolveErr
		}
		if resolved.Status != db.SecretSyncOperationConflict {
			return db.SecretSyncOperation{}, common_errors.NewValidationError(
				"only a conflicting synchronization can be resolved",
			)
		}
		if resolved.SyncID != current.ID || resolved.SyncRevision != current.Revision {
			return db.SecretSyncOperation{}, common_errors.NewValidationError(
				"conflicting synchronization configuration has changed",
			)
		}
	}
	operation, err := s.secretSyncRepo.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: requestID, SyncID: current.ID, ProjectID: current.ProjectID,
		StorageID: current.StorageID, SyncRevision: current.Revision,
		ResolveOperationID: resolveOperationID, RequestedBy: requestedBy,
	})
	if err != nil || operation.Status != db.SecretSyncOperationPending {
		return operation, err
	}
	now := time.Now().UTC()
	claimed, ok, err := s.secretSyncRepo.ClaimSecretSyncOperation(
		operation.ID, now, now.Add(secretSyncOperationLease),
	)
	if err != nil || !ok {
		return operation, err
	}
	return s.RunSecretSyncOperation(ctx, claimed)
}

func (s *SecretStorageServiceImpl) RunSecretSyncOperation(
	ctx context.Context,
	operation db.SecretSyncOperation,
) (db.SecretSyncOperation, error) {
	if s.secretSyncRepo == nil {
		return operation, errors.New("secret sync repository unavailable")
	}
	sync, err := s.secretSyncRepo.GetSecretSync(operation.SyncID)
	if err != nil {
		return s.completeFailedSecretSync(operation, "configuration_unavailable", err)
	}
	if sync.Revision != operation.SyncRevision {
		operation.Status = db.SecretSyncOperationConflict
		operation.ConflictCount = 1
		operation.ErrorCategory = "configuration_changed"
		operation.Outcomes = []db.SecretSyncItemOutcome{}
		err = s.secretSyncRepo.CompleteSecretSyncOperation(operation, nil)
		return operation, err
	}
	var resolved *db.SecretSyncOperation
	if operation.ResolveOperationID != nil {
		resolvedOperation, resolveErr := s.secretSyncRepo.GetSecretSyncOperation(
			operation.ProjectID, operation.StorageID, *operation.ResolveOperationID,
		)
		if resolveErr != nil {
			return s.completeFailedSecretSync(operation, "resolution_unavailable", resolveErr)
		}
		resolved = &resolvedOperation
	}
	renewLease := func() error {
		now := time.Now().UTC()
		renewed, renewErr := s.secretSyncRepo.RenewSecretSyncOperationLease(
			operation.ID, operation.Attempt, now, now.Add(secretSyncOperationLease),
		)
		if renewErr != nil {
			return fmt.Errorf("%w: %v", errSecretSyncLeaseLost, renewErr)
		}
		if !renewed {
			return errSecretSyncLeaseLost
		}
		return nil
	}
	execution, runErr := pro.SyncSecrets(
		ctx, sync, operation, resolved, renewLease,
		s.secretStorageRepo, s.accessKeyRepo, s.encryptionService,
	)
	if runErr != nil {
		if errors.Is(runErr, errSecretSyncLeaseLost) {
			return operation, runErr
		}
		return s.completeFailedSecretSync(operation, "provider_unavailable", runErr)
	}
	operation.Outcomes = execution.Outcomes
	for _, outcome := range execution.Outcomes {
		switch outcome.Status {
		case db.SecretSyncItemChanged:
			operation.ChangedCount++
		case db.SecretSyncItemSkipped:
			operation.SkippedCount++
		case db.SecretSyncItemConflict:
			operation.ConflictCount++
		case db.SecretSyncItemFailed:
			operation.ErrorCategory = outcome.ErrorCategory
		}
	}
	switch {
	case operation.ConflictCount > 0:
		operation.Status = db.SecretSyncOperationConflict
		if operation.ErrorCategory == "" {
			operation.ErrorCategory = "remote_changed"
		}
	case operation.ErrorCategory != "":
		operation.Status = db.SecretSyncOperationFailed
	default:
		operation.Status = db.SecretSyncOperationSucceeded
	}
	if err = s.secretSyncRepo.CompleteSecretSyncOperation(operation, execution.Paths); err != nil {
		return operation, err
	}
	_ = s.secretSyncRepo.MarkSecretSyncSynced(
		sync.ID, operation.Status == db.SecretSyncOperationSucceeded, time.Now().UTC(),
	)
	return operation, nil
}

func (s *SecretStorageServiceImpl) completeFailedSecretSync(
	operation db.SecretSyncOperation,
	category string,
	cause error,
) (db.SecretSyncOperation, error) {
	operation.Status = db.SecretSyncOperationFailed
	operation.ErrorCategory = category
	operation.Outcomes = []db.SecretSyncItemOutcome{}
	if err := s.secretSyncRepo.CompleteSecretSyncOperation(operation, nil); err != nil {
		return operation, err
	}
	_ = s.secretSyncRepo.MarkSecretSyncSynced(operation.SyncID, false, time.Now().UTC())
	return operation, cause
}

func (s *SecretStorageServiceImpl) GetSecretSyncHistory(
	projectID int,
	storageID int,
	limit int,
) ([]db.SecretSyncOperation, error) {
	if s.secretSyncRepo == nil {
		return nil, errors.New("secret sync repository unavailable")
	}
	return s.secretSyncRepo.GetSecretSyncOperations(projectID, storageID, limit)
}

func (s *SecretStorageServiceImpl) TestConnection(
	ctx context.Context,
	projectID int,
	storageID int,
) (pro_interfaces.SecretProviderHealth, error) {
	tester, ok := s.encryptionService.(RuntimeSecretProviderTester)
	if !ok {
		return pro_interfaces.SecretProviderHealth{}, errors.New("runtime secret provider tester unavailable")
	}
	return tester.TestRuntimeSecretProvider(ctx, projectID, storageID)
}
