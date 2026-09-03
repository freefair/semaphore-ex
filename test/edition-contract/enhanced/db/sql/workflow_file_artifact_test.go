package sql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowdb "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowFileArtifactStoreStagesFinalizesAndStreamsContent(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	content := []byte("artifact-content")
	artifact := fixture.metadata("release", content)
	created, err := repository.CreateWorkflowFileArtifact(artifact)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactStaging, created.State)
	assert.Equal(t, 1, created.Revision)
	assert.Zero(t, created.UploadedBytes)

	listed, err := repository.GetWorkflowFileArtifacts(fixture.projectID, fixture.runID, coredb.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	assert.Empty(t, listed, "staging content must not be visible")

	first := content[:8]
	created, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: 0, Data: first,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(len(first)), created.UploadedBytes)
	staleRevision := created.Revision - 1

	_, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: staleRevision, OffsetBytes: int64(len(first)), Data: content[len(first):],
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactConflict)
	_, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: int64(len(first) + 1), Data: content[len(first):],
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactConflict)

	created, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: int64(len(first)), Data: content[len(first):],
	})
	require.NoError(t, err)
	created, err = repository.FinalizeWorkflowFileArtifact(pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision,
	})
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactAvailable, created.State)
	require.NotNil(t, created.FinalizedAt)
	require.NotNil(t, created.ExpiresAt)
	assert.Equal(t, time.Duration(created.Retention.RetentionSeconds)*time.Second, created.ExpiresAt.Sub(*created.FinalizedAt))

	listed, err = repository.GetWorkflowFileArtifacts(fixture.projectID, fixture.runID, coredb.RetrieveQueryParams{Count: 10})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ID)
	_, err = repository.GetWorkflowFileArtifact(fixture.projectID+1, fixture.runID, created.ID)
	assert.ErrorIs(t, err, coredb.ErrNotFound)

	lease, err := repository.AcquireWorkflowFileArtifactDownloadLease(pro_interfaces.WorkflowFileArtifactLeaseRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		TTL: time.Minute,
	})
	require.NoError(t, err)
	var downloaded bytes.Buffer
	written, err := repository.StreamWorkflowFileArtifactContent(context.Background(), lease, &downloaded)
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), written)
	assert.Equal(t, content, downloaded.Bytes())
	var renewed struct {
		ExpiresAt time.Time `db:"expires_at"`
	}
	require.NoError(t, store.GetConnection().SelectOne(&renewed,
		"select expires_at from workflow_file_artifact_download_lease where lease_token=?", lease.LeaseToken))
	assert.True(t, renewed.ExpiresAt.After(lease.ExpiresAt), "active stream must renew its GC fence")
	assert.True(t, renewed.ExpiresAt.Equal(lease.CreatedAt.Add(coredb.MaxWorkflowFileArtifactDownloadLease)), "renewal must keep an absolute upper bound")
	secondDeadline, err := repository.renewWorkflowFileArtifactDownloadLease(lease)
	require.NoError(t, err)
	assert.True(t, secondDeadline.Equal(renewed.ExpiresAt), "repeated renewal must not extend the absolute deadline")
	require.NoError(t, repository.ReleaseWorkflowFileArtifactDownloadLease(lease))
}

func TestWorkflowFileArtifactStoreEnforcesChecksumAndRunQuota(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	tamperedRetention := fixture.metadata("tampered-retention", []byte("abc"))
	tamperedRetention.Retention.MaxRunBytes--
	_, err := repository.CreateWorkflowFileArtifact(tamperedRetention)
	assert.ErrorIs(t, err, coredb.ErrInvalidOperation)

	bad := fixture.metadata("bad-checksum", []byte("abc"))
	bad.SHA256 = artifactSHA256([]byte("different"))
	created, err := repository.CreateWorkflowFileArtifact(bad)
	require.NoError(t, err)
	created, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: 0, Data: []byte("abc"),
	})
	require.NoError(t, err)
	_, err = repository.FinalizeWorkflowFileArtifact(pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision,
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactChecksum)
	reloaded, err := repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactStaging, reloaded.State)

	_, err = store.GetConnection().Exec(
		"update workflow_file_artifact_run_usage set reserved_bytes=? where workflow_run_id=?",
		coredb.MaxWorkflowFileArtifactRunBytes-4, fixture.runID,
	)
	require.NoError(t, err)
	first := fixture.metadata("quota-first", []byte("1234"))
	_, err = repository.CreateWorkflowFileArtifact(first)
	require.NoError(t, err)
	second := fixture.metadata("quota-second", []byte("1"))
	_, err = repository.CreateWorkflowFileArtifact(second)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactQuotaExceeded)
}

