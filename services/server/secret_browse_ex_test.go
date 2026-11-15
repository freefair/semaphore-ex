package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKV records what the Vault/OpenBao port was asked and answers with a
// fixed tree: mount "secret" with folders apps/web and apps/billing.
type fakeKV struct {
	credentials []string
	drafts      []bool
	reads       []pro_interfaces.SecretReference
	invalid     bool
}

func (f *fakeKV) ValidateGlobalKV(string, map[string]any) error {
	if f.invalid {
		return errors.New("invalid")
	}
	return nil
}

func (f *fakeKV) record(connection pro_interfaces.GlobalKVConnection) {
	f.credentials = append(f.credentials, string(connection.Credential))
	f.drafts = append(f.drafts, connection.Draft)
}

func (f *fakeKV) ReadGlobalKV(_ context.Context, connection pro_interfaces.GlobalKVConnection, reference pro_interfaces.SecretReference) ([]byte, error) {
	f.record(connection)
	f.reads = append(f.reads, reference)
	return []byte("kv-value"), nil
}

func (f *fakeKV) GlobalKVIdentity(_ context.Context, connection pro_interfaces.GlobalKVConnection) (pro_interfaces.SecretBrowseIdentity, error) {
	f.record(connection)
	return pro_interfaces.SecretBrowseIdentity{Name: "approle-semaphore"}, nil
}

func (f *fakeKV) BrowseGlobalKV(_ context.Context, connection pro_interfaces.GlobalKVConnection, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error) {
	f.record(connection)
	switch {
	case location.Mount == "":
		return []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseMount, Name: "kv-team", Value: "kv-team"},
			{Kind: pro_interfaces.SecretBrowseMount, Name: "secret", Value: "secret"},
		}, nil
	case location.Path == "":
		return []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "apps", Value: "apps"},
			{Kind: pro_interfaces.SecretBrowseSecret, Name: "root-token", Value: "root-token"},
		}, nil
	case location.Path == "apps":
		return []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "billing", Value: "apps/billing"},
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "web", Value: "apps/web"},
		}, nil
	default:
		return []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseSecret, Name: "db", Value: location.Path + "/db"}}, nil
	}
}

func browseFixture(t *testing.T, kv pro_interfaces.GlobalKVStore) (SecretBrowseService, GlobalSecretStorageService, *pamtest.Environment) {
	t.Helper()
	store, _, encryption := newGlobalStorageFixture(t)
	env := pamtest.Start(t, pamtest.Options{
		Organization: pamtest.FakeOrganization{ID: "org-1", Name: "Acme", Slug: "acme"},
		Projects: []pamtest.FakeProject{
			{ID: "proj-1", Name: "Infrastructure", Slug: "infrastructure", Environments: []pamtest.FakeEnvironment{{Name: "Development", Slug: "dev"}, {Name: "Production", Slug: "prod"}}},
			{ID: "proj-2", Name: "Licenses", Slug: "licenses", Environments: []pamtest.FakeEnvironment{{Name: "Production", Slug: "prod"}}},
		},
		Secrets: map[string]pamtest.FakeSecret{
			pamtest.SecretKey("proj-1", "prod", "/", "ROOT_TOKEN"):             {Value: "root-value", Version: 1},
			pamtest.SecretKey("proj-1", "prod", "/apps/web", "DB_PASSWORD"):    {Value: "web-value", Version: 4},
			pamtest.SecretKey("proj-1", "prod", "/apps/billing", "STRIPE_KEY"): {Value: "billing-value", Version: 2},
		},
	})
	service := NewGlobalSecretStorageService(store, NewAccessKeyService(store, encryption, nil), kv)
	return NewSecretBrowseService(store, store, store, encryption, kv), service, env
}

