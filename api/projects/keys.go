package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/semaphoreui/semaphore/services/server"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

type KeyController struct {
	accessKeyService server.AccessKeyService
}

const generatedSSHKeyRequestMaxBytes = 4 << 10

type generatedSSHKeyRequest struct {
	Name      string                          `json:"name"`
	Login     string                          `json:"login"`
	Algorithm server.GeneratedSSHKeyAlgorithm `json:"algorithm"`
}

type rotateGeneratedSSHKeyRequest struct {
	Algorithm       server.GeneratedSSHKeyAlgorithm `json:"algorithm"`
	ConfirmRotation bool                            `json:"confirm_rotation"`
}

type accessKeyReadDTO struct {
	ID        int               `json:"id"`
	Name      string            `json:"name"`
	Type      db.AccessKeyType  `json:"type"`
	ProjectID *int              `json:"project_id"`
	Plain     *string           `json:"plain,omitempty"`
	Empty     bool              `json:"empty,omitempty"`
	Owner     db.AccessKeyOwner `json:"owner,omitempty"`

	SSH           struct{} `json:"ssh"`
	LoginPassword struct{} `json:"login_password"`

	SourceStorageID      *int                            `json:"source_storage_id,omitempty"`
	SourceStorageKey     *string                         `json:"source_storage_key,omitempty"`
	SourceStorageType    *db.AccessKeySourceStorageType  `json:"source_storage_type,omitempty"`
	SourceStorageMount   string                          `json:"source_storage_mount,omitempty"`
	SourceStorageVersion int                             `json:"source_storage_version,omitempty"`
	SourceStorageField   string                          `json:"source_storage_field,omitempty"`
	Synchronized         bool                            `json:"synchronized,omitempty"`
	GeneratedSSHKey      *server.GeneratedSSHKeyMetadata `json:"generated_ssh_key,omitempty"`
}

type generatedSSHKeyResponse struct {
	Key         accessKeyReadDTO                `json:"key"`
	PublicKey   string                          `json:"public_key"`
	Fingerprint string                          `json:"fingerprint"`
	Algorithm   server.GeneratedSSHKeyAlgorithm `json:"algorithm"`
}

func NewKeyController(
	accessKeyService server.AccessKeyService,
) *KeyController {
	return &KeyController{
		accessKeyService: accessKeyService,
	}
}

func accessKeyReadDTOFrom(key db.AccessKey) accessKeyReadDTO {
	response := accessKeyReadDTO{
		ID:                   key.ID,
		Name:                 key.Name,
		Type:                 key.Type,
		ProjectID:            key.ProjectID,
		Plain:                key.Plain,
		Empty:                key.Empty,
		Owner:                key.Owner,
		SourceStorageID:      key.SourceStorageID,
		SourceStorageKey:     key.SourceStorageKey,
		SourceStorageType:    key.SourceStorageType,
		SourceStorageMount:   key.SourceStorageMount,
		SourceStorageVersion: key.SourceStorageVersion,
		SourceStorageField:   key.SourceStorageField,
		Synchronized:         key.Synchronized,
	}
	if metadata, ok := server.ParseGeneratedSSHKeyMetadata(key.Plain); ok {
		response.GeneratedSSHKey = metadata
	}
	return response
}

func writeGeneratedSSHKeyRequestError(w http.ResponseWriter) {
	helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
		"error": "invalid generated SSH key request",
	})
}

func decodeStrictGeneratedSSHKeyRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, generatedSSHKeyRequestMaxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeGeneratedSSHKeyRequestError(w)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeGeneratedSSHKeyRequestError(w)
		return false
	}
	return true
}

func (c *KeyController) generatedSSHKeyService(w http.ResponseWriter) (server.GeneratedSSHKeyService, bool) {
	service, ok := c.accessKeyService.(server.GeneratedSSHKeyService)
	if !ok {
		helpers.WriteJSON(w, http.StatusNotImplemented, map[string]string{"error": "generated SSH keys are unavailable"})
		return nil, false
	}
	return service, true
}

