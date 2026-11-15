package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browseVault serves a token lookup, the UI mount list and one KV v2 mount
// "secret" holding apps/web/db and apps/billing/stripe.
func browseVault(t *testing.T) *httptest.Server {
	t.Helper()
	tree := map[string][]string{
		"":     {"apps/", "root-token"},
		"apps": {"billing/", "web/"},
	}
	values := map[string]map[string]any{
		"apps/web/db": {"username": "app", "password": "web-password", "port": float64(5432)},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Vault-Token") != "browse-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/v1/auth/token/lookup-self":
			writeJSON(t, w, map[string]any{"data": map[string]any{"display_name": "approle-semaphore", "policies": []string{"default", "semaphore-read"}}})
		case r.URL.Path == "/v1/sys/internal/ui/mounts":
			writeJSON(t, w, map[string]any{"data": map[string]any{"secret": map[string]any{
				"secret/":    map[string]any{"type": "kv", "options": map[string]any{"version": "2"}},
				"legacy/":    map[string]any{"type": "kv", "options": map[string]any{"version": "1"}},
				"transit/":   map[string]any{"type": "transit"},
				"cubbyhole/": map[string]any{"type": "cubbyhole"},
			}}})
		case strings.HasPrefix(r.URL.Path, "/v1/secret/metadata/") && r.URL.Query().Get("list") == "true":
			path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/secret/metadata/"), "/")
			keys, ok := tree[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(t, w, map[string]any{"data": map[string]any{"keys": keys}})
		case strings.HasPrefix(r.URL.Path, "/v1/secret/data/"):
			data, ok := values[strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(t, w, map[string]any{"data": map[string]any{"data": data, "metadata": map[string]any{"version": 3}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestVaultBrowseListsNamesWithoutValues(t *testing.T) {
	server := browseVault(t)
	deserializer, _ := newRuntimeDeserializer(server.URL, nil, "browse-token", runtimeCapabilityProvider{active: true})
	ctx := context.Background()
	browse := func(location pro_interfaces.SecretBrowseLocation) []pro_interfaces.SecretBrowseNode {
		nodes, err := deserializer.BrowseRuntimeSecretProvider(ctx, 3, 9, location)
		require.NoError(t, err)
		return nodes
	}

	t.Run("the identity proves the connection", func(t *testing.T) {
		identity, err := deserializer.RuntimeSecretProviderIdentity(ctx, 3, 9)
		require.NoError(t, err)
		assert.Equal(t, "approle-semaphore", identity.Name)
		assert.Equal(t, "Policies: default, semaphore-read", identity.Description)
	})

	t.Run("only KV v2 mounts are offered", func(t *testing.T) {
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseMount, Name: "secret", Value: "secret"}},
			browse(pro_interfaces.SecretBrowseLocation{}))
	})

	t.Run("folders come before secrets and carry their full path", func(t *testing.T) {
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "apps", Value: "apps"},
			{Kind: pro_interfaces.SecretBrowseSecret, Name: "root-token", Value: "root-token"},
		}, browse(pro_interfaces.SecretBrowseLocation{Mount: "secret"}))
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "billing", Value: "apps/billing"},
			{Kind: pro_interfaces.SecretBrowseFolder, Name: "web", Value: "apps/web"},
		}, browse(pro_interfaces.SecretBrowseLocation{Mount: "secret", Path: "apps"}))
	})

	t.Run("an empty folder is an empty list", func(t *testing.T) {
		assert.Empty(t, browse(pro_interfaces.SecretBrowseLocation{Mount: "secret", Path: "apps/none"}))
	})

	t.Run("fields are names of text values only, never values", func(t *testing.T) {
		nodes := browse(pro_interfaces.SecretBrowseLocation{Mount: "secret", Secret: "apps/web/db"})
		assert.Equal(t, []pro_interfaces.SecretBrowseNode{
			{Kind: pro_interfaces.SecretBrowseField, Name: "password", Value: "password", Version: 3},
			{Kind: pro_interfaces.SecretBrowseField, Name: "username", Value: "username", Version: 3},
		}, nodes)
		encoded, err := json.Marshal(nodes)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "web-password")
	})

	t.Run("unsafe locations are refused", func(t *testing.T) {
		for _, location := range []pro_interfaces.SecretBrowseLocation{
			{Mount: "secret", Path: "../sys"},
			{Mount: "sys/mounts"},
			{Mount: "secret", Path: "/apps"},
			{Mount: "secret", Secret: "apps/../../sys/raw"},
		} {
			_, err := deserializer.BrowseRuntimeSecretProvider(ctx, 3, 9, location)
			assert.Equal(t, pro_interfaces.SecretProviderErrorValidation, errorCategory(err), location)
		}
	})

	t.Run("a disabled capability browses nothing", func(t *testing.T) {
		disabled, _ := newRuntimeDeserializer(server.URL, nil, "browse-token", runtimeCapabilityProvider{active: false})
		_, err := disabled.BrowseRuntimeSecretProvider(ctx, 3, 9, pro_interfaces.SecretBrowseLocation{})
		assert.Error(t, err)
	})
}
