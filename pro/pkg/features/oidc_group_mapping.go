package features

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type communityOIDCGroupMappingService struct{}

func NewOIDCGroupMappingService(db.OIDCGroupMappingRepository) pro_interfaces.OIDCGroupMappingService {
	return &communityOIDCGroupMappingService{}
}

func (*communityOIDCGroupMappingService) Available(context.Context) error {
	return pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) GroupMappings(context.Context, string) ([]pro_interfaces.OIDCGroupMapping, error) {
	return nil, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) SaveGroupMapping(context.Context, pro_interfaces.OIDCGroupMappingRequest) (pro_interfaces.OIDCGroupMapping, error) {
	return pro_interfaces.OIDCGroupMapping{}, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) DeleteGroupMapping(context.Context, pro_interfaces.OIDCGroupMappingDeleteRequest) error {
	return pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) PreviewGroupMappings(context.Context, pro_interfaces.OIDCGroupPreviewRequest) (pro_interfaces.OIDCGroupPreview, error) {
	return pro_interfaces.OIDCGroupPreview{}, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) ReconcileGroupMappings(context.Context, pro_interfaces.OIDCGroupPreviewRequest) (pro_interfaces.OIDCGroupPreview, error) {
	return pro_interfaces.OIDCGroupPreview{}, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) GroupReconciliationHistory(context.Context, string, int) ([]db.OIDCGroupReconciliation, error) {
	return nil, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

func (*communityOIDCGroupMappingService) EffectiveGroupAssignments(context.Context, string) ([]pro_interfaces.OIDCRoleAssignment, error) {
	return nil, pro_interfaces.ErrOIDCGroupMappingUnavailable
}

var _ pro_interfaces.OIDCGroupMappingService = (*communityOIDCGroupMappingService)(nil)