// KeyMiddleware ensures a key exists and loads it to the context
func KeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		keyID, ok := helpers.GetIntParamOrAbort("key_id", w, r)
		if !ok {
			return
		}

		key, err := helpers.Store(r).GetAccessKey(project.ID, keyID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		// Task-bound survey-secret keys are internal: they must not be
		// readable or mutable through the generic key endpoints.
		if key.Owner == db.AccessKeyTaskSecret {
			helpers.WriteError(w, db.ErrNotFound)
			return
		}

		r = helpers.SetContextValue(r, "accessKey", key)
		next.ServeHTTP(w, r)
	})
}

func GetKeyRefs(w http.ResponseWriter, r *http.Request) {
	key := helpers.GetFromContext(r, "accessKey").(db.AccessKey)
	refs, err := helpers.Store(r).GetAccessKeyRefs(*key.ProjectID, key.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, refs)
}

// GetKeys retrieves sorted keys from the database
func GetKeys(w http.ResponseWriter, r *http.Request) {
	if key := helpers.GetFromContext(r, "accessKey"); key != nil {
		k := key.(db.AccessKey)
		server.ExposeRuntimeSecretReference(&k)
		helpers.WriteJSON(w, http.StatusOK, accessKeyReadDTOFrom(k))
		return
	}

	project := helpers.GetFromContext(r, "project").(db.Project)
	var keys []db.AccessKey

	keys, err := helpers.Store(r).GetAccessKeys(project.ID, db.GetAccessKeyOptions{}, helpers.QueryParams(r.URL))

	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	response := make([]accessKeyReadDTO, 0, len(keys))
	for index := range keys {
		server.ExposeRuntimeSecretReference(&keys[index])
		response = append(response, accessKeyReadDTOFrom(keys[index]))
	}

	helpers.WriteJSON(w, http.StatusOK, response)
}

// AddKey adds a new key to the database
func (c *KeyController) AddKey(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var key db.AccessKey

	if !helpers.Bind(w, r, &key) {
		return
	}

	if key.ProjectID == nil || *key.ProjectID != project.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}

	// Plain cannot be passed via a request
	key.Plain = nil
	key.IgnorePlain = true
	key.Synchronized = false

	// Task-bound survey-secret keys are created internally at task start only.
	if key.Owner == db.AccessKeyTaskSecret {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid key owner",
		})
		return
	}

	//if err := key.Validate(true); err != nil {
	//	helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
	//		"error": err.Error(),
	//	})
	//	return
	//}

	newKey, err := c.accessKeyService.Create(key)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   *newKey.ProjectID,
		ObjectType:  db.EventKey,
		ObjectID:    newKey.ID,
		Description: fmt.Sprintf("Access Key %s created", key.Name),
	})

	// Reload key to drop sensitive fields
	key, err = helpers.Store(r).GetAccessKey(*newKey.ProjectID, newKey.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	server.ExposeRuntimeSecretReference(&key)

	helpers.WriteJSON(w, http.StatusCreated, accessKeyReadDTOFrom(key))
}

// GenerateSSHKey creates a new local SSH access key without accepting any
// private-key, type, project, or storage field from the request body.
func (c *KeyController) GenerateSSHKey(w http.ResponseWriter, r *http.Request) {
	var request generatedSSHKeyRequest
	if !decodeStrictGeneratedSSHKeyRequest(w, r, &request) {
		return
	}
	service, ok := c.generatedSSHKeyService(w)
	if !ok {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	result, err := service.CreateGeneratedSSHKey(server.CreateGeneratedSSHKeyRequest{
		ProjectID: project.ID,
		Name:      request.Name,
		Login:     request.Login,
		Algorithm: request.Algorithm,
	})
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventKey,
		ObjectID:    result.Key.ID,
		Description: fmt.Sprintf("Access Key %s (ID %d) SSH key generated (%s, %s)", result.Key.Name, result.Key.ID, result.Algorithm, result.Fingerprint),
	})
	helpers.WriteJSON(w, http.StatusCreated, generatedSSHKeyResponse{
		Key:         accessKeyReadDTOFrom(result.Key),
		PublicKey:   result.PublicKey,
		Fingerprint: result.Fingerprint,
		Algorithm:   result.Algorithm,
	})
}

