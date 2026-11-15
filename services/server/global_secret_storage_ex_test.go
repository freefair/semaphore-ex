package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGlobalStorageFixture(t *testing.T) (*sql.SqlDb, GlobalSecretStorageService, AccessKeyEncryptionService) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	encryption := NewAccessKeyEncryptionService(store, nil, nil, nil)
	return store, NewGlobalSecretStorageService(store, NewAccessKeyService(store, encryption, nil), nil), encryption
}

func infisicalParams(siteURL string, ca []byte) db.MapStringAnyField {
	return db.MapStringAnyField{"url": siteURL, "client_id": pamtest.ClientID, "ca_certificate": string(ca),
		"secret_scopes": []any{map[string]any{"project_id": "proj-1", "environment": "prod", "path": ""}}}
}

func TestGlobalSecretStorageServiceKeepsTheClientSecretOutOfResponses(t *testing.T) {
	store, service, encryption := newGlobalStorageFixture(t)
	project, err := store.CreateProject(db.Project{Name: "granted"})
	require.NoError(t, err)

	created, err := service.Create(db.GlobalSecretStorage{
		Name: "Infisical", Type: db.SecretStorageTypeInfisical, Secret: "client-secret-value",
		Params: db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id",
			"pam_routes": []any{map[string]any{"host": "Web-1.Example.com", "account": "servers/web-1"}}},
		ProjectIDs: []int{project.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, created.Secret)
	assert.Equal(t, []int{project.ID}, created.ProjectIDs)
	require.NotNil(t, created.CredentialKeyID)
	assert.Equal(t, []any{map[string]any{"host": "web-1.example.com", "account": "servers/web-1"}}, created.Params["pam_routes"], "routes are normalized")

	key, err := store.GetGlobalSecretStorageKey(*created.CredentialKeyID)
	require.NoError(t, err)
	assert.Nil(t, key.ProjectID, "the client secret belongs to no project")
	require.NoError(t, encryption.DeserializeSecret(&key))
	assert.Equal(t, "client-secret-value", key.String)

	t.Run("an update without a secret keeps the key", func(t *testing.T) {
		changed := created
		changed.Name = "Infisical EU"
		updated, err := service.Update(changed, created.Revision)
		require.NoError(t, err)
		assert.Equal(t, created.CredentialKeyID, updated.CredentialKeyID)
	})

	t.Run("a new secret replaces the key", func(t *testing.T) {
		current, err := service.Get(created.ID)
		require.NoError(t, err)
		current.Secret = "rotated-secret"
		updated, err := service.Update(current, current.Revision)
		require.NoError(t, err)
		require.NotNil(t, updated.CredentialKeyID)
		assert.NotEqual(t, *created.CredentialKeyID, *updated.CredentialKeyID)
		_, err = store.GetGlobalSecretStorageKey(*created.CredentialKeyID)
		assert.ErrorIs(t, err, db.ErrNotFound, "the old key is removed")
	})

	t.Run("a stale update leaves the stored key", func(t *testing.T) {
		current, err := service.Get(created.ID)
		require.NoError(t, err)
		stale := current
		stale.Secret = "never-stored"
		_, err = service.Update(stale, current.Revision-1)
		assert.ErrorIs(t, err, db.ErrGlobalCredentialRevisionConflict)
		after, err := service.Get(created.ID)
		require.NoError(t, err)
		assert.Equal(t, current.CredentialKeyID, after.CredentialKeyID)
	})

	t.Run("delete removes the key", func(t *testing.T) {
		current, err := service.Get(created.ID)
		require.NoError(t, err)
		require.NoError(t, service.Delete(created.ID))
		_, err = store.GetGlobalSecretStorageKey(*current.CredentialKeyID)
		assert.ErrorIs(t, err, db.ErrNotFound)
	})
}

func TestGlobalSecretStorageServiceRejectsInvalidConnections(t *testing.T) {
	_, service, _ := newGlobalStorageFixture(t)
	valid := func() db.GlobalSecretStorage {
		return db.GlobalSecretStorage{Name: "Infisical", Type: db.SecretStorageTypeInfisical, Secret: "secret",
			Params: db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id"}}
	}
	env := db.AccessKeySourceStorageVault
	tests := []struct {
		name   string
		change func(*db.GlobalSecretStorage)
	}{
		{"missing name", func(s *db.GlobalSecretStorage) { s.Name = " " }},
		{"other type", func(s *db.GlobalSecretStorage) { s.Type = db.SecretStorageTypeVault }},
		{"plain http", func(s *db.GlobalSecretStorage) { s.Params["url"] = "http://infisical.example.com" }},
		{"missing secret", func(s *db.GlobalSecretStorage) { s.Secret = "" }},
		{"unsupported secret source", func(s *db.GlobalSecretStorage) { s.SourceStorageType = &env }},
		{"wildcard route", func(s *db.GlobalSecretStorage) {
			s.Params["pam_routes"] = []any{map[string]any{"host": "*.example.com", "account": "servers/web"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := valid()
			tt.change(&storage)
			_, err := service.Create(storage)
			assert.Error(t, err)
		})
	}
}

func TestGlobalInfisicalSecretSourceResolvesGlobalCredentials(t *testing.T) {
	fake := pamtest.Start(t, pamtest.Options{Secrets: map[string]pamtest.FakeSecret{
		pamtest.SecretKey("proj-1", "prod", "/", "LICENSE_KEY"):         {Value: "license-value", Version: 4},
		pamtest.SecretKey("proj-1", "prod", "/apps/web", "DB_PASSWORD"): {Value: "db-value", Version: 2},
	}})
	store, service, encryption := newGlobalStorageFixture(t)
	storage, err := service.Create(db.GlobalSecretStorage{
		Name: "Infisical", Type: db.SecretStorageTypeInfisical, Secret: pamtest.ClientSecret,
		Params: infisicalParams(fake.URL(), fake.APICACert),
	})
	require.NoError(t, err)
	fallback := &recordingExternalAdapter{}
	router := NewGlobalCredentialProviderRouter(fallback, store, encryption, nil)
	reference := func(path, field string, version int) db.GlobalCredentialExternalReference {
		return db.GlobalCredentialExternalReference{
			Provider: db.GlobalCredentialProviderInfisical, ProviderID: db.GlobalSecretStorageProviderID(storage.ID),
			Mount: "proj-1", Environment: "prod", Path: path, Field: field, Version: version,
		}
	}

	t.Run("root secret with the current version", func(t *testing.T) {
		result, err := router.ResolveGlobalCredentialExternal(context.Background(), reference("", "LICENSE_KEY", 0))
		require.NoError(t, err)
		assert.Equal(t, "license-value", string(result.Value))
		assert.Equal(t, 4, result.ProviderVersion)
	})

	t.Run("folder and pinned version", func(t *testing.T) {
		result, err := router.ResolveGlobalCredentialExternal(context.Background(), reference("apps/web", "DB_PASSWORD", 2))
		require.NoError(t, err)
		assert.Equal(t, "db-value", string(result.Value))
	})

	t.Run("failures are value-free", func(t *testing.T) {
		for name, ref := range map[string]db.GlobalCredentialExternalReference{
			"unknown secret":  reference("", "MISSING", 0),
			"unknown storage": {Provider: db.GlobalCredentialProviderInfisical, ProviderID: "storage-999", Mount: "proj-1", Environment: "prod", Field: "LICENSE_KEY"},
			"invalid path":    reference("../etc", "LICENSE_KEY", 0),
		} {
			t.Run(name, func(t *testing.T) {
				_, err := router.ResolveGlobalCredentialExternal(context.Background(), ref)
				require.Error(t, err)
				assert.Equal(t, "global credential provider unavailable", err.Error())
			})
		}
	})

	t.Run("other providers use the configured adapter", func(t *testing.T) {
		vault := db.GlobalCredentialExternalReference{Provider: "vault", ProviderID: "primary", Mount: "kv", Path: "app", Version: 1, Field: "token"}
		_, err := router.ResolveGlobalCredentialExternal(context.Background(), vault)
		require.NoError(t, err)
		assert.Equal(t, []db.GlobalCredentialExternalReference{vault}, fallback.references)
	})
}

func TestGlobalInfisicalSecretSourceReadsTheClientSecretFromTheEnvironment(t *testing.T) {
	fake := pamtest.Start(t, pamtest.Options{Secrets: map[string]pamtest.FakeSecret{
		pamtest.SecretKey("proj-1", "prod", "/", "LICENSE_KEY"): {Value: "license-value", Version: 1},
	}})
	store, service, encryption := newGlobalStorageFixture(t)
	t.Setenv("QA_TEST_INFISICAL_CLIENT", pamtest.ClientSecret)
	source := db.AccessKeySourceStorageEnv
	storage, err := service.Create(db.GlobalSecretStorage{
		Name: "Infisical", Type: db.SecretStorageTypeInfisical, Secret: "QA_TEST_INFISICAL_CLIENT", SourceStorageType: &source,
		Params: infisicalParams(fake.URL(), fake.APICACert),
	})
	require.NoError(t, err)
	router := NewGlobalCredentialProviderRouter(nil, store, encryption, nil)

	result, err := router.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
		Provider: db.GlobalCredentialProviderInfisical, ProviderID: db.GlobalSecretStorageProviderID(storage.ID),
		Mount: "proj-1", Environment: "prod", Field: "LICENSE_KEY",
	})
	require.NoError(t, err)
	assert.Equal(t, "license-value", string(result.Value))
}

type recordingExternalAdapter struct {
	references []db.GlobalCredentialExternalReference
}

func (a *recordingExternalAdapter) ResolveGlobalCredentialExternal(_ context.Context, reference db.GlobalCredentialExternalReference) (pro_interfaces.GlobalCredentialExternalResult, error) {
	a.references = append(a.references, reference)
	if reference.Provider == db.GlobalCredentialProviderInfisical {
		return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("unexpected infisical reference")
	}
	return pro_interfaces.GlobalCredentialExternalResult{Value: []byte("vault-value"), ProviderVersion: 1}, nil
}

func TestLoadInfisicalPAMForProjectAddsGrantedGlobalStorages(t *testing.T) {
	store, service, _ := newGlobalStorageFixture(t)
	granted, err := store.CreateProject(db.Project{Name: "granted"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	global, err := service.Create(db.GlobalSecretStorage{
		Name: "Company Infisical", Type: db.SecretStorageTypeInfisical, Secret: "client-secret",
		Params: db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id",
			"pam_routes": []any{map[string]any{"host": "db-1", "account": "servers/db-1"}}},
		ProjectIDs: []int{granted.ID},
	})
	require.NoError(t, err)
	project := storedInfisical(2, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}})
	projectStorages := infisicalStorageRepository{storages: []db.SecretStorage{project}}
	projectKeys := infisicalKeyRepository{keys: map[int][]db.AccessKey{2: {{ID: 20, Type: db.AccessKeyString}}}}

	t.Run("a granted project gets both", func(t *testing.T) {
		configs, err := LoadInfisicalPAMForProject(projectStorages, projectKeys, store, granted.ID)
		require.NoError(t, err)
		require.Len(t, configs, 2)
		assert.False(t, configs[0].Global)
		assert.True(t, configs[1].Global)
		assert.Equal(t, global.ID, configs[1].StorageID)
		assert.Equal(t, "Company Infisical", configs[1].StorageName)
		assert.Equal(t, *global.CredentialKeyID, configs[1].CredentialKeyID)
		assert.Nil(t, configs[1].Credential.ProjectID)
		assert.Equal(t, []db.InfisicalPAMRoute{{Host: "db-1", Account: "servers/db-1"}}, configs[1].Routes)
	})

	t.Run("a project without the grant gets only its own", func(t *testing.T) {
		configs, err := LoadInfisicalPAMForProject(projectStorages, projectKeys, store, other.ID)
		require.NoError(t, err)
		require.Len(t, configs, 1)
		assert.False(t, configs[0].Global)
	})

	t.Run("a host routed by the project and globally fails", func(t *testing.T) {
		conflicting := infisicalStorageRepository{storages: []db.SecretStorage{
			storedInfisical(2, []any{map[string]any{"host": "db-1", "account": "servers/db-local"}}),
		}}
		_, err := LoadInfisicalPAMForProject(conflicting, projectKeys, store, granted.ID)
		assert.ErrorContains(t, err, "more than one Infisical storage")
	})

	t.Run("without global storages the project storages still load", func(t *testing.T) {
		configs, err := LoadInfisicalPAMForProject(projectStorages, projectKeys, nil, granted.ID)
		require.NoError(t, err)
		assert.Len(t, configs, 1)
	})
}

func TestGlobalInfisicalSecretSourceReadsOnlyInsideItsScopes(t *testing.T) {
	fake := pamtest.Start(t, pamtest.Options{Secrets: map[string]pamtest.FakeSecret{
		pamtest.SecretKey("proj-1", "prod", "/apps/web", "DB_PASSWORD"):     {Value: "web-value", Version: 1},
		pamtest.SecretKey("proj-1", "prod", "/apps/billing", "DB_PASSWORD"): {Value: "billing-value", Version: 1},
		pamtest.SecretKey("proj-1", "dev", "/apps/web", "DB_PASSWORD"):      {Value: "dev-value", Version: 1},
		pamtest.SecretKey("proj-2", "prod", "/apps/web", "DB_PASSWORD"):     {Value: "other-value", Version: 1},
	}})
	store, service, encryption := newGlobalStorageFixture(t)
	params := infisicalParams(fake.URL(), fake.APICACert)
	params["secret_scopes"] = []any{map[string]any{"project_id": "proj-1", "environment": "prod", "path": "/apps/web/"}}
	storage, err := service.Create(db.GlobalSecretStorage{Name: "Scoped", Type: db.SecretStorageTypeInfisical, Secret: pamtest.ClientSecret, Params: params})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"project_id": "proj-1", "environment": "prod", "path": "apps/web"}}, storage.Params["secret_scopes"], "scope folders are normalized")
	router := NewGlobalCredentialProviderRouter(nil, store, encryption, nil)
	resolve := func(project, environment, path string) error {
		_, err := router.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
			Provider: db.GlobalCredentialProviderInfisical, ProviderID: db.GlobalSecretStorageProviderID(storage.ID),
			Mount: project, Environment: environment, Path: path, Field: "DB_PASSWORD",
		})
		return err
	}

	assert.NoError(t, resolve("proj-1", "prod", "apps/web"))
	for name, args := range map[string][3]string{
		"other folder":       {"proj-1", "prod", "apps/billing"},
		"folder name prefix": {"proj-1", "prod", "apps/webshop"},
		"parent folder":      {"proj-1", "prod", "apps"},
		"other environment":  {"proj-1", "dev", "apps/web"},
		"other project":      {"proj-2", "prod", "apps/web"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, resolve(args[0], args[1], args[2]))
		})
	}

	t.Run("a storage without scopes reads nothing", func(t *testing.T) {
		unscoped := infisicalParams(fake.URL(), fake.APICACert)
		delete(unscoped, "secret_scopes")
		empty, err := service.Create(db.GlobalSecretStorage{Name: "PAM only", Type: db.SecretStorageTypeInfisical, Secret: pamtest.ClientSecret, Params: unscoped})
		require.NoError(t, err)
		_, err = router.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
			Provider: db.GlobalCredentialProviderInfisical, ProviderID: db.GlobalSecretStorageProviderID(empty.ID),
			Mount: "proj-1", Environment: "prod", Path: "apps/web", Field: "DB_PASSWORD",
		})
		assert.Error(t, err)
	})
}

