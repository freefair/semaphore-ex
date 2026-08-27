package server

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type schedulerSecretSyncRepository struct {
	db.SecretSyncRepository
	claimed []db.SecretSyncOperation
	syncs   []db.SecretSync
}

func (r *schedulerSecretSyncRepository) ClaimPendingSecretSyncOperations(
	time.Time,
	time.Time,
	int,
) ([]db.SecretSyncOperation, error) {
	return r.claimed, nil
}

func (r *schedulerSecretSyncRepository) GetSyncEnabledSecretSyncs() ([]db.SecretSync, error) {
	return r.syncs, nil
}

type schedulerSecretStorageService struct {
	SecretStorageService
	runOperations []db.SecretSyncOperation
	requested     []db.SecretSync
	requestIDs    []string
}

func (s *schedulerSecretStorageService) RunSecretSyncOperation(
	_ context.Context,
	operation db.SecretSyncOperation,
) (db.SecretSyncOperation, error) {
	s.runOperations = append(s.runOperations, operation)
	return operation, nil
}

func (s *schedulerSecretStorageService) RequestSecretSync(
	_ context.Context,
	sync db.SecretSync,
	requestID string,
	_ *int,
	_ *int,
) (db.SecretSyncOperation, error) {
	s.requested = append(s.requested, sync)
	s.requestIDs = append(s.requestIDs, requestID)
	return db.SecretSyncOperation{}, nil
}

func TestSecretStorageSyncSchedulerRunsLeasedWorkAndOnlySchedulesOutboundSync(t *testing.T) {
	oldAttempt := time.Now().UTC().Add(-2 * time.Hour)
	repo := &schedulerSecretSyncRepository{
		claimed: []db.SecretSyncOperation{{ID: 17, Status: db.SecretSyncOperationRunning}},
		syncs: []db.SecretSync{
			{
				ID: 31, Direction: db.SecretSyncDirectionReadOnly,
				SyncEnabled: true, SyncInterval: 1,
			},
			{
				ID: 32, Direction: db.SecretSyncDirectionOutbound,
				SyncEnabled: true, SyncInterval: 1, LastSyncedAt: &oldAttempt,
			},
		},
	}
	service := &schedulerSecretStorageService{}
	scheduler := NewSecretStorageSyncScheduler(repo, service)

	scheduler.tick()

	require.Len(t, service.runOperations, 1)
	assert.Equal(t, 17, service.runOperations[0].ID)
	require.Len(t, service.requested, 1)
	assert.Equal(t, 32, service.requested[0].ID)
	require.Len(t, service.requestIDs, 1)
	assert.Contains(t, service.requestIDs[0], "auto:32:")
}

func TestSecretSyncDueUsesLatestAttemptAndRequiresPositiveInterval(t *testing.T) {
	now := time.Now().UTC()
	recentFailure := now.Add(-30 * time.Second)
	oldSuccess := now.Add(-2 * time.Hour)

	assert.False(t, secretSyncDue(db.SecretSync{SyncInterval: 0}, now))
	assert.True(t, secretSyncDue(db.SecretSync{SyncInterval: 5}, now))
	assert.False(t, secretSyncDue(db.SecretSync{
		SyncInterval: 5, LastSyncedAt: &oldSuccess, LastSyncFailedAt: &recentFailure,
	}, now))
}