// UpdateKey updates key in database
// nolint: gocyclo
func (c *KeyController) UpdateKey(w http.ResponseWriter, r *http.Request) {
	var key db.AccessKey
	oldKey := helpers.GetFromContext(r, "accessKey").(db.AccessKey)

	if !helpers.Bind(w, r, &key) {
		return
	}

	// access key ID and project ID in the body and the path must be the same
	if key.ID != oldKey.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Access key id in URL and in body must be the same",
		})
		return
	}

	if oldKey.ProjectID == nil || key.ProjectID == nil || *key.ProjectID != *oldKey.ProjectID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You can not move access key to other project",
		})
		return
	}
	if key.OverrideSecret {
		if _, generated := server.ParseGeneratedSSHKeyMetadata(oldKey.Plain); generated {
			helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Generated SSH keys must be replaced with the explicit rotate action",
			})
			return
		}
	}

	if oldKey.Synchronized {
		if key.Name != oldKey.Name || key.Type != oldKey.Type {
			helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Name and type of synchronized key cannot be changed",
			})
			return
		}
	}

	// Plain cannot be passed via a request
	key.Plain = nil
	key.IgnorePlain = true
	key.Synchronized = oldKey.Synchronized

	if err := clearRepositoryCachesForKey(r, key); err != nil {
		helpers.WriteError(w, err)
		return
	}

	err := c.accessKeyService.Update(key)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   *oldKey.ProjectID,
		ObjectType:  db.EventKey,
		ObjectID:    oldKey.ID,
		Description: fmt.Sprintf("Access Key %s updated", key.Name),
	})

	w.WriteHeader(http.StatusNoContent)
}

// RotateGeneratedSSHKey replaces a local SSH access key only through the
// explicit, confirmed generation service command.
func (c *KeyController) RotateGeneratedSSHKey(w http.ResponseWriter, r *http.Request) {
	var request rotateGeneratedSSHKeyRequest
	if !decodeStrictGeneratedSSHKeyRequest(w, r, &request) {
		return
	}
	service, ok := c.generatedSSHKeyService(w)
	if !ok {
		return
	}
	key := helpers.GetFromContext(r, "accessKey").(db.AccessKey)
	if key.ProjectID == nil || !request.ConfirmRotation || key.Type != db.AccessKeySSH || key.Synchronized || key.SourceStorageType != nil || server.ValidateGeneratedSSHKeyAlgorithm(request.Algorithm) != nil {
		writeGeneratedSSHKeyRequestError(w)
		return
	}
	if err := clearRepositoryCachesForKey(r, key); err != nil {
		helpers.WriteError(w, err)
		return
	}
	result, err := service.RotateGeneratedSSHKey(server.RotateGeneratedSSHKeyRequest{
		ProjectID:       *key.ProjectID,
		KeyID:           key.ID,
		Algorithm:       request.Algorithm,
		ConfirmRotation: request.ConfirmRotation,
	})
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   *key.ProjectID,
		ObjectType:  db.EventKey,
		ObjectID:    key.ID,
		Description: fmt.Sprintf("Access Key %s (ID %d) SSH key rotated (%s, %s)", result.Key.Name, key.ID, result.Algorithm, result.Fingerprint),
	})
	helpers.WriteJSON(w, http.StatusOK, generatedSSHKeyResponse{
		Key:         accessKeyReadDTOFrom(result.Key),
		PublicKey:   result.PublicKey,
		Fingerprint: result.Fingerprint,
		Algorithm:   result.Algorithm,
	})
}

func clearRepositoryCachesForKey(r *http.Request, key db.AccessKey) error {
	repos, err := helpers.Store(r).GetRepositories(*key.ProjectID, db.RetrieveQueryParams{})
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if repo.SSHKeyID == key.ID {
			if err = repo.ClearCache(); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveKey deletes a key from the database
func (c *KeyController) RemoveKey(w http.ResponseWriter, r *http.Request) {
	key := helpers.GetFromContext(r, "accessKey").(db.AccessKey)

	err := c.accessKeyService.Delete(*key.ProjectID, key.ID)
	if errors.Is(err, db.ErrInvalidOperation) {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": "Access Key is in use by one or more templates",
			"inUse": true,
		})
		return
	}

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogDelete, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   *key.ProjectID,
		ObjectType:  db.EventKey,
		ObjectID:    key.ID,
		Description: fmt.Sprintf("Access Key %s deleted", key.Name),
	})

	w.WriteHeader(http.StatusNoContent)
}
