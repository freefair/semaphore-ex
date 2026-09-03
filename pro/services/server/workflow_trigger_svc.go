package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowTriggerService struct{}

var _ pro_interfaces.WorkflowTriggerService = (*workflowTriggerService)(nil)

func NewWorkflowTriggerService(
	_ db.WorkflowTriggerManager,
	_ pro_interfaces.WorkflowTriggerWorkflowStore,
	_ pro_interfaces.WorkflowService,
	_ pro_interfaces.WorkflowTriggerIdentityStore,
	_ pro_interfaces.CapabilityProvider,
) pro_interfaces.WorkflowTriggerService {
	return &workflowTriggerService{}
}

func NewWorkflowTriggerScheduler(db.WorkflowTriggerManager, pro_interfaces.WorkflowTriggerService) pro_interfaces.WorkflowTriggerScheduler {
	return nil
}

func (s *workflowTriggerService) List(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowTrigger, error) {
	return []db.WorkflowTrigger{}, nil
}
func (s *workflowTriggerService) Get(context.Context, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, db.ErrNotFound
}
func (s *workflowTriggerService) Create(context.Context, int, int, db.WorkflowTrigger, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) Update(context.Context, int, int, int, db.WorkflowTrigger, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, db.ErrNotFound
}
func (s *workflowTriggerService) Delete(context.Context, int, int, int, *db.User) error {
	return db.ErrNotFound
}
func (s *workflowTriggerService) SetEnabled(context.Context, int, int, int, int, bool, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, db.ErrNotFound
}
func (s *workflowTriggerService) RotateCredential(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) Test(context.Context, int, int, int, map[string]json.RawMessage, *db.User) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) FireExternal(context.Context, int, int, int, db.WorkflowTriggerType, string, string, map[string]json.RawMessage) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) FireSignedWebhook(context.Context, int, int, int, pro_interfaces.WebhookSignedRequest) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) FireScheduled(context.Context, int, int, int, time.Time) (pro_interfaces.WorkflowTriggerFireResult, error) {
	return pro_interfaces.WorkflowTriggerFireResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) History(context.Context, int, int, int, db.RetrieveQueryParams, *db.User) ([]pro_interfaces.WorkflowTriggerHistoryEntry, error) {
	return []pro_interfaces.WorkflowTriggerHistoryEntry{}, nil
}
func (s *workflowTriggerService) StageWebhookSigningKey(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) BootstrapWebhookSigningKey(context.Context, int, int, int, int, *db.User) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	return pro_interfaces.WorkflowTriggerCredentialResult{}, db.ErrNotFound
}
func (s *workflowTriggerService) PromoteWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, db.ErrNotFound
}
func (s *workflowTriggerService) RevokeWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error) {
	return db.WorkflowTrigger{}, db.ErrNotFound
}
