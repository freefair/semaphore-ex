package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
)

// SecretBrowseController serves connection tests and value-free listings of
// secret stores, so locations are picked instead of typed.
type SecretBrowseController struct {
	service server.SecretBrowseService
}

func NewSecretBrowseController(service server.SecretBrowseService) *SecretBrowseController {
	return &SecretBrowseController{service: service}
}

type secretBrowseDraftRequest struct {
	Storage  db.GlobalSecretStorage              `json:"storage"`
	Location pro_interfaces.SecretBrowseLocation `json:"location"`
}

// TestGlobalDraft tests a global connection as entered in its dialog.
func (c *SecretBrowseController) TestGlobalDraft(w http.ResponseWriter, r *http.Request) {
	var request secretBrowseDraftRequest
	if !decodeSecretBrowseRequest(w, r, &request) {
		return
	}
	identity, err := c.service.TestGlobalDraft(r.Context(), request.Storage)
	writeSecretBrowseResult(w, identity, err)
}

// BrowseGlobalDraft lists a global connection as entered in its dialog.
func (c *SecretBrowseController) BrowseGlobalDraft(w http.ResponseWriter, r *http.Request) {
	var request secretBrowseDraftRequest
	if !decodeSecretBrowseRequest(w, r, &request) {
		return
	}
	nodes, err := c.service.BrowseGlobalDraft(r.Context(), request.Storage, request.Location)
	writeSecretBrowseResult(w, nodes, err)
}

// TestGlobalStorage tests a saved global connection.
func (c *SecretBrowseController) TestGlobalStorage(w http.ResponseWriter, r *http.Request) {
	id, ok := globalSecretStorageID(w, r)
	if !ok {
		return
	}
	identity, err := c.service.TestGlobalStorage(r.Context(), id)
	writeSecretBrowseResult(w, identity, err)
}

// BrowseProvider lists a global connection for the global credential form,
// limited to the connection's secret scopes: a credential outside them could
// not be resolved, and its creator may not look past them.
func (c *SecretBrowseController) BrowseProvider(w http.ResponseWriter, r *http.Request) {
	id, stored := db.ParseGlobalSecretStorageProviderID(mux.Vars(r)["provider_id"])
	if !stored {
		helpers.WriteErrorStatus(w, "Only connections managed under Administration > Global secret storages can be browsed", http.StatusBadRequest)
		return
	}
	nodes, err := c.service.BrowseGlobalStorage(r.Context(), id, secretBrowseLocation(r), true)
	writeSecretBrowseResult(w, nodes, err)
}

// TestProjectStorage tests a project storage and reports who it acts as.
func (c *SecretBrowseController) TestProjectStorage(w http.ResponseWriter, r *http.Request) {
	storage, ok := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	if !ok {
		helpers.WriteErrorStatus(w, "Secret storage was not found", http.StatusNotFound)
		return
	}
	identity, err := c.service.TestProjectStorage(r.Context(), storage.ProjectID, storage.ID)
	writeSecretBrowseResult(w, identity, err)
}

// BrowseProjectStorage lists one level of a project storage.
func (c *SecretBrowseController) BrowseProjectStorage(w http.ResponseWriter, r *http.Request) {
	storage, ok := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	if !ok {
		helpers.WriteErrorStatus(w, "Secret storage was not found", http.StatusNotFound)
		return
	}
	nodes, err := c.service.BrowseProjectStorage(r.Context(), storage.ProjectID, storage.ID, secretBrowseLocation(r))
	writeSecretBrowseResult(w, nodes, err)
}

func secretBrowseLocation(r *http.Request) pro_interfaces.SecretBrowseLocation {
	query := r.URL.Query()
	return pro_interfaces.SecretBrowseLocation{
		Project: query.Get("project"), Environment: query.Get("environment"),
		Mount: query.Get("mount"), Path: query.Get("path"), Secret: query.Get("secret"),
	}
}

func decodeSecretBrowseRequest(w http.ResponseWriter, r *http.Request, request *secretBrowseDraftRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(request); err != nil {
		helpers.WriteErrorStatus(w, "Invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// writeSecretBrowseResult answers with the result or with a message that
// says what to do, never with provider text or configuration values.
func writeSecretBrowseResult(w http.ResponseWriter, value any, err error) {
	if err == nil {
		helpers.WriteJSON(w, http.StatusOK, value)
		return
	}
	message, status, reason := secretBrowseFailure(err)
	helpers.WriteJSON(w, status, map[string]string{"error": message, "reason": reason})
}

func secretBrowseFailure(err error) (string, int, string) {
	var validation *common_errors.ValidationError
	if errors.As(err, &validation) {
		return validation.Error(), http.StatusBadRequest, "validation"
	}
	if errors.Is(err, db.ErrNotFound) {
		return "The connection was not found", http.StatusNotFound, "not_found"
	}
	reason := string(infisicalpam.CategoryOf(err))
	var provider pro_interfaces.SecretProviderError
	if errors.As(err, &provider) {
		reason = string(provider.Category)
	}
	switch reason {
	case "authentication":
		return "The store rejected the connection's credentials. Check the client ID and secret or the token.", http.StatusBadRequest, reason
	case "permission":
		return "The connection may not list this. Grant it list access in the store, or enter the location by hand.", http.StatusBadRequest, reason
	case "not_found", "field_missing":
		return "Nothing was found at this location.", http.StatusBadRequest, reason
	case "validation":
		return "The location or the connection settings are not valid.", http.StatusBadRequest, reason
	case "tls":
		return "The store's TLS certificate is not trusted. Add its CA certificate to the connection.", http.StatusBadRequest, reason
	case "timeout":
		return "The store did not answer in time. Check the address and the network.", http.StatusBadGateway, reason
	case "capability_disabled":
		return "Runtime secrets are switched off on this server.", http.StatusBadRequest, reason
	case "response_invalid", "response_too_large":
		return "The store's answer could not be read. Check that the address points to the store's API.", http.StatusBadGateway, reason
	case "", "unavailable", "transport":
		return "The store could not be reached. Check the address and the network.", http.StatusBadGateway, "unavailable"
	default:
		return "The store could not be read: " + reason + ".", http.StatusBadGateway, reason
	}
}

// newSecretBrowseService builds the browse service; global connections are
// browsable when the store keeps them.
func newSecretBrowseService(store db.Store, encryption server.AccessKeyEncryptionService) server.SecretBrowseService {
	global, _ := store.(db.GlobalSecretStorageRepository)
	return server.NewSecretBrowseService(store, store, global, encryption, proServer.NewGlobalKVStore())
}

// registerProjectSecretBrowseRoutes adds the connection test and browsing to
// a project storage. Browsing serves whoever creates or changes keys from the
// storage; the test serves whoever changes the storage.
func registerProjectSecretBrowseRoutes(storages *mux.Router, controller *SecretBrowseController) {
	keyEditor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := helpers.GetFromContext(r, "user").(*db.User)
			permissions, _ := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
			if user == nil || (!user.Admin && !permissions.Can(db.CanCreateAccessKeys) && !permissions.Can(db.CanUpdateAccessKeys)) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	storages.Handle("/{storage_id}/identity", projects.GetMustHavePermissionMiddleware(db.CanUpdateAccessKeys)(http.HandlerFunc(controller.TestProjectStorage))).Methods("POST")
	storages.Handle("/{storage_id}/browse", keyEditor(http.HandlerFunc(controller.BrowseProjectStorage))).Methods("GET", "HEAD")
}
