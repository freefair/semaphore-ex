package projects

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	pro "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
)

type SecretStorageController struct {
	secretRepo           db.SecretStorageRepository
	secretStorageService server.SecretStorageService
	capabilityProvider   pro_interfaces.CapabilityProvider
}

func SecretStorageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		storageID, ok := helpers.GetIntParamOrAbort("storage_id", w, r)
		if !ok {
			return
		}

		storage, err := helpers.Store(r).GetSecretStorage(project.ID, storageID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		keys, err := helpers.Store(r).GetAccessKeys(project.ID, db.GetAccessKeyOptions{
			Owner:     db.AccessKeySecretStorage,
			StorageID: &storage.ID,
		}, db.RetrieveQueryParams{})

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		if len(keys) == 0 {
			if pro.StorageRequiresSecret(storage) {
				helpers.WriteErrorStatus(w, "Access key not found", http.StatusNotFound)
				return
			}
		} else {
			storage.SourceStorageType = keys[0].SourceStorageType
			storage.Secret = ""
		}

		r = helpers.SetContextValue(r, "secretStorage", storage)
		next.ServeHTTP(w, r)
	})
}

func NewSecretStorageController(
	secretRepo db.SecretStorageRepository,
	secretStorageService server.SecretStorageService,
	capabilityProviders ...pro_interfaces.CapabilityProvider,
) *SecretStorageController {
	var capabilityProvider pro_interfaces.CapabilityProvider
	if len(capabilityProviders) > 0 {
		capabilityProvider = capabilityProviders[0]
	}
	return &SecretStorageController{
		secretRepo:           secretRepo,
		secretStorageService: secretStorageService,
		capabilityProvider:   capabilityProvider,
	}
}

func (c *SecretStorageController) GetRefs(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	key := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	refs, err := helpers.Store(r).GetSecretStorageRefs(key.ProjectID, key.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, refs)
}

func (c *SecretStorageController) GetSecretStorages(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	storages, err := c.secretStorageService.GetSecretStorages(project.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, storages)
}

func (c *SecretStorageController) GetSecretStorage(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	storage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)

	helpers.WriteJSON(w, http.StatusOK, storage)
}

func (c *SecretStorageController) Update(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	oldStorage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)

	var storage db.SecretStorage
	if !helpers.Bind(w, r, &storage) {
		return
	}

	if storage.ID != oldStorage.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Secret storage id in URL and in body must be the same",
		})
		return
	}

	if storage.ProjectID != oldStorage.ProjectID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You can not move secret storage to other project",
		})
		return
	}

	err := c.secretStorageService.Update(storage)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	storage.Secret = ""

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   oldStorage.ProjectID,
		ObjectType:  db.EventSchedule,
		ObjectID:    oldStorage.ID,
		Description: fmt.Sprintf("Secret storage with ID %d has been updated", storage.ID),
	})

	helpers.WriteJSON(w, http.StatusOK, storage)
}

func (c *SecretStorageController) Add(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	var storage db.SecretStorage

	if !helpers.Bind(w, r, &storage) {
		return
	}

	if storage.ProjectID != project.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}

	newStorage, err := c.secretStorageService.Create(storage)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   newStorage.ProjectID,
		ObjectType:  db.EventKey,
		ObjectID:    newStorage.ID,
		Description: fmt.Sprintf("Secret storage %s has been created", storage.Name),
	})

	helpers.WriteJSON(w, http.StatusCreated, newStorage)
}

func (c *SecretStorageController) Remove(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	storageID, ok := helpers.GetIntParamOrAbort("storage_id", w, r)
	if !ok {
		return
	}

	err := c.secretStorageService.Delete(project.ID, storageID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *SecretStorageController) TestConnection(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessExecute) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	storage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)
	health, err := c.secretStorageService.TestConnection(r.Context(), project.ID, storage.ID)
	if err != nil {
		helpers.WriteJSON(w, http.StatusServiceUnavailable, health)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, health)
}

func (c *SecretStorageController) requireCapability(
	w http.ResponseWriter,
	r *http.Request,
	access pro_interfaces.CapabilityAccess,
) bool {
	if c.capabilityProvider == nil {
		writeRuntimeSecretCapabilityError(w, pro_interfaces.CapabilityDeniedError{
			Decision: pro_interfaces.NewCapabilityDecision(
				pro_interfaces.CapabilityRuntimeSecrets,
				pro_interfaces.CapabilityStateUnavailable,
				pro_interfaces.CapabilityReasonProviderUnavailable,
				nil,
				nil,
			),
			Required: access,
		})
		return false
	}
	user := helpers.UserFromContext(r)
	snapshot, err := c.capabilityProvider.Resolve(r.Context(), pro_interfaces.CapabilityRequest{
		UserID: user.ID, IsAdmin: user.Admin, At: tz.Now(),
	})
	if err == nil {
		err = snapshot.Require(pro_interfaces.CapabilityRuntimeSecrets, access)
	}
	if err != nil {
		writeRuntimeSecretCapabilityError(w, err)
		return false
	}
	return true
}

func writeRuntimeSecretCapabilityError(w http.ResponseWriter, err error) {
	var denied pro_interfaces.CapabilityDeniedError
	if errors.As(err, &denied) {
		status := http.StatusForbidden
		if denied.Decision.State() == pro_interfaces.CapabilityStateUnavailable {
			status = http.StatusNotFound
		}
		helpers.WriteJSON(w, status, map[string]any{
			"error": "CAPABILITY_DENIED", "capability": denied.Decision.ID(),
			"state": denied.Decision.State(), "reason": denied.Decision.Reason(),
			"required_access": denied.Required,
		})
		return
	}
	helpers.WriteErrorStatus(w, "CAPABILITY_PROVIDER_ERROR", http.StatusServiceUnavailable)
}

func (c *SecretStorageController) SyncSecrets(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	oldStorage := helpers.GetFromContext(r, "secretStorage").(db.SecretStorage)

	var storage db.SecretStorage
	if !helpers.Bind(w, r, &storage) {
		return
	}

	if storage.ID != oldStorage.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Secret storage id in URL and in body must be the same",
		})
		return
	}

	if storage.ProjectID != oldStorage.ProjectID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You can not move secret storage to other project",
		})
		return
	}

	sync, err := helpers.Store(r).GetStorageSecretSync(storage.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	err = c.secretStorageService.SyncSecrets(sync)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   oldStorage.ProjectID,
		ObjectType:  db.EventSchedule,
		ObjectID:    oldStorage.ID,
		Description: fmt.Sprintf("Secret storage with ID %d has been synced", storage.ID),
	})

	helpers.WriteJSON(w, http.StatusOK, storage)
}
