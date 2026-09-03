package pro_interfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

var ErrWorkflowTriggerCredentialRejected = errors.New("workflow trigger credential rejected")
var ErrWorkflowTriggerTypeMismatch = errors.New("workflow trigger type mismatch")
var ErrWorkflowTriggerPermissionDenied = errors.New("workflow trigger permission denied")
var ErrWorkflowTriggerWebhookRejected = errors.New("workflow trigger webhook rejected")
var ErrWorkflowTriggerWebhookReplay = errors.New("workflow trigger webhook replay")
var ErrWorkflowTriggerSigningUnavailable = errors.New("workflow trigger signing unavailable")
var ErrWorkflowTriggerSigningStateConflict = errors.New("workflow trigger signing state conflict")

type WorkflowTriggerCredentialResult struct {
	Trigger              db.WorkflowTrigger `json:"trigger"`
	Credential           string             `json:"credential,omitempty"`
	WebhookSigningSecret string             `json:"webhook_signing_secret,omitempty"`
}

// WorkflowTriggerHistoryEntry is deliberately narrower than the persistence
// record. In particular, request inputs, event hashes, snapshots and internal
// failure details never cross the API boundary.
type WorkflowTriggerHistoryEntry struct {
	ID                    int                                `json:"id"`
	TriggerID             int                                `json:"workflow_trigger_id"`
	Status                db.WorkflowTriggerInvocationStatus `json:"status"`
	RunID                 *int                               `json:"run_id,omitempty"`
	Result                string                             `json:"result,omitempty"`
	WebhookEventID        string                             `json:"webhook_event_id,omitempty"`
	WebhookKeyID          string                             `json:"webhook_key_id,omitempty"`
	WebhookSignedAt       *time.Time                         `json:"webhook_signed_at,omitempty"`
	WebhookReplayCount    int                                `json:"webhook_replay_count,omitempty"`
	WebhookLastReplayedAt *time.Time                         `json:"webhook_last_replayed_at,omitempty"`
	Created               time.Time                          `json:"created"`
	Updated               time.Time                          `json:"updated"`
}

type WorkflowTriggerFireResult struct {
	Trigger    db.WorkflowTrigger           `json:"trigger"`
	Invocation db.WorkflowTriggerInvocation `json:"invocation"`
	Run        db.WorkflowRun               `json:"run"`
	Duplicate  bool                         `json:"duplicate"`
}

type WorkflowTriggerWorkflowStore interface {
	GetWorkflowTemplate(projectID int, workflowID int) (db.WorkflowTemplate, error)
	GetWorkflowRunByID(projectID int, runID int) (db.WorkflowRun, error)
}

type WorkflowTriggerIdentityStore interface {
	GetUser(userID int) (db.User, error)
	GetProjectUser(projectID int, userID int) (db.ProjectUser, error)
	GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error)
}

type WorkflowTriggerService interface {
	List(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowTrigger, error)
	Get(context.Context, int, int, int, *db.User) (db.WorkflowTrigger, error)
	Create(context.Context, int, int, db.WorkflowTrigger, *db.User) (WorkflowTriggerCredentialResult, error)
	Update(context.Context, int, int, int, db.WorkflowTrigger, *db.User) (db.WorkflowTrigger, error)
	Delete(context.Context, int, int, int, *db.User) error
	SetEnabled(context.Context, int, int, int, int, bool, *db.User) (db.WorkflowTrigger, error)
	RotateCredential(context.Context, int, int, int, int, *db.User) (WorkflowTriggerCredentialResult, error)
	Test(context.Context, int, int, int, map[string]json.RawMessage, *db.User) (WorkflowTriggerFireResult, error)
	FireExternal(context.Context, int, int, int, db.WorkflowTriggerType, string, string, map[string]json.RawMessage) (WorkflowTriggerFireResult, error)
	FireSignedWebhook(context.Context, int, int, int, WebhookSignedRequest) (WorkflowTriggerFireResult, error)
	FireScheduled(context.Context, int, int, int, time.Time) (WorkflowTriggerFireResult, error)
	History(context.Context, int, int, int, db.RetrieveQueryParams, *db.User) ([]WorkflowTriggerHistoryEntry, error)
	StageWebhookSigningKey(context.Context, int, int, int, int, *db.User) (WorkflowTriggerCredentialResult, error)
	BootstrapWebhookSigningKey(context.Context, int, int, int, int, *db.User) (WorkflowTriggerCredentialResult, error)
	PromoteWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error)
	RevokeWebhookSigningKey(context.Context, int, int, int, int, *db.User) (db.WorkflowTrigger, error)
}

type WorkflowTriggerScheduler interface {
	Start()
	Stop()
	RunOnce(context.Context, time.Time)
}

type WorkflowTriggerController interface {
	GetTriggers(http.ResponseWriter, *http.Request)
	AddTrigger(http.ResponseWriter, *http.Request)
	GetTrigger(http.ResponseWriter, *http.Request)
	UpdateTrigger(http.ResponseWriter, *http.Request)
	DeleteTrigger(http.ResponseWriter, *http.Request)
	SetTriggerEnabled(http.ResponseWriter, *http.Request)
	RotateTriggerCredential(http.ResponseWriter, *http.Request)
	StageWebhookSigningKey(http.ResponseWriter, *http.Request)
	BootstrapWebhookSigningKey(http.ResponseWriter, *http.Request)
	PromoteWebhookSigningKey(http.ResponseWriter, *http.Request)
	RevokeWebhookSigningKey(http.ResponseWriter, *http.Request)
	TestTrigger(http.ResponseWriter, *http.Request)
	GetTriggerHistory(http.ResponseWriter, *http.Request)
	InvokeAPITrigger(http.ResponseWriter, *http.Request)
	InvokeWebhookTrigger(http.ResponseWriter, *http.Request)
}
