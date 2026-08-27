package server

import (
	"context"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type communityAuditWebhookService struct{}

func NewAuditWebhookService(_ db.AuditWebhookRepository, _ *metrics.Metrics) pro_interfaces.AuditWebhookService {
	return &communityAuditWebhookService{}
}

func (*communityAuditWebhookService) PrepareDelivery(context.Context, pro_interfaces.AuditEvent) (*db.AuditWebhookDelivery, error) {
	return nil, nil
}
func (*communityAuditWebhookService) Notify() {}
func (*communityAuditWebhookService) Configuration(context.Context) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, pro_interfaces.ErrAuditWebhookUnavailable
}
func (*communityAuditWebhookService) Configure(context.Context, pro_interfaces.AuditWebhookConfigInput) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, pro_interfaces.ErrAuditWebhookUnavailable
}
func (*communityAuditWebhookService) TestDelivery(context.Context) (pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return pro_interfaces.AuditWebhookDeliveryDTO{}, pro_interfaces.ErrAuditWebhookUnavailable
}
func (*communityAuditWebhookService) SetPaused(context.Context, bool) (pro_interfaces.AuditWebhookConfigDTO, error) {
	return pro_interfaces.AuditWebhookConfigDTO{}, pro_interfaces.ErrAuditWebhookUnavailable
}
func (*communityAuditWebhookService) DeliveryHistory(context.Context, db.RetrieveQueryParams) ([]pro_interfaces.AuditWebhookDeliveryDTO, error) {
	return nil, pro_interfaces.ErrAuditWebhookUnavailable
}
func (*communityAuditWebhookService) Start()       {}
func (*communityAuditWebhookService) Close() error { return nil }

var _ pro_interfaces.AuditWebhookService = (*communityAuditWebhookService)(nil)