func TestSecretBrowseServiceListsGlobalInfisicalByName(t *testing.T) {
	browse, storages, env := browseFixture(t, nil)
	ctx := context.Background()
	params := infisicalParams(env.API.URL, env.APICACert)
	params["secret_scopes"] = []any{map[string]any{"project_id": "proj-1", "environment": "prod", "path": "apps/web"}}
	storage, err := storages.Create(db.GlobalSecretStorage{Name: "Company Infisical", Type: db.SecretStorageTypeInfisical, Secret: pamtest.ClientSecret, Params: params})
	require.NoError(t, err)

	t.Run("the test names the organization", func(t *testing.T) {
		identity, err := browse.TestGlobalStorage(ctx, storage.ID)
		require.NoError(t, err)
		assert.Equal(t, "Acme", identity.Name)
	})

	t.Run("unscoped browsing shows every project with its name", func(t *testing.T) {
		nodes, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{}, false)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseProject, Name: "Infrastructure", Value: "proj-1"},
			{Kind: pro_interfaces.SecretBrowseProject, Name: "Licenses", Value: "proj-2"},
		}, nodes)
	})

	t.Run("scoped browsing stays inside the scopes", func(t *testing.T) {
		projects, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{}, true)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseProject, Name: "Infrastructure", Value: "proj-1"}}, projects)

		environments, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Project: "proj-1"}, true)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseEnvironment, Name: "Production", Value: "prod"}}, environments)

		root, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Project: "proj-1", Environment: "prod"}, true)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseFolder, Name: "apps", Value: "apps"}}, root,
			"above the scope only the way to it is shown, no secrets")

		apps, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Project: "proj-1", Environment: "prod", Path: "apps"}, true)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseFolder, Name: "web", Value: "apps/web"}}, apps)

		web, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Project: "proj-1", Environment: "prod", Path: "apps/web"}, true)
		require.NoError(t, err)
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseSecret, Name: "DB_PASSWORD", Value: "DB_PASSWORD", Version: 4}}, web)
		encoded, err := json.Marshal(web)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "web-value")

		outside, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Project: "proj-1", Environment: "prod", Path: "apps/billing"}, true)
		require.NoError(t, err)
		assert.Empty(t, outside)
	})

	t.Run("a draft is tested with its entered secret before saving", func(t *testing.T) {
		identity, err := browse.TestGlobalDraft(ctx, db.GlobalSecretStorage{Name: "Draft", Type: db.SecretStorageTypeInfisical, Secret: pamtest.ClientSecret, Params: infisicalParams(env.API.URL, env.APICACert)})
		require.NoError(t, err)
		assert.Equal(t, "Acme", identity.Name)

		_, err = browse.TestGlobalDraft(ctx, db.GlobalSecretStorage{Name: "Draft", Type: db.SecretStorageTypeInfisical, Secret: "wrong", Params: infisicalParams(env.API.URL, env.APICACert)})
		assert.Error(t, err)
	})

	t.Run("a saved connection's draft without a new secret uses the stored one", func(t *testing.T) {
		draft := storage
		draft.Secret = ""
		draft.Name = "Renamed"
		identity, err := browse.TestGlobalDraft(ctx, draft)
		require.NoError(t, err)
		assert.Equal(t, "Acme", identity.Name)
	})
}

func TestSecretBrowseServiceKeepsGlobalVaultInsideItsScopes(t *testing.T) {
	kv := &fakeKV{}
	browse, storages, _ := browseFixture(t, kv)
	ctx := context.Background()
	storage, err := storages.Create(db.GlobalSecretStorage{
		Name: "Company Vault", Type: db.SecretStorageTypeVault, Secret: "vault-token",
		Params: db.MapStringAnyField{"url": "https://vault.example.test", "auth_method": "token",
			"secret_scopes": []any{map[string]any{"project_id": "secret", "path": "apps/web"}}},
	})
	require.NoError(t, err)

	mounts, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{}, true)
	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseMount, Name: "secret", Value: "secret"}}, mounts)

	root, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Mount: "secret"}, true)
	require.NoError(t, err)
	assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseFolder, Name: "apps", Value: "apps"}}, root)

	unscoped, err := browse.BrowseGlobalStorage(ctx, storage.ID, pro_interfaces.SecretBrowseLocation{Mount: "secret"}, false)
	require.NoError(t, err)
	assert.Len(t, unscoped, 2)

	assert.Equal(t, "vault-token", kv.credentials[0], "the stored credential reaches the port")
	assert.False(t, kv.drafts[0], "a saved connection uses the shared client")

	_, err = browse.TestGlobalDraft(ctx, db.GlobalSecretStorage{Name: "Draft", Type: db.SecretStorageTypeOpenBao, Secret: "draft-token",
		Params: db.MapStringAnyField{"url": "https://bao.example.test"}})
	require.NoError(t, err)
	assert.True(t, kv.drafts[len(kv.drafts)-1], "a draft never touches the token cache")
}

