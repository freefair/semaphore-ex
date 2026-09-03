package pro_interfaces

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

func TestWorkflowFileArtifactRequestsValidateBounds(t *testing.T) {
	appendRequest := WorkflowFileArtifactAppendRequest{
		ProjectID: 1, WorkflowRunID: 2, ArtifactID: 3, ExpectedRevision: 1,
		OffsetBytes: 0, Data: []byte("artifact"),
	}
	if err := appendRequest.Validate(); err != nil {
		t.Fatalf("valid append request rejected: %v", err)
	}
	appendRequest.Data = make([]byte, db.MaxWorkflowFileArtifactChunkBytes+1)
	if err := appendRequest.Validate(); err == nil {
		t.Fatal("oversized append request accepted")
	}

	leaseRequest := WorkflowFileArtifactLeaseRequest{
		ProjectID: 1, WorkflowRunID: 2, ArtifactID: 3,
		TTL: db.MinWorkflowFileArtifactDownloadLease,
	}
	if err := leaseRequest.Validate(); err != nil {
		t.Fatalf("valid lease request rejected: %v", err)
	}
	leaseRequest.TTL = db.MaxWorkflowFileArtifactDownloadLease + time.Second
	if err := leaseRequest.Validate(); err == nil {
		t.Fatal("oversized lease accepted")
	}
}
