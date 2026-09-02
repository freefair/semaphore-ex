package server

import (
	"context"
	"errors"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type unavailableGlobalCredentialService struct{}
type unavailableGlobalCredentialRuntimeResolver struct{}
type unavailableGlobalCredentialExternalAdapter struct{}

func NewGlobalCredentialService(db.GlobalCredentialRepository) pro_interfaces.GlobalCredentialServiceFacade {
	return &unavailableGlobalCredentialService{}
}

// NewGlobalCredentialRuntimeResolver preserves the runtime seam in Community
// builds while failing closed before any material can be resolved or injected.
func NewGlobalCredentialRuntimeResolver(db.Store, ...pro_interfaces.GlobalCredentialRuntimeDependencies) pro_interfaces.GlobalCredentialRuntimeResolver {
	return &unavailableGlobalCredentialRuntimeResolver{}
}

func NewGlobalCredentialExternalAdapter(*util.ConfigType, ...func(string) (string, bool)) pro_interfaces.GlobalCredentialExternalAdapter {
	return &unavailableGlobalCredentialExternalAdapter{}
}

func (*unavailableGlobalCredentialExternalAdapter) ResolveGlobalCredentialExternal(context.Context, db.GlobalCredentialExternalReference) (pro_interfaces.GlobalCredentialExternalResult, error) {
	return pro_interfaces.GlobalCredentialExternalResult{}, errors.New("global credential provider unavailable")
}

func (*unavailableGlobalCredentialRuntimeResolver) ResolveAndInject(context.Context, pro_interfaces.GlobalCredentialResolutionRequest, pro_interfaces.GlobalCredentialInjector) (pro_interfaces.GlobalCredentialResolutionSnapshot, error) {
	return pro_interfaces.GlobalCredentialResolutionSnapshot{
		Outcome: pro_interfaces.GlobalCredentialResolutionDenied,
		Reason:  pro_interfaces.GlobalCredentialResolutionReasonCapability,
	}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) CreateGlobalCredential(context.Context, int, pro_interfaces.GlobalCredentialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) GetGlobalCredential(context.Context, int) (pro_interfaces.GlobalCredentialDTO, error) {
	return pro_interfaces.GlobalCredentialDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListGlobalCredentials(context.Context, db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) UpdateGlobalCredential(context.Context, int, int, int, pro_interfaces.GlobalCredentialMetadataInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) SetGlobalCredentialEnabled(context.Context, int, int, int, bool) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) RotateGlobalCredential(context.Context, int, int, int, pro_interfaces.GlobalCredentialMaterialInput) (pro_interfaces.GlobalCredentialSummaryDTO, error) {
	return pro_interfaces.GlobalCredentialSummaryDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) DeleteGlobalCredential(context.Context, int, int, int) error {
	return pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) CreateGlobalCredentialGrant(context.Context, int, int, pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListGlobalCredentialGrants(context.Context, int, db.RetrieveQueryParams) ([]pro_interfaces.GlobalCredentialGrantDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) UpdateGlobalCredentialGrant(context.Context, int, int, int, int, pro_interfaces.GlobalCredentialGrantInput) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) SetGlobalCredentialGrantStatus(context.Context, int, int, int, int, db.GlobalCredentialGrantStatus) (pro_interfaces.GlobalCredentialGrantDTO, error) {
	return pro_interfaces.GlobalCredentialGrantDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) DeleteGlobalCredentialGrant(context.Context, int, int, int, int) error {
	return pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListGlobalCredentialGrantProjects(context.Context) ([]pro_interfaces.GlobalCredentialGrantProjectDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListGrantedCredentials(context.Context, int, db.RetrieveQueryParams) ([]pro_interfaces.GrantedCredentialDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListGlobalCredentialUsage(context.Context, int, pro_interfaces.GlobalCredentialUsageQuery) ([]pro_interfaces.GlobalCredentialUsageDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) GetGlobalCredentialImpact(context.Context, int) (pro_interfaces.GlobalCredentialImpactDTO, error) {
	return pro_interfaces.GlobalCredentialImpactDTO{}, pro_interfaces.ErrGlobalCredentialNotAvailable
}
func (*unavailableGlobalCredentialService) ListTaskGlobalCredentialUsage(context.Context, int, int, pro_interfaces.GlobalCredentialUsageQuery) ([]pro_interfaces.GlobalCredentialUsageDTO, error) {
	return nil, pro_interfaces.ErrGlobalCredentialNotAvailable
}

var _ pro_interfaces.GlobalCredentialServiceFacade = (*unavailableGlobalCredentialService)(nil)
var _ pro_interfaces.GlobalCredentialRuntimeResolver = (*unavailableGlobalCredentialRuntimeResolver)(nil)
var _ pro_interfaces.GlobalCredentialExternalAdapter = (*unavailableGlobalCredentialExternalAdapter)(nil)
