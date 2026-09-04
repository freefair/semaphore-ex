package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func GetSecretStorages(repo db.SecretStorageRepository, projectID int) ([]db.SecretStorage, error) {
	storages, err := repo.GetSecretStorages(projectID)
	if err != nil {
		return nil, err
	}
	result := make([]db.SecretStorage, 0, len(storages))
	for _, storage := range storages {
		if storage.Type != db.SecretStorageTypeVault && storage.Type != db.SecretStorageTypeOpenBao {
			continue
		}
		storage.Secret = ""
		result = append(result, storage)
	}
	return result, nil
}

// StorageRequiresSecret preserves the core secret-storage contract while
// defaulting every unknown or malformed configuration to credential-required.
func StorageRequiresSecret(storage db.SecretStorage) bool {
	useIAMRole, _ := storage.Params["use_iam_role"].(bool)
	return storage.Type != db.SecretStorageTypeAwsSm || !useIAMRole
}

func SyncSecrets(
	ctx context.Context,
	sync db.SecretSync,
	_ db.SecretSyncOperation,
	resolved *db.SecretSyncOperation,
	renewLease func() error,
	storageRepo db.SecretStorageRepository,
	accessKeyRepo db.AccessKeyManager,
	decryptor DvlsStorageTokenDeserializer,
) (db.SecretSyncExecution, error) {
	result := db.SecretSyncExecution{
		Outcomes: make([]db.SecretSyncItemOutcome, 0, len(sync.Paths)),
		Paths:    make([]db.SecretSyncPath, 0, len(sync.Paths)),
	}
	storage, err := storageRepo.GetSecretStorage(sync.ProjectID, sync.StorageID)
	if err != nil {
		return result, err
	}
	if storage.Type != db.SecretStorageTypeVault && storage.Type != db.SecretStorageTypeOpenBao {
		return result, errors.New("managed secret storage type is unsupported")
	}
	if sync.Direction == db.SecretSyncDirectionReadOnly || storage.ReadOnly {
		for _, path := range sync.Paths {
			result.Outcomes = append(result.Outcomes, managedOutcome(path, db.SecretSyncItemSkipped, "read_only"))
		}
		return result, nil
	}
	if sync.Direction != db.SecretSyncDirectionOutbound {
		return result, errors.New("managed secret sync direction is invalid")
	}
	provider, ok := decryptor.(pro_interfaces.ManagedSecretProvider)
	if !ok {
		return result, errors.New("managed secret provider is unavailable")
	}
	resolvedConflicts := conflictVersions(resolved)
	for _, configuredPath := range sync.Paths {
		if renewLease != nil {
			if err = renewLease(); err != nil {
				return result, err
			}
		}
		path := configuredPath
		outcome := managedOutcome(path, db.SecretSyncItemFailed, "")
		if err = path.ValidateManaged(); err != nil {
			outcome.ErrorCategory = "validation"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		key, keyErr := accessKeyRepo.GetAccessKey(sync.ProjectID, path.AccessKeyID)
		if keyErr != nil || key.ProjectID == nil || *key.ProjectID != sync.ProjectID {
			outcome.Status = db.SecretSyncItemSkipped
			outcome.ErrorCategory = "key_unavailable"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		if key.SourceStorageType != nil || key.Owner != db.AccessKeyShared {
			outcome.Status = db.SecretSyncItemSkipped
			outcome.ErrorCategory = "key_not_local"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		if err = decryptor.DeserializeSecret(&key); err != nil {
			outcome.ErrorCategory = "key_unavailable"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		value, serializeErr := managedAccessKeyValue(key)
		clearAccessKeyValue(&key)
		if serializeErr != nil {
			outcome.Status = db.SecretSyncItemSkipped
			outcome.ErrorCategory = "key_type_unsupported"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		fingerprint := pro_interfaces.SecretContentFingerprint(value)
		reference := pro_interfaces.SecretReference{
			StorageID: sync.StorageID, Mount: path.Mount, Path: path.Path, Field: path.Field,
		}
		remote, readErr := provider.ReadManagedSecretField(ctx, sync.ProjectID, reference)
		if readErr != nil {
			zero(value)
			outcome.ErrorCategory = string(errorCategory(readErr))
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		remoteFingerprint := ""
		if remote.Exists {
			remoteFingerprint = pro_interfaces.SecretContentFingerprint(remote.Value)
			zero(remote.Value)
		}
		if remote.Exists && remoteFingerprint == fingerprint {
			zero(value)
			path.RemoteVersion = remote.Version
			path.ContentFingerprint = fingerprint
			outcome.Status = db.SecretSyncItemSkipped
			outcome.ErrorCategory = "unchanged"
			outcome.RemoteVersion = remote.Version
			outcome.ContentFingerprint = fingerprint
			result.Paths = append(result.Paths, path)
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		if expected, resolving := resolvedConflicts[path.ID]; resolving {
			if remote.Version != expected {
				zero(value)
				outcome.Status = db.SecretSyncItemConflict
				outcome.RemoteVersion = remote.Version
				outcome.ErrorCategory = "remote_changed"
				result.Outcomes = append(result.Outcomes, outcome)
				continue
			}
		} else if managedConflict(path, fingerprint, remoteFingerprint, remote.Exists) {
			zero(value)
			outcome.Status = db.SecretSyncItemConflict
			outcome.RemoteVersion = remote.Version
			outcome.ErrorCategory = "remote_changed"
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		if renewLease != nil {
			if err = renewLease(); err != nil {
				zero(value)
				return result, err
			}
		}
		version, writeErr := provider.WriteManagedSecretField(
			ctx, sync.ProjectID, reference, value, remote.Version,
		)
		zero(value)
		if writeErr != nil {
			category := errorCategory(writeErr)
			outcome.ErrorCategory = string(category)
			if category == pro_interfaces.SecretProviderErrorConflict {
				outcome.Status = db.SecretSyncItemConflict
				outcome.RemoteVersion = remote.Version
			}
			result.Outcomes = append(result.Outcomes, outcome)
			continue
		}
		path.RemoteVersion = version
		path.ContentFingerprint = fingerprint
		outcome.Status = db.SecretSyncItemChanged
		outcome.RemoteVersion = version
		outcome.ContentFingerprint = fingerprint
		result.Paths = append(result.Paths, path)
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}

func managedAccessKeyValue(key db.AccessKey) ([]byte, error) {
	switch key.Type {
	case db.AccessKeyString:
		return []byte(key.String), nil
	case db.AccessKeyLoginPassword:
		return json.Marshal(key.LoginPassword)
	case db.AccessKeySSH:
		return json.Marshal(key.SshKey)
	default:
		return nil, errors.New("access key type is unsupported")
	}
}

func clearAccessKeyValue(key *db.AccessKey) {
	key.String = ""
	key.LoginPassword = db.LoginPassword{}
	key.SshKey = db.SshKey{}
}

func managedOutcome(
	path db.SecretSyncPath,
	status db.SecretSyncItemStatus,
	category string,
) db.SecretSyncItemOutcome {
	return db.SecretSyncItemOutcome{
		MappingID: path.ID, AccessKeyID: path.AccessKeyID, Mount: path.Mount,
		Path: path.Path, Field: path.Field, Status: status, ErrorCategory: category,
	}
}

func conflictVersions(operation *db.SecretSyncOperation) map[int]int {
	versions := map[int]int{}
	if operation == nil || operation.Status != db.SecretSyncOperationConflict {
		return versions
	}
	for _, outcome := range operation.Outcomes {
		if outcome.Status == db.SecretSyncItemConflict {
			versions[outcome.MappingID] = outcome.RemoteVersion
		}
	}
	return versions
}

func managedConflict(
	path db.SecretSyncPath,
	localFingerprint string,
	remoteFingerprint string,
	remoteExists bool,
) bool {
	if path.ContentFingerprint == "" {
		return remoteExists && remoteFingerprint != localFingerprint
	}
	if !remoteExists {
		return true
	}
	return remoteFingerprint != path.ContentFingerprint && remoteFingerprint != localFingerprint
}
