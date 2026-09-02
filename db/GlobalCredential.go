package db

import (
	"bytes"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	ErrGlobalCredentialRevisionConflict = errors.New("global credential revision conflict")
	ErrGlobalCredentialGrantConflict    = errors.New("global credential grant conflict")
	ErrGlobalCredentialGrantExists      = errors.New("global credential grant already exists")
	ErrGlobalCredentialGrantDependency  = errors.New("global credential grant dependency conflict")
	ErrGlobalCredentialEnabled          = errors.New("global credential must be disabled before deletion")
	ErrGlobalCredentialGrantsExist      = errors.New("global credential grants must be deleted before deletion")
)

// GlobalCredentialType deliberately starts with the only executable shape
// Slice 060 can describe safely. New material shapes require an explicit
// contract extension rather than accepting an untyped JSON blob.
type GlobalCredentialType string

const GlobalCredentialTypeString GlobalCredentialType = "string"

type GlobalCredentialMaterialKind string

const (
	GlobalCredentialMaterialLocalEncrypted    GlobalCredentialMaterialKind = "local_encrypted"
	GlobalCredentialMaterialExternalReference GlobalCredentialMaterialKind = "external_reference"
)

type GlobalCredentialGrantOperation int64

const (
	GlobalCredentialGrantOperationReference GlobalCredentialGrantOperation = 1 << iota
	GlobalCredentialGrantOperationConsume
)

const allGlobalCredentialGrantOperations = GlobalCredentialGrantOperationReference | GlobalCredentialGrantOperationConsume

type GlobalCredentialGrantStatus string

const (
	GlobalCredentialGrantStatusActive  GlobalCredentialGrantStatus = "active"
	GlobalCredentialGrantStatusRevoked GlobalCredentialGrantStatus = "revoked"
)

var (
	globalCredentialIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	globalCredentialMountPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	globalCredentialFieldPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	globalCredentialPathSegment       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@+-]{0,127}$`)
	globalCredentialFingerprint       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// GlobalCredentialExternalReference is a value-free identity held outside a
// project SecretStorage. Slice 060 persists but never resolves this value.
// Provider-specific resolution is deliberately deferred to Slice 061.
type GlobalCredentialExternalReference struct {
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id"`
	Mount      string `json:"mount"`
	Path       string `json:"path"`
	Version    int    `json:"version"`
	Field      string `json:"field"`
}

func (r GlobalCredentialExternalReference) IsZero() bool {
	return r.Provider == "" && r.ProviderID == "" && r.Mount == "" && r.Path == "" && r.Version == 0 && r.Field == ""
}

func (r GlobalCredentialExternalReference) Validate() error {
	if (r.Provider != "vault" && r.Provider != "openbao") ||
		!globalCredentialIdentifierPattern.MatchString(r.ProviderID) ||
		!globalCredentialMountPattern.MatchString(r.Mount) ||
		!globalCredentialFieldPattern.MatchString(r.Field) || r.Version <= 0 ||
		!validGlobalCredentialReferencePath(r.Path) {
		return errors.New("global credential external reference is invalid")
	}
	return nil
}

// Scan implements sql.Scanner for the one value-free structured database
// field. Invalid persisted JSON fails closed rather than becoming a partially
// usable reference.
func (r *GlobalCredentialExternalReference) Scan(value any) error {
	if value == nil {
		*r = GlobalCredentialExternalReference{}
		return nil
	}
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return errors.New("unsupported global credential external reference")
	}
	if len(raw) == 0 {
		*r = GlobalCredentialExternalReference{}
		return nil
	}
	if len(raw) > 2048 {
		return errors.New("global credential external reference is invalid")
	}
	var decoded GlobalCredentialExternalReference
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("global credential external reference is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("global credential external reference is invalid")
	}
	if err := decoded.Validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

func validGlobalCredentialReferencePath(path string) bool {
	if len(path) == 0 || len(path) > 512 || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." || !globalCredentialPathSegment.MatchString(segment) {
			return false
		}
	}
	return true
}

