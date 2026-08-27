package server

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managedSecretStorageRepository struct {
	db.SecretStorageRepository
	db.SecretSyncRepository
	current  db.SecretSync
	resolved db.SecretSyncOperation
	created  bool
}

func (r *managedSecretStorageRepository) GetSecretSync(int) (db.SecretSync, error) {
	return r.current, nil
}

func (r *managedSecretStorageRepository) GetSecretSyncOperation(
	int,
	int,
	int,
) (db.SecretSyncOperation, error) {
	return r.resolved, nil
}

func (r *managedSecretStorageRepository) CreateSecretSyncOperation(
	operation db.SecretSyncOperation,
) (db.SecretSyncOperation, error) {
	r.created = true
	operation.ID = 10
	operation.Status = db.SecretSyncOperationSucceeded
	return operation, nil
}

func TestRequestSecretSyncRejectsStaleConflictResolution(t *testing.T) {
	repository := &managedSecretStorageRepository{
		current: db.SecretSync{ID: 4, ProjectID: 3, StorageID: 9, Revision: 3},
		resolved: db.SecretSyncOperation{
			ID: 8, ProjectID: 3, StorageID: 9, SyncID: 4,
			SyncRevision: 2, Status: db.SecretSyncOperationConflict,
		},
	}
	service := NewSecretStorageService(repository, nil, nil, nil)
	resolveID := 8

	_, err := service.RequestSecretSync(
		context.Background(), repository.current, "manual:revision-check", nil, &resolveID,
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "configuration has changed")
	assert.False(t, repository.created)
}
