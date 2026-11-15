package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingBrowseService answers browse calls with a fixed result and
// records the arguments.
type recordingBrowseService struct {
	location pro_interfaces.SecretBrowseLocation
	scoped   bool
	storage  int
	err      error
}

func (s *recordingBrowseService) TestProjectStorage(context.Context, int, int) (pro_interfaces.SecretBrowseIdentity, error) {
	return pro_interfaces.SecretBrowseIdentity{Name: "project"}, s.err
}

func (s *recordingBrowseService) BrowseProjectStorage(_ context.Context, _ int, storageID int, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error) {
	s.storage, s.location = storageID, location
	return []pro_interfaces.SecretBrowseNode{{Kind: pro_interfaces.SecretBrowseSecret, Name: "db", Value: "apps/db"}}, s.err
}

func (s *recordingBrowseService) TestGlobalStorage(context.Context, int) (pro_interfaces.SecretBrowseIdentity, error) {
	return pro_interfaces.SecretBrowseIdentity{Name: "global"}, s.err
}

func (s *recordingBrowseService) BrowseGlobalStorage(_ context.Context, storageID int, location pro_interfaces.SecretBrowseLocation, scoped bool) ([]pro_interfaces.SecretBrowseNode, error) {
	s.storage, s.location, s.scoped = storageID, location, scoped
	return []pro_interfaces.SecretBrowseNode{}, s.err
}

func (s *recordingBrowseService) TestGlobalDraft(context.Context, db.GlobalSecretStorage) (pro_interfaces.SecretBrowseIdentity, error) {
	return pro_interfaces.SecretBrowseIdentity{Name: "draft"}, s.err
}

func (s *recordingBrowseService) BrowseGlobalDraft(_ context.Context, _ db.GlobalSecretStorage, location pro_interfaces.SecretBrowseLocation) ([]pro_interfaces.SecretBrowseNode, error) {
	s.location = location
	return []pro_interfaces.SecretBrowseNode{}, s.err
}

func TestSecretBrowseControllerScopesProviderBrowsing(t *testing.T) {
	service := &recordingBrowseService{}
	controller := NewSecretBrowseController(service)
	router := mux.NewRouter()
	router.Path("/providers/{provider_id}/browse").HandlerFunc(controller.BrowseProvider)

	t.Run("a database connection is browsed inside its scopes", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/providers/storage-7/browse?project=proj-1&environment=prod&path=apps", nil))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, 7, service.storage)
		assert.True(t, service.scoped, "credential forms only see what they may reference")
		assert.Equal(t, pro_interfaces.SecretBrowseLocation{Project: "proj-1", Environment: "prod", Path: "apps"}, service.location)
	})

	t.Run("a provider from the configuration file is not browsable", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/providers/primary/browse", nil))
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), "Global secret storages")
	})
}

func TestSecretBrowseFailuresSayWhatToDo(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		reason  string
		message string
	}{
		{"rejected credentials", pro_interfaces.SecretProviderError{Category: pro_interfaces.SecretProviderErrorAuthentication}, http.StatusBadRequest, "authentication", "rejected the connection's credentials"},
		{"no list permission", pro_interfaces.SecretProviderError{Category: pro_interfaces.SecretProviderErrorPermission}, http.StatusBadRequest, "permission", "enter the location by hand"},
		{"untrusted certificate", pro_interfaces.SecretProviderError{Category: pro_interfaces.SecretProviderErrorTLS}, http.StatusBadRequest, "tls", "CA certificate"},
		{"timeout", pro_interfaces.SecretProviderError{Category: pro_interfaces.SecretProviderErrorTimeout}, http.StatusBadGateway, "timeout", "did not answer"},
		{"unknown failure", errors.New("dial tcp 10.0.0.5:8200: connection refused"), http.StatusBadGateway, "unavailable", "could not be reached"},
		{"invalid settings", common_errors.NewValidationError("the credential is required"), http.StatusBadRequest, "validation", "the credential is required"},
		{"missing connection", db.ErrNotFound, http.StatusNotFound, "not_found", "was not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeSecretBrowseResult(response, nil, tt.err)
			assert.Equal(t, tt.status, response.Code)
			var body map[string]string
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, tt.reason, body["reason"])
			assert.Contains(t, body["error"], tt.message)
			assert.False(t, strings.Contains(body["error"], "10.0.0.5"), "provider details never reach the response")
		})
	}
}

func TestSecretBrowseControllerTestsDraftsFromTheBody(t *testing.T) {
	service := &recordingBrowseService{}
	controller := NewSecretBrowseController(service)

	response := httptest.NewRecorder()
	controller.BrowseGlobalDraft(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"storage":{"name":"Draft","type":"vault"},"location":{"mount":"secret","path":"apps"}}`)))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, pro_interfaces.SecretBrowseLocation{Mount: "secret", Path: "apps"}, service.location)

	response = httptest.NewRecorder()
	controller.TestGlobalDraft(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`not json`)))
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestSecretTestFailuresPointAtTheCredential(t *testing.T) {
	response := httptest.NewRecorder()
	writeSecretTestResult(response, nil, pro_interfaces.SecretProviderError{Category: pro_interfaces.SecretProviderErrorPermission})
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "refused the credential")
	assert.NotContains(t, response.Body.String(), "list access")
}