func TestWorkflowFileArtifactStoreFencesExpiryWithLeaseAndTerminalRun(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	artifact := finalizeWorkflowFileArtifact(t, repository, fixture, "expiry", []byte("expire-me"))
	lease, err := repository.AcquireWorkflowFileArtifactDownloadLease(pro_interfaces.WorkflowFileArtifactLeaseRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		TTL: time.Minute,
	})
	require.NoError(t, err)
	finalizedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := finalizedAt.Add(time.Duration(artifact.Retention.RetentionSeconds) * time.Second)
	createdAt := finalizedAt.Add(-time.Hour)
	_, err = store.GetConnection().Exec("update workflow_file_artifact set created_at=?, finalized_at=?, expires_at=? where id=?", createdAt, finalizedAt, expiresAt, artifact.ID)
	require.NoError(t, err)
	reference := coredb.WorkflowFileArtifactReference{ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID}
	expired, err := repository.ExpireWorkflowFileArtifact(reference)
	require.NoError(t, err)
	assert.False(t, expired, "a running workflow cannot lose artifact content")
	_, err = store.GetConnection().Exec("update project__workflow_run set status=? where id=?", coredb.WorkflowRunSucceeded, fixture.runID)
	require.NoError(t, err)
	expired, err = repository.ExpireWorkflowFileArtifact(reference)
	assert.False(t, expired)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactDownloadActive)

	require.NoError(t, repository.ReleaseWorkflowFileArtifactDownloadLease(lease))
	expired, err = repository.ExpireWorkflowFileArtifact(reference)
	require.NoError(t, err)
	assert.True(t, expired)
	reloaded, err := repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, artifact.ID)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactExpired, reloaded.State)
	var chunks int
	require.NoError(t, store.GetConnection().SelectOne(&chunks, "select count(1) from workflow_file_artifact_chunk where artifact_id=?", artifact.ID))
	assert.Zero(t, chunks)
}

func TestWorkflowFileArtifactStoreReconcilesOnlyStaleStagingUploads(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	stale, err := repository.CreateWorkflowFileArtifact(fixture.metadata("stale", []byte("stale")))
	require.NoError(t, err)
	stale, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: stale.ID,
		ExpectedRevision: stale.Revision, OffsetBytes: 0, Data: []byte("stale"),
	})
	require.NoError(t, err)
	current, err := repository.CreateWorkflowFileArtifact(fixture.metadata("current", []byte("current")))
	require.NoError(t, err)
	_, err = store.GetConnection().Exec("update workflow_file_artifact set created_at=? where id=?", time.Now().UTC().Add(-48*time.Hour), stale.ID)
	require.NoError(t, err)

	count, err := repository.ReconcileStaleWorkflowFileArtifactUploads(24*time.Hour, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	stale, err = repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, stale.ID)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactFailed, stale.State)
	current, err = repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, current.ID)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactStaging, current.State)
}

