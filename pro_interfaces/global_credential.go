package pro_interfaces

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

const (
	GlobalCredentialUsagePageMax = 100
	GlobalCredentialBindingMax   = 64
)

var globalCredentialBindingTargetPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// GlobalCredentialResolutionOutcome is deliberately closed because it is
// persisted in the append-only usage ledger and shown to administrators.
type GlobalCredentialResolutionOutcome string

const (
	GlobalCredentialResolutionAllowed GlobalCredentialResolutionOutcome = "allowed"
	GlobalCredentialResolutionDenied  GlobalCredentialResolutionOutcome = "denied"
	GlobalCredentialResolutionFailure GlobalCredentialResolutionOutcome = "failure"
)

// GlobalCredentialResolutionReason is a value-free remediation code. Provider
// errors, decrypted material and reference paths never cross this boundary.
type GlobalCredentialResolutionReason string

const (
	GlobalCredentialResolutionReasonAllowed               GlobalCredentialResolutionReason = "allowed"
	GlobalCredentialResolutionReasonActorRequired         GlobalCredentialResolutionReason = "actor_required"
	GlobalCredentialResolutionReasonTaskPermission        GlobalCredentialResolutionReason = "task_permission_denied"
	GlobalCredentialResolutionReasonConsumePermission     GlobalCredentialResolutionReason = "consume_permission_denied"
	GlobalCredentialResolutionReasonCapability            GlobalCredentialResolutionReason = "capability_unavailable"
	GlobalCredentialResolutionReasonCredentialUnavailable GlobalCredentialResolutionReason = "credential_unavailable"
	GlobalCredentialResolutionReasonGrantUnavailable      GlobalCredentialResolutionReason = "grant_unavailable"
	GlobalCredentialResolutionReasonProviderUnavailable   GlobalCredentialResolutionReason = "provider_unavailable"
	GlobalCredentialResolutionReasonAuditUnavailable      GlobalCredentialResolutionReason = "audit_unavailable"
	GlobalCredentialResolutionReasonInjectionFailed       GlobalCredentialResolutionReason = "injection_failed"
)

// GlobalCredentialTaskBinding is a value-free request to materialize a global
// credential into one task-scoped environment name. It is persisted separately
// from material and must be reauthorized when the task is dispatched.
type GlobalCredentialTaskBinding struct {
	CredentialID int    `json:"credential_id"`
	Target       string `json:"target"`
}

func (b GlobalCredentialTaskBinding) Validate() error {
	if b.CredentialID <= 0 || !globalCredentialBindingTargetPattern.MatchString(b.Target) {
		return errors.New("global credential task binding is invalid")
	}
	return nil
}

// ValidateGlobalCredentialTaskBindings rejects duplicate targets and
// credential IDs so an execution has one unambiguous provenance row per value.
func ValidateGlobalCredentialTaskBindings(bindings []GlobalCredentialTaskBinding) error {
	if len(bindings) > GlobalCredentialBindingMax {
		return errors.New("global credential task bindings exceed maximum")
	}
	targets := make(map[string]struct{}, len(bindings))
	credentials := make(map[int]struct{}, len(bindings))
	for _, binding := range bindings {
		if err := binding.Validate(); err != nil {
			return err
		}
		if _, duplicate := targets[binding.Target]; duplicate {
			return errors.New("global credential task binding target is duplicated")
		}
		if _, duplicate := credentials[binding.CredentialID]; duplicate {
			return errors.New("global credential task binding credential is duplicated")
		}
		targets[binding.Target] = struct{}{}
		credentials[binding.CredentialID] = struct{}{}
	}
	return nil
}

// GlobalCredentialResolutionRequest contains only durable task identity and a
// value-free binding. A zero ActorID represents an actorless trigger; it is
// still structurally valid so the resolver can persist an actor_required audit
// before blocking schedules and integrations.
type GlobalCredentialResolutionRequest struct {
	TaskID             int                         `json:"task_id"`
	ProjectID          int                         `json:"project_id"`
	TemplateID         int                         `json:"template_id"`
	ActorID            int                         `json:"actor_id"`
	RunnerID           *int                        `json:"runner_id,omitempty"`
	DispatchGeneration int                         `json:"dispatch_generation,omitempty"`
	Binding            GlobalCredentialTaskBinding `json:"binding"`
}

func (r GlobalCredentialResolutionRequest) Validate() error {
	if r.TaskID <= 0 || r.ProjectID <= 0 || r.TemplateID <= 0 || r.ActorID < 0 ||
		r.DispatchGeneration < 0 || (r.RunnerID != nil && *r.RunnerID <= 0) {
		return errors.New("global credential resolution request is invalid")
	}
	return r.Binding.Validate()
}

