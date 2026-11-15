package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalSecretStorageRepository(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	first, err := store.CreateProject(db.Project{Name: "first"})
	require.NoError(t, err)
	second, err := store.CreateProject(db.Project{Name: "second"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{Name: "client", Type: db.AccessKeyString, Owner: db.AccessKeyGlobalSecretStorage})
	require.NoError(t, err)

	created, err := store.CreateGlobalSecretStorage(db.GlobalSecretStorage{
		Name: " Infisical ", Type: db.SecretStorageTypeInfisical,
		Params:          db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client"},
		CredentialKeyID: &key.ID, ProjectIDs: []int{second.ID, first.ID, second.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, "Infisical", created.Name)
	assert.Equal(t, 1, created.Revision)
	assert.Equal(t, []int{first.ID, second.ID}, created.ProjectIDs)
	assert.Equal(t, "https://app.infisical.com", created.Params["url"])

	t.Run("lists by project", func(t *testing.T) {
		forFirst, err := store.GetGlobalSecretStoragesForProject(first.ID)
		require.NoError(t, err)
		require.Len(t, forFirst, 1)
		assert.Equal(t, created.ID, forFirst[0].ID)
	})

	t.Run("update requires the current revision", func(t *testing.T) {
		changed := created
		changed.ProjectIDs = []int{second.ID}
		updated, err := store.UpdateGlobalSecretStorage(changed, created.Revision)
		require.NoError(t, err)
		assert.Equal(t, 2, updated.Revision)
		assert.Equal(t, []int{second.ID}, updated.ProjectIDs)
		forFirst, err := store.GetGlobalSecretStoragesForProject(first.ID)
		require.NoError(t, err)
		assert.Empty(t, forFirst)

		_, err = store.UpdateGlobalSecretStorage(changed, created.Revision)
		assert.ErrorIs(t, err, db.ErrGlobalCredentialRevisionConflict)
	})

	t.Run("names are unique", func(t *testing.T) {
		_, err := store.CreateGlobalSecretStorage(db.GlobalSecretStorage{Name: "Infisical", Type: db.SecretStorageTypeInfisical})
		assert.Error(t, err)
	})

	t.Run("loads only its own project-less key", func(t *testing.T) {
		loaded, err := store.GetGlobalSecretStorageKey(key.ID)
		require.NoError(t, err)
		assert.Equal(t, key.ID, loaded.ID)

		projectKey, err := store.CreateAccessKey(db.AccessKey{Name: "project", Type: db.AccessKeyString, ProjectID: &first.ID})
		require.NoError(t, err)
		_, err = store.GetGlobalSecretStorageKey(projectKey.ID)
		assert.ErrorIs(t, err, db.ErrNotFound)
		require.NoError(t, store.DeleteGlobalSecretStorageKey(projectKey.ID))
		_, err = store.GetAccessKey(first.ID, projectKey.ID)
		assert.NoError(t, err, "deleting through the global path must not touch project keys")
	})

	t.Run("delete removes the grants", func(t *testing.T) {
		require.NoError(t, store.DeleteGlobalSecretStorage(created.ID))
		_, err := store.GetGlobalSecretStorage(created.ID)
		assert.ErrorIs(t, err, db.ErrNotFound)
		forSecond, err := store.GetGlobalSecretStoragesForProject(second.ID)
		require.NoError(t, err)
		assert.Empty(t, forSecond)
		assert.ErrorIs(t, store.DeleteGlobalSecretStorage(created.ID), db.ErrNotFound)
	})
}

func TestGlobalSecretStorageProviderID(t *testing.T) {
	assert.Equal(t, "storage-12", db.GlobalSecretStorageProviderID(12))
	for _, valid := range []string{"storage-1", "storage-12"} {
		_, ok := db.ParseGlobalSecretStorageProviderID(valid)
		assert.True(t, ok, valid)
	}
	for _, invalid := range []string{"", "storage-", "storage-0", "storage-012", "storage--1", "storage-1a", "primary"} {
		_, ok := db.ParseGlobalSecretStorageProviderID(invalid)
		assert.False(t, ok, invalid)
	}
}

func TestInfisicalGlobalCredentialReferenceValidation(t *testing.T) {
	valid := db.GlobalCredentialExternalReference{
		Provider: db.GlobalCredentialProviderInfisical, ProviderID: "storage-3",
		Mount: "5c2a1b7e-0f3d-4b6a-9e2f-1d8c7b6a5e4f", Environment: "prod", Field: "DB_PASSWORD",
	}
	tests := []struct {
		name   string
		change func(*db.GlobalCredentialExternalReference)
		valid  bool
	}{
		{"root folder, current version", func(*db.GlobalCredentialExternalReference) {}, true},
		{"nested folder and pinned version", func(r *db.GlobalCredentialExternalReference) { r.Path = "apps/web"; r.Version = 4 }, true},
		{"provider ID of a configured provider", func(r *db.GlobalCredentialExternalReference) { r.ProviderID = "primary" }, false},
		{"missing environment", func(r *db.GlobalCredentialExternalReference) { r.Environment = "" }, false},
		{"absolute folder", func(r *db.GlobalCredentialExternalReference) { r.Path = "/apps" }, false},
		{"traversal", func(r *db.GlobalCredentialExternalReference) { r.Path = "apps/.." }, false},
		{"negative version", func(r *db.GlobalCredentialExternalReference) { r.Version = -1 }, false},
		{"secret name with a slash", func(r *db.GlobalCredentialExternalReference) { r.Field = "a/b" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reference := valid
			tt.change(&reference)
			if tt.valid {
				assert.NoError(t, reference.Validate())
			} else {
				assert.Error(t, reference.Validate())
			}
		})
	}
	t.Run("vault references reject an environment", func(t *testing.T) {
		vault := db.GlobalCredentialExternalReference{Provider: "vault", ProviderID: "primary", Mount: "kv", Path: "app", Version: 1, Field: "token", Environment: "prod"}
		assert.Error(t, vault.Validate())
	})
	assert.Equal(t, "/apps/web", db.GlobalCredentialExternalReference{Path: "apps/web"}.InfisicalSecretPath())
	assert.Equal(t, "/", db.GlobalCredentialExternalReference{}.InfisicalSecretPath())
}
