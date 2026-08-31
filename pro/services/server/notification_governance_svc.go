package server

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// NewNotificationGovernanceService keeps Community builds compatible with the
// Enhanced governance interface while exposing no notification capability.
func NewNotificationGovernanceService(_ db.NotificationRepository) pro_interfaces.NotificationGovernanceServiceFacade {
	return communityNotificationGovernanceService{}
}

// NewNotificationDispatcher keeps Community startup lifecycle-compatible while
// intentionally running no Enhanced notification worker.
func NewNotificationDispatcher(_ db.NotificationRepository) pro_interfaces.NotificationDeliveryDispatcher {
	return communityNotificationDeliveryDispatcher{}
}

type communityNotificationDeliveryDispatcher struct{}

func (communityNotificationDeliveryDispatcher) RegisterAdapter(pro_interfaces.NotificationProviderAdapter) error {
	return pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationDeliveryDispatcher) Start()       {}
func (communityNotificationDeliveryDispatcher) Close() error { return nil }

type communityNotificationGovernanceService struct{}

func (communityNotificationGovernanceService) CreateDestination(context.Context, *int, pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) GetDestination(context.Context, *int, int) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) ListDestinations(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationDestinationDTO, error) {
	return nil, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) UpdateDestination(context.Context, *int, int, int, pro_interfaces.NotificationDestinationInput) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) DeleteDestination(context.Context, *int, int, int) error {
	return pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) SetDestinationPaused(context.Context, *int, int, int, bool) (pro_interfaces.NotificationDestinationDTO, error) {
	return pro_interfaces.NotificationDestinationDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) CreateRule(context.Context, *int, pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	return pro_interfaces.NotificationRuleDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) ListRules(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationRuleDTO, error) {
	return nil, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) UpdateRule(context.Context, *int, int, int, pro_interfaces.NotificationRuleInput) (pro_interfaces.NotificationRuleDTO, error) {
	return pro_interfaces.NotificationRuleDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) DeleteRule(context.Context, *int, int, int) error {
	return pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) PreviewRouting(context.Context, *int, pro_interfaces.NotificationEvent) ([]pro_interfaces.NotificationRoutingPreviewDTO, error) {
	return nil, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) EnqueueTestDelivery(context.Context, *int, int) (pro_interfaces.NotificationDeliveryDTO, error) {
	return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) DeliveryHistory(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationDeliveryDTO, error) {
	return nil, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) EventHistory(context.Context, *int, db.RetrieveQueryParams) ([]pro_interfaces.NotificationEventHistoryDTO, error) {
	return nil, pro_interfaces.ErrNotificationUnavailable
}
func (communityNotificationGovernanceService) RetryDelivery(context.Context, *int, int) (pro_interfaces.NotificationDeliveryDTO, error) {
	return pro_interfaces.NotificationDeliveryDTO{}, pro_interfaces.ErrNotificationUnavailable
}

var _ pro_interfaces.NotificationGovernanceServiceFacade = communityNotificationGovernanceService{}