func TestGlobalSecretStorageServiceAcceptsVaultAndOpenBao(t *testing.T) {
	kv := &fakeKV{}
	_, storages, _ := browseFixture(t, kv)
	base := func() db.GlobalSecretStorage {
		return db.GlobalSecretStorage{Name: "Vault", Type: db.SecretStorageTypeVault, Secret: "token",
			Params: db.MapStringAnyField{"url": "https://vault.example.test", "auth_method": "approle", "role_id": "semaphore"}}
	}

	created, err := storages.Create(base())
	require.NoError(t, err)
	assert.Empty(t, created.Secret)
	assert.Equal(t, []any{}, created.Params["secret_scopes"])

	tests := []struct {
		name   string
		change func(*db.GlobalSecretStorage)
	}{
		{"a credential in clear parameters", func(s *db.GlobalSecretStorage) { s.Params["token"] = "leaked" }},
		{"PAM project assignments", func(s *db.GlobalSecretStorage) { s.ProjectIDs = []int{1} }},
		{"a scope with an environment", func(s *db.GlobalSecretStorage) {
			s.Params["secret_scopes"] = []any{map[string]any{"project_id": "secret", "environment": "prod"}}
		}},
		{"a scope with an unsafe path", func(s *db.GlobalSecretStorage) {
			s.Params["secret_scopes"] = []any{map[string]any{"project_id": "secret", "path": "../sys"}}
		}},
		{"an unknown type", func(s *db.GlobalSecretStorage) { s.Type = db.SecretStorageTypeDvls }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := base()
			tt.change(&storage)
			_, err := storages.Create(storage)
			assert.Error(t, err)
		})
	}

	t.Run("settings the port rejects", func(t *testing.T) {
		kv.invalid = true
		defer func() { kv.invalid = false }()
		_, err := storages.Create(base())
		assert.Error(t, err)
	})

	t.Run("without the port only Infisical can be configured", func(t *testing.T) {
		_, withoutPort, _ := browseFixture(t, nil)
		_, err := withoutPort.Create(base())
		assert.Error(t, err)
	})
}

func TestGlobalCredentialProviderRouterReadsVaultThroughScopedConnections(t *testing.T) {
	kv := &fakeKV{}
	store, _, encryption := newGlobalStorageFixture(t)
	storages := NewGlobalSecretStorageService(store, NewAccessKeyService(store, encryption, nil), kv)
	storage, err := storages.Create(db.GlobalSecretStorage{
		Name: "Vault", Type: db.SecretStorageTypeVault, Secret: "vault-token",
		Params: db.MapStringAnyField{"url": "https://vault.example.test",
			"secret_scopes": []any{map[string]any{"project_id": "secret", "path": "apps"}}},
	})
	require.NoError(t, err)
	router := NewGlobalCredentialProviderRouter(nil, store, encryption, kv)
	reference := db.GlobalCredentialExternalReference{
		Provider: "vault", ProviderID: db.GlobalSecretStorageProviderID(storage.ID), Mount: "secret", Path: "apps/web/db", Field: "password", Version: 3,
	}

	result, err := router.ResolveGlobalCredentialExternal(context.Background(), reference)
	require.NoError(t, err)
	assert.Equal(t, "kv-value", string(result.Value))
	assert.Equal(t, 3, result.ProviderVersion)
	require.Len(t, kv.reads, 1)
	assert.Equal(t, "apps/web/db", kv.reads[0].Path)

	t.Run("outside the scopes nothing is read", func(t *testing.T) {
		outside := reference
		outside.Path = "infra/root"
		_, err := router.ResolveGlobalCredentialExternal(context.Background(), outside)
		assert.Error(t, err)
		assert.Len(t, kv.reads, 1)
	})

	t.Run("a provider type that does not match the connection is refused", func(t *testing.T) {
		wrongType := reference
		wrongType.Provider = "openbao"
		_, err := router.ResolveGlobalCredentialExternal(context.Background(), wrongType)
		assert.Error(t, err)
	})
}
