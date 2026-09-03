package pro_interfaces

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// WorkflowFileArtifactDownload is the value-free hand-off between the
// authorization facade and the streaming controller. Deadline is absolute;
// neither retries nor a slow client may extend it.
type WorkflowFileArtifactDownload struct {
	Metadata db.WorkflowFileArtifactMetadata
	Lease    db.WorkflowFileArtifactDownloadLease
	Deadline time.Time
	ActorID  int
}

func (download WorkflowFileArtifactDownload) Validate() error {
	if download.Metadata.Validate() != nil || download.Metadata.State != db.WorkflowFileArtifactAvailable ||
		download.Lease.Validate() != nil || download.Lease.ArtifactID != download.Metadata.ID ||
		download.Deadline.IsZero() || !download.Deadline.Equal(download.Lease.CreatedAt.Add(db.MaxWorkflowFileArtifactDownloadLease)) ||
		download.ActorID < 1 {
		return db.ErrInvalidOperation
	}
	return nil
}

// WorkflowFileArtifactServiceFacade keeps binary-artifact orchestration out
// of the historical WorkflowService interface so Community and upstream
// workflow implementations retain their existing contract.
type WorkflowFileArtifactServiceFacade interface {
	BeginWorkflowFileArtifact(context.Context, int, int, db.WorkflowFileArtifactUpload, *db.User) (db.WorkflowFileArtifactMetadata, error)
	AppendWorkflowFileArtifact(context.Context, WorkflowFileArtifactAppendRequest, *db.User) (db.WorkflowFileArtifactMetadata, error)
	FinalizeWorkflowFileArtifact(context.Context, WorkflowFileArtifactMutationRequest, *db.User) (db.WorkflowFileArtifactMetadata, error)
	GetWorkflowFileArtifact(context.Context, int, int, int, *db.User) (db.WorkflowFileArtifactMetadata, error)
	GetWorkflowFileArtifacts(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowFileArtifactMetadata, error)
	AcquireWorkflowFileArtifactDownload(context.Context, int, int, int, *db.User) (WorkflowFileArtifactDownload, error)
	StreamWorkflowFileArtifactDownload(context.Context, WorkflowFileArtifactDownload, io.Writer) (int64, error)
	ReleaseWorkflowFileArtifactDownload(WorkflowFileArtifactDownload) error
}

type WorkflowFileArtifactController interface {
	BeginWorkflowFileArtifact(http.ResponseWriter, *http.Request)
	AppendWorkflowFileArtifact(http.ResponseWriter, *http.Request)
	FinalizeWorkflowFileArtifact(http.ResponseWriter, *http.Request)
	GetWorkflowFileArtifacts(http.ResponseWriter, *http.Request)
	GetWorkflowFileArtifact(http.ResponseWriter, *http.Request)
	DownloadWorkflowFileArtifact(http.ResponseWriter, *http.Request)
}

type WorkflowFileArtifactRetentionWorker interface {
	Start()
	Stop()
	RunOnce(context.Context) error
}

type WorkflowFileArtifactAuditConfigurer interface {
	ConfigureWorkflowFileArtifactAudit(AuditServiceFacade)
}

// WorkflowFileArtifactIdentityStore supplies live authorization, producer,
// credential-provenance, and delegated global-governance state.
type WorkflowFileArtifactIdentityStore interface {
	WorkflowAuthorizationIdentityStore
	GetTask(projectID int, taskID int) (db.Task, error)
	GetTaskGlobalCredentialUsage(projectID int, taskID int, query db.GlobalCredentialUsageQuery) ([]db.GlobalCredentialUsage, error)
	GetEffectiveGlobalPermissions(userID int) (db.GlobalPermission, error)
}

// AuthorizeWorkflowFileArtifactAccess applies only the immutable artifact
// narrowing after the caller has already established live workflow/run view.
func AuthorizeWorkflowFileArtifactAccess(policy db.WorkflowFileArtifactAccessPolicy, identity db.ProjectWorkflowRoleIdentity, knownRoles map[db.ProjectRoleReference]bool) bool {
	if policy.Validate() != nil || identity.Validate() != nil || !knownRoles[identity.Reference] {
		return false
	}
	if len(policy.RoleIDs) == 0 {
		return true
	}
	for _, roleID := range policy.RoleIDs {
		if knownRoles[roleID] && roleID == identity.Reference {
			return true
		}
	}
	return false
}
