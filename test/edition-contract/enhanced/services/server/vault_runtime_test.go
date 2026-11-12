package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeSecretStorageRepo struct {
	db.SecretStorageRepository
	storage db.SecretStorage
	err     error
}

func (r *runtimeSecretStorageRepo) GetSecretStorage(projectID int, storageID int) (db.SecretStorage, error) {
	if r.err != nil {
		return db.SecretStorage{}, r.err
	}
	if r.storage.ProjectID != projectID || r.storage.ID != storageID {
		return db.SecretStorage{}, db.ErrNotFound
	}
	return r.storage, nil
}

type runtimeSecretAccessKeyRepo struct {
	db.AccessKeyManager
	key db.AccessKey
}

func (r *runtimeSecretAccessKeyRepo) GetAccessKeys(
	projectID int,
	options db.GetAccessKeyOptions,
	_ db.RetrieveQueryParams,
) ([]db.AccessKey, error) {
	if r.key.ProjectID == nil || *r.key.ProjectID != projectID || options.StorageID == nil {
		return []db.AccessKey{}, nil
	}
	return []db.AccessKey{r.key}, nil
}

type runtimeCredentialReader struct {
	credential string
	reads      atomic.Int64
}

func (r *runtimeCredentialReader) DeserializeSecret(key *db.AccessKey) error {
	r.reads.Add(1)
	key.String = r.credential
	return nil
}

type runtimeCapabilityProvider struct{ active bool }

func (p runtimeCapabilityProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	state := pro_interfaces.CapabilityStateDisabled
	reason := pro_interfaces.CapabilityReasonDisabledByAdmin
	var access []pro_interfaces.CapabilityAccess
	if p.active {
		state = pro_interfaces.CapabilityStateActive
		reason = pro_interfaces.CapabilityReasonActive
		access = []pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessWrite, pro_interfaces.CapabilityAccessExecute,
		}
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets, state, reason, access, nil,
		),
	}), nil
}

func (p runtimeCapabilityProvider) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, errors.New("not implemented")
}

func (p runtimeCapabilityProvider) GetRuntimeSecretsConfiguration(
	context.Context,
	pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilityConfiguration, error) {
	return pro_interfaces.CapabilityConfiguration{
		ID: pro_interfaces.CapabilityRuntimeSecrets,
	}, nil
}

func TestVaultRuntimeReadsVersionedKVWithoutPersistingValue(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, "/v1/team/data/app/api", r.URL.Path)
		assert.Equal(t, "4", r.URL.Query().Get("version"))
		assert.Equal(t, "bootstrap-token", r.Header.Get("X-Vault-Token"))
		assert.Equal(t, "org/platform", r.Header.Get("X-Vault-Namespace"))
		writeJSON(t, w, map[string]any{"data": map[string]any{
			"data": map[string]any{"password": "resolved-only-in-memory"},
		}})
	}))
	defer server.Close()
	deserializer, reader := newRuntimeDeserializer(server.URL, db.MapStringAnyField{
		"namespace": "org/platform", "auth_method": "token",
	}, "bootstrap-token", runtimeCapabilityProvider{active: true})
	reference := pro_interfaces.SecretReference{
		StorageID: 9, Mount: "team", Path: "app/api", Version: 4, Field: "password",
	}

	value, err := deserializer.ResolveRuntimeSecret(context.Background(), 3, reference)

	require.NoError(t, err)
	assert.Equal(t, "resolved-only-in-memory", string(value))
	zero(value)
	assert.Equal(t, int64(1), requests.Load())
	assert.Equal(t, int64(1), reader.reads.Load())
	assert.Empty(t, deserializer.client.cache, "static and returned secrets must not be cached")
}