func TestWorkflowFileArtifactStoreSerializesConcurrentAppendsAndReservations(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	artifact, err := repository.CreateWorkflowFileArtifact(fixture.metadata("append-race", []byte("abcdef")))
	require.NoError(t, err)
	start := make(chan struct{})
	appendErrors := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, appendErr := repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
				ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
				ExpectedRevision: artifact.Revision, OffsetBytes: 0, Data: []byte("abc"),
			})
			appendErrors <- appendErr
		}()
	}
	close(start)
	wait.Wait()
	close(appendErrors)
	appendSuccesses, appendConflicts := 0, 0
	for appendErr := range appendErrors {
		switch {
		case appendErr == nil:
			appendSuccesses++
		case errors.Is(appendErr, pro_interfaces.ErrWorkflowFileArtifactConflict):
			appendConflicts++
		default:
			t.Fatalf("unexpected concurrent append error: %v", appendErr)
		}
	}
	assert.Equal(t, 1, appendSuccesses)
	assert.Equal(t, 1, appendConflicts)

	_, err = store.GetConnection().Exec(
		"update workflow_file_artifact_run_usage set reserved_bytes=? where workflow_run_id=?",
		coredb.MaxWorkflowFileArtifactRunBytes-3, fixture.runID,
	)
	require.NoError(t, err)
	start = make(chan struct{})
	createErrors := make(chan error, 2)
	wait = sync.WaitGroup{}
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, createErr := repository.CreateWorkflowFileArtifact(fixture.metadata(fmt.Sprintf("quota-race-%d", index), []byte("123")))
			createErrors <- createErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(createErrors)
	createSuccesses, quotaFailures := 0, 0
	for createErr := range createErrors {
		switch {
		case createErr == nil:
			createSuccesses++
		case errors.Is(createErr, pro_interfaces.ErrWorkflowFileArtifactQuotaExceeded):
			quotaFailures++
		default:
			t.Fatalf("unexpected concurrent reservation error: %v", createErr)
		}
	}
	assert.Equal(t, 1, createSuccesses)
	assert.Equal(t, 1, quotaFailures)
}

func TestWorkflowFileArtifactStoreFencesSupersededAttemptInsideMutation(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)
	artifact, err := repository.CreateWorkflowFileArtifact(fixture.metadata("attempt-fence", []byte("x")))
	require.NoError(t, err)
	_, err = store.GetConnection().Exec("update task set assignment_generation=assignment_generation+1 where id=?", fixture.taskID)
	require.NoError(t, err)
	_, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision, OffsetBytes: 0, Data: []byte("x"),
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactConflict)
}

func TestWorkflowArtifactRetentionPolicyIsAppendOnlyAndCannotWiden(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)

	_, found, err := repository.GetWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionGlobal, nil)
	require.NoError(t, err)
	assert.False(t, found)
	now := time.Now().UTC()
	global, err := repository.PublishWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionGlobal, Revision: 1,
		RetentionSeconds: int64((30 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 32 << 20, MaxRunBytes: 128 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, global.Revision)
	_, err = repository.PublishWorkflowArtifactRetentionPolicy(global, 0)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)

	projectID := fixture.projectID
	project, err := repository.PublishWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionProject, ProjectID: &projectID, Revision: 1,
		RetentionSeconds: int64((7 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 16 << 20, MaxRunBytes: 64 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, project.Revision)

	wider := project
	wider.Revision++
	wider.MaxRunBytes = global.MaxRunBytes + 1
	_, err = repository.PublishWorkflowArtifactRetentionPolicy(wider, project.Revision)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)

	narrowerGlobal := global
	narrowerGlobal.Revision++
	narrowerGlobal.MaxRunBytes = project.MaxRunBytes - 1
	_, err = repository.PublishWorkflowArtifactRetentionPolicy(narrowerGlobal, global.Revision)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)
}

