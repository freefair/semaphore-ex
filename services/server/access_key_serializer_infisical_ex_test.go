package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// singleStorageRepository serves one project storage by ID.
type singleStorageRepository struct {
	db.SecretStorageRepository
	storage db.SecretStorage
}

func (r singleStorageRepository) GetSecretStorage(projectID int, storageID int) (db.SecretStorage, error) {
	if projectID != r.storage.ProjectID || storageID != r.storage.ID {
		return db.SecretStorage{}, db.ErrNotFound
	}
	return r.storage, nil
}

// plainCredentialDecryption treats the stored credential as already decrypted.
type plainCredentialDecryption struct {
	AccessKeyEncryptionService
}

func (plainCredentialDecryption) DeserializeSecret(key *db.AccessKey) error {
	return nil
}

func TestInfisicalAccessKeyDeserializerReadsTheReferencedSecret(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{Secrets: map[string]pamtest.FakeSecret{
		pamtest.SecretKey("proj-1", "prod", "/apps/web", "DB_PASSWORD"): {Value: "web-value", Version: 4},
		pamtest.SecretKey("proj-1", "prod", "/", "ROOT_TOKEN"):          {Value: "root-value", Version: 1},
	}})
	storage := db.SecretStorage{ID: 5, ProjectID: 3, Type: db.SecretStorageTypeInfisical, Params: db.MapStringAnyField{
		"url": env.API.URL, "client_id": pamtest.ClientID, "ca_certificate": string(env.APICACert),
	}}
	keys := infisicalKeyRepository{keys: map[int][]db.AccessKey{5: {{ID: 50, Type: db.AccessKeyString, String: pamtest.ClientSecret}}}}
	deserializer := newInfisicalAccessKeyDeserializer(keys, singleStorageRepository{storage: storage}, plainCredentialDecryption{})
	projectID, storageID := 3, 5
	key := func(reference pro_interfaces.SecretReference) *db.AccessKey {
		encoded, err := reference.Encode()
		require.NoError(t, err)
		vault := db.AccessKeySourceStorageVault
		return &db.AccessKey{ProjectID: &projectID, SourceStorageID: &storageID, SourceStorageType: &vault, SourceStorageKey: &encoded}
	}

	t.Run("a secret in a folder", func(t *testing.T) {
		value, err := deserializer.DeserializeSecret(key(pro_interfaces.SecretReference{StorageID: 5, Mount: "proj-1", Path: "prod/apps/web", Field: "DB_PASSWORD"}))
		require.NoError(t, err)
		assert.Equal(t, "web-value", value)
	})

	t.Run("a secret in the root folder", func(t *testing.T) {
		value, err := deserializer.DeserializeSecret(key(pro_interfaces.SecretReference{StorageID: 5, Mount: "proj-1", Path: "prod", Field: "ROOT_TOKEN"}))
		require.NoError(t, err)
		assert.Equal(t, "root-value", value)
	})

	t.Run("a pinned version that does not exist", func(t *testing.T) {
		_, err := deserializer.DeserializeSecret(key(pro_interfaces.SecretReference{StorageID: 5, Mount: "proj-1", Path: "prod/apps/web", Field: "DB_PASSWORD", Version: 2}))
		assert.Error(t, err)
	})

	t.Run("a reference to another storage", func(t *testing.T) {
		_, err := deserializer.DeserializeSecret(key(pro_interfaces.SecretReference{StorageID: 6, Mount: "proj-1", Path: "prod", Field: "ROOT_TOKEN"}))
		assert.Error(t, err)
	})

	t.Run("errors carry neither the reference nor the provider message", func(t *testing.T) {
		_, err := deserializer.DeserializeSecret(key(pro_interfaces.SecretReference{StorageID: 5, Mount: "proj-1", Path: "prod/missing", Field: "NOPE"}))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "NOPE")
		assert.NotContains(t, err.Error(), "proj-1")
	})

	t.Run("Infisical storages cannot be written", func(t *testing.T) {
		assert.Error(t, deserializer.SerializeSecret(&db.AccessKey{}))
		assert.Error(t, deserializer.DeleteSecret(&db.AccessKey{}))
	})
}

func TestNormalizeRuntimeSecretReferenceAcceptsInfisicalStorages(t *testing.T) {
	storage := db.SecretStorage{ID: 5, ProjectID: 3, Type: db.SecretStorageTypeInfisical}
	projectID, storageID := 3, 5
	vault := db.AccessKeySourceStorageVault
	build := func(path string) *db.AccessKey {
		return &db.AccessKey{ProjectID: &projectID, SourceStorageID: &storageID, SourceStorageType: &vault,
			SourceStorageKey: &path, SourceStorageMount: "proj-1", SourceStorageField: "DB_PASSWORD"}
	}

	accepted := build("prod/apps/web")
	require.NoError(t, normalizeRuntimeSecretReference(accepted, singleStorageRepository{storage: storage}))
	reference, err := pro_interfaces.DecodeSecretReference(*accepted.SourceStorageKey)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.SecretReference{StorageID: 5, Mount: "proj-1", Path: "prod/apps/web", Field: "DB_PASSWORD"}, reference)

	assert.Error(t, normalizeRuntimeSecretReference(build("Prod Env/apps"), singleStorageRepository{storage: storage}),
		"the first path segment must be an environment slug")
}