func TestVaultRuntimeDeserializesCanonicalReferenceAtExecutionBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/secret/data/app/runtime", r.URL.Path)
		writeSecretResponse(t, w, "token", "task-only-value")
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializer(
		server.URL, nil, "bootstrap-token", runtimeCapabilityProvider{active: true},
	)
	reference := pro_interfaces.SecretReference{
		StorageID: 9, Mount: "secret", Path: "app/runtime", Field: "token",
	}
	encoded, err := reference.Encode()
	require.NoError(t, err)
	projectID := 3
	storageID := 9
	sourceType := db.AccessKeySourceStorageVault
	key := db.AccessKey{
		ProjectID: &projectID, SourceStorageID: &storageID,
		SourceStorageType: &sourceType, SourceStorageKey: &encoded,
	}

	value, err := deserializer.DeserializeSecret(&key)

	require.NoError(t, err)
	assert.Equal(t, "task-only-value", value)
	assert.Equal(t, encoded, *key.SourceStorageKey)
	assert.Empty(t, deserializer.client.cache)
}

func TestVaultManagedSecretFieldUsesKVPatchAndCAS(t *testing.T) {
	var patchRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/team/data/apps/api", r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, map[string]any{"data": map[string]any{
				"data":     map[string]any{"password": "remote-before"},
				"metadata": map[string]any{"version": 2},
			}})
		case http.MethodPatch:
			patchRequests.Add(1)
			assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"))
			var body struct {
				Options map[string]int    `json:"options"`
				Data    map[string]string `json:"data"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, 2, body.Options["cas"])
			assert.Equal(t, "local-after", body.Data["password"])
			writeJSON(t, w, map[string]any{"data": map[string]any{"version": 3}})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializerWithStorage(
		server.URL, nil, "bootstrap-token", runtimeCapabilityProvider{active: true},
		db.SecretStorageTypeVault, false,
	)
	reference := pro_interfaces.SecretReference{
		StorageID: 9, Mount: "team", Path: "apps/api", Field: "password",
	}

	field, err := deserializer.ReadManagedSecretField(context.Background(), 3, reference)
	require.NoError(t, err)
	assert.True(t, field.Exists)
	assert.Equal(t, 2, field.Version)
	assert.Equal(t, "remote-before", string(field.Value))
	zero(field.Value)
	version, err := deserializer.WriteManagedSecretField(
		context.Background(), 3, reference, []byte("local-after"), 2,
	)
	require.NoError(t, err)
	assert.Equal(t, 3, version)
	assert.Equal(t, int64(1), patchRequests.Load())
}

func TestVaultManagedSecretFieldClassifiesMissingAndCASConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializerWithStorage(
		server.URL, nil, "bootstrap-token", runtimeCapabilityProvider{active: true},
		db.SecretStorageTypeVault, false,
	)
	reference := pro_interfaces.SecretReference{
		StorageID: 9, Mount: "secret", Path: "missing", Field: "value",
	}
	field, err := deserializer.ReadManagedSecretField(context.Background(), 3, reference)
	require.NoError(t, err)
	assert.False(t, field.Exists)
	assert.Zero(t, field.Version)
	_, err = deserializer.WriteManagedSecretField(
		context.Background(), 3, reference, []byte("not-in-error"), 0,
	)
	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorConflict, errorCategory(err))
	assert.NotContains(t, err.Error(), "not-in-error")
}

func TestVaultRuntimeSerializesCredentialsDirectlyToVaultAndOpenBao(t *testing.T) {
	tests := []struct {
		name        string
		storageType db.SecretStorageType
		keyType     db.AccessKeyType
		stringValue string
		loginPass   db.LoginPassword
		sshKey      db.SshKey
		expected    string
		method      string
		version     int
	}{
		{
			name: "Vault string creates missing KV v2 document", storageType: db.SecretStorageTypeVault,
			keyType: db.AccessKeyString, stringValue: "string-secret", expected: "string-secret",
			method: http.MethodPost, version: 0,
		},
		{
			name: "OpenBao login password updates only the field", storageType: db.SecretStorageTypeOpenBao,
			keyType: db.AccessKeyLoginPassword, loginPass: db.LoginPassword{Login: "svc", Password: "password"},
			expected: `{"login":"svc","password":"password"}`, method: http.MethodPatch, version: 7,
		},
		{
			name: "OpenBao login password creates missing KV v2 document", storageType: db.SecretStorageTypeOpenBao,
			keyType: db.AccessKeyLoginPassword, loginPass: db.LoginPassword{Login: "svc", Password: "password"},
			expected: `{"login":"svc","password":"password"}`, method: http.MethodPost, version: 0,
		},
		{
			name: "Vault login password updates only the field", storageType: db.SecretStorageTypeVault,
			keyType: db.AccessKeyLoginPassword, loginPass: db.LoginPassword{Login: "svc", Password: "password"},
			expected: `{"login":"svc","password":"password"}`, method: http.MethodPatch, version: 7,
		},
		{
			name: "Vault SSH key creates missing KV v2 document", storageType: db.SecretStorageTypeVault,
			keyType: db.AccessKeySSH, sshKey: db.SshKey{
				Login: "deploy", Passphrase: "phrase", PrivateKey: "private-key",
			},
			expected: `{"login":"deploy","passphrase":"phrase","private_key":"private-key"}`,
			method:   http.MethodPost, version: 0,
		},
		{
			name: "OpenBao string updates only the field", storageType: db.SecretStorageTypeOpenBao,
			keyType: db.AccessKeyString, stringValue: "updated-secret", expected: "updated-secret",
			method: http.MethodPatch, version: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/team/data/apps/credential", r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					if tt.version == 0 {
						http.NotFound(w, r)
						return
					}
					writeJSON(t, w, map[string]any{"data": map[string]any{
						"data":     map[string]any{"secret": "remote-before", "sibling": "preserve"},
						"metadata": map[string]any{"version": tt.version},
					}})
				case tt.method:
					if tt.method == http.MethodPatch {
						assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"))
					} else {
						assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
					}
					var body struct {
						Options map[string]int `json:"options"`
						Data    map[string]any `json:"data"`
					}
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					assert.Equal(t, tt.version, body.Options["cas"])
					assert.Equal(t, map[string]any{"secret": tt.expected}, body.Data,
						"writes must neither copy siblings nor serialize locally")
					writeJSON(t, w, map[string]any{"data": map[string]any{"version": tt.version + 1}})
				default:
					assert.Failf(t, "unexpected method", "method=%s", r.Method)
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()
			deserializer, _ := newRuntimeDeserializerWithStorage(
				server.URL, db.MapStringAnyField{"mount": "team"}, "bootstrap-token",
				runtimeCapabilityProvider{active: true}, tt.storageType, false,
			)
			projectID, storageID := 3, 9
			sourceType := db.AccessKeySourceStorageVault
			path := "apps/credential"
			key := db.AccessKey{
				ProjectID: &projectID, SourceStorageID: &storageID,
				SourceStorageType: &sourceType, SourceStorageKey: &path,
				SourceStorageMount: "team", SourceStorageField: "secret", Type: tt.keyType,
				String: tt.stringValue, LoginPassword: tt.loginPass, SshKey: tt.sshKey,
			}

			err := deserializer.SerializeSecret(&key)

			require.NoError(t, err)
			assert.Empty(t, key.String)
			assert.Equal(t, db.LoginPassword{}, key.LoginPassword)
			assert.Equal(t, db.SshKey{}, key.SshKey)
			assert.Nil(t, key.Secret)
		})
	}
}

func TestVaultRuntimeDeleteSecretRemovesOnlyManagedFieldWithCAS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/team/data/apps/credential", r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, map[string]any{"data": map[string]any{
				"data":     map[string]any{"secret": "private-value", "sibling": "preserve"},
				"metadata": map[string]any{"version": 5},
			}})
		case http.MethodPatch:
			assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"))
			var body struct {
				Options map[string]int `json:"options"`
				Data    map[string]any `json:"data"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, 5, body.Options["cas"])
			assert.Equal(t, map[string]any{"secret": nil}, body.Data)
			writeJSON(t, w, map[string]any{"data": map[string]any{"version": 6}})
		default:
			assert.Failf(t, "unexpected method", "method=%s", r.Method)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializerWithStorage(
		server.URL, db.MapStringAnyField{"mount": "team"}, "bootstrap-token",
		runtimeCapabilityProvider{active: true}, db.SecretStorageTypeVault, false,
	)
	projectID, storageID := 3, 9
	sourceType := db.AccessKeySourceStorageVault
	encoded, err := (pro_interfaces.SecretReference{
		StorageID: storageID, Mount: "team", Path: "apps/credential", Field: "secret",
	}).Encode()
	require.NoError(t, err)
	key := db.AccessKey{
		ProjectID: &projectID, SourceStorageID: &storageID,
		SourceStorageType: &sourceType, SourceStorageKey: &encoded,
		String: "must-not-be-persisted",
	}

	err = deserializer.DeleteSecret(&key)

	require.NoError(t, err)
	assert.Empty(t, key.String)
}

