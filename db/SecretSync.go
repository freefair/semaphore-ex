package db

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

type SecretSyncDirection string

const (
	SecretSyncDirectionReadOnly SecretSyncDirection = "read_only"
	SecretSyncDirectionOutbound SecretSyncDirection = "outbound"
)

func (d SecretSyncDirection) Validate() error {
	switch d {
	case SecretSyncDirectionReadOnly, SecretSyncDirectionOutbound:
		return nil
	default:
		return errors.New("secret sync direction is invalid")
	}
}

// SecretSync describes a unit of remote-storage synchronization.
// A row with EnvironmentID == nil represents storage-level sync (imports
// access keys). A row with EnvironmentID set represents env-scoped sync
// (imports environment variables for that variable group).
type SecretSync struct {
	ID            int  `db:"id" json:"id" backup:"-"`
	ProjectID     int  `db:"project_id" json:"project_id" backup:"-"`
	StorageID     int  `db:"storage_id" json:"storage_id" backup:"-"`
	EnvironmentID *int `db:"environment_id" json:"environment_id,omitempty" backup:"-"`

	SyncEnabled bool                `db:"sync_enabled" json:"sync_enabled"`
	Direction   SecretSyncDirection `db:"direction" json:"direction"`
	Revision    int                 `db:"revision" json:"revision"`
	// SyncInterval is the auto-sync period in minutes. Zero disables auto-sync.
	SyncInterval     int        `db:"sync_interval" json:"sync_interval"`
	LastSyncedAt     *time.Time `db:"last_synced_at" json:"last_synced_at,omitempty"`
	LastSyncFailedAt *time.Time `db:"last_sync_failed_at" json:"last_sync_failed_at,omitempty"`

	Paths []SecretSyncPath `db:"-" json:"paths"`
}

type SecretSyncPath struct {
	ID                 int    `db:"id" json:"id" backup:"-"`
	SyncID             int    `db:"sync_id" json:"sync_id" backup:"-"`
	Path               string `db:"path" json:"path"`
	Prefix             string `db:"prefix" json:"prefix"`
	Separator          string `db:"separator" json:"separator"`
	AccessKeyID        int    `db:"access_key_id" json:"access_key_id,omitempty"`
	Mount              string `db:"mount" json:"mount,omitempty"`
	Field              string `db:"field" json:"field,omitempty"`
	RemoteVersion      int    `db:"remote_version" json:"remote_version,omitempty"`
	ContentFingerprint string `db:"content_fingerprint" json:"content_fingerprint,omitempty"`
}

var (
	managedSecretMountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	managedSecretFieldPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	managedSecretPathSegment  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@+-]{0,127}$`)
)

func (p SecretSyncPath) ValidateManaged() error {
	if p.AccessKeyID <= 0 {
		return errors.New("access key id must be positive")
	}
	if !managedSecretMountPattern.MatchString(p.Mount) {
		return errors.New("secret mount is invalid")
	}
	if len(p.Path) == 0 || len(p.Path) > 512 || strings.HasPrefix(p.Path, "/") ||
		strings.HasSuffix(p.Path, "/") {
		return errors.New("secret path is invalid")
	}
	for _, segment := range strings.Split(p.Path, "/") {
		if segment == "." || segment == ".." || !managedSecretPathSegment.MatchString(segment) {
			return errors.New("secret path is invalid")
		}
	}
	if !managedSecretFieldPattern.MatchString(p.Field) {
		return errors.New("secret field is invalid")
	}
	if p.RemoteVersion < 0 {
		return errors.New("remote version must not be negative")
	}
	return nil
}

type SecretSyncOperationStatus string

const (
	SecretSyncOperationPending   SecretSyncOperationStatus = "pending"
	SecretSyncOperationRunning   SecretSyncOperationStatus = "running"
	SecretSyncOperationSucceeded SecretSyncOperationStatus = "succeeded"
	SecretSyncOperationFailed    SecretSyncOperationStatus = "failed"
	SecretSyncOperationConflict  SecretSyncOperationStatus = "conflict"
)

type SecretSyncItemStatus string

const (
	SecretSyncItemChanged  SecretSyncItemStatus = "changed"
	SecretSyncItemSkipped  SecretSyncItemStatus = "skipped"
	SecretSyncItemConflict SecretSyncItemStatus = "conflict"
	SecretSyncItemFailed   SecretSyncItemStatus = "failed"
)

type SecretSyncItemOutcome struct {
	MappingID          int                  `json:"mapping_id"`
	AccessKeyID        int                  `json:"access_key_id"`
	Mount              string               `json:"mount"`
	Path               string               `json:"path"`
	Field              string               `json:"field"`
	Status             SecretSyncItemStatus `json:"status"`
	ContentFingerprint string               `json:"content_fingerprint,omitempty"`
	RemoteVersion      int                  `json:"remote_version,omitempty"`
	ErrorCategory      string               `json:"error_category,omitempty"`
}

type SecretSyncOperation struct {
	ID                 int                       `db:"id" json:"id"`
	RequestID          string                    `db:"request_id" json:"request_id"`
	SyncID             int                       `db:"sync_id" json:"sync_id"`
	ProjectID          int                       `db:"project_id" json:"project_id"`
	StorageID          int                       `db:"storage_id" json:"storage_id"`
	SyncRevision       int                       `db:"sync_revision" json:"sync_revision"`
	ResolveOperationID *int                      `db:"resolve_operation_id" json:"resolve_operation_id,omitempty"`
	RequestedBy        *int                      `db:"requested_by" json:"requested_by,omitempty"`
	Status             SecretSyncOperationStatus `db:"status" json:"status"`
	Attempt            int                       `db:"attempt" json:"attempt"`
	LeaseUntil         *time.Time                `db:"lease_until" json:"lease_until,omitempty"`
	CreatedAt          time.Time                 `db:"created_at" json:"created_at"`
	StartedAt          *time.Time                `db:"started_at" json:"started_at,omitempty"`
	FinishedAt         *time.Time                `db:"finished_at" json:"finished_at,omitempty"`
	UpdatedAt          time.Time                 `db:"updated_at" json:"updated_at"`
	ChangedCount       int                       `db:"changed_count" json:"changed_count"`
	SkippedCount       int                       `db:"skipped_count" json:"skipped_count"`
	ConflictCount      int                       `db:"conflict_count" json:"conflict_count"`
	ErrorCategory      string                    `db:"error_category" json:"error_category,omitempty"`
	OutcomeJSON        string                    `db:"outcome" json:"-"`
	Outcomes           []SecretSyncItemOutcome   `db:"-" json:"outcomes"`
}

type SecretSyncExecution struct {
	Outcomes []SecretSyncItemOutcome
	Paths    []SecretSyncPath
}

// SecretStorageSyncPath is retained as an alias for backward compatibility
// with callers that predate the SecretSync refactor.
type SecretStorageSyncPath = SecretSyncPath
