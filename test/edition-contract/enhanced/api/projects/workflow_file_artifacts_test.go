package projects

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowFileArtifactServiceStub struct {
	pro_interfaces.WorkflowFileArtifactServiceFacade
	artifact    db.WorkflowFileArtifactMetadata
	download    pro_interfaces.WorkflowFileArtifactDownload
	content     []byte
	err         error
	acquired    int
	streamed    int
	released    int
	beginCalls  int
	appendCalls int
}

func (stub *workflowFileArtifactServiceStub) BeginWorkflowFileArtifact(context.Context, int, int, db.WorkflowFileArtifactUpload, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	stub.beginCalls++
	return stub.artifact, stub.err
}

func (stub *workflowFileArtifactServiceStub) AppendWorkflowFileArtifact(context.Context, pro_interfaces.WorkflowFileArtifactAppendRequest, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	stub.appendCalls++
	return stub.artifact, stub.err
}

func (stub *workflowFileArtifactServiceStub) FinalizeWorkflowFileArtifact(context.Context, pro_interfaces.WorkflowFileArtifactMutationRequest, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return stub.artifact, stub.err
}

func (stub *workflowFileArtifactServiceStub) GetWorkflowFileArtifact(context.Context, int, int, int, *db.User) (db.WorkflowFileArtifactMetadata, error) {
	return stub.artifact, stub.err
}

func (stub *workflowFileArtifactServiceStub) GetWorkflowFileArtifacts(context.Context, int, int, db.RetrieveQueryParams, *db.User) ([]db.WorkflowFileArtifactMetadata, error) {
	return []db.WorkflowFileArtifactMetadata{stub.artifact}, stub.err
}

func (stub *workflowFileArtifactServiceStub) AcquireWorkflowFileArtifactDownload(context.Context, int, int, int, *db.User) (pro_interfaces.WorkflowFileArtifactDownload, error) {
	stub.acquired++
	return stub.download, stub.err
}

func (stub *workflowFileArtifactServiceStub) StreamWorkflowFileArtifactDownload(_ context.Context, _ pro_interfaces.WorkflowFileArtifactDownload, writer io.Writer) (int64, error) {
	stub.streamed++
	written, err := writer.Write(stub.content)
	return int64(written), err
}

func (stub *workflowFileArtifactServiceStub) ReleaseWorkflowFileArtifactDownload(pro_interfaces.WorkflowFileArtifactDownload) error {
	stub.released++
	return nil
}

type workflowFileArtifactDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (recorder *workflowFileArtifactDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	recorder.deadline = deadline
	return recorder.err
}

func TestWorkflowFileArtifactControllerDownloadsWithSafeHeadersAndDeadline(t *testing.T) {
	content := []byte("safe-content")
	metadata, lease, deadline := workflowFileArtifactHTTPFixture(t, content)
	service := &workflowFileArtifactServiceStub{
		artifact: metadata, content: content,
		download: pro_interfaces.WorkflowFileArtifactDownload{Metadata: metadata, Lease: lease, Deadline: deadline, ActorID: 5},
	}
	controller := NewWorkflowFileArtifactController(service)
	recorder := &workflowFileArtifactDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	controller.DownloadWorkflowFileArtifact(recorder, workflowFileArtifactRequest(http.MethodGet, "/content", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, content, recorder.Body.Bytes())
	assert.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "none", recorder.Header().Get("Accept-Ranges"))
	assert.Equal(t, metadata.SHA256, recorder.Header().Get("X-Checksum-SHA256"))
	assert.Contains(t, recorder.Header().Get("Content-Disposition"), "release.bin")
	assert.Equal(t, deadline, recorder.deadline)
	assert.Equal(t, 1, service.acquired)
	assert.Equal(t, 1, service.streamed)
	assert.Equal(t, 1, service.released)
}