func TestVaultRuntimeDirectWriteHonorsReadOnlyStorage(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	deserializer, reader := newRuntimeDeserializerWithStorage(
		server.URL, nil, "bootstrap-token", runtimeCapabilityProvider{active: true},
		db.SecretStorageTypeVault, true,
	)
	projectID, storageID := 3, 9
	sourceType := db.AccessKeySourceStorageVault
	path := "apps/credential"
	key := db.AccessKey{
		ProjectID: &projectID, SourceStorageID: &storageID,
		SourceStorageType: &sourceType, SourceStorageKey: &path,
		SourceStorageMount: "secret", SourceStorageField: "value",
		Type: db.AccessKeyString, String: "must-not-be-persisted",
	}

	err := deserializer.SerializeSecret(&key)

	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorPermission, errorCategory(err))
	assert.Zero(t, requests.Load())
	assert.Zero(t, reader.reads.Load(), "read-only storage must not read its bootstrap credential")
	assert.Empty(t, key.String)
	assert.Nil(t, key.Secret)
}

func TestVaultRuntimeCredentialWritesReportPermissionAndCASConflicts(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		category pro_interfaces.SecretProviderErrorCategory
	}{
		{
			name: "provider permission denied",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				http.NotFound(w, r)
			},
			category: pro_interfaces.SecretProviderErrorPermission,
		},
		{
			name: "CAS conflict",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSON(t, w, map[string]any{"data": map[string]any{
						"data":     map[string]any{"value": "remote-before"},
						"metadata": map[string]any{"version": 4},
					}})
					return
				}
				w.WriteHeader(http.StatusBadRequest)
			},
			category: pro_interfaces.SecretProviderErrorConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			deserializer, _ := newRuntimeDeserializerWithStorage(
				server.URL, nil, "bootstrap-token", runtimeCapabilityProvider{active: true},
				db.SecretStorageTypeVault, false,
			)
			projectID, storageID := 3, 9
			sourceType := db.AccessKeySourceStorageVault
			path := "apps/credential"
			key := db.AccessKey{
				ProjectID: &projectID, SourceStorageID: &storageID,
				SourceStorageType: &sourceType, SourceStorageKey: &path,
				SourceStorageMount: "secret", SourceStorageField: "value",
				Type: db.AccessKeyString, String: "must-not-be-persisted",
			}

			err := deserializer.SerializeSecret(&key)

			require.Error(t, err)
			assert.Equal(t, tt.category, errorCategory(err))
			assert.NotContains(t, err.Error(), "must-not-be-persisted")
			assert.Empty(t, key.String)
			assert.Nil(t, key.Secret)
		})
	}
}

