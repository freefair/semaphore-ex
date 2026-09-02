package pro_interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

var (
	ErrGlobalCredentialInvalidInput       = errors.New("global credential input is invalid")
	ErrGlobalCredentialNotFound           = errors.New("global credential was not found")
	ErrGlobalCredentialRevisionConflict   = errors.New("global credential revision conflict")
	ErrGlobalCredentialGrantConflict      = errors.New("global credential grant conflict")
	ErrGlobalCredentialNotAvailable       = errors.New("global credential is not available")
	ErrGlobalCredentialEncryptionRequired = errors.New("global credential encryption is required")
	ErrGlobalCredentialGrantExists        = errors.New("global credential grant already exists")
	ErrGlobalCredentialDependencyConflict = errors.New("global credential dependency conflict")
)

// GlobalCredentialSummaryDTO is the least-privilege global view. It is used
// for list and mutation responses so a grant or rotation administrator cannot
// learn ownership, storage topology, timestamps, or an external secret path.
type GlobalCredentialSummaryDTO struct {
	ID             int                             `json:"id"`
	Type           db.GlobalCredentialType         `json:"type"`
	DisplayName    string                          `json:"display_name"`
	Enabled        bool                            `json:"enabled"`
	Revision       int                             `json:"revision"`
	CurrentVersion int                             `json:"current_version"`
	Fingerprint    string                          `json:"fingerprint"`
	MaterialKind   db.GlobalCredentialMaterialKind `json:"material_kind"`
}

// GlobalCredentialMaterialInput is write-only. Exactly one field is supplied
// for create or rotate; metadata updates retain material by omission. The only
// local material type in this contract is a string, rather than arbitrary JSON.
type GlobalCredentialMaterialInput struct {
	StringValue       *string                               `json:"string_value,omitempty"`
	ExternalReference *db.GlobalCredentialExternalReference `json:"external_reference,omitempty"`
}

// GlobalCredentialInput is the bounded create/update representation. No
// material is returned by any DTO in this package.
type GlobalCredentialInput struct {
	Type        db.GlobalCredentialType        `json:"type"`
	DisplayName string                         `json:"display_name"`
	Material    *GlobalCredentialMaterialInput `json:"material,omitempty"`
}

// GlobalCredentialMetadataInput deliberately excludes Material. Rotation is a
// separate authorization boundary and metadata callers cannot smuggle a new
// encrypted value through an update request.
type GlobalCredentialMetadataInput struct {
	DisplayName string `json:"display_name"`
}

// GlobalCredentialDTO is an administrator metadata view. Its fingerprint is
// opaque and random; owner attribution is intentionally omitted from project
// views below.
type GlobalCredentialDTO struct {
	ID                int                                   `json:"id"`
	Type              db.GlobalCredentialType               `json:"type"`
	DisplayName       string                                `json:"display_name"`
	OwnerUserID       int                                   `json:"owner_user_id"`
	Enabled           bool                                  `json:"enabled"`
	Revision          int                                   `json:"revision"`
	CurrentVersion    int                                   `json:"current_version"`
	Fingerprint       string                                `json:"fingerprint"`
	MaterialKind      db.GlobalCredentialMaterialKind       `json:"material_kind"`
	ExternalReference *db.GlobalCredentialExternalReference `json:"external_reference,omitempty"`
	Created           time.Time                             `json:"created"`
	Updated           time.Time                             `json:"updated"`
}

type GlobalCredentialGrantInput struct {
	ProjectID  int                               `json:"project_id"`
	Operations db.GlobalCredentialGrantOperation `json:"operations"`
	ExpiresAt  *time.Time                        `json:"expires_at,omitempty"`
}

type GlobalCredentialGrantDTO struct {
	ID           int                               `json:"id"`
	CredentialID int                               `json:"credential_id"`
	ProjectID    int                               `json:"project_id"`
	Operations   db.GlobalCredentialGrantOperation `json:"operations"`
	ExpiresAt    *time.Time                        `json:"expires_at,omitempty"`
	Status       db.GlobalCredentialGrantStatus    `json:"status"`
	Revision     int                               `json:"revision"`
	Created      time.Time                         `json:"created"`
	Updated      time.Time                         `json:"updated"`
}

// GrantedCredentialDTO is the only DTO project-scoped callers may receive.
// It intentionally cannot identify an owner or reveal a version's material,
// external-reference path, provider identity or opaque fingerprint.
type GrantedCredentialDTO struct {
	CredentialID  int                               `json:"credential_id"`
	Type          db.GlobalCredentialType           `json:"type"`
	DisplayName   string                            `json:"display_name"`
	Version       int                               `json:"version"`
	Operations    db.GlobalCredentialGrantOperation `json:"operations"`
	GrantID       int                               `json:"grant_id"`
	GrantRevision int                               `json:"grant_revision"`
	ExpiresAt     *time.Time                        `json:"expires_at,omitempty"`
}

// GlobalCredentialGrantProjectDTO is the only project shape available to a
// delegated global grant administrator.
type GlobalCredentialGrantProjectDTO struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GlobalCredentialServiceFacade is the Enhanced boundary used by the later
// HTTP layer. Slice 060 deliberately exposes no resolve/inject operation.
type GlobalCredentialServiceFacade interface {
	CreateGlobalCredential(context.Context, int, GlobalCredentialInput) (GlobalCredentialSummaryDTO, error)
	GetGlobalCredential(context.Context, int) (GlobalCredentialDTO, error)
	ListGlobalCredentials(context.Context, db.RetrieveQueryParams) ([]GlobalCredentialSummaryDTO, error)
	UpdateGlobalCredential(context.Context, int, int, int, GlobalCredentialMetadataInput) (GlobalCredentialSummaryDTO, error)
	SetGlobalCredentialEnabled(context.Context, int, int, int, bool) (GlobalCredentialSummaryDTO, error)
	RotateGlobalCredential(context.Context, int, int, int, GlobalCredentialMaterialInput) (GlobalCredentialSummaryDTO, error)
	DeleteGlobalCredential(context.Context, int, int, int) error
	CreateGlobalCredentialGrant(context.Context, int, int, GlobalCredentialGrantInput) (GlobalCredentialGrantDTO, error)
	ListGlobalCredentialGrants(context.Context, int, db.RetrieveQueryParams) ([]GlobalCredentialGrantDTO, error)
	UpdateGlobalCredentialGrant(context.Context, int, int, int, int, GlobalCredentialGrantInput) (GlobalCredentialGrantDTO, error)
	SetGlobalCredentialGrantStatus(context.Context, int, int, int, int, db.GlobalCredentialGrantStatus) (GlobalCredentialGrantDTO, error)
	DeleteGlobalCredentialGrant(context.Context, int, int, int, int) error
	ListGlobalCredentialGrantProjects(context.Context) ([]GlobalCredentialGrantProjectDTO, error)
	ListGrantedCredentials(context.Context, int, db.RetrieveQueryParams) ([]GrantedCredentialDTO, error)
}