func TestWorkflowArtifactRetentionBootstrapSerializesGlobalAndProjectPublish(t *testing.T) {
	store, repository, fixture := workflowFileArtifactFixture(t)
	t.Cleanup(store.Close)
	now := time.Now().UTC()
	projectID := fixture.projectID
	global := coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionGlobal, Revision: 1,
		RetentionSeconds: int64((5 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 8 << 20, MaxRunBytes: 32 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}
	project := coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionProject, ProjectID: &projectID, Revision: 1,
		RetentionSeconds: int64((7 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 16 << 20, MaxRunBytes: 64 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}
	type publishResult struct {
		scope coredb.WorkflowArtifactRetentionScope
		err   error
	}
	start := make(chan struct{})
	results := make(chan publishResult, 2)
	for _, policy := range []coredb.WorkflowArtifactRetentionPolicy{global, project} {
		policy := policy
		go func() {
			<-start
			_, publishErr := repository.PublishWorkflowArtifactRetentionPolicy(policy, 0)
			results <- publishResult{scope: policy.Scope, err: publishErr}
		}()
	}
	close(start)
	first, second := <-results, <-results
	close(results)
	resultSet := []publishResult{first, second}
	successes, conflicts := 0, 0
	for _, result := range resultSet {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, pro_interfaces.ErrWorkflowArtifactRetentionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected %s bootstrap result: %v", result.scope, result.err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)

	storedGlobal, globalFound, err := repository.GetWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionGlobal, nil)
	require.NoError(t, err)
	storedProject, projectFound, err := repository.GetWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionProject, &projectID)
	require.NoError(t, err)
	if globalFound && projectFound {
		_, err = coredb.ResolveWorkflowArtifactRetention(storedGlobal, &storedProject)
		require.NoError(t, err)
	}
}

func TestWorkflowFileArtifactDatabaseMatrix(t *testing.T) {
	if os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX") != "1" {
		t.Skip("external workflow artifact database matrix is disabled")
	}
	store := workflowFileArtifactMatrixStore(t)
	t.Cleanup(store.Close)
	repository, fixture := workflowFileArtifactMatrixFixtureInStore(t, store)

	now := time.Now().UTC()
	globalPolicy := coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionGlobal, Revision: 1,
		RetentionSeconds: int64((14 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 32 << 20, MaxRunBytes: 128 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}
	globalSuccesses, globalConflicts := concurrentRetentionPublishes(t, repository, globalPolicy, 0)
	assert.Equal(t, 1, globalSuccesses)
	assert.Equal(t, 1, globalConflicts)

	projectID := fixture.projectID
	projectPolicy := coredb.WorkflowArtifactRetentionPolicy{
		Scope: coredb.WorkflowArtifactRetentionProject, ProjectID: &projectID, Revision: 1,
		RetentionSeconds: int64((7 * 24 * time.Hour) / time.Second),
		MaxArtifactBytes: 16 << 20, MaxRunBytes: 64 << 20,
		CreatedByUserID: fixture.userID, CreatedAt: now,
	}
	projectSuccesses, projectConflicts := concurrentRetentionPublishes(t, repository, projectPolicy, 0)
	assert.Equal(t, 1, projectSuccesses)
	assert.Equal(t, 1, projectConflicts)

	global, found, err := repository.GetWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionGlobal, nil)
	require.NoError(t, err)
	require.True(t, found)
	project, found, err := repository.GetWorkflowArtifactRetentionPolicy(coredb.WorkflowArtifactRetentionProject, &projectID)
	require.NoError(t, err)
	require.True(t, found)
	effective, err := coredb.ResolveWorkflowArtifactRetention(global, &project)
	require.NoError(t, err)
	assert.Equal(t, project.RetentionSeconds, effective.RetentionSeconds)
	assert.Equal(t, project.MaxArtifactBytes, effective.MaxArtifactBytes)
	assert.Equal(t, project.MaxRunBytes, effective.MaxRunBytes)

	content := []byte("matrix-content")
	metadata := fixture.metadata("matrix", content)
	metadata.Retention = effective
	artifact, err := repository.CreateWorkflowFileArtifact(metadata)
	require.NoError(t, err)
	start := make(chan struct{})
	appendErrors := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, appendErr := repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
				ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
				ExpectedRevision: artifact.Revision, OffsetBytes: 0, Data: content[:6],
			})
			appendErrors <- appendErr
		}()
	}
	close(start)
	appendSuccesses, appendConflicts := 0, 0
	for range 2 {
		switch appendErr := <-appendErrors; {
		case appendErr == nil:
			appendSuccesses++
		case errors.Is(appendErr, pro_interfaces.ErrWorkflowFileArtifactConflict):
			appendConflicts++
		default:
			t.Fatalf("unexpected concurrent append error: %v", appendErr)
		}
	}
	assert.Equal(t, 1, appendSuccesses)
	assert.Equal(t, 1, appendConflicts)

	artifact, err = repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, artifact.ID)
	require.NoError(t, err)
	artifact, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision, OffsetBytes: artifact.UploadedBytes, Data: content[artifact.UploadedBytes:],
	})
	require.NoError(t, err)
	artifact, err = repository.FinalizeWorkflowFileArtifact(pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision,
	})
	require.NoError(t, err)
	lease, err := repository.AcquireWorkflowFileArtifactDownloadLease(pro_interfaces.WorkflowFileArtifactLeaseRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID, TTL: time.Minute,
	})
	require.NoError(t, err)
	var downloaded bytes.Buffer
	_, err = repository.StreamWorkflowFileArtifactContent(context.Background(), lease, &downloaded)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded.Bytes())

	expiredAt := now.Add(-24 * time.Hour)
	finalizedAt := expiredAt.Add(-time.Duration(effective.RetentionSeconds) * time.Second)
	createdAt := finalizedAt.Add(-time.Hour)
	_, err = store.GetConnection().Exec("update workflow_file_artifact set created_at=?, finalized_at=?, expires_at=? where id=?", createdAt, finalizedAt, expiredAt, artifact.ID)
	require.NoError(t, err)
	_, err = store.GetConnection().Exec("update project__workflow_run set status=? where id=?", coredb.WorkflowRunSucceeded, fixture.runID)
	require.NoError(t, err)
	reference := coredb.WorkflowFileArtifactReference{ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID}
	expired, err := repository.ExpireWorkflowFileArtifact(reference)
	assert.False(t, expired)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactDownloadActive)
	require.NoError(t, repository.ReleaseWorkflowFileArtifactDownloadLease(lease))
	expired, err = repository.ExpireWorkflowFileArtifact(reference)
	require.NoError(t, err)
	assert.True(t, expired)

	interruptedMetadata := fixture.metadata("matrix-interrupted", []byte("incomplete"))
	interruptedMetadata.Retention = effective
	interrupted, err := repository.CreateWorkflowFileArtifact(interruptedMetadata)
	require.NoError(t, err)
	interrupted, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: interrupted.ID,
		ExpectedRevision: interrupted.Revision, OffsetBytes: 0, Data: []byte("incom"),
	})
	require.NoError(t, err)
	_, err = store.GetConnection().Exec("update workflow_file_artifact set created_at=? where id=?", now.Add(-48*time.Hour), interrupted.ID)
	require.NoError(t, err)
	reconciled, err := repository.ReconcileStaleWorkflowFileArtifactUploads(24*time.Hour, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, reconciled)
	interrupted, err = repository.GetWorkflowFileArtifact(fixture.projectID, fixture.runID, interrupted.ID)
	require.NoError(t, err)
	assert.Equal(t, coredb.WorkflowFileArtifactFailed, interrupted.State)
}