func TestGlobalSecretStorageServiceValidatesScopesAndRunnerTrust(t *testing.T) {
	_, service, _ := newGlobalStorageFixture(t)
	create := func(change func(db.MapStringAnyField)) error {
		params := db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id"}
		change(params)
		_, err := service.Create(db.GlobalSecretStorage{Name: "Check", Type: db.SecretStorageTypeInfisical, Secret: "s", Params: params})
		return err
	}
	assert.Error(t, create(func(p db.MapStringAnyField) { p["secret_scopes"] = "everything" }))
	assert.Error(t, create(func(p db.MapStringAnyField) {
		p["secret_scopes"] = []any{map[string]any{"project_id": "", "environment": "prod"}}
	}))
	assert.Error(t, create(func(p db.MapStringAnyField) {
		p["secret_scopes"] = []any{map[string]any{"project_id": "proj-1", "environment": "Prod"}}
	}))
	assert.Error(t, create(func(p db.MapStringAnyField) {
		p["secret_scopes"] = []any{map[string]any{"project_id": "proj-1", "environment": "prod", "path": "apps/.."}}
	}))
	assert.Error(t, create(func(p db.MapStringAnyField) { p["allow_remote_runners"] = "yes" }))

	created, err := service.Create(db.GlobalSecretStorage{Name: "Runners", Type: db.SecretStorageTypeInfisical, Secret: "s",
		Params: db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id"}})
	require.NoError(t, err)
	assert.Equal(t, false, created.Params["allow_remote_runners"], "remote runners are off by default")
	assert.Equal(t, []any{}, created.Params["secret_scopes"])
}

func TestLoadInfisicalPAMForProjectCarriesRunnerTrust(t *testing.T) {
	store, service, _ := newGlobalStorageFixture(t)
	project, err := store.CreateProject(db.Project{Name: "granted"})
	require.NoError(t, err)
	for _, allowed := range []bool{false, true} {
		_, err := service.Create(db.GlobalSecretStorage{
			Name: "Runner trust " + fmt.Sprint(allowed), Type: db.SecretStorageTypeInfisical, Secret: "s",
			Params: db.MapStringAnyField{"url": "https://app.infisical.com", "client_id": "client-id", "allow_remote_runners": allowed,
				"pam_routes": []any{map[string]any{"host": fmt.Sprintf("host-%t", allowed), "account": "servers/web"}}},
			ProjectIDs: []int{project.ID},
		})
		require.NoError(t, err)
	}
	configs, err := LoadInfisicalPAMForProject(infisicalStorageRepository{}, infisicalKeyRepository{keys: map[int][]db.AccessKey{}}, store, project.ID)
	require.NoError(t, err)
	require.Len(t, configs, 2)
	assert.False(t, configs[0].AllowRemoteRunners)
	assert.True(t, configs[1].AllowRemoteRunners)
}
