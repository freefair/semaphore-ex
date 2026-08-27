package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	log "github.com/sirupsen/logrus"
)

const secretStorageSyncTickInterval = 60 * time.Second

// SecretStorageSyncScheduler walks every sync-enabled SecretSync row
// (storage-level and env-scoped) and runs SyncSecrets when the configured
// interval has elapsed.
type SecretStorageSyncScheduler struct {
	secretSyncRepo       db.SecretSyncRepository
	secretStorageService SecretStorageService
	tickDeduplicator     interface{ TryLockExecution(scheduleID int) bool }

	stop chan struct{}
	wg   sync.WaitGroup
}

func NewSecretStorageSyncScheduler(
	secretSyncRepo db.SecretSyncRepository,
	secretStorageService SecretStorageService,
) *SecretStorageSyncScheduler {
	return &SecretStorageSyncScheduler{
		secretSyncRepo:       secretSyncRepo,
		secretStorageService: secretStorageService,
		stop:                 make(chan struct{}),
	}
}

const secretStorageSyncTickLockID = 0

func (s *SecretStorageSyncScheduler) SetTickDeduplicator(d interface{ TryLockExecution(scheduleID int) bool }) {
	s.tickDeduplicator = d
}

func (s *SecretStorageSyncScheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

func (s *SecretStorageSyncScheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *SecretStorageSyncScheduler) run() {
	defer s.wg.Done()

	ticker := time.NewTicker(secretStorageSyncTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *SecretStorageSyncScheduler) tick() {
	if s.tickDeduplicator != nil && !s.tickDeduplicator.TryLockExecution(secretStorageSyncTickLockID) {
		return
	}

	now := tz.Now()
	claimed, err := s.secretSyncRepo.ClaimPendingSecretSyncOperations(
		now, now.Add(secretSyncOperationLease), 10,
	)
	if err != nil {
		log.WithError(err).Warn("secret sync: failed to claim pending operations")
		return
	}
	for _, operation := range claimed {
		if _, runErr := s.secretStorageService.RunSecretSyncOperation(
			context.Background(), operation,
		); runErr != nil {
			log.WithError(runErr).
				WithField("operation_id", operation.ID).
				Warn("secret sync operation failed")
		}
	}

	syncs, err := s.secretSyncRepo.GetSyncEnabledSecretSyncs()
	if err != nil {
		log.WithError(err).Warn("secret sync: failed to list sync-enabled configs")
		return
	}

	for _, sync := range syncs {
		if sync.Direction != db.SecretSyncDirectionOutbound || !secretSyncDue(sync, now) {
			continue
		}
		requestID := fmt.Sprintf("auto:%d:%d", sync.ID, now.Unix()/60)
		if _, syncErr := s.secretStorageService.RequestSecretSync(
			context.Background(), sync, requestID, nil, nil,
		); syncErr != nil {
			log.WithError(syncErr).
				WithField("sync_id", sync.ID).
				WithField("storage_id", sync.StorageID).
				Warn("secret sync failed")
		}
	}
}

func secretSyncDue(sync db.SecretSync, now time.Time) bool {
	if sync.SyncInterval <= 0 {
		return false
	}

	var lastAttempt time.Time
	if sync.LastSyncedAt != nil {
		lastAttempt = *sync.LastSyncedAt
	}
	if sync.LastSyncFailedAt != nil && sync.LastSyncFailedAt.After(lastAttempt) {
		lastAttempt = *sync.LastSyncFailedAt
	}

	if lastAttempt.IsZero() {
		return true
	}

	return now.Sub(lastAttempt) >= time.Duration(sync.SyncInterval)*time.Minute
}
