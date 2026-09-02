package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
	"io"
	"net/http"
)

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
