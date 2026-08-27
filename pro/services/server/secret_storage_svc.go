package server

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

func GetSecretStorages(repo db.SecretStorageRepository, projectID int) (storages []db.SecretStorage, err error) {
	storages = make([]db.SecretStorage, 0)
	return
}

func StorageRequiresSecret(_ db.SecretStorage) bool {
	return true
}

func SyncSecrets(
	_ context.Context,
	_ db.SecretSync,
	_ db.SecretSyncOperation,
	_ *db.SecretSyncOperation,
	_ func() error,
	_ db.SecretStorageRepository,
	_ db.AccessKeyManager,
	_ DvlsStorageTokenDeserializer,
) (db.SecretSyncExecution, error) {
	return db.SecretSyncExecution{}, errors.New("managed secret synchronization is unavailable")
}
