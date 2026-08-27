package sql

import (
	"strings"
	"testing"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretSyncOperationIsDurableIdempotentAndLeaseSafe(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "managed secrets"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{
		ProjectID: project.ID, Name: "Vault", Type: db.SecretStorageTypeVault,
		SyncDirection: db.SecretSyncDirectionOutbound,
		SyncPaths: []db.SecretSyncPath{{
			AccessKeyID: 17, Mount: "secret", Path: "apps/api", Field: "password",
		}},
	})
	require.NoError(t, err)
	sync, err := store.GetStorageSecretSync(storage.ID)
	require.NoError(t, err)
	require.Len(t, sync.Paths, 1)

	first, err := store.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: "manual:request-0001", SyncID: sync.ID, ProjectID: project.ID,
		StorageID: storage.ID, SyncRevision: sync.Revision,
	})
	require.NoError(t, err)
	retry, err := store.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: "manual:request-0001", SyncID: sync.ID, ProjectID: project.ID,
		StorageID: storage.ID, SyncRevision: sync.Revision,
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, retry.ID)

	now := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)
	claimed, ok, err := store.ClaimSecretSyncOperation(first.ID, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	second, err := store.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: "manual:request-0002", SyncID: sync.ID, ProjectID: project.ID,
		StorageID: storage.ID, SyncRevision: sync.Revision,
	})
	require.NoError(t, err)
	_, ok, err = store.ClaimSecretSyncOperation(second.ID, now, now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, ok, "a second operation for the same sync must not run concurrently")

	path := sync.Paths[0]
	path.RemoteVersion = 3
	path.ContentFingerprint = "sha256:value-free"
	claimed.Status = db.SecretSyncOperationSucceeded
	claimed.ChangedCount = 1
	claimed.Outcomes = []db.SecretSyncItemOutcome{{
		MappingID: path.ID, AccessKeyID: path.AccessKeyID, Mount: path.Mount,
		Path: path.Path, Field: path.Field, Status: db.SecretSyncItemChanged,
		ContentFingerprint: path.ContentFingerprint, RemoteVersion: path.RemoteVersion,
	}}
	require.NoError(t, store.CompleteSecretSyncOperation(claimed, []db.SecretSyncPath{path}))

	claimedSecond, ok, err := store.ClaimSecretSyncOperation(second.ID, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	claimedSecond.Status = db.SecretSyncOperationFailed
	claimedSecond.ErrorCategory = "unavailable"
	require.NoError(t, store.CompleteSecretSyncOperation(claimedSecond, nil))

	history, err := store.GetSecretSyncOperations(project.ID, storage.ID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, second.ID, history[0].ID)
	assert.Equal(t, first.ID, history[1].ID)
	require.Len(t, history[1].Outcomes, 1)
	assert.Equal(t, "sha256:value-free", history[1].Outcomes[0].ContentFingerprint)

	updated, err := store.GetStorageSecretSync(storage.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, updated.Paths[0].RemoteVersion)
	assert.Equal(t, "sha256:value-free", updated.Paths[0].ContentFingerprint)
}

func TestOutboundSecretSyncDirectionPersistsWithoutMappings(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "managed secrets"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{
		ProjectID:     project.ID,
		Name:          "Vault",
		Type:          db.SecretStorageTypeVault,
		SyncDirection: db.SecretSyncDirectionOutbound,
	})
	require.NoError(t, err)

	sync, err := store.GetStorageSecretSync(storage.ID)
	require.NoError(t, err)
	assert.Equal(t, db.SecretSyncDirectionOutbound, sync.Direction)
	assert.Empty(t, sync.Paths)
}

func TestSecretSyncOperationExpiredLeaseCanBeReclaimed(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "lease recovery"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{
		ProjectID: project.ID, Name: "OpenBao", Type: db.SecretStorageTypeOpenBao,
		SyncDirection: db.SecretSyncDirectionOutbound,
		SyncPaths: []db.SecretSyncPath{{
			AccessKeyID: 22, Mount: "secret", Path: "apps/recovery", Field: "token",
		}},
	})
	require.NoError(t, err)
	sync, err := store.GetStorageSecretSync(storage.ID)
	require.NoError(t, err)
	operation, err := store.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: "auto:lease-recovery", SyncID: sync.ID, ProjectID: project.ID,
		StorageID: storage.ID, SyncRevision: sync.Revision,
	})
	require.NoError(t, err)
	now := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	_, ok, err := store.ClaimSecretSyncOperation(operation.ID, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	reclaimed, ok, err := store.ClaimSecretSyncOperation(
		operation.ID, now.Add(2*time.Minute), now.Add(3*time.Minute),
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 2, reclaimed.Attempt)
}