func concurrentRetentionPublishes(t *testing.T, repository *WorkflowFileArtifactStore, policy coredb.WorkflowArtifactRetentionPolicy, expectedRevision int) (int, int) {
	t.Helper()
	start := make(chan struct{})
	errorsByPublish := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := repository.PublishWorkflowArtifactRetentionPolicy(policy, expectedRevision)
			errorsByPublish <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		switch err := <-errorsByPublish; {
		case err == nil:
			successes++
		case errors.Is(err, pro_interfaces.ErrWorkflowArtifactRetentionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected retention publish error: %v", err)
		}
	}
	return successes, conflicts
}

type workflowFileArtifactTestFixture struct {
	projectID          int
	workflowTemplateID int
	runID              int
	nodeID             int
	taskID             int
	attempt            int
	producerTemplateID int
	userID             int
}

func workflowFileArtifactFixture(t *testing.T) (*coresql.SqlDb, *WorkflowFileArtifactStore, workflowFileArtifactTestFixture) {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	repository, fixture := workflowFileArtifactFixtureInStore(t, store)
	return store, repository, fixture
}

func workflowFileArtifactFixtureInStore(t *testing.T, store *coresql.SqlDb) (*WorkflowFileArtifactStore, workflowFileArtifactTestFixture) {
	t.Helper()
	project, err := store.CreateProject(coredb.Project{Name: "Workflow file artifact test"})
	require.NoError(t, err)
	projectID := project.ID
	workflowRepository := NewWorkflowStore(store.GetConnection())
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	workflow, err := workflowRepository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	now := time.Now().UTC()
	run, err := workflowdb.BuildWorkflowRunSnapshot(workflow, map[int]coredb.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, fmt.Sprintf("artifact-%d", now.UnixNano()), now)
	require.NoError(t, err)
	run.Status = coredb.WorkflowRunRunning
	run, err = workflowRepository.CreateWorkflowRun(run)
	require.NoError(t, err)
	nodeID := workflow.Nodes[0].ID
	userID := user.ID
	task, err := store.CreateTask(coredb.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskRunningStatus,
		WorkflowRunID: &run.ID, WorkflowNodeID: &nodeID, UserID: &userID,
		WorkflowTemplateSnapshot: &run.Nodes[0].TemplateSnapshotJSON, Created: now,
	}, 0)
	require.NoError(t, err)
	return NewWorkflowFileArtifactStore(store.GetConnection()), workflowFileArtifactTestFixture{
		projectID: projectID, workflowTemplateID: workflow.ID, runID: run.ID,
		nodeID: nodeID, taskID: task.ID, attempt: task.AssignmentGeneration,
		producerTemplateID: templateOne.ID, userID: user.ID,
	}
}

