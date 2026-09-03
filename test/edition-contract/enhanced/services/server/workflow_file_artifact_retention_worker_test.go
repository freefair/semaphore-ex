package server

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowFileArtifactRetentionRepositoryStub struct {
	pro_interfaces.WorkflowFileArtifactRepository
	expiry        []db.WorkflowFileArtifactReference
	stale         []db.WorkflowFileArtifactReference
	expiredIDs    map[int]bool
	cleanupFailed *db.WorkflowFileArtifactReference
	cleanupErr    error
}

func (stub *workflowFileArtifactRetentionRepositoryStub) GetWorkflowFileArtifactExpiryCandidates(limit int) ([]db.WorkflowFileArtifactReference, error) {
	if limit != workflowFileArtifactRetentionBatch {
		return nil, db.ErrInvalidOperation
	}
	result := stub.expiry
	stub.expiry = nil
	return result, nil
}

func (stub *workflowFileArtifactRetentionRepositoryStub) ExpireWorkflowFileArtifact(reference db.WorkflowFileArtifactReference) (bool, error) {
	if reference.ArtifactID == 2 {
		return false, pro_interfaces.ErrWorkflowFileArtifactDownloadActive
	}
	if stub.expiredIDs[reference.ArtifactID] {
		return false, nil
	}
	stub.expiredIDs[reference.ArtifactID] = true
	return true, nil
}

func (stub *workflowFileArtifactRetentionRepositoryStub) ReconcileStaleWorkflowFileArtifactUploadsDetailed(olderThan time.Duration, limit int) (pro_interfaces.WorkflowFileArtifactCleanupResult, error) {
	if olderThan != workflowFileArtifactStagingMaxAge || limit != workflowFileArtifactRetentionBatch {
		return pro_interfaces.WorkflowFileArtifactCleanupResult{}, db.ErrInvalidOperation
	}
	result := stub.stale
	stub.stale = nil
	return pro_interfaces.WorkflowFileArtifactCleanupResult{Reconciled: result, Failed: stub.cleanupFailed}, stub.cleanupErr
}

func TestWorkflowFileArtifactRetentionWorkerAuditsConcreteCleanupFailure(t *testing.T) {
	failed := db.WorkflowFileArtifactReference{ProjectID: 7, WorkflowRunID: 9, ArtifactID: 4}
	repository := &workflowFileArtifactRetentionRepositoryStub{
		expiredIDs: map[int]bool{}, cleanupFailed: &failed, cleanupErr: assert.AnError,
	}
	audit := &workflowFileArtifactAuditStub{}
	worker := NewWorkflowFileArtifactRetentionWorker(repository, audit)
	require.ErrorIs(t, worker.RunOnce(context.Background()), assert.AnError)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionWorkflowFileArtifactCleanup, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[0].Outcome)
	assert.Equal(t, "artifact:4", audit.events[0].TargetID)
}

type workflowFileArtifactAuditStub struct {
	events []pro_interfaces.AuditEvent
}

func (stub *workflowFileArtifactAuditStub) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	stub.events = append(stub.events, event)
	return nil
}

func TestWorkflowFileArtifactRetentionWorkerIsBoundedIdempotentAndValueFree(t *testing.T) {
	repository := &workflowFileArtifactRetentionRepositoryStub{
		expiry: []db.WorkflowFileArtifactReference{
			{ProjectID: 7, WorkflowRunID: 9, ArtifactID: 1},
			{ProjectID: 7, WorkflowRunID: 9, ArtifactID: 2},
		},
		stale:      []db.WorkflowFileArtifactReference{{ProjectID: 8, WorkflowRunID: 10, ArtifactID: 3}},
		expiredIDs: map[int]bool{},
	}
	audit := &workflowFileArtifactAuditStub{}
	worker := NewWorkflowFileArtifactRetentionWorker(repository, audit)
	require.NotNil(t, worker)
	require.NoError(t, worker.RunOnce(context.Background()))
	require.Len(t, audit.events, 2)
	assert.Equal(t, pro_interfaces.AuditActionWorkflowFileArtifactExpire, audit.events[0].Action)
	assert.Equal(t, "artifact:1", audit.events[0].TargetID)
	assert.Equal(t, pro_interfaces.AuditActionWorkflowFileArtifactCleanup, audit.events[1].Action)
	assert.Equal(t, "artifact:3", audit.events[1].TargetID)
	for _, event := range audit.events {
		assert.Nil(t, event.ActorID)
		assert.Empty(t, event.RoleProvenance)
	}
	require.NoError(t, worker.RunOnce(context.Background()))
	assert.Len(t, audit.events, 2, "an idempotent retry must not emit duplicate lifecycle events")

	worker.Start()
	worker.Start()
	worker.Stop()
	worker.Stop()
}

func TestWorkflowFileArtifactRetentionWorkerExpiresRealSQLContentAfterTerminalRun(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "file-artifact-worker")
	require.NoError(t, err)
	task := fixture.enqueuer.tasks[0]
	repository := workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection())
	service := NewWorkflowFileArtifactService(repository, fixture.repository, fixture.store)
	content := []byte("retained")
	artifact, err := service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, db.WorkflowFileArtifactUpload{
		WorkflowNodeID: *task.WorkflowNodeID, TaskID: task.ID, Attempt: task.AssignmentGeneration,
		LogicalName: "retained", Filename: "retained.bin", MediaType: "application/octet-stream",
		SizeBytes: int64(len(content)), SHA256: artifactServiceSHA256(content),
		AccessPolicy: db.WorkflowFileArtifactAccessPolicy{Revision: 1},
	}, &fixture.user)
	require.NoError(t, err)
	artifact, err = service.AppendWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision, OffsetBytes: 0, Data: content,
	}, &fixture.user)
	require.NoError(t, err)
	artifact, err = service.FinalizeWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: artifact.ID, ExpectedRevision: artifact.Revision,
	}, &fixture.user)
	require.NoError(t, err)
	finalizedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := finalizedAt.Add(time.Duration(artifact.Retention.RetentionSeconds) * time.Second)
	_, err = fixture.store.GetConnection().Exec(
		"update workflow_file_artifact set created_at=?, finalized_at=?, expires_at=? where id=?",
		finalizedAt.Add(-time.Hour), finalizedAt, expiresAt, artifact.ID,
	)
	require.NoError(t, err)
	_, err = fixture.store.GetConnection().Exec("update project__workflow_run set status=? where id=?", db.WorkflowRunSucceeded, run.ID)
	require.NoError(t, err)
	audit := &workflowFileArtifactAuditStub{}
	worker := NewWorkflowFileArtifactRetentionWorker(repository, audit)
	require.NoError(t, worker.RunOnce(context.Background()))

	reloaded, err := repository.GetWorkflowFileArtifact(fixture.projectID, run.ID, artifact.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowFileArtifactExpired, reloaded.State)
	var chunks int
	require.NoError(t, fixture.store.GetConnection().SelectOne(&chunks,
		"select count(1) from workflow_file_artifact_chunk where artifact_id=?", artifact.ID))
	assert.Zero(t, chunks)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditReasonWorkflowFileArtifactExpired, audit.events[0].Reason)

	require.NoError(t, worker.RunOnce(context.Background()))
	assert.Len(t, audit.events, 1)
}
