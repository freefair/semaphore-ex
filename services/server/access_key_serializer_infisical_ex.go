package server

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// A Key Store key can read its value from a project Infisical storage. The
// reference reuses the runtime secret reference: Mount is the Infisical
// project ID, Path the environment slug optionally followed by a folder
// ("prod" or "prod/apps/web"), and Field the secret name. An environment
// slug cannot contain a slash, so the split is unambiguous.

func validateInfisicalKeyReference(reference pro_interfaces.SecretReference) error {
	environment, _, _ := strings.Cut(reference.Path, "/")
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(environment) {
		return common_errors.NewValidationError("an Infisical key needs an environment, an optional folder and a secret name")
	}
	return nil
}

// infisicalSecretRequest turns a key reference into an Infisical request.
func infisicalSecretRequest(reference pro_interfaces.SecretReference) infisicalpam.SecretRequest {
	environment, folder, _ := strings.Cut(reference.Path, "/")
	return infisicalpam.SecretRequest{
		ProjectID: reference.Mount, Environment: environment, Path: "/" + folder,
		Name: reference.Field, Version: reference.Version,
	}
}

type infisicalAccessKeyDeserializer struct {
	keys       db.AccessKeyManager
	storages   db.SecretStorageRepository
	encryption AccessKeyEncryptionService
}

func newInfisicalAccessKeyDeserializer(keys db.AccessKeyManager, storages db.SecretStorageRepository, encryption AccessKeyEncryptionService) AccessKeyDeserializer {
	return &infisicalAccessKeyDeserializer{keys: keys, storages: storages, encryption: encryption}
}

func (d *infisicalAccessKeyDeserializer) SerializeSecret(*db.AccessKey) error {
	return errors.New("Infisical storages are read-only")
}

func (d *infisicalAccessKeyDeserializer) DeleteSecret(*db.AccessKey) error {
	return errors.New("Infisical storages are read-only")
}

// DeserializeSecret reads the referenced secret with the storage's machine
// identity. Errors never carry the reference or a provider message.
func (d *infisicalAccessKeyDeserializer) DeserializeSecret(key *db.AccessKey) (string, error) {
	unavailable := errors.New("the Infisical secret could not be read")
	if key == nil || key.ProjectID == nil || key.SourceStorageID == nil || key.SourceStorageKey == nil {
		return "", unavailable
	}
	reference, err := pro_interfaces.DecodeSecretReference(*key.SourceStorageKey)
	if err != nil || reference.StorageID != *key.SourceStorageID || validateInfisicalKeyReference(reference) != nil {
		return "", unavailable
	}
	storage, err := d.storages.GetSecretStorage(*key.ProjectID, *key.SourceStorageID)
	if err != nil || storage.Type != db.SecretStorageTypeInfisical {
		return "", unavailable
	}
	credentials, err := d.keys.GetAccessKeys(*key.ProjectID, db.GetAccessKeyOptions{
		Owner: db.AccessKeySecretStorage, StorageID: &storage.ID,
	}, db.RetrieveQueryParams{})
	if err != nil || len(credentials) != 1 {
		return "", unavailable
	}
	credential := credentials[0]
	if err = d.encryption.DeserializeSecret(&credential); err != nil || credential.String == "" {
		return "", unavailable
	}
	session, err := newInfisicalBrowseSession(context.Background(), storage.Params, credential.String)
	if err != nil {
		return "", unavailable
	}
	secret, err := session.client.GetSecret(context.Background(), session.token, infisicalSecretRequest(reference))
	if err != nil {
		return "", unavailable
	}
	defer zeroBytes(secret.Value)
	return string(secret.Value), nil
}
