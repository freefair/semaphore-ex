package db

import (
	"strconv"
	"strings"
	"time"
)

// AccessKeyGlobalSecretStorage owns the client secret of a global secret
// storage. Such keys have no project, so project key lists never show them.
const AccessKeyGlobalSecretStorage AccessKeyOwner = "global_storage"

// GlobalSecretStorage is a connection configured once for the whole server.
// The first supported type is Infisical: it serves global credentials as a
// secret source and routes SSH through Infisical PAM for granted projects.
type GlobalSecretStorage struct {
	ID              int               `db:"id" json:"id"`
	Name            string            `db:"name" json:"name"`
	Type            SecretStorageType `db:"type" json:"type"`
	Params          MapStringAnyField `db:"params" json:"params"`
	CredentialKeyID *int              `db:"credential_key_id" json:"-"`
	Revision        int               `db:"revision" json:"revision"`
	Created         time.Time         `db:"created" json:"created"`
	Updated         time.Time         `db:"updated" json:"updated"`

	// ProjectIDs lists the projects whose tasks use the storage's PAM routes.
	// Persisted in global_secret_storage_project.
	ProjectIDs []int `db:"-" json:"project_ids"`

	// Secret is write-only: the client secret for the database source, or
	// the environment variable name or file path for those sources.
	Secret            string                      `db:"-" json:"secret,omitempty"`
	SourceStorageType *AccessKeySourceStorageType `db:"-" json:"source_storage_type,omitempty"`
}

// GlobalSecretStorageProviderID is the provider identity a global credential
// uses to reference the storage. It is derived from the ID so a rename never
// breaks a stored reference.
func GlobalSecretStorageProviderID(storageID int) string {
	return "storage-" + strconv.Itoa(storageID)
}

// ParseGlobalSecretStorageProviderID returns the storage ID of a provider
// identity produced by GlobalSecretStorageProviderID.
func ParseGlobalSecretStorageProviderID(providerID string) (int, bool) {
	raw, ok := strings.CutPrefix(providerID, "storage-")
	if !ok || raw == "" || strings.HasPrefix(raw, "0") {
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 || strconv.Itoa(id) != raw {
		return 0, false
	}
	return id, true
}

// GlobalSecretStorageRepository persists global secret storages. *SqlDb
// implements it; callers obtain it by type assertion so db.Store stays
// unchanged for upstream merges.
type GlobalSecretStorageRepository interface {
	GetGlobalSecretStorages() ([]GlobalSecretStorage, error)
	GetGlobalSecretStorage(storageID int) (GlobalSecretStorage, error)
	GetGlobalSecretStoragesForProject(projectID int) ([]GlobalSecretStorage, error)
	CreateGlobalSecretStorage(storage GlobalSecretStorage) (GlobalSecretStorage, error)
	// UpdateGlobalSecretStorage applies the change only when the stored
	// revision equals expectedRevision and returns ErrGlobalCredentialRevisionConflict
	// otherwise.
	UpdateGlobalSecretStorage(storage GlobalSecretStorage, expectedRevision int) (GlobalSecretStorage, error)
	DeleteGlobalSecretStorage(storageID int) error
	// GetGlobalSecretStorageKey loads the encrypted client secret record.
	GetGlobalSecretStorageKey(keyID int) (AccessKey, error)
	DeleteGlobalSecretStorageKey(keyID int) error
}