func TestSecretSyncOperationLeaseRenewalAndAttemptFencing(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "lease fencing"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{
		ProjectID: project.ID, Name: "Vault", Type: db.SecretStorageTypeVault,
		SyncDirection: db.SecretSyncDirectionOutbound,
	})
	require.NoError(t, err)
	sync, err := store.GetStorageSecretSync(storage.ID)
	require.NoError(t, err)
	operation, err := store.CreateSecretSyncOperation(db.SecretSyncOperation{
		RequestID: "auto:lease-fencing", SyncID: sync.ID, ProjectID: project.ID,
		StorageID: storage.ID, SyncRevision: sync.Revision,
	})
	require.NoError(t, err)
	now := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	first, ok, err := store.ClaimSecretSyncOperation(operation.ID, now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)

	renewed, err := store.RenewSecretSyncOperationLease(
		operation.ID, first.Attempt, now.Add(30*time.Second), now.Add(3*time.Minute),
	)
	require.NoError(t, err)
	assert.True(t, renewed)
	_, ok, err = store.ClaimSecretSyncOperation(
		operation.ID, now.Add(2*time.Minute), now.Add(4*time.Minute),
	)
	require.NoError(t, err)
	assert.False(t, ok, "the renewed lease must prevent an early reclaim")
	second, ok, err := store.ClaimSecretSyncOperation(
		operation.ID, now.Add(4*time.Minute), now.Add(6*time.Minute),
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, first.Attempt+1, second.Attempt)

	renewed, err = store.RenewSecretSyncOperationLease(
		operation.ID, first.Attempt, now.Add(4*time.Minute), now.Add(7*time.Minute),
	)
	require.NoError(t, err)
	assert.False(t, renewed, "a stale worker must not renew the reclaimed operation")
	first.Status = db.SecretSyncOperationFailed
	require.Error(t, store.CompleteSecretSyncOperation(first, nil))
	second.Status = db.SecretSyncOperationFailed
	require.NoError(t, store.CompleteSecretSyncOperation(second, nil))
}

func TestSecretSyncOperationClaimStatementIsDialectSafe(t *testing.T) {
	now := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	leaseUntil := now.Add(time.Minute)

	mysqlQuery, mysqlArgs := secretSyncOperationClaimStatement(
		gorp.MySQLDialect{}, 17, now, leaseUntil,
	)
	assert.Contains(t, mysqlQuery, "left join project__secret_sync_operation active")
	assert.NotContains(t, mysqlQuery, "not exists")
	assert.Len(t, mysqlArgs, 10)

	sqliteQuery, sqliteArgs := secretSyncOperationClaimStatement(
		gorp.SqliteDialect{}, 17, now, leaseUntil,
	)
	assert.Contains(t, sqliteQuery, "not exists")
	assert.NotContains(t, sqliteQuery, "left join")
	assert.Len(t, sqliteArgs, 10)
}

func TestManagedSecretMigrationPreparesForEverySQLDialect(t *testing.T) {
	tests := []struct {
		name       string
		dialect    string
		gorp       gorp.Dialect
		expectedID string
	}{
		{"sqlite", "sqlite", gorp.SqliteDialect{}, "integer primary key autoincrement"},
		{"mysql", "mysql", gorp.MySQLDialect{}, "integer primary key auto_increment"},
		{"postgres", "postgres", gorp.PostgresDialect{}, "serial primary key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &SqlDb{connection: SqlDbConnection{sql: &gorp.DbMap{Dialect: tt.gorp}}}
			queries := getVersionSQL(tt.dialect, "v2.20.11.sql", false)
			prepared := make([]string, len(queries))
			for index, query := range queries {
				prepared[index] = store.prepareMigration(query)
			}
			joined := strings.ToLower(strings.Join(prepared, ";"))
			assert.Contains(t, joined, tt.expectedID)
			if tt.name != "sqlite" {
				assert.NotContains(t, joined, "autoincrement")
			}
		})
	}
}
