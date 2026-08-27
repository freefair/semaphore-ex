package server

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
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

func SyncSecrets(
	db.SecretSync,
	db.SecretStorageRepository,
	db.AccessKeyManager,
	DvlsStorageTokenDeserializer,
) error {
	return errors.New("runtime secret providers do not synchronize secret values")
}
