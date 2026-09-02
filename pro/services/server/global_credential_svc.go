package server

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type unavailableGlobalCredentialService struct{}

func NewGlobalCredentialService(db.GlobalCredentialRepository) pro_interfaces.GlobalCredentialServiceFacade {
	return &unavailableGlobalCredentialService{}
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

var _ pro_interfaces.GlobalCredentialServiceFacade = (*unavailableGlobalCredentialService)(nil)
