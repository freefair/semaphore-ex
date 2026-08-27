package server

import (
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type AccessKeyService interface {
	Update(key db.AccessKey) error
	Create(key db.AccessKey) (newKey db.AccessKey, err error)
	GetAll(projectID int, options db.GetAccessKeyOptions, params db.RetrieveQueryParams) ([]db.AccessKey, error)
	Delete(projectID int, keyID int) (err error)
}

type AccessKeyServiceImpl struct {
	accessKeyRepo      db.AccessKeyManager
	encryptionService  AccessKeyEncryptionService
	secretStorageRepo  db.SecretStorageRepository
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewAccessKeyService(
	accessKeyRepo db.AccessKeyManager,
	encryptionService AccessKeyEncryptionService,
	secretStorageRepo db.SecretStorageRepository,
	capabilityProviders ...pro_interfaces.CapabilityProvider,
) AccessKeyService {
	var capabilityProvider pro_interfaces.CapabilityProvider
	if len(capabilityProviders) > 0 {
		capabilityProvider = capabilityProviders[0]
	}
	return &AccessKeyServiceImpl{
		accessKeyRepo:      accessKeyRepo,
		encryptionService:  encryptionService,
		secretStorageRepo:  secretStorageRepo,
		capabilityProvider: capabilityProvider,
	}
}

func (s *AccessKeyServiceImpl) Delete(projectID int, keyID int) (err error) {
	key, err := s.accessKeyRepo.GetAccessKey(projectID, keyID)
	if err != nil {
		return
	}

	if key.SourceStorageID != nil {
		var storage db.SecretStorage
		storage, err = s.secretStorageRepo.GetSecretStorage(projectID, *key.SourceStorageID)
		if err != nil {
			return
		}

		if storage.ReadOnly || key.Synchronized {
			// Do nothing

			//if key.Synchronized {
			//	err = common_errors.NewUserErrorS("cannot delete synchronized secret from read-only storage")
			//}
		} else {
			err = s.encryptionService.DeleteSecret(&key)
		}

		if err != nil {
			return
		}
	}

	err = s.accessKeyRepo.DeleteAccessKey(projectID, keyID)

	return
}

func (s *AccessKeyServiceImpl) GetAll(projectID int, options db.GetAccessKeyOptions, params db.RetrieveQueryParams) ([]db.AccessKey, error) {
	return s.accessKeyRepo.GetAccessKeys(projectID, options, params)
}

func (s *AccessKeyServiceImpl) Create(key db.AccessKey) (newKey db.AccessKey, err error) {
	if err = s.requireRuntimeSecretWrite(key); err != nil {
		return
	}
	if err = normalizeRuntimeSecretReference(&key, s.secretStorageRepo); err != nil {
		return
	}

	// SerializeSecret encrypts/persists the secret for writable backends. For read-only
	// external storage the secret is not stored in Semaphore, so SerializeSecret fails
	// with ErrReadOnlyStorage; we still create the access key row (metadata / reference).
	err = s.encryptionService.SerializeSecret(&key)
	if err != nil && !errors.Is(err, ErrReadOnlyStorage) {
		return
	}

	newKey, err = s.accessKeyRepo.CreateAccessKey(key)
	return
}

func (s *AccessKeyServiceImpl) Update(key db.AccessKey) (err error) {
	if !key.OverrideSecret {
		err = s.accessKeyRepo.UpdateAccessKey(key)
		return
	}
	if err = s.requireRuntimeSecretWrite(key); err != nil {
		return
	}
	if err = normalizeRuntimeSecretReference(&key, s.secretStorageRepo); err != nil {
		return
	}

	var oldKey db.AccessKey
	oldKey, err = s.accessKeyRepo.GetAccessKey(*key.ProjectID, key.ID)
	if err != nil {
		return
	}

	if oldKey.SourceStorageType != nil && !oldKey.IsNativelyReadOnly() {
		// validate if it is secure to override secret storage

		var oldSt db.SecretStorage
		oldSt, err = s.secretStorageRepo.GetSecretStorage(*key.ProjectID, *oldKey.SourceStorageID)
		if err != nil {
			return
		}

		if !oldSt.ReadOnly && (key.SourceStorageID == nil || *oldKey.SourceStorageID != *key.SourceStorageID) {
			err = common_errors.NewUserErrorS("cannot override secret storage")
			return
		}
	}

	if !key.IsNativelyReadOnly() {
		err = s.encryptionService.SerializeSecret(&key)
		if err != nil && !(key.SourceStorageType != nil &&
			*key.SourceStorageType == db.AccessKeySourceStorageVault &&
			errors.Is(err, ErrReadOnlyStorage)) {
			return
		}
	}

	err = s.accessKeyRepo.UpdateAccessKey(key)

	return
}