func TestVaultRuntimeAppRoleCachesAndRenewsOnlyProviderToken(t *testing.T) {
	var logins atomic.Int64
	var renewals atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/custom-approle/login":
			logins.Add(1)
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "role-a", body["role_id"])
			assert.Equal(t, "secret-id-a", body["secret_id"])
			writeAuthResponse(t, w, "short-token", 10, true)
		case "/v1/auth/token/renew-self":
			renewals.Add(1)
			assert.Equal(t, "short-token", r.Header.Get("X-Vault-Token"))
			writeAuthResponse(t, w, "renewed-token", 10, true)
		case "/v1/secret/data/app":
			assert.Contains(t, []string{"short-token", "renewed-token"}, r.Header.Get("X-Vault-Token"))
			writeSecretResponse(t, w, "token", "task-value")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializer(server.URL, db.MapStringAnyField{
		"auth_method": "approle", "auth_mount": "custom-approle", "role_id": "role-a",
	}, "secret-id-a", runtimeCapabilityProvider{active: true})
	current := time.Unix(1_700_000_000, 0).UTC()
	deserializer.client.now = func() time.Time { return current }
	reference := pro_interfaces.SecretReference{StorageID: 9, Mount: "secret", Path: "app", Field: "token"}

	first, err := deserializer.ResolveRuntimeSecret(context.Background(), 3, reference)
	require.NoError(t, err)
	zero(first)
	current = current.Add(9 * time.Second)
	second, err := deserializer.ResolveRuntimeSecret(context.Background(), 3, reference)
	require.NoError(t, err)
	zero(second)

	assert.Equal(t, int64(1), logins.Load())
	assert.Equal(t, int64(1), renewals.Load())
	assert.Equal(t, "renewed-token", string(deserializer.client.cache[9].value))
	assert.NotContains(t, string(deserializer.client.cache[9].value), "task-value")
}

