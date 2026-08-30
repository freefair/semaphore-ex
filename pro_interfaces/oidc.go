package pro_interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// OIDCGroupProviderConfiguration is the secret-free provider projection used
// by the administration API. Provider credentials and endpoint metadata are
// deliberately not part of this contract.
type OIDCGroupProviderConfiguration struct {
	ID                 string                      `json:"id"`
	DisplayName        string                      `json:"display_name"`
	ClaimConfiguration OIDCGroupClaimConfiguration `json:"claim_configuration"`
}

type OIDCGroupMappingRequest struct {
	ActorID          int
	ActorIsAdmin     bool
	Configuration    OIDCGroupClaimConfiguration
	Mapping          OIDCGroupMapping
	ExpectedRevision int
	Now              time.Time
}

type OIDCGroupMappingDeleteRequest struct {
	ActorID          int
	ActorIsAdmin     bool
	ProviderID       string
	MappingID        string
	ExpectedRevision int
	Now              time.Time
}

type OIDCGroupPreviewRequest struct {
	ActorID       *int
	ActorIsAdmin  bool
	ProviderID    string
	Configuration OIDCGroupClaimConfiguration
	Claim         OIDCGroupClaimSet
	UserID        int
	Source        string
	Now           time.Time
}

// OIDCGroupMappingService owns deterministic group-to-role policy. Callers
// pass only the normalized allow-listed group claim, never tokens or complete
// provider claim documents.
type OIDCGroupMappingService interface {
	Available(context.Context) error
	GroupMappings(context.Context, string) ([]OIDCGroupMapping, error)
	SaveGroupMapping(context.Context, OIDCGroupMappingRequest) (OIDCGroupMapping, error)
	DeleteGroupMapping(context.Context, OIDCGroupMappingDeleteRequest) error
	PreviewGroupMappings(context.Context, OIDCGroupPreviewRequest) (OIDCGroupPreview, error)
	ReconcileGroupMappings(context.Context, OIDCGroupPreviewRequest) (OIDCGroupPreview, error)
	GroupReconciliationHistory(context.Context, string, int) ([]db.OIDCGroupReconciliation, error)
	EffectiveGroupAssignments(context.Context, string) ([]OIDCRoleAssignment, error)
}

var (
	ErrOIDCGroupMappingUnavailable     = errors.New("OIDC group mapping capability unavailable")
	ErrOIDCGroupMappingForbidden       = errors.New("OIDC group mapping operation forbidden")
	ErrOIDCGroupMappingPreviewStale    = errors.New("OIDC group mapping preview is stale")
	ErrOIDCGroupMappingCollision       = errors.New("OIDC group mapping collision")
	ErrOIDCGroupProtectedAdministrator = errors.New("OIDC group mapping would remove a protected administrator")
)
