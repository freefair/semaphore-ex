package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGlobalSecretStorageTestController(t *testing.T) *GlobalSecretStorageController {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	encryption := server.NewAccessKeyEncryptionService(store, nil, nil, nil)
	return NewGlobalSecretStorageController(server.NewGlobalSecretStorageService(store, server.NewAccessKeyService(store, encryption, nil), nil))
}

func globalStorageRequest(method, path, body string, vars map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if vars != nil {
		r = mux.SetURLVars(r, vars)
	}
	return r
}

func TestGlobalSecretStorageControllerNeverReturnsTheClientSecret(t *testing.T) {
	controller := newGlobalSecretStorageTestController(t)
	body := `{"name":"Infisical","type":"infisical","secret":"client-secret-value","params":{"url":"https://app.infisical.com","client_id":"client-id"}}`

	created := httptest.NewRecorder()
	controller.Create(created, globalStorageRequest(http.MethodPost, "/api/global-secret-storages", body, nil))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	assert.NotContains(t, created.Body.String(), "client-secret-value")
	assert.NotContains(t, created.Body.String(), "credential_key_id")
	var storage db.GlobalSecretStorage
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &storage))
	id := strconv.Itoa(storage.ID)

	listed := httptest.NewRecorder()
	controller.List(listed, globalStorageRequest(http.MethodGet, "/api/global-secret-storages", "", nil))
	assert.Equal(t, http.StatusOK, listed.Code)
	assert.NotContains(t, listed.Body.String(), "client-secret-value")

	t.Run("a stale revision is a conflict", func(t *testing.T) {
		update := httptest.NewRecorder()
		controller.Update(update, globalStorageRequest(http.MethodPut, "/api/global-secret-storages/"+id,
			`{"name":"Renamed","revision":99,"params":{"url":"https://app.infisical.com","client_id":"client-id"}}`,
			map[string]string{"storage_id": id}))
		assert.Equal(t, http.StatusConflict, update.Code)
	})

	t.Run("invalid settings are a validation error", func(t *testing.T) {
		update := httptest.NewRecorder()
		controller.Update(update, globalStorageRequest(http.MethodPut, "/api/global-secret-storages/"+id,
			`{"name":"Renamed","revision":1,"params":{"url":"http://plain.example.com","client_id":"client-id"}}`,
			map[string]string{"storage_id": id}))
		assert.Equal(t, http.StatusBadRequest, update.Code)
		assert.Contains(t, update.Body.String(), "https")
	})

	t.Run("providers list storages and configured providers without values", func(t *testing.T) {
		previous := util.Config.GlobalCredentialProviders
		util.Config.GlobalCredentialProviders = map[string]util.GlobalCredentialProviderConfig{"primary": {Type: "openbao", URL: "https://openbao.example.test"}}
		t.Cleanup(func() { util.Config.GlobalCredentialProviders = previous })
		providers := httptest.NewRecorder()
		controller.ListProviders(providers, globalStorageRequest(http.MethodGet, "/api/global-credential-providers", "", nil))
		require.Equal(t, http.StatusOK, providers.Code)
		var listed []GlobalCredentialProviderDTO
		require.NoError(t, json.Unmarshal(providers.Body.Bytes(), &listed))
		assert.Equal(t, []GlobalCredentialProviderDTO{
			{Provider: "openbao", ProviderID: "primary", Name: "primary", Source: "configuration"},
			{Provider: "infisical", ProviderID: "storage-" + id, Name: "Infisical", Source: "database"},
		}, listed)
		assert.NotContains(t, providers.Body.String(), "openbao.example.test")
	})

	t.Run("delete", func(t *testing.T) {
		deleted := httptest.NewRecorder()
		controller.Delete(deleted, globalStorageRequest(http.MethodDelete, "/api/global-secret-storages/"+id, "", map[string]string{"storage_id": id}))
		assert.Equal(t, http.StatusNoContent, deleted.Code)
		missing := httptest.NewRecorder()
		controller.Get(missing, globalStorageRequest(http.MethodGet, "/api/global-secret-storages/"+id, "", map[string]string{"storage_id": id}))
		assert.Equal(t, http.StatusNotFound, missing.Code)
	})
}

func TestGlobalSecretStorageRoutesRequireAdministrators(t *testing.T) {
	handler := adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for name, user := range map[string]*db.User{"member": {ID: 2}, "administrator": {ID: 1, Admin: true}} {
		t.Run(name, func(t *testing.T) {
			r := helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/api/global-secret-storages", nil), "user", user)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if user.Admin {
				assert.Equal(t, http.StatusOK, w.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, w.Code)
			}
		})
	}
}

// The credential item route is registered earlier and matches any
// /global-credentials/{x}; the provider list must not live under it.
func TestGlobalCredentialProvidersRouteIsNotShadowed(t *testing.T) {
	router := mux.NewRouter()
	api := router.PathPrefix("/api").Subrouter()
	api.Path("/global-credentials/{credential_id}").HandlerFunc(func(http.ResponseWriter, *http.Request) {}).Methods("GET", "HEAD")
	registerGlobalSecretStorageRoutes(api, newGlobalSecretStorageTestController(t), NewSecretBrowseController(nil), nil, func(h http.Handler) http.Handler { return h })

	var match mux.RouteMatch
	require.True(t, router.Match(httptest.NewRequest(http.MethodGet, "/api/global-credential-providers", nil), &match))
	template, err := match.Route.GetPathTemplate()
	require.NoError(t, err)
	assert.Equal(t, "/api/global-credential-providers", template)
}
