package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
	pro "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type SecretStorageService interface {
	GetSecretStorage(projectID int, storageID int) (db.SecretStorage, error)
	Update(storage db.SecretStorage) error
	Delete(projectID int, storageID int) error
	GetSecretStorages(projectID int) ([]db.SecretStorage, error)
	Create(storage db.SecretStorage) (res db.SecretStorage, err error)
	SyncSecrets(sync db.SecretSync) error
	RequestSecretSync(context.Context, db.SecretSync, string, *int, *int) (db.SecretSyncOperation, error)
	RunSecretSyncOperation(context.Context, db.SecretSyncOperation) (db.SecretSyncOperation, error)
	GetSecretSyncHistory(int, int, int) ([]db.SecretSyncOperation, error)
	TestConnection(context.Context, int, int) (pro_interfaces.SecretProviderHealth, error)
}

func NewSecretStorageService(
	secretStorageRepo db.SecretStorageRepository,
	accessKeyRepo db.AccessKeyManager,
	accessKeyService AccessKeyService,
	encryptionService AccessKeyEncryptionService,
) SecretStorageService {
	return &SecretStorageServiceImpl{
		secretStorageRepo: secretStorageRepo,
		accessKeyRepo:     accessKeyRepo,
		accessKeyService:  accessKeyService,
		encryptionService: encryptionService,
		secretSyncRepo: func() db.SecretSyncRepository {
			repo, _ := secretStorageRepo.(db.SecretSyncRepository)
			return repo
		}(),
	}
}

type SecretStorageServiceImpl struct {
	secretStorageRepo db.SecretStorageRepository
	accessKeyRepo     db.AccessKeyManager
	accessKeyService  AccessKeyService
	encryptionService AccessKeyEncryptionService
	secretSyncRepo    db.SecretSyncRepository
}

var secretSyncRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,63}$`)
var errSecretSyncLeaseLost = errors.New("secret sync operation lease lost")

const secretSyncOperationLease = 2 * time.Minute

func (s *SecretStorageServiceImpl) SyncSecrets(sync db.SecretSync) error {
	_, err := pro.SyncSecrets(
		context.Background(), sync, db.SecretSyncOperation{}, nil, nil,
		s.secretStorageRepo, s.accessKeyRepo, s.encryptionService,
	)
	return err
}

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

func (s *SecretStorageServiceImpl) Delete(projectID int, storageID int) (err error) {
	storage, err := s.secretStorageRepo.GetSecretStorage(projectID, storageID)
	if err != nil {
		return
	}

	if storage.SyncEnabled {
		var syncedKeys []db.AccessKey
		syncedKeys, err = s.accessKeyRepo.GetAccessKeys(projectID, db.GetAccessKeyOptions{
			IgnoreOwner:     true,
			SourceStorageID: &storageID,
		}, db.RetrieveQueryParams{})
		if err != nil {
			return
		}

		for _, key := range syncedKeys {
			if err = s.accessKeyRepo.DeleteAccessKey(projectID, key.ID); err != nil {
				return
			}
		}
	}

	err = s.secretStorageRepo.DeleteSecretStorage(projectID, storageID)
	if err != nil {
		return
	}

	keys, err := s.accessKeyService.GetAll(projectID, db.GetAccessKeyOptions{
		Owner:     db.AccessKeySecretStorage,
		StorageID: &storageID,
	}, db.RetrieveQueryParams{})

	if err != nil {
		return
	}

	for _, key := range keys {
		err = s.accessKeyService.Delete(projectID, key.ID)
	}

	return
}

func (s *SecretStorageServiceImpl) GetSecretStorage(projectID int, storageID int) (res db.SecretStorage, err error) {
	res, err = s.secretStorageRepo.GetSecretStorage(projectID, storageID)
	res.Secret = ""
	return
}

func (s *SecretStorageServiceImpl) Create(storage db.SecretStorage) (res db.SecretStorage, err error) {
	if err = ValidateRuntimeSecretStorage(&storage); err != nil {
		return
	}
	sourceStorageType := storage.SourceStorageType
	sourceStorageKey := ""

	if !pro.StorageRequiresSecret(storage) {
		// The storage authenticates without credentials stored in Semaphore
		// (for example an AWS IAM role), so no access key is created.
		return s.secretStorageRepo.CreateSecretStorage(storage)
	}

	if storage.Secret == "" {
		err = common_errors.NewUserErrorS("secret must be set")
		return
	}

	if sourceStorageType != nil {
		switch *sourceStorageType {
		case db.AccessKeySourceStorageEnv:
			sourceStorageKey = storage.Secret
		case db.AccessKeySourceStorageFile:
			sourceStorageKey = storage.Secret
		default:
			err = common_errors.NewUserErrorS("unsupported source storage type")
			return
		}
	}

	res, err = s.secretStorageRepo.CreateSecretStorage(storage)

	if err != nil {
		return
	}

	key := db.AccessKey{
		Name:              random.String(10),
		Type:              db.AccessKeyString,
		ProjectID:         &storage.ProjectID,
		Owner:             db.AccessKeySecretStorage,
		StorageID:         &res.ID,
		SourceStorageType: sourceStorageType,
	}

	if sourceStorageKey != "" {
		key.SourceStorageKey = &sourceStorageKey
	} else {
		key.String = storage.Secret
	}

	_, err = s.accessKeyService.Create(key)
	res.Secret = ""
	return
}

func (s *SecretStorageServiceImpl) Update(storage db.SecretStorage) (err error) {
	if err = ValidateRuntimeSecretStorage(&storage); err != nil {
		return
	}
	err = s.secretStorageRepo.UpdateSecretStorage(storage)
	if err != nil {
		return
	}

	keys, err := s.accessKeyService.GetAll(storage.ProjectID, db.GetAccessKeyOptions{
		Owner:     db.AccessKeySecretStorage,
		StorageID: &storage.ID,
	}, db.RetrieveQueryParams{})

	if err != nil {
		return
	}

	if !pro.StorageRequiresSecret(storage) {
		// The storage switched to ambient credentials (for example an AWS IAM
		// role), so previously stored credentials are removed.
		for _, key := range keys {
			if err = s.accessKeyService.Delete(storage.ProjectID, key.ID); err != nil {
				return
			}
		}
		return
	}

	if len(keys) == 0 {
		if storage.Secret == "" {
			// empty vault token means the user didn't set a new token,
			// so we don't create a new access key.
			return
		}

		sourceStorageType := storage.SourceStorageType
		sourceStorageKey := ""

		if sourceStorageType != nil {
			switch *sourceStorageType {
			case db.AccessKeySourceStorageEnv, db.AccessKeySourceStorageFile:
				sourceStorageKey = storage.Secret
			default:
				err = errors.New("unsupported source storage type")
				return
			}
		}

		newKey := db.AccessKey{
			Name:              random.String(10),
			Type:              db.AccessKeyString,
			ProjectID:         &storage.ProjectID,
			Owner:             db.AccessKeySecretStorage,
			StorageID:         &storage.ID,
			SourceStorageType: sourceStorageType,
		}

		if sourceStorageKey != "" {
			newKey.SourceStorageKey = &sourceStorageKey
		} else {
			newKey.String = storage.Secret
		}

		_, err = s.accessKeyService.Create(newKey)

	} else {
		vault := keys[0]
		if storage.Secret == "" {
			// Do nothing if the vault token is empty,
			// as it means the user haven't set a new token.

			//err = s.keyRepo.DeleteAccessKey(storage.ProjectID, vault.ID)
			return
		}

		sourceStorageType := storage.SourceStorageType
		sourceStorageKey := ""

		if sourceStorageType != nil {
			switch *sourceStorageType {
			case db.AccessKeySourceStorageEnv, db.AccessKeySourceStorageFile:
				sourceStorageKey = storage.Secret
			default:
				err = errors.New("unsupported source storage type")
				return
			}
		}

		vault.OverrideSecret = true
		vault.SourceStorageType = sourceStorageType
		if sourceStorageKey != "" {
			vault.SourceStorageKey = &sourceStorageKey
			vault.String = ""
			// Clear previously persisted encrypted secret when switching to env/file source.
			vault.Secret = nil
		} else {
			vault.SourceStorageKey = nil
			vault.String = storage.Secret
		}

		err = s.accessKeyService.Update(vault)
	}

	return
}

func (s *SecretStorageServiceImpl) GetSecretStorages(projectID int) (storages []db.SecretStorage, err error) {
	storages, err = pro.GetSecretStorages(s.secretStorageRepo, projectID)
	for index := range storages {
		storages[index].Secret = ""
	}
	return
}
