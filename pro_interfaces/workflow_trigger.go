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

type WorkflowTriggerCredentialResult struct {
	Trigger    db.WorkflowTrigger `json:"trigger"`
	Credential string             `json:"credential,omitempty"`
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
	FireScheduled(context.Context, int, int, int, time.Time) (WorkflowTriggerFireResult, error)
	History(context.Context, int, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowTriggerInvocation, error)
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
	TestTrigger(http.ResponseWriter, *http.Request)
	GetTriggerHistory(http.ResponseWriter, *http.Request)
	InvokeAPITrigger(http.ResponseWriter, *http.Request)
	InvokeWebhookTrigger(http.ResponseWriter, *http.Request)
}