// Value implements driver.Valuer. Empty is stored as an empty value only for
// local encrypted versions; validation rejects it for external references.
func (r GlobalCredentialExternalReference) Value() (driver.Value, error) {
	if r.IsZero() {
		return "", nil
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// GlobalCredential holds global metadata only. OwnerUserID is attribution,
// not a foreign key: user lifecycle must not silently destroy credentials.
type GlobalCredential struct {
	ID             int                  `db:"id" json:"id"`
	Type           GlobalCredentialType `db:"type" json:"type"`
	DisplayName    string               `db:"display_name" json:"display_name"`
	OwnerUserID    int                  `db:"owner_user_id" json:"owner_user_id"`
	Enabled        bool                 `db:"enabled" json:"enabled"`
	Revision       int                  `db:"revision" json:"revision"`
	CurrentVersion int                  `db:"current_version" json:"current_version"`
	Created        time.Time            `db:"created" json:"created"`
	Updated        time.Time            `db:"updated" json:"updated"`
}

// GlobalCredentialVersion is immutable once created. It contains exactly one
// material representation: ciphertext for local string material or a typed,
// value-free external reference.
type GlobalCredentialVersion struct {
	ID                int                               `db:"id" json:"id"`
	CredentialID      int                               `db:"credential_id" json:"credential_id"`
	Version           int                               `db:"version" json:"version"`
	Fingerprint       string                            `db:"fingerprint" json:"fingerprint"`
	MaterialKind      GlobalCredentialMaterialKind      `db:"material_kind" json:"material_kind"`
	EncryptedMaterial string                            `db:"encrypted_material" json:"-"`
	ExternalReference GlobalCredentialExternalReference `db:"external_reference" json:"-"`
	CreatedByUserID   int                               `db:"created_by_user_id" json:"created_by_user_id"`
	Created           time.Time                         `db:"created" json:"created"`
}

// GlobalCredentialGrant is the sole durable authority between one global
// credential and one project. A grant is never inferred from project keys.
type GlobalCredentialGrant struct {
	ID              int                            `db:"id" json:"id"`
	CredentialID    int                            `db:"credential_id" json:"credential_id"`
	ProjectID       int                            `db:"project_id" json:"project_id"`
	Operations      GlobalCredentialGrantOperation `db:"operations" json:"operations"`
	ExpiresAt       *time.Time                     `db:"expires_at" json:"expires_at,omitempty"`
	Status          GlobalCredentialGrantStatus    `db:"status" json:"status"`
	Revision        int                            `db:"revision" json:"revision"`
	CreatedByUserID int                            `db:"created_by_user_id" json:"created_by_user_id"`
	RevokedByUserID *int                           `db:"revoked_by_user_id" json:"revoked_by_user_id,omitempty"`
	RevokedAt       *time.Time                     `db:"revoked_at" json:"revoked_at,omitempty"`
	Created         time.Time                      `db:"created" json:"created"`
	Updated         time.Time                      `db:"updated" json:"updated"`
}

// GlobalCredentialGrantedMetadata is the deliberately narrow project view.
// It intentionally excludes owner, material, reference, fingerprint and the
// storage identifiers that Slice 061 will need only at resolution time.
type GlobalCredentialGrantedMetadata struct {
	CredentialID  int                            `db:"credential_id" json:"credential_id"`
	Type          GlobalCredentialType           `db:"type" json:"type"`
	DisplayName   string                         `db:"display_name" json:"display_name"`
	Version       int                            `db:"version" json:"version"`
	Operations    GlobalCredentialGrantOperation `db:"operations" json:"operations"`
	GrantID       int                            `db:"grant_id" json:"grant_id"`
	GrantRevision int                            `db:"grant_revision" json:"grant_revision"`
	ExpiresAt     *time.Time                     `db:"expires_at" json:"expires_at,omitempty"`
}

// GlobalCredentialGrantProject is the bounded project identity exposed to a
// global grant administrator. Project configuration never crosses this seam.
type GlobalCredentialGrantProject struct {
	ID   int    `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

func ValidateGlobalCredential(credential GlobalCredential) error {
	if credential.Type != GlobalCredentialTypeString ||
		strings.TrimSpace(credential.DisplayName) == "" || len(credential.DisplayName) > 128 ||
		credential.OwnerUserID <= 0 || credential.Revision <= 0 || credential.CurrentVersion <= 0 {
		return errors.New("global credential is invalid")
	}
	return nil
}

func ValidateGlobalCredentialVersion(version GlobalCredentialVersion) error {
	if version.CredentialID <= 0 || version.Version <= 0 ||
		!globalCredentialFingerprint.MatchString(version.Fingerprint) || version.CreatedByUserID <= 0 {
		return errors.New("global credential version is invalid")
	}
	local := version.EncryptedMaterial != ""
	external := !version.ExternalReference.IsZero()
	switch version.MaterialKind {
	case GlobalCredentialMaterialLocalEncrypted:
		if !local || external || len(version.EncryptedMaterial) > 65536 {
			return errors.New("global credential version material is invalid")
		}
	case GlobalCredentialMaterialExternalReference:
		if local || !external || version.ExternalReference.Validate() != nil {
			return errors.New("global credential version material is invalid")
		}
	default:
		return errors.New("global credential version material is invalid")
	}
	return nil
}

func ValidateGlobalCredentialGrant(grant GlobalCredentialGrant) error {
	if grant.CredentialID <= 0 || grant.ProjectID <= 0 || grant.Revision <= 0 ||
		grant.Operations == 0 || grant.Operations&^allGlobalCredentialGrantOperations != 0 ||
		grant.CreatedByUserID <= 0 ||
		(grant.Status != GlobalCredentialGrantStatusActive && grant.Status != GlobalCredentialGrantStatusRevoked) {
		return errors.New("global credential grant is invalid")
	}
	if grant.ExpiresAt != nil && grant.ExpiresAt.Location() != time.UTC {
		return errors.New("global credential grant expiry must use UTC")
	}
	if grant.Status == GlobalCredentialGrantStatusActive && (grant.RevokedByUserID != nil || grant.RevokedAt != nil) {
		return errors.New("active global credential grant cannot have revoke metadata")
	}
	if grant.Status == GlobalCredentialGrantStatusRevoked &&
		(grant.RevokedByUserID == nil || *grant.RevokedByUserID <= 0 || grant.RevokedAt == nil) {
		return errors.New("revoked global credential grant requires revoke metadata")
	}
	return nil
}

// IsEffectiveAt evaluates dynamic expiry at the caller-supplied server UTC
// time. A disabled credential or a mismatching project/operation fails closed.
func (grant GlobalCredentialGrant) IsEffectiveAt(projectID int, operation GlobalCredentialGrantOperation, credentialEnabled bool, now time.Time) bool {
	if !credentialEnabled || projectID <= 0 || projectID != grant.ProjectID ||
		grant.Status != GlobalCredentialGrantStatusActive || !grant.Operations.Can(operation) ||
		operation == 0 || operation&^allGlobalCredentialGrantOperations != 0 || now.Location() != time.UTC {
		return false
	}
	return grant.ExpiresAt == nil || grant.ExpiresAt.After(now)
}

func (operations GlobalCredentialGrantOperation) Can(required GlobalCredentialGrantOperation) bool {
	return required != 0 && operations&required == required
}

// NewGlobalCredentialVersionFingerprint produces an opaque, per-version
// random identity. It is never derived from local material.
func NewGlobalCredentialVersionFingerprint() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate global credential fingerprint: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
