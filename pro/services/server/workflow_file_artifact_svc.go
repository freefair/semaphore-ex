package server

import (
	"context"
	"io"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type workflowFileArtifactService struct{}

var _ pro_interfaces.WorkflowFileArtifactServiceFacade = (*workflowFileArtifactService)(nil)

func NewWorkflowFileArtifactService(
	pro_interfaces.WorkflowFileArtifactRepository,
	db.WorkflowManager,
	pro_interfaces.WorkflowFileArtifactIdentityStore,
) pro_interfaces.WorkflowFileArtifactServiceFacade {
	return &workflowFileArtifactService{}
}

func (*workflowFileArtifactService) BeginWorkflowFileArtifact(context.Context, int, int, db.WorkflowFileArtifactUpload, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return db.WorkflowFileArtifactMetadata{}, db.ErrNotFound
}

func (*workflowFileArtifactService) AppendWorkflowFileArtifact(context.Context, pro_interfaces.WorkflowFileArtifactAppendRequest, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return db.WorkflowFileArtifactMetadata{}, db.ErrNotFound
}

func (*workflowFileArtifactService) FinalizeWorkflowFileArtifact(context.Context, pro_interfaces.WorkflowFileArtifactMutationRequest, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return db.WorkflowFileArtifactMetadata{}, db.ErrNotFound
}

func (*workflowFileArtifactService) GetWorkflowFileArtifact(context.Context, int, int, int, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return db.WorkflowFileArtifactMetadata{}, db.ErrNotFound
}

func (*workflowFileArtifactService) GetWorkflowFileArtifacts(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowFileArtifactMetadata, error) {
	return []db.WorkflowFileArtifactMetadata{}, nil
}

func (*workflowFileArtifactService) AcquireWorkflowFileArtifactDownload(context.Context, int, int, int, *db.User) (pro_interfaces.WorkflowFileArtifactDownload, error) {
	return pro_interfaces.WorkflowFileArtifactDownload{}, db.ErrNotFound
}

func (*workflowFileArtifactService) StreamWorkflowFileArtifactDownload(context.Context, pro_interfaces.WorkflowFileArtifactDownload, io.Writer) (int64, error) {
	return 0, db.ErrNotFound
}

func (*workflowFileArtifactService) RecordWorkflowFileArtifactDownloadFailure(pro_interfaces.WorkflowFileArtifactDownload) error {
	return db.ErrNotFound
}

func (*workflowFileArtifactService) ReleaseWorkflowFileArtifactDownload(pro_interfaces.WorkflowFileArtifactDownload) error {
	return db.ErrNotFound
}