func TestWorkflowFileArtifactControllerRejectsRangesAndOversizedChunks(t *testing.T) {
	content := []byte("safe-content")
	metadata, lease, deadline := workflowFileArtifactHTTPFixture(t, content)
	service := &workflowFileArtifactServiceStub{
		artifact: metadata, content: content,
		download: pro_interfaces.WorkflowFileArtifactDownload{Metadata: metadata, Lease: lease, Deadline: deadline, ActorID: 5},
	}
	controller := NewWorkflowFileArtifactController(service)
	rangeRequest := workflowFileArtifactRequest(http.MethodGet, "/content", nil)
	rangeRequest.Header.Set("Range", "bytes=0-1")
	rangeRecorder := httptest.NewRecorder()
	controller.DownloadWorkflowFileArtifact(rangeRecorder, rangeRequest)
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, rangeRecorder.Code)
	assert.Equal(t, "bytes */12", rangeRecorder.Header().Get("Content-Range"))
	assert.Zero(t, service.acquired)

	oversized := strings.NewReader(strings.Repeat("x", db.MaxWorkflowFileArtifactChunkBytes+1))
	appendRecorder := httptest.NewRecorder()
	controller.AppendWorkflowFileArtifact(appendRecorder, workflowFileArtifactRequest(http.MethodPut, "/content?revision=1&offset=0", oversized))
	assert.Equal(t, http.StatusRequestEntityTooLarge, appendRecorder.Code)
	assert.Zero(t, service.appendCalls)
}

func TestWorkflowFileArtifactControllerReleasesLeaseWhenDeadlineCannotBeSet(t *testing.T) {
	content := []byte("safe-content")
	metadata, lease, deadline := workflowFileArtifactHTTPFixture(t, content)
	service := &workflowFileArtifactServiceStub{
		artifact: metadata, content: content,
		download: pro_interfaces.WorkflowFileArtifactDownload{Metadata: metadata, Lease: lease, Deadline: deadline, ActorID: 5},
	}
	controller := NewWorkflowFileArtifactController(service)
	recorder := &workflowFileArtifactDeadlineRecorder{ResponseRecorder: httptest.NewRecorder(), err: assert.AnError}
	controller.DownloadWorkflowFileArtifact(recorder, workflowFileArtifactRequest(http.MethodGet, "/content", nil))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Zero(t, service.streamed)
	assert.Equal(t, 1, service.released)
}

func TestWorkflowFileArtifactControllerFailsClosedWithoutAuthenticatedContext(t *testing.T) {
	controller := NewWorkflowFileArtifactController(&workflowFileArtifactServiceStub{})
	request := httptest.NewRequest(http.MethodGet, "/content", nil)
	request = mux.SetURLVars(request, map[string]string{"artifact_id": "101"})
	recorder := httptest.NewRecorder()
	assert.NotPanics(t, func() { controller.DownloadWorkflowFileArtifact(recorder, request) })
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func workflowFileArtifactRequest(method string, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request = mux.SetURLVars(request, map[string]string{
		"project_id": "7", "workflow_id": "41", "run_id": "91", "artifact_id": "101",
	})
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	request = helpers.SetContextValue(request, "workflow_run", db.WorkflowRun{ID: 91, ProjectID: 7, WorkflowTemplateID: 41})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 5})
	return request
}

func workflowFileArtifactHTTPFixture(t *testing.T, content []byte) (db.WorkflowFileArtifactMetadata, db.WorkflowFileArtifactDownloadLease, time.Time) {
	t.Helper()
	created := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	finalized := created.Add(time.Minute)
	expires := finalized.Add(30 * 24 * time.Hour)
	metadata := db.WorkflowFileArtifactMetadata{
		ID: 101, ProjectID: 7, WorkflowTemplateID: 41, WorkflowRunID: 91,
		WorkflowNodeID: 3, WorkflowDefinitionRevision: 1, TaskID: 51, Attempt: 0,
		LogicalName: "release", Filename: "release.bin", MediaType: "application/octet-stream",
		SizeBytes: int64(len(content)), UploadedBytes: int64(len(content)),
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		State:  db.WorkflowFileArtifactAvailable, Revision: 3,
		ProducerUserID: 5, ProducerTemplateID: 61, ProducerVersion: "semaphore:test",
		AccessPolicy: db.WorkflowFileArtifactAccessPolicy{Revision: 1},
		Retention:    db.DefaultWorkflowArtifactRetentionSnapshot(),
		CreatedAt:    created, FinalizedAt: &finalized, ExpiresAt: &expires,
	}
	require.NoError(t, metadata.CanonicalizeForPersistence())
	lease := db.WorkflowFileArtifactDownloadLease{
		LeaseToken: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactID: metadata.ID, CreatedAt: created, ExpiresAt: created.Add(time.Minute),
	}
	return metadata, lease, created.Add(db.MaxWorkflowFileArtifactDownloadLease)
}