// GlobalCredentialResolutionSnapshot is safe to persist, return to task UI,
// and include in usage history. It has no material or provider path field.
type GlobalCredentialResolutionSnapshot struct {
	TaskID             int                               `json:"task_id"`
	ProjectID          int                               `json:"project_id"`
	ActorID            int                               `json:"actor_id"`
	RunnerID           *int                              `json:"runner_id,omitempty"`
	DispatchGeneration int                               `json:"dispatch_generation,omitempty"`
	Target             string                            `json:"target"`
	CredentialID       int                               `json:"credential_id"`
	GrantID            int                               `json:"grant_id,omitempty"`
	CredentialVersion  int                               `json:"credential_version,omitempty"`
	VersionFingerprint string                            `json:"version_fingerprint,omitempty"`
	ProviderVersion    int                               `json:"provider_version,omitempty"`
	Outcome            GlobalCredentialResolutionOutcome `json:"outcome"`
	Reason             GlobalCredentialResolutionReason  `json:"reason"`
	OccurredAt         time.Time                         `json:"occurred_at"`
}

// GlobalCredentialInjector is intentionally available only to execution
// plumbing. HTTP/API facades receive GlobalCredentialResolutionSnapshot, never
// the material supplied to this callback.
type GlobalCredentialInjector interface {
	InjectGlobalCredential(context.Context, string, []byte) error
}

// GlobalCredentialExternalResult carries transient provider material and its
// provider version. Callers must zero Value after injection or failure.
type GlobalCredentialExternalResult struct {
	Value           []byte
	ProviderVersion int
}

type GlobalCredentialExternalAdapter interface {
	ResolveGlobalCredentialExternal(context.Context, db.GlobalCredentialExternalReference) (GlobalCredentialExternalResult, error)
}

type GlobalCredentialMaterialDecryptor interface {
	OptionEncryptionEnabled() bool
	DecryptOption(string) ([]byte, error)
}

// GlobalCredentialRuntimeDependencies keeps Community and Enhanced
// constructors source-compatible. A missing capability provider is fail-closed
// in Enhanced; Community always returns its unavailable resolver.
type GlobalCredentialRuntimeDependencies struct {
	Cipher     GlobalCredentialMaterialDecryptor
	Capability CapabilityProvider
	External   GlobalCredentialExternalAdapter
}

type GlobalCredentialRuntimeResolver interface {
	ResolveAndInject(context.Context, GlobalCredentialResolutionRequest, GlobalCredentialInjector) (GlobalCredentialResolutionSnapshot, error)
}

type GlobalCredentialUsageQuery struct {
	ProjectID *int                               `json:"project_id,omitempty"`
	TaskID    *int                               `json:"task_id,omitempty"`
	Outcome   *GlobalCredentialResolutionOutcome `json:"outcome,omitempty"`
	BeforeID  int                                `json:"before_id,omitempty"`
	Count     int                                `json:"count"`
}

func (q GlobalCredentialUsageQuery) Validate() error {
	if q.Count < 1 || q.Count > GlobalCredentialUsagePageMax || q.BeforeID < 0 ||
		(q.ProjectID != nil && *q.ProjectID <= 0) || (q.TaskID != nil && *q.TaskID <= 0) {
		return errors.New("global credential usage query is invalid")
	}
	if q.Outcome != nil && *q.Outcome != GlobalCredentialResolutionAllowed &&
		*q.Outcome != GlobalCredentialResolutionDenied && *q.Outcome != GlobalCredentialResolutionFailure {
		return fmt.Errorf("global credential usage outcome is invalid")
	}
	return nil
}

type GlobalCredentialUsageDTO struct {
	ID       int                                `json:"id"`
	Snapshot GlobalCredentialResolutionSnapshot `json:"snapshot"`
}

type GlobalCredentialImpactDTO struct {
	CredentialID     int        `json:"credential_id"`
	UsageCount       int        `json:"usage_count"`
	ProjectCount     int        `json:"project_count"`
	ActiveGrantCount int        `json:"active_grant_count"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
}

var (
	ErrGlobalCredentialInvalidInput       = errors.New("global credential input is invalid")
	ErrGlobalCredentialNotFound           = errors.New("global credential was not found")
	ErrGlobalCredentialRevisionConflict   = errors.New("global credential revision conflict")
	ErrGlobalCredentialGrantConflict      = errors.New("global credential grant conflict")
	ErrGlobalCredentialNotAvailable       = errors.New("global credential is not available")
	ErrGlobalCredentialEncryptionRequired = errors.New("global credential encryption is required")
	ErrGlobalCredentialGrantExists        = errors.New("global credential grant already exists")
	ErrGlobalCredentialDependencyConflict = errors.New("global credential dependency conflict")
	ErrGlobalCredentialResolutionDenied   = errors.New("global credential resolution denied")
	ErrGlobalCredentialAuditUnavailable   = errors.New("global credential usage audit unavailable")
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
	ListGlobalCredentialUsage(context.Context, int, GlobalCredentialUsageQuery) ([]GlobalCredentialUsageDTO, error)
	GetGlobalCredentialImpact(context.Context, int) (GlobalCredentialImpactDTO, error)
	ListTaskGlobalCredentialUsage(context.Context, int, int, GlobalCredentialUsageQuery) ([]GlobalCredentialUsageDTO, error)
}
