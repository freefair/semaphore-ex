package server

import (
	stdsql "database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeSecretDirectStoragePersistsOnlyReferenceInSQL(t *testing.T) {
	for _, providerType := range []db.SecretStorageType{db.SecretStorageTypeVault, db.SecretStorageTypeOpenBao} {
		for _, keyType := range []db.AccessKeyType{db.AccessKeyString, db.AccessKeyLoginPassword, db.AccessKeySSH} {
			t.Run(string(providerType)+"/"+string(keyType), func(t *testing.T) {
				previous := util.Config
				store := sql.InitConfigCreateTestStore()
				t.Cleanup(func() {
					store.Close()
					util.Config = previous
				})
				project, err := store.CreateProject(db.Project{Name: "direct credential storage"})
				require.NoError(t, err)
				storage, err := store.CreateSecretStorage(db.SecretStorage{
					ProjectID: project.ID, Name: "external", Type: providerType, ReadOnly: false,
				})
				require.NoError(t, err)
				remoteVersion := 0
				remoteData := map[string]any{}
				requests := 0
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					assert.Equal(t, "/v1/team/data/credentials/service", r.URL.Path)
					assert.Equal(t, "bootstrap-test-token", r.Header.Get("X-Vault-Token"))
					w.Header().Set("Content-Type", "application/json")
					switch r.Method {
					case http.MethodGet:
						if remoteVersion == 0 {
							http.NotFound(w, r)
							return
						}
						require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
							"data": remoteData, "metadata": map[string]int{"version": remoteVersion},
						}}))
					case http.MethodPost, http.MethodPatch:
						var body struct {
							Options map[string]int `json:"options"`
							Data    map[string]any `json:"data"`
						}
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						require.Equal(t, remoteVersion, body.Options["cas"])
						require.Len(t, body.Data, 1)
						require.Contains(t, body.Data, "credential")
						if remoteVersion == 0 {
							assert.Equal(t, http.MethodPost, r.Method)
						} else {
							assert.Equal(t, http.MethodPatch, r.Method)
							assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"))
						}
						remoteData["credential"] = body.Data["credential"]
						remoteData["sibling"] = "untouched"
						remoteVersion++
						require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
							"data": map[string]int{"version": remoteVersion},
						}))
					default:
						assert.Fail(t, "unexpected provider request")
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				}))
				t.Cleanup(provider.Close)
				storage.Params = db.MapStringAnyField{
					"url": provider.URL, "mount": "team", "auth_method": "token",
				}
				storages := &runtimeStorageRepository{storage: storage}
				capabilities := runtimeSecretWriteCapability{enabled: true}
				encryption := NewAccessKeyEncryptionService(store, nil, storages, nil, capabilities)
				bootstrap := db.AccessKey{
					Name: "bootstrap", Type: db.AccessKeyString, ProjectID: &project.ID,
					Owner: db.AccessKeySecretStorage, StorageID: &storage.ID, String: "bootstrap-test-token",
				}
				require.NoError(t, encryption.SerializeSecret(&bootstrap))
				_, err = store.CreateAccessKey(bootstrap)
				require.NoError(t, err)
				service := NewAccessKeyService(store, encryption, storages, capabilities)
				sourceType := db.AccessKeySourceStorageVault
				path := "credentials/service"
				key := db.AccessKey{
					Name: "external credential", Type: keyType, ProjectID: &project.ID,
					SourceStorageType: &sourceType, SourceStorageID: &storage.ID,
					SourceStorageKey: &path, SourceStorageField: "credential",
				}
				setDirectStorageTestValue(&key, "external-first")

				created, err := service.Create(key)
				require.NoError(t, err)
				assertRuntimeReferenceOnlySQL(t, store, project.ID, created.ID)
				var credentialCount int
				require.NoError(t, store.Sql().QueryRow(
					"select count(*) from access_key where project_id=? and owner=?",
					project.ID, db.AccessKeyShared,
				).Scan(&credentialCount))
				assert.Equal(t, 1, credentialCount, "direct storage must not create a local-origin copy")
				assert.Equal(t, 1, remoteVersion)
				assert.Equal(t, "untouched", remoteData["sibling"])
				resolved := created
				require.NoError(t, encryption.DeserializeSecret(&resolved))
				assertDirectStorageTestValue(t, resolved, "external-first")

				local := db.AccessKey{Name: "was local", Type: keyType, ProjectID: &project.ID}
				setDirectStorageTestValue(&local, "old-local-value")
				local, err = service.Create(local)
				require.NoError(t, err)
				require.NotNil(t, local.Secret)
				_, err = store.Sql().Exec(
					"update access_key set plain=? where project_id=? and id=?",
					"legacy-local-sensitive-state", project.ID, local.ID,
				)
				require.NoError(t, err)
				key.ID = local.ID
				key.OverrideSecret = true
				setDirectStorageTestValue(&key, "external-override")
				require.NoError(t, service.Update(key))
				assertRuntimeReferenceOnlySQL(t, store, project.ID, local.ID)
				require.NoError(t, store.Sql().QueryRow(
					"select count(*) from access_key where project_id=? and owner=? and secret is not null",
					project.ID, db.AccessKeyShared,
				).Scan(&credentialCount))
				assert.Zero(t, credentialCount, "no shared credential may retain a local secret")
				assert.Equal(t, 2, remoteVersion)
				assert.Equal(t, "untouched", remoteData["sibling"])
				resolved, err = store.GetAccessKey(project.ID, local.ID)
				require.NoError(t, err)
				require.NoError(t, encryption.DeserializeSecret(&resolved))
				assertDirectStorageTestValue(t, resolved, "external-override")
				assert.Equal(t, 6, requests)
			})
		}
	}
}