func TestVaultRuntimeSupportsKubernetesJWTAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/k8s-prod/login":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "semaphore", body["role"])
			assert.Equal(t, "service-account-jwt", body["jwt"])
			writeAuthResponse(t, w, "k8s-token", 60, false)
		case "/v1/secret/data/app":
			writeSecretResponse(t, w, "value", "from-kubernetes-auth")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deserializer, _ := newRuntimeDeserializer(server.URL, db.MapStringAnyField{
		"auth_method": "kubernetes", "auth_mount": "k8s-prod", "role": "semaphore",
	}, "service-account-jwt", runtimeCapabilityProvider{active: true})

	value, err := deserializer.ResolveRuntimeSecret(context.Background(), 3, pro_interfaces.SecretReference{
		StorageID: 9, Mount: "secret", Path: "app", Field: "value",
	})

	require.NoError(t, err)
	assert.Equal(t, "from-kubernetes-auth", string(value))
	zero(value)
}

func TestVaultRuntimeConnectionHealthAndRedactedFailureCategories(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/auth/token/lookup-self", r.URL.Path)
			writeJSON(t, w, map[string]any{"data": map[string]any{"id": "redacted-by-client"}})
		}))
		defer server.Close()
		deserializer, _ := newRuntimeDeserializer(server.URL, nil, "health-token", runtimeCapabilityProvider{active: true})
		health, err := deserializer.TestRuntimeSecretProvider(context.Background(), 3, 9)
		require.NoError(t, err)
		assert.Equal(t, pro_interfaces.SecretProviderHealthHealthy, health.State)
		assert.Empty(t, health.ErrorCategory)
	})

	tests := []struct {
		name     string
		handler  http.HandlerFunc
		params   db.MapStringAnyField
		category pro_interfaces.SecretProviderErrorCategory
	}{
		{name: "denied", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }, category: pro_interfaces.SecretProviderErrorPermission},
		{name: "bad request", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }, category: pro_interfaces.SecretProviderErrorResponseInvalid},
		{name: "missing health endpoint", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }, category: pro_interfaces.SecretProviderErrorResponseInvalid},
		{name: "too large", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 65))) }, params: db.MapStringAnyField{"max_response_bytes": float64(64)}, category: pro_interfaces.SecretProviderErrorResponseTooLarge},
		{name: "timeout", handler: func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			writeJSON(t, w, map[string]any{})
		}, params: db.MapStringAnyField{"timeout": "10ms"}, category: pro_interfaces.SecretProviderErrorTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			deserializer, _ := newRuntimeDeserializer(server.URL, tt.params, "must-not-leak", runtimeCapabilityProvider{active: true})
			health, err := deserializer.TestRuntimeSecretProvider(context.Background(), 3, 9)
			require.Error(t, err)
			assert.Equal(t, tt.category, health.ErrorCategory)
			assert.NotContains(t, err.Error(), "must-not-leak")
			assert.NotContains(t, err.Error(), server.URL)
		})
	}
}

