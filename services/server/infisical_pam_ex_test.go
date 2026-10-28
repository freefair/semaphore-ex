package server

import (
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func infisicalStorage(params db.MapStringAnyField) db.SecretStorage {
	return db.SecretStorage{ProjectID: 1, Name: "infisical", Type: db.SecretStorageTypeInfisical, Params: params, Secret: "client-secret"}
}

func validInfisicalParams() db.MapStringAnyField {
	return db.MapStringAnyField{
		"url":       "https://infisical.example.com/",
		"client_id": "3f6b2a1c-1111-2222-3333-444455556666",
		"pam_routes": []any{
			map[string]any{"host": " Web-1.Example.com ", "account": "servers/web-1"},
			map[string]any{"host": "db-1", "account": "servers/db-1"},
		},
	}
}

func TestValidateInfisicalStorageNormalizes(t *testing.T) {
	storage := infisicalStorage(validInfisicalParams())
	storage.Params["unknown"] = "dropped"

	require.NoError(t, ValidateRuntimeSecretStorage(&storage))

	assert.Equal(t, db.MapStringAnyField{
		"url":              "https://infisical.example.com",
		"client_id":        "3f6b2a1c-1111-2222-3333-444455556666",
		"session_duration": "1h0m0s",
		"pam_routes": []any{
			map[string]any{"host": "web-1.example.com", "account": "servers/web-1"},
			map[string]any{"host": "db-1", "account": "servers/db-1"},
		},
	}, storage.Params)
	assert.True(t, storage.ReadOnly)
	assert.Equal(t, db.SecretSyncDirectionReadOnly, storage.SyncDirection)
	assert.False(t, storage.SyncEnabled)
}

func TestValidateInfisicalStorageRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*db.SecretStorage)
		message string
	}{
		{"http url", func(s *db.SecretStorage) { s.Params["url"] = "http://infisical.example.com" }, "https"},
		{"missing client id", func(s *db.SecretStorage) { delete(s.Params, "client_id") }, "client id"},
		{"bad organization", func(s *db.SecretStorage) { s.Params["organization_slug"] = "Bad Org" }, "organization slug"},
		{"bad CA", func(s *db.SecretStorage) { s.Params["ca_certificate"] = "not pem" }, "CA certificate"},
		{"short session", func(s *db.SecretStorage) { s.Params["session_duration"] = "10s" }, "session duration"},
		{"long session", func(s *db.SecretStorage) { s.Params["session_duration"] = "48h" }, "session duration"},
		{"wildcard host", func(s *db.SecretStorage) {
			s.Params["pam_routes"] = []any{map[string]any{"host": "*.example.com", "account": "a/b"}}
		}, "host name"},
		{"config injection host", func(s *db.SecretStorage) {
			s.Params["pam_routes"] = []any{map[string]any{"host": "web\n  ProxyCommand sh", "account": "a/b"}}
		}, "host name"},
		{"duplicate host", func(s *db.SecretStorage) {
			s.Params["pam_routes"] = []any{
				map[string]any{"host": "web", "account": "a/b"},
				map[string]any{"host": "WEB", "account": "a/c"},
			}
		}, "mapped twice"},
		{"account without folder", func(s *db.SecretStorage) {
			s.Params["pam_routes"] = []any{map[string]any{"host": "web", "account": "web"}}
		}, "folder/account"},
		{"routes not a list", func(s *db.SecretStorage) { s.Params["pam_routes"] = "web=a/b" }, "PAM routes"},
		{"missing secret on create", func(s *db.SecretStorage) { s.Secret = "" }, "client secret"},
		{"sync requested", func(s *db.SecretStorage) { s.SyncDirection = db.SecretSyncDirectionOutbound }, "synchronized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := infisicalStorage(validInfisicalParams())
			tt.mutate(&storage)

			err := ValidateRuntimeSecretStorage(&storage)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestValidateInfisicalStorageAllowsUpdateWithoutSecret(t *testing.T) {
	storage := infisicalStorage(validInfisicalParams())
	storage.ID = 4
	storage.Secret = ""

	assert.NoError(t, ValidateRuntimeSecretStorage(&storage))
}

type infisicalStorageRepository struct {
	db.SecretStorageRepository
	storages []db.SecretStorage
}

func (r infisicalStorageRepository) GetSecretStorages(int) ([]db.SecretStorage, error) {
	return r.storages, nil
}

type infisicalKeyRepository struct {
	db.AccessKeyManager
	keys map[int][]db.AccessKey
}

func (r infisicalKeyRepository) GetAccessKeys(_ int, options db.GetAccessKeyOptions, _ db.RetrieveQueryParams) ([]db.AccessKey, error) {
	if options.Owner != db.AccessKeySecretStorage || options.StorageID == nil {
		return nil, errors.New("unexpected query")
	}
	return r.keys[*options.StorageID], nil
}

func storedInfisical(id int, routes []any) db.SecretStorage {
	params := validInfisicalParams()
	params["pam_routes"] = routes
	storage := infisicalStorage(params)
	storage.ID = id
	storage.Secret = ""
	return storage
}

func TestLoadProjectInfisicalPAM(t *testing.T) {
	storages := infisicalStorageRepository{storages: []db.SecretStorage{
		{ID: 1, Type: db.SecretStorageTypeVault},
		storedInfisical(2, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}}),
		storedInfisical(3, []any{}),
	}}
	keys := infisicalKeyRepository{keys: map[int][]db.AccessKey{2: {{ID: 20, Type: db.AccessKeyString}}}}

	configs, err := LoadProjectInfisicalPAM(storages, keys, 1)

	require.NoError(t, err)
	require.Len(t, configs, 1)
	assert.Equal(t, 2, configs[0].StorageID)
	assert.Equal(t, 20, configs[0].CredentialKeyID)
	assert.Equal(t, 20, configs[0].Credential.ID)
	assert.Equal(t, []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}}, configs[0].Routes)
	assert.Equal(t, "1h0m0s", configs[0].SessionDuration)
}

func TestLoadProjectInfisicalPAMRejectsHostRoutedTwice(t *testing.T) {
	route := []any{map[string]any{"host": "web-1", "account": "servers/web-1"}}
	storages := infisicalStorageRepository{storages: []db.SecretStorage{storedInfisical(2, route), storedInfisical(3, route)}}
	keys := infisicalKeyRepository{keys: map[int][]db.AccessKey{2: {{ID: 20}}, 3: {{ID: 30}}}}

	_, err := LoadProjectInfisicalPAM(storages, keys, 1)

	assert.ErrorContains(t, err, "more than one Infisical storage")
}

func TestLoadProjectInfisicalPAMRequiresCredential(t *testing.T) {
	storages := infisicalStorageRepository{storages: []db.SecretStorage{
		storedInfisical(2, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}}),
	}}

	_, err := LoadProjectInfisicalPAM(storages, infisicalKeyRepository{keys: map[int][]db.AccessKey{}}, 1)

	assert.ErrorContains(t, err, "no client secret")
}

func TestValidateTaskInfisicalPAM(t *testing.T) {
	config := db.TaskInfisicalPAM{
		SiteURL: "https://infisical.example.com", ClientID: "abc", SessionDuration: "30m0s",
		Routes: []db.InfisicalPAMRoute{{Host: "web", Account: "a/b"}},
	}
	assert.NoError(t, ValidateTaskInfisicalPAM(config))

	config.Routes = append(config.Routes, db.InfisicalPAMRoute{Host: "bad host", Account: "a/b"})
	assert.Error(t, ValidateTaskInfisicalPAM(config))
}
