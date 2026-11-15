package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
)

// GlobalSecretStorageController manages connections configured once for the
// server. Only administrators manage them; responses never contain secrets.
type GlobalSecretStorageController struct {
	service server.GlobalSecretStorageService
}

func NewGlobalSecretStorageController(service server.GlobalSecretStorageService) *GlobalSecretStorageController {
	return &GlobalSecretStorageController{service: service}
}

// GlobalCredentialProviderDTO is one source a global credential can
// reference: a configured Vault/OpenBao provider or a global storage.
type GlobalCredentialProviderDTO struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id"`
	Name       string `json:"name"`
	// Scopes lists where an Infisical connection may read secrets; empty
	// for Vault/OpenBao providers.
	Scopes []db.GlobalSecretStorageScope `json:"scopes,omitempty"`
}

// registerGlobalSecretStorages wires the routes when the store persists
// global storages; without it the routes stay absent.
func registerGlobalSecretStorages(
	authenticatedAPI *mux.Router,
	store db.Store,
	accessKeyService server.AccessKeyService,
	auditFacade pro_interfaces.AuditServiceFacade,
	delegatedProjectRolesSnapshot func(http.Handler) http.Handler,
) {
	repo, ok := store.(db.GlobalSecretStorageRepository)
	if !ok {
		return
	}
	registerGlobalSecretStorageRoutes(authenticatedAPI,
		NewGlobalSecretStorageController(server.NewGlobalSecretStorageService(repo, accessKeyService)),
		auditFacade, delegatedProjectRolesSnapshot)
}

func registerGlobalSecretStorageRoutes(
	authenticatedAPI *mux.Router,
	controller *GlobalSecretStorageController,
	auditFacade pro_interfaces.AuditServiceFacade,
	delegatedProjectRolesSnapshot func(http.Handler) http.Handler,
) {
	admin := func(handler http.HandlerFunc) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(adminMiddleware(handler)))
	}
	providerRead := func(handler http.HandlerFunc) http.Handler {
		return delegatedProjectRolesSnapshot(EnhancedGlobalPermissionAuditMiddleware(auditFacade)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.UserFromContext(r)
			for _, permission := range []db.GlobalPermission{db.CanManageGlobalCredentialsMetadata, db.CanManageGlobalCredentialsRotate} {
				allowed, err := hasGlobalPermission(r, user, permission)
				if err != nil {
					helpers.WriteError(w, err)
					return
				}
				if allowed {
					handler.ServeHTTP(w, r)
					return
				}
			}
			w.WriteHeader(http.StatusForbidden)
		})))
	}
	authenticatedAPI.Path("/global-secret-storages").Handler(admin(controller.List)).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-secret-storages").Handler(admin(controller.Create)).Methods("POST")
	authenticatedAPI.Path("/global-secret-storages/{storage_id}").Handler(admin(controller.Get)).Methods("GET", "HEAD")
	authenticatedAPI.Path("/global-secret-storages/{storage_id}").Handler(admin(controller.Update)).Methods("PUT")
	authenticatedAPI.Path("/global-secret-storages/{storage_id}").Handler(admin(controller.Delete)).Methods("DELETE")
	authenticatedAPI.Path("/global-credential-providers").Handler(providerRead(controller.ListProviders)).Methods("GET", "HEAD")
}

func (c *GlobalSecretStorageController) List(w http.ResponseWriter, r *http.Request) {
	storages, err := c.service.List()
	c.write(w, http.StatusOK, storages, err)
}

func (c *GlobalSecretStorageController) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := globalSecretStorageID(w, r)
	if !ok {
		return
	}
	storage, err := c.service.Get(id)
	c.write(w, http.StatusOK, storage, err)
}

func (c *GlobalSecretStorageController) Create(w http.ResponseWriter, r *http.Request) {
	var storage db.GlobalSecretStorage
	if !decodeGlobalSecretStorage(w, r, &storage) {
		return
	}
	created, err := c.service.Create(storage)
	c.write(w, http.StatusCreated, created, err)
}

func (c *GlobalSecretStorageController) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := globalSecretStorageID(w, r)
	if !ok {
		return
	}
	var storage db.GlobalSecretStorage
	if !decodeGlobalSecretStorage(w, r, &storage) {
		return
	}
	expected := storage.Revision
	storage.ID = id
	updated, err := c.service.Update(storage, expected)
	c.write(w, http.StatusOK, updated, err)
}

func (c *GlobalSecretStorageController) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := globalSecretStorageID(w, r)
	if !ok {
		return
	}
	c.write(w, http.StatusNoContent, nil, c.service.Delete(id))
}

// ListProviders lists value-free provider identities for the global
// credential form: configured Vault/OpenBao providers and global storages.
func (c *GlobalSecretStorageController) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers := make([]GlobalCredentialProviderDTO, 0)
	if util.Config != nil {
		for id, provider := range util.Config.GlobalCredentialProviders {
			providers = append(providers, GlobalCredentialProviderDTO{Provider: provider.Type, ProviderID: id, Name: id})
		}
	}
	sort.Slice(providers, func(left, right int) bool { return providers[left].ProviderID < providers[right].ProviderID })
	storages, err := c.service.List()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	for _, storage := range storages {
		providers = append(providers, GlobalCredentialProviderDTO{
			Provider: db.GlobalCredentialProviderInfisical, ProviderID: db.GlobalSecretStorageProviderID(storage.ID), Name: storage.Name,
			Scopes: server.GlobalSecretStorageScopes(storage),
		})
	}
	helpers.WriteJSON(w, http.StatusOK, providers)
}

func (c *GlobalSecretStorageController) write(w http.ResponseWriter, status int, value any, err error) {
	switch {
	case err == nil && status == http.StatusNoContent:
		w.WriteHeader(status)
	case err == nil:
		if storage, ok := value.(db.GlobalSecretStorage); ok {
			storage.Secret = ""
			value = storage
		}
		helpers.WriteJSON(w, status, value)
	case errors.Is(err, db.ErrNotFound):
		helpers.WriteErrorStatus(w, "Global secret storage was not found", http.StatusNotFound)
	case errors.Is(err, db.ErrGlobalCredentialRevisionConflict):
		helpers.WriteErrorStatus(w, "Global secret storage was changed by someone else; reload it", http.StatusConflict)
	default:
		var validation *common_errors.ValidationError
		if errors.As(err, &validation) {
			helpers.WriteErrorStatus(w, validation.Error(), http.StatusBadRequest)
			return
		}
		helpers.WriteError(w, err)
	}
}

func globalSecretStorageID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(mux.Vars(r)["storage_id"])
	if err != nil || id <= 0 {
		helpers.WriteErrorStatus(w, "Invalid storage ID", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func decodeGlobalSecretStorage(w http.ResponseWriter, r *http.Request, storage *db.GlobalSecretStorage) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(storage); err != nil {
		helpers.WriteErrorStatus(w, "Invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}
