package db

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotNormalizesLegacyLocalRepositoryPolicy(t *testing.T) {
	var snapshot TaskExecutionSnapshot
	raw := `{"version":1,"project_id":1,"fingerprint":"legacy","template":{"id":2,"project_id":1},"repository":{"id":3,"project_id":1,"git_url":"/srv/local","monorepo":true}}`
	require.NoError(t, json.Unmarshal([]byte(raw), &snapshot))
	decoded, err := DecodeTaskExecutionSnapshot(raw, 1, 2)
	require.NoError(t, err)
	assert.False(t, decoded.Repository.Monorepo)
	encoded, err := EncodeTaskExecutionSnapshot(snapshot)
	require.NoError(t, err)
	assert.True(t, snapshot.Repository.Monorepo, "normalization preserves the caller's historical snapshot")
	var result TaskExecutionSnapshot
	require.NoError(t, json.Unmarshal([]byte(encoded), &result))
	assert.False(t, result.Repository.Monorepo)
}

func TestDecodeTaskExecutionSnapshotDropsStaleNonFileInventoryRepository(t *testing.T) {
	repositoryID := 9
	snapshot := TaskExecutionSnapshot{
		Version:             1,
		ProjectID:           1,
		Fingerprint:         "snapshot",
		Template:            Template{ID: 2, ProjectID: 1},
		Repository:          Repository{ID: 3, ProjectID: 1},
		Inventory:           &Inventory{ID: 4, ProjectID: 1, Type: InventoryStatic, RepositoryID: &repositoryID},
		InventoryRepository: &Repository{ID: repositoryID, ProjectID: 1},
	}

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)

	decoded, err := DecodeTaskExecutionSnapshot(string(raw), 1, 2)
	require.NoError(t, err)
	require.NotNil(t, decoded.Inventory)
	assert.Nil(t, decoded.Inventory.RepositoryID)
	assert.Nil(t, decoded.InventoryRepository)
}

func TestDecodeTaskExecutionSnapshotKeepsFileInventoryRepository(t *testing.T) {
	repositoryID := 9
	snapshot := TaskExecutionSnapshot{
		Version:             1,
		ProjectID:           1,
		Fingerprint:         "snapshot",
		Template:            Template{ID: 2, ProjectID: 1},
		Repository:          Repository{ID: 3, ProjectID: 1},
		Inventory:           &Inventory{ID: 4, ProjectID: 1, Type: InventoryFile, RepositoryID: &repositoryID},
		InventoryRepository: &Repository{ID: repositoryID, ProjectID: 1},
	}

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)

	decoded, err := DecodeTaskExecutionSnapshot(string(raw), 1, 2)
	require.NoError(t, err)
	require.NotNil(t, decoded.Inventory)
	require.NotNil(t, decoded.Inventory.RepositoryID)
	assert.Equal(t, repositoryID, *decoded.Inventory.RepositoryID)
	require.NotNil(t, decoded.InventoryRepository)
	assert.Equal(t, repositoryID, decoded.InventoryRepository.ID)
}

func TestEncodeTaskExecutionSnapshotDoesNotMutateCallerInventory(t *testing.T) {
	repositoryID := 9
	inventory := &Inventory{ID: 4, ProjectID: 1, Type: InventoryStatic, RepositoryID: &repositoryID}
	snapshot := TaskExecutionSnapshot{
		Version: 1, ProjectID: 1, Fingerprint: "snapshot",
		Template: Template{ID: 2, ProjectID: 1}, Repository: Repository{ID: 3, ProjectID: 1},
		Inventory: inventory, InventoryRepository: &Repository{ID: repositoryID, ProjectID: 1},
	}

	_, err := EncodeTaskExecutionSnapshot(snapshot)
	require.NoError(t, err)
	require.NotNil(t, inventory.RepositoryID)
	assert.Equal(t, repositoryID, *inventory.RepositoryID)
}

func TestDecodeTaskExecutionSnapshotRejectsForeignFileInventoryRepository(t *testing.T) {
	repositoryID := 9
	snapshot := TaskExecutionSnapshot{
		Version: 1, ProjectID: 1, Fingerprint: "snapshot",
		Template: Template{ID: 2, ProjectID: 1}, Repository: Repository{ID: 3, ProjectID: 1},
		Inventory:           &Inventory{ID: 4, ProjectID: 1, Type: InventoryFile, RepositoryID: &repositoryID},
		InventoryRepository: &Repository{ID: repositoryID, ProjectID: 2},
	}
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)

	_, err = DecodeTaskExecutionSnapshot(string(raw), 1, 2)
	assert.EqualError(t, err, "task execution snapshot inventory repository is invalid")
}
