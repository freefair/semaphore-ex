package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentSecretSyncDirectionDependsOnManagedPaths(t *testing.T) {
	storageID := 9
	withoutPaths := environmentSecretSync(db.Environment{ID: 1, ProjectID: 2, SecretStorageID: &storageID, SyncEnabled: true, SyncInterval: 15})
	// SaveSecretSync normalizes this empty direction to read_only, preserving
	// existing pull-only storage-sync behavior.
	require.Empty(t, withoutPaths.Direction)
	require.True(t, withoutPaths.SyncEnabled)
	require.Equal(t, 15, withoutPaths.SyncInterval)

	withPaths := environmentSecretSync(db.Environment{
		ID: 1, ProjectID: 2, SecretStorageID: &storageID,
		SyncPaths: []db.SecretSyncPath{{AccessKeyID: 3, Mount: "secret", Path: "applications/test", Field: "token"}},
	})

	require.Equal(t, db.SecretSyncDirectionOutbound, withPaths.Direction)
	require.Equal(t, 3, withPaths.Paths[0].AccessKeyID)
}
