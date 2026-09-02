package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalCredentialExternalAdapterReadsExplicitVaultAndOpenBaoVersions(t *testing.T) {
	for _, providerType := range []string{"vault", "openbao"} {
		t.Run(providerType, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/kv/data/app/token", r.URL.Path)
				assert.Equal(t, "3", r.URL.Query().Get("version"))
				assert.Equal(t, "bootstrap-value", r.Header.Get("X-Vault-Token"))
				_, _ = w.Write([]byte(`{"data":{"data":{"token":"resolved-value"}}}`))
			}))
			defer provider.Close()
			config := &util.ConfigType{GlobalCredentialProviders: map[string]util.GlobalCredentialProviderConfig{
				"primary": {Type: providerType, URL: provider.URL},
			}}
			adapter := NewGlobalCredentialExternalAdapter(config, func(name string) (string, bool) {
				assert.Equal(t, "SEMAPHORE_GLOBAL_CREDENTIAL_PROVIDER_PRIMARY_CREDENTIAL", name)
				return "bootstrap-value", true
			})

			resolved, err := adapter.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
				Provider: providerType, ProviderID: "primary", Mount: "kv", Path: "app/token", Version: 3, Field: "token",
			})
			require.NoError(t, err)
			assert.Equal(t, "resolved-value", string(resolved.Value))
			assert.Equal(t, 3, resolved.ProviderVersion)
			zero(resolved.Value)
		})
	}
}

func TestGlobalCredentialExternalAdapterFailsClosedForMissingCredentialAndInvalidRegistry(t *testing.T) {
	valid := &util.ConfigType{GlobalCredentialProviders: map[string]util.GlobalCredentialProviderConfig{
		"primary": {Type: "vault", URL: "http://127.0.0.1:1"},
	}}
	missingCredential := NewGlobalCredentialExternalAdapter(valid, func(string) (string, bool) { return "", false })
	_, err := missingCredential.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
		Provider: "vault", ProviderID: "primary", Mount: "kv", Path: "app/token", Version: 1, Field: "token",
	})
	assert.Error(t, err)

	collision := &util.ConfigType{GlobalCredentialProviders: map[string]util.GlobalCredentialProviderConfig{
		"primary-vault": {Type: "vault", URL: "http://127.0.0.1:1"},
		"primary_vault": {Type: "vault", URL: "http://127.0.0.1:1"},
	}}
	invalidRegistry := NewGlobalCredentialExternalAdapter(collision, func(string) (string, bool) { return "bootstrap", true })
	_, err = invalidRegistry.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
		Provider: "vault", ProviderID: "primary-vault", Mount: "kv", Path: "app/token", Version: 1, Field: "token",
	})
	assert.Error(t, err)

	badProvider := &util.ConfigType{GlobalCredentialProviders: map[string]util.GlobalCredentialProviderConfig{
		"primary": {Type: "vault", URL: "https://example.test/path"},
	}}
	adapter := NewGlobalCredentialExternalAdapter(badProvider, func(string) (string, bool) { return "bootstrap", true })
	_, err = adapter.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
		Provider: "vault", ProviderID: "primary", Mount: "kv", Path: "app/token", Version: 1, Field: "token",
	})
	assert.Error(t, err)
}

func TestGlobalCredentialExternalAdapterRejectsUnsafeAuthMetadata(t *testing.T) {
	for _, provider := range []util.GlobalCredentialProviderConfig{
		{Type: "vault", URL: "http://127.0.0.1:1", AuthMethod: "approle", AuthMount: "auth/approle", RoleID: "role"},
		{Type: "vault", URL: "http://127.0.0.1:1", AuthMethod: "approle", AuthMount: "approle\r\nX-Injected: value", RoleID: "role"},
		{Type: "vault", URL: "http://127.0.0.1:1", AuthMethod: "approle", AuthMount: "approle", RoleID: "role\r\ninvalid"},
		{Type: "vault", URL: "http://127.0.0.1:1", AuthMethod: "kubernetes", AuthMount: "kubernetes", Role: "role\r\ninvalid"},
		{Type: "vault", URL: "http://127.0.0.1:1", AuthMethod: "approle", AuthMount: "approle", RoleID: strings.Repeat("r", 1025)},
	} {
		_, err := globalCredentialProviderConfiguration("primary", 1, provider)
		assert.Errorf(t, err, "provider = %#v", provider)
	}
}

func TestGlobalCredentialExternalAdapterDoesNotReturnRawProviderFailures(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "raw-provider-path-and-token", http.StatusInternalServerError)
	}))
	defer provider.Close()
	adapter := NewGlobalCredentialExternalAdapter(&util.ConfigType{GlobalCredentialProviders: map[string]util.GlobalCredentialProviderConfig{
		"primary": {Type: "vault", URL: provider.URL},
	}}, func(string) (string, bool) { return "bootstrap", true })
	_, err := adapter.ResolveGlobalCredentialExternal(context.Background(), db.GlobalCredentialExternalReference{
		Provider: "vault", ProviderID: "primary", Mount: "kv", Path: "app/token", Version: 1, Field: "token",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "raw-provider")
	assert.False(t, strings.Contains(err.Error(), "bootstrap"))
}
