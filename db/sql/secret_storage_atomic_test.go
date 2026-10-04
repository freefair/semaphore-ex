package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteSecretStorageRollsBackCredentialsWhenStorageIsReferenced(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage transaction"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{Name: "token", Type: db.AccessKeyString, ProjectID: &project.ID, Owner: db.AccessKeySecretStorage, StorageID: &storage.ID})
	require.NoError(t, err)
	_, err = store.CreateEnvironment(db.Environment{ProjectID: project.ID, Name: "uses storage", JSON: "{}", SecretStorageID: &storage.ID})
	require.NoError(t, err)

	err = store.DeleteSecretStorage(project.ID, storage.ID)

	require.Error(t, err)
	_, err = store.GetSecretStorage(project.ID, storage.ID)
	require.NoError(t, err)
	storedKey, err := store.GetAccessKey(project.ID, key.ID)
	require.NoError(t, err)
	assert.Equal(t, key.ID, storedKey.ID)
}

func TestDeleteSecretStorageScopesOwnedAndSyncedCredentials(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage scope"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	owned, err := store.CreateAccessKey(db.AccessKey{Name: "owned", Type: db.AccessKeyString, ProjectID: &project.ID, Owner: db.AccessKeySecretStorage, StorageID: &storage.ID})
	require.NoError(t, err)
	synced, err := store.CreateAccessKey(db.AccessKey{Name: "synced", Type: db.AccessKeyString, ProjectID: &project.ID, SourceStorageID: &storage.ID})
	require.NoError(t, err)
	require.NoError(t, store.SaveSecretSync(db.SecretSync{
		ProjectID:   project.ID,
		StorageID:   storage.ID,
		SyncEnabled: true,
		Direction:   db.SecretSyncDirectionReadOnly,
	}))

	assert.ErrorIs(t, store.DeleteSecretStorage(other.ID, storage.ID), db.ErrNotFound)
	require.NoError(t, store.DeleteSecretStorage(project.ID, storage.ID))
	_, err = store.GetAccessKey(project.ID, owned.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetAccessKey(project.ID, synced.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	assert.ErrorIs(t, store.DeleteSecretStorage(project.ID, storage.ID), db.ErrNotFound)
}

func TestDeleteSecretStorageRefusesSourceCredentialsWhenSyncIsDisabled(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage disabled sync"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	synced, err := store.CreateAccessKey(db.AccessKey{Name: "synced", Type: db.AccessKeyString, ProjectID: &project.ID, SourceStorageID: &storage.ID})
	require.NoError(t, err)

	err = store.DeleteSecretStorage(project.ID, storage.ID)

	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	_, err = store.GetSecretStorage(project.ID, storage.ID)
	require.NoError(t, err)
	_, err = store.GetAccessKey(project.ID, synced.ID)
	require.NoError(t, err)
}

func TestDeleteSecretStorageRefusesProjectDefaultReference(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage default reference"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	_, err = store.exec("update project set default_secret_storage_id=? where id=?", storage.ID, project.ID)
	require.NoError(t, err)

	err = store.DeleteSecretStorage(project.ID, storage.ID)

	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	_, err = store.GetSecretStorage(project.ID, storage.ID)
	require.NoError(t, err)
}

func TestDeleteSecretStorageRefusesInventoryCredentialReference(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage inventory reference"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{Name: "owned", Type: db.AccessKeySSH, ProjectID: &project.ID, Owner: db.AccessKeySecretStorage, StorageID: &storage.ID})
	require.NoError(t, err)
	_, err = store.CreateInventory(db.Inventory{ProjectID: project.ID, Name: "uses credential", Type: db.InventoryStatic, SSHKeyID: &key.ID})
	require.NoError(t, err)

	err = store.DeleteSecretStorage(project.ID, storage.ID)

	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	_, err = store.GetSecretStorage(project.ID, storage.ID)
	require.NoError(t, err)
	_, err = store.GetAccessKey(project.ID, key.ID)
	require.NoError(t, err)
}

func TestDeleteSecretStorageRollsBackCredentialsWhenOwnedKeyHasHostConfigReference(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "storage host-config reference"})
	require.NoError(t, err)
	storage, err := store.CreateSecretStorage(db.SecretStorage{ProjectID: project.ID, Name: "vault", Type: db.SecretStorageTypeVault})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{Name: "owned", Type: db.AccessKeySSH, ProjectID: &project.ID, Owner: db.AccessKeySecretStorage, StorageID: &storage.ID})
	require.NoError(t, err)
	mapping, err := store.CreateHostConfig(db.HostConfig{ProjectID: project.ID, Type: db.HostConfigHost, Name: "github.com", SSHKeyID: key.ID})
	require.NoError(t, err)

	err = store.DeleteSecretStorage(project.ID, storage.ID)

	assert.ErrorIs(t, err, db.ErrInvalidOperation)
	_, err = store.GetSecretStorage(project.ID, storage.ID)
	require.NoError(t, err)
	storedKey, err := store.GetAccessKey(project.ID, key.ID)
	require.NoError(t, err)
	assert.Equal(t, key.ID, storedKey.ID)
	storedMapping, err := store.GetHostConfig(project.ID, mapping.ID)
	require.NoError(t, err)
	assert.Equal(t, mapping.ID, storedMapping.ID)
}