func workflowFileArtifactMatrixFixtureInStore(t *testing.T, store *coresql.SqlDb) (*WorkflowFileArtifactStore, workflowFileArtifactTestFixture) {
	t.Helper()
	project, err := store.CreateProject(coredb.Project{Name: "Workflow file artifact matrix"})
	require.NoError(t, err)
	projectID := project.ID
	user, templateOne, _ := workflowRunResources(t, store, projectID)
	workflowTemplateID, err := store.GetConnection().Insert("id",
		"insert into project__workflow_template(project_id, name, description, start_version, definition_version, revision, max_parallel_tasks, parameter_definitions, access_policy, access_policy_revision) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		projectID, "Artifact matrix", nil, nil, coredb.WorkflowDefinitionVersion, 1, 0, "[]", "{}", 1,
	)
	require.NoError(t, err)
	now := time.Now().UTC()
	workflowRepository := NewWorkflowStore(store.GetConnection())
	run, err := workflowRepository.CreateWorkflowRun(coredb.WorkflowRun{
		ProjectID: projectID, WorkflowTemplateID: workflowTemplateID,
		Status: coredb.WorkflowRunRunning, DesiredState: coredb.WorkflowRunDesiredRunning,
		ReconciliationState: coredb.WorkflowRunReconciliationHealthy,
		ActorUserID:         user.ID, DefinitionVersion: coredb.WorkflowDefinitionVersion, DefinitionRevision: 1,
		DefinitionSnapshotJSON: "{}", ParameterSnapshotJSON: "{}", TriggerSnapshotJSON: "{}",
		CorrelationID: fmt.Sprintf("artifact-matrix-%d", now.UnixNano()), Created: now,
	})
	require.NoError(t, err)
	nodeID := 1
	userID := user.ID
	task, err := store.CreateTask(coredb.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskRunningStatus,
		WorkflowRunID: &run.ID, WorkflowNodeID: &nodeID, UserID: &userID,
		Created: now,
	}, 0)
	require.NoError(t, err)
	return NewWorkflowFileArtifactStore(store.GetConnection()), workflowFileArtifactTestFixture{
		projectID: projectID, workflowTemplateID: workflowTemplateID, runID: run.ID,
		nodeID: nodeID, taskID: task.ID, attempt: task.AssignmentGeneration,
		producerTemplateID: templateOne.ID, userID: user.ID,
	}
}