func TestVaultRuntimeTLSCAAndOutageClassification(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"data": map[string]any{}})
	}))
	defer tlsServer.Close()

	untrusted, _ := newRuntimeDeserializer(tlsServer.URL, nil, "token", runtimeCapabilityProvider{active: true})
	_, err := untrusted.TestRuntimeSecretProvider(context.Background(), 3, 9)
	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorTLS, errorCategory(err))

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
	trusted, _ := newRuntimeDeserializer(tlsServer.URL, db.MapStringAnyField{
		"ca_certificate": string(certificate),
	}, "token", runtimeCapabilityProvider{active: true})
	health, err := trusted.TestRuntimeSecretProvider(context.Background(), 3, 9)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderHealthHealthy, health.State)

	outage := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	outageURL := outage.URL
	outage.Close()
	unavailable, _ := newRuntimeDeserializer(outageURL, nil, "token", runtimeCapabilityProvider{active: true})
	_, err = unavailable.TestRuntimeSecretProvider(context.Background(), 3, 9)
	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorUnavailable, errorCategory(err))
}

func TestVaultRuntimeRejectsUnsafeAddressAndDisabledCapabilityBeforeNetwork(t *testing.T) {
	_, err := parseProviderConfiguration(db.SecretStorage{
		ID: 9, ProjectID: 3, Type: db.SecretStorageTypeVault,
		Params: db.MapStringAnyField{"url": "http://vault.example", "auth_method": "token"},
	})
	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorValidation, errorCategory(err))

	deserializer, reader := newRuntimeDeserializer("http://127.0.0.1:1", nil, "token", runtimeCapabilityProvider{active: false})
	_, err = deserializer.ResolveRuntimeSecret(context.Background(), 3, pro_interfaces.SecretReference{
		StorageID: 9, Mount: "secret", Path: "app", Field: "value",
	})
	require.Error(t, err)
	assert.Equal(t, pro_interfaces.SecretProviderErrorCapabilityDisabled, errorCategory(err))
	assert.Zero(t, reader.reads.Load(), "disabled capability must block credential access")
}

func newRuntimeDeserializer(
	address string,
	params db.MapStringAnyField,
	credential string,
	provider pro_interfaces.CapabilityProvider,
) (*VaultAccessKeyDeserializer, *runtimeCredentialReader) {
	return newRuntimeDeserializerWithStorage(
		address, params, credential, provider, db.SecretStorageTypeVault, true,
	)
}

func newRuntimeDeserializerWithStorage(
	address string,
	params db.MapStringAnyField,
	credential string,
	provider pro_interfaces.CapabilityProvider,
	storageType db.SecretStorageType,
	readOnly bool,
) (*VaultAccessKeyDeserializer, *runtimeCredentialReader) {
	projectID := 3
	storageID := 9
	if params == nil {
		params = db.MapStringAnyField{}
	}
	params["url"] = address
	params["mount"] = "secret"
	if _, ok := params["auth_method"]; !ok {
		params["auth_method"] = "token"
	}
	storageRepo := &runtimeSecretStorageRepo{storage: db.SecretStorage{
		ID: storageID, ProjectID: projectID, Type: storageType,
		Params: params, ReadOnly: readOnly,
	}}
	accessKeyRepo := &runtimeSecretAccessKeyRepo{key: db.AccessKey{
		ID: 12, ProjectID: &projectID, StorageID: &storageID,
		Owner: db.AccessKeySecretStorage, Type: db.AccessKeyString,
	}}
	reader := &runtimeCredentialReader{credential: credential}
	return NewVaultAccessKeyDeserializer(accessKeyRepo, storageRepo, reader, provider), reader
}

func writeAuthResponse(t *testing.T, w http.ResponseWriter, token string, lease int64, renewable bool) {
	t.Helper()
	writeJSON(t, w, map[string]any{"auth": map[string]any{
		"client_token": token, "lease_duration": lease, "renewable": renewable,
	}})
}

func writeSecretResponse(t *testing.T, w http.ResponseWriter, field string, value any) {
	t.Helper()
	writeJSON(t, w, map[string]any{"data": map[string]any{"data": map[string]any{field: value}}})
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}
