package pro_interfaces

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

var (
	ErrWorkflowFileArtifactConflict       = errors.New("workflow file artifact revision conflict")
	ErrWorkflowFileArtifactQuotaExceeded  = errors.New("workflow file artifact quota exceeded")
	ErrWorkflowFileArtifactIncomplete     = errors.New("workflow file artifact content is incomplete")
	ErrWorkflowFileArtifactChecksum       = errors.New("workflow file artifact checksum does not match")
	ErrWorkflowFileArtifactNotAvailable   = errors.New("workflow file artifact is not available")
	ErrWorkflowFileArtifactDownloadActive = errors.New("workflow file artifact download is active")
	ErrWorkflowArtifactRetentionConflict  = errors.New("workflow artifact retention policy conflict")
)

type WorkflowFileArtifactAppendRequest struct {
	ProjectID        int
	WorkflowRunID    int
	ArtifactID       int
	ExpectedRevision int
	OffsetBytes      int64
	Data             []byte
}

func (request WorkflowFileArtifactAppendRequest) Validate() error {
	if request.ProjectID < 1 || request.WorkflowRunID < 1 || request.ArtifactID < 1 ||
		request.ExpectedRevision < 1 || request.OffsetBytes < 0 || len(request.Data) < 1 ||
		len(request.Data) > db.MaxWorkflowFileArtifactChunkBytes {
		return db.ErrInvalidOperation
	}
	return nil
}

type WorkflowFileArtifactMutationRequest struct {
	ProjectID        int
	WorkflowRunID    int
	ArtifactID       int
	ExpectedRevision int
}

func (request WorkflowFileArtifactMutationRequest) Validate() error {
	if request.ProjectID < 1 || request.WorkflowRunID < 1 || request.ArtifactID < 1 || request.ExpectedRevision < 1 {
		return db.ErrInvalidOperation
	}
	return nil
}

type WorkflowFileArtifactLeaseRequest struct {
	ProjectID     int
	WorkflowRunID int
	ArtifactID    int
	TTL           time.Duration
}

type WorkflowFileArtifactCleanupResult struct {
	Reconciled []db.WorkflowFileArtifactReference
	Failed     *db.WorkflowFileArtifactReference
}

func (request WorkflowFileArtifactLeaseRequest) Validate() error {
	if request.ProjectID < 1 || request.WorkflowRunID < 1 || request.ArtifactID < 1 ||
		request.TTL < db.MinWorkflowFileArtifactDownloadLease || request.TTL > db.MaxWorkflowFileArtifactDownloadLease {
		return db.ErrInvalidOperation
	}
	return nil
}

// WorkflowFileArtifactRepository is the Enhanced persistence boundary. It
// owns quota serialization, exact-offset chunk appends, checksum finalization,
// download leases, and terminal-run retention transitions. Authorization and
// transport behavior remain service/controller responsibilities.
type WorkflowFileArtifactRepository interface {
	GetWorkflowArtifactRetentionPolicy(db.WorkflowArtifactRetentionScope, *int) (db.WorkflowArtifactRetentionPolicy, bool, error)
	PublishWorkflowArtifactRetentionPolicy(db.WorkflowArtifactRetentionPolicy, int) (db.WorkflowArtifactRetentionPolicy, error)
	CreateWorkflowFileArtifact(db.WorkflowFileArtifactMetadata) (db.WorkflowFileArtifactMetadata, error)
	AppendWorkflowFileArtifactChunk(WorkflowFileArtifactAppendRequest) (db.WorkflowFileArtifactMetadata, error)
	GetWorkflowFileArtifact(projectID int, workflowRunID int, artifactID int) (db.WorkflowFileArtifactMetadata, error)
	GetWorkflowFileArtifacts(projectID int, workflowRunID int, params db.RetrieveQueryParams) ([]db.WorkflowFileArtifactMetadata, error)
	FinalizeWorkflowFileArtifact(WorkflowFileArtifactMutationRequest) (db.WorkflowFileArtifactMetadata, error)
	FailWorkflowFileArtifact(WorkflowFileArtifactMutationRequest) (db.WorkflowFileArtifactMetadata, error)
	AcquireWorkflowFileArtifactDownloadLease(WorkflowFileArtifactLeaseRequest) (db.WorkflowFileArtifactDownloadLease, error)
	StreamWorkflowFileArtifactContent(context.Context, db.WorkflowFileArtifactDownloadLease, io.Writer) (int64, error)
	ReleaseWorkflowFileArtifactDownloadLease(db.WorkflowFileArtifactDownloadLease) error
	GetWorkflowFileArtifactExpiryCandidates(limit int) ([]db.WorkflowFileArtifactReference, error)
	ExpireWorkflowFileArtifact(db.WorkflowFileArtifactReference) (bool, error)
	ReconcileStaleWorkflowFileArtifactUploads(olderThan time.Duration, limit int) (int, error)
	ReconcileStaleWorkflowFileArtifactUploadsDetailed(olderThan time.Duration, limit int) (WorkflowFileArtifactCleanupResult, error)
}