func TestRuntimeSecretReferenceOnlyCreationDoesNotContactProvider(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "read-only", false: "writable"}[readOnly], func(t *testing.T) {
			previous := util.Config
			store := sql.InitConfigCreateTestStore()
			t.Cleanup(func() {
				store.Close()
				util.Config = previous
			})
			project, err := store.CreateProject(db.Project{Name: "runtime reference"})
			require.NoError(t, err)
			storage, err := store.CreateSecretStorage(db.SecretStorage{
				ProjectID: project.ID, Name: "external", Type: db.SecretStorageTypeOpenBao, ReadOnly: readOnly,
			})
			require.NoError(t, err)
			// No bootstrap key or reachable provider is needed for a reference.
			storage.Params = db.MapStringAnyField{"url": "http://127.0.0.1:1", "mount": "team"}
			storages := &runtimeStorageRepository{storage: storage}
			capabilities := runtimeSecretWriteCapability{enabled: true}
			encryption := NewAccessKeyEncryptionService(store, nil, storages, nil, capabilities)
			service := NewAccessKeyService(store, encryption, storages, capabilities)
			sourceType := db.AccessKeySourceStorageVault
			path := "credentials/service"
			key, err := service.Create(db.AccessKey{
				Name: "reference", ProjectID: &project.ID, Type: db.AccessKeyString,
				SourceStorageType: &sourceType, SourceStorageID: &storage.ID,
				SourceStorageKey: &path, SourceStorageField: "credential",
				SourceStorageVersion: 4,
			})
			require.NoError(t, err)
			assertRuntimeReferenceOnlySQL(t, store, project.ID, key.ID)
			reference, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey)
			require.NoError(t, err)
			assert.Equal(t, 4, reference.Version)
		})
	}
}

func assertRuntimeReferenceOnlySQL(t *testing.T, store *sql.SqlDb, projectID, keyID int) {
	t.Helper()
	var secret, plain stdsql.NullString
	require.NoError(t, store.Sql().QueryRow(
		"select secret, plain from access_key where project_id=? and id=?", projectID, keyID,
	).Scan(&secret, &plain))
	assert.False(t, secret.Valid, "external credential must have SQL NULL secret")
	assert.False(t, plain.Valid, "external credential must have SQL NULL plain")
	key, err := store.GetAccessKey(projectID, keyID)
	require.NoError(t, err)
	assert.Nil(t, key.Secret)
	assert.Nil(t, key.Plain)
	assert.Empty(t, key.String)
	assert.Equal(t, db.LoginPassword{}, key.LoginPassword)
	assert.Equal(t, db.SshKey{}, key.SshKey)
	require.NotNil(t, key.SourceStorageID)
	require.NotNil(t, key.SourceStorageType)
	assert.Equal(t, db.AccessKeySourceStorageVault, *key.SourceStorageType)
	require.NotNil(t, key.SourceStorageKey)
	reference, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey)
	require.NoError(t, err)
	assert.Equal(t, *key.SourceStorageID, reference.StorageID)
}

func setDirectStorageTestValue(key *db.AccessKey, value string) {
	key.String = ""
	key.LoginPassword = db.LoginPassword{}
	key.SshKey = db.SshKey{}
	switch key.Type {
	case db.AccessKeyString:
		key.String = value
	case db.AccessKeyLoginPassword:
		key.LoginPassword = db.LoginPassword{Login: "service", Password: value}
	case db.AccessKeySSH:
		key.SshKey = db.SshKey{Login: "service", Passphrase: "phrase", PrivateKey: value}
	}
}

func assertDirectStorageTestValue(t *testing.T, key db.AccessKey, value string) {
	t.Helper()
	switch key.Type {
	case db.AccessKeyString:
		assert.Equal(t, value, key.String)
	case db.AccessKeyLoginPassword:
		assert.Equal(t, db.LoginPassword{Login: "service", Password: value}, key.LoginPassword)
	case db.AccessKeySSH:
		assert.Equal(t, db.SshKey{Login: "service", Passphrase: "phrase", PrivateKey: value}, key.SshKey)
	}
}