func workflowFileArtifactMatrixStore(t *testing.T) *coresql.SqlDb {
	t.Helper()
	dialect := os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_DIALECT")
	require.Contains(t, []string{util.DbDriverMySQL, util.DbDriverPostgres}, dialect)
	database := os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_DB_NAME")
	require.Truef(t, strings.HasSuffix(database, "_artifact_matrix"), "matrix database %q must end in _artifact_matrix", database)
	dbConfig := &util.DbConfig{
		Dialect: dialect, Hostname: os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_DB_HOST"),
		DbName: database, Username: os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_DB_USER"),
		Password: os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_DB_PASS"),
	}
	require.NotEmpty(t, dbConfig.Hostname)
	require.NotEmpty(t, dbConfig.Username)
	if dialect == util.DbDriverPostgres {
		dbConfig.Options = map[string]string{"sslmode": "disable"}
	}
	util.Config = &util.ConfigType{
		Dialect: dialect, MySQL: dbConfig, Postgres: dbConfig,
		Log:     &util.ConfigLog{Events: &util.EventLogType{}, Tasks: &util.TaskLogType{}},
		Process: &util.ConfigProcess{}, Runners: &util.RunnersConfig{},
		Apps: map[string]util.App{"ansible": {}, "bash": {}},
	}
	store := coresql.CreateDb(dialect)
	store.Connect()
	var tableCount int
	query := "select count(*) from information_schema.tables where table_schema=database()"
	if dialect == util.DbDriverPostgres {
		query = "select count(*) from information_schema.tables where table_schema='public' and table_type='BASE TABLE'"
	}
	require.NoError(t, store.Sql().SelectOne(&tableCount, query))
	if os.Getenv("SEMAPHORE_WORKFLOW_ARTIFACT_MATRIX_PREMIGRATED") == "1" {
		require.NotZero(t, tableCount, "pre-migrated workflow artifact matrix database must contain the schema")
		var migrationCount int
		require.NoError(t, store.Sql().SelectOne(
			&migrationCount, store.PrepareQuery("select count(1) from migrations where version=?"), "2.20.65",
		))
		require.Equal(t, 1, migrationCount, "pre-migrated workflow artifact matrix database must include v2.20.65")
	} else {
		require.Zero(t, tableCount, "workflow artifact matrix database must be empty")
		require.NoError(t, coredb.Migrate(store, nil))
	}
	return store
}

func (fixture workflowFileArtifactTestFixture) metadata(name string, content []byte) coredb.WorkflowFileArtifactMetadata {
	return coredb.WorkflowFileArtifactMetadata{
		ProjectID: fixture.projectID, WorkflowTemplateID: fixture.workflowTemplateID,
		WorkflowRunID: fixture.runID, WorkflowNodeID: fixture.nodeID,
		WorkflowDefinitionRevision: 1, TaskID: fixture.taskID, Attempt: fixture.attempt,
		LogicalName: name, Filename: name + ".bin", MediaType: "application/octet-stream",
		SizeBytes: int64(len(content)), SHA256: artifactSHA256(content),
		ProducerUserID: fixture.userID, ProducerTemplateID: fixture.producerTemplateID,
		ProducerVersion: "semaphore:test",
		AccessPolicy:    coredb.WorkflowFileArtifactAccessPolicy{Revision: 1},
		Retention:       coredb.DefaultWorkflowArtifactRetentionSnapshot(),
	}
}

func finalizeWorkflowFileArtifact(t *testing.T, repository *WorkflowFileArtifactStore, fixture workflowFileArtifactTestFixture, name string, content []byte) coredb.WorkflowFileArtifactMetadata {
	t.Helper()
	artifact, err := repository.CreateWorkflowFileArtifact(fixture.metadata(name, content))
	require.NoError(t, err)
	artifact, err = repository.AppendWorkflowFileArtifactChunk(pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision, OffsetBytes: 0, Data: content,
	})
	require.NoError(t, err)
	artifact, err = repository.FinalizeWorkflowFileArtifact(pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: fixture.runID, ArtifactID: artifact.ID,
		ExpectedRevision: artifact.Revision,
	})
	require.NoError(t, err)
	return artifact
}

func artifactSHA256(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
