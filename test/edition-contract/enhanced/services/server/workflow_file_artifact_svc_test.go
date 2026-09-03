package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowFileArtifactServiceDerivesProvenanceAndEnforcesCurrentRoles(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "file-artifact-service")
	require.NoError(t, err)
	require.NotEmpty(t, fixture.enqueuer.tasks)
	task := fixture.enqueuer.tasks[0]

	_, err = fixture.store.CreateGlobalCredentialUsage(db.GlobalCredentialUsage{
		TaskID: task.ID, ProjectID: fixture.projectID, ActorID: fixture.user.ID,
		DispatchGeneration: task.AssignmentGeneration + 1, Target: "environment.stale_token",
		CredentialID: 21, GrantID: 22, CredentialVersion: 1,
		VersionFingerprint: artifactServiceSHA256([]byte("stale-version")),
		Outcome:            "allowed", Reason: "resolved", OccurredAt: time.Now().UTC().Add(-time.Minute),
	})
	require.NoError(t, err)
	usage, err := fixture.store.CreateGlobalCredentialUsage(db.GlobalCredentialUsage{
		TaskID: task.ID, ProjectID: fixture.projectID, ActorID: fixture.user.ID,
		DispatchGeneration: task.AssignmentGeneration, Target: "environment.deploy_token",
		CredentialID: 11, GrantID: 12, CredentialVersion: 3,
		VersionFingerprint: artifactServiceSHA256([]byte("credential-version")), ProviderVersion: 2,
		Outcome: "allowed", Reason: "resolved", OccurredAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	repository := workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection())
	service := NewWorkflowFileArtifactService(repository, fixture.repository, fixture.store)
	audit := &workflowFileArtifactAuditStub{}
	service.(pro_interfaces.WorkflowFileArtifactAuditConfigurer).ConfigureWorkflowFileArtifactAudit(audit)
	content := []byte("service-artifact")
	created, err := service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, db.WorkflowFileArtifactUpload{
		WorkflowNodeID: *task.WorkflowNodeID, TaskID: task.ID, Attempt: task.AssignmentGeneration,
		LogicalName: "release", Filename: "release.bin", MediaType: "application/octet-stream",
		SizeBytes: int64(len(content)), SHA256: artifactServiceSHA256(content),
		AccessPolicy: db.WorkflowFileArtifactAccessPolicy{
			Revision: 1, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		},
	}, &fixture.user)
	require.NoError(t, err)
	require.Len(t, created.CredentialProvenance, 1)
	assert.Equal(t, usage.CredentialID, created.CredentialProvenance[0].CredentialID)
	assert.Equal(t, usage.VersionFingerprint, created.CredentialProvenance[0].VersionFingerprint)
	assert.Equal(t, task.TemplateID, created.ProducerTemplateID)
	assert.Equal(t, fixture.user.ID, created.ProducerUserID)
	assert.Equal(t, db.DefaultWorkflowArtifactRetentionSnapshot(), created.Retention)

	manager, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "artifact-manager", Name: "Artifact Manager", Email: "artifact-manager@example.invalid",
	})
	require.NoError(t, err)
	_, err = fixture.store.CreateProjectUser(db.ProjectUser{ProjectID: fixture.projectID, UserID: manager.ID, Role: db.ProjectManager})
	require.NoError(t, err)
	created, err = service.AppendWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: 0, Data: content,
	}, &fixture.user)
	require.NoError(t, err)
	created, err = service.FinalizeWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactMutationRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision,
	}, &fixture.user)
	require.NoError(t, err)
	_, err = service.GetWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, created.ID, &manager)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)
	_, err = service.AcquireWorkflowFileArtifactDownload(context.Background(), fixture.projectID, run.ID, created.ID, &manager)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)
	managerList, err := service.GetWorkflowFileArtifacts(context.Background(), fixture.projectID, run.ID, db.RetrieveQueryParams{Count: 10}, &manager)
	require.NoError(t, err)
	assert.Empty(t, managerList)

	listed, err := service.GetWorkflowFileArtifacts(context.Background(), fixture.projectID, run.ID, db.RetrieveQueryParams{Count: 10}, &fixture.user)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	download, err := service.AcquireWorkflowFileArtifactDownload(context.Background(), fixture.projectID, run.ID, created.ID, &fixture.user)
	require.NoError(t, err)
	assert.True(t, download.Deadline.Equal(download.Lease.CreatedAt.Add(db.MaxWorkflowFileArtifactDownloadLease)))
	var output bytes.Buffer
	written, err := service.StreamWorkflowFileArtifactDownload(context.Background(), download, &output)
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), written)
	assert.Equal(t, content, output.Bytes())
	require.NoError(t, service.ReleaseWorkflowFileArtifactDownload(download))
	require.Len(t, audit.events, 2)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, pro_interfaces.AuditOutcomeAllowed, audit.events[1].Outcome)
}

func TestWorkflowFileArtifactServiceRejectsForeignProducerAndFiltersDeniedMetadata(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "file-artifact-denial")
	require.NoError(t, err)
	task := fixture.enqueuer.tasks[0]
	repository := workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection())
	service := NewWorkflowFileArtifactService(repository, fixture.repository, fixture.store)
	outsider, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "artifact-outsider", Name: "Artifact Outsider", Email: "artifact-outsider@example.invalid",
	})
	require.NoError(t, err)
	upload := db.WorkflowFileArtifactUpload{
		WorkflowNodeID: *task.WorkflowNodeID, TaskID: task.ID, Attempt: task.AssignmentGeneration,
		LogicalName: "denied", Filename: "denied.txt", MediaType: "text/plain",
		SizeBytes: 1, SHA256: artifactServiceSHA256([]byte("x")),
		AccessPolicy: db.WorkflowFileArtifactAccessPolicy{Revision: 1},
	}
	_, err = service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, upload, &outsider)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)

	created, err := service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, upload, &fixture.user)
	require.NoError(t, err)
	_, err = service.AppendWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: created.ID,
		ExpectedRevision: created.Revision, OffsetBytes: 0, Data: []byte("x"),
	}, &outsider)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)
	listed, err := service.GetWorkflowFileArtifacts(context.Background(), fixture.projectID, run.ID, db.RetrieveQueryParams{Count: 10}, &outsider)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)
	assert.Nil(t, listed)
}

func TestWorkflowFileArtifactServiceFencesRevokedExecutionRoleAndSupersededAttempt(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "file-artifact-fences")
	require.NoError(t, err)
	task := fixture.enqueuer.tasks[0]
	repository := workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection())
	service := NewWorkflowFileArtifactService(repository, fixture.repository, fixture.store)
	newUpload := func(name string) db.WorkflowFileArtifactUpload {
		return db.WorkflowFileArtifactUpload{
			WorkflowNodeID: *task.WorkflowNodeID, TaskID: task.ID, Attempt: task.AssignmentGeneration,
			LogicalName: name, Filename: name + ".txt", MediaType: "text/plain",
			SizeBytes: 1, SHA256: artifactServiceSHA256([]byte("x")),
			AccessPolicy: db.WorkflowFileArtifactAccessPolicy{Revision: 1},
		}
	}
	roleArtifact, err := service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, newUpload("role-fence"), &fixture.user)
	require.NoError(t, err)
	attemptArtifact, err := service.BeginWorkflowFileArtifact(context.Background(), fixture.projectID, run.ID, newUpload("attempt-fence"), &fixture.user)
	require.NoError(t, err)

	backup, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "artifact-backup-owner", Name: "Artifact Backup Owner", Email: "artifact-backup-owner@example.invalid",
	})
	require.NoError(t, err)
	_, err = fixture.store.CreateProjectUser(db.ProjectUser{ProjectID: fixture.projectID, UserID: backup.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	membership, err := fixture.store.GetProjectUser(fixture.projectID, fixture.user.ID)
	require.NoError(t, err)
	membership.Role = db.ProjectGuest
	require.NoError(t, fixture.store.UpdateProjectUser(membership))
	_, err = service.AppendWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: roleArtifact.ID,
		ExpectedRevision: roleArtifact.Revision, OffsetBytes: 0, Data: []byte("x"),
	}, &fixture.user)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)

	membership, err = fixture.store.GetProjectUser(fixture.projectID, fixture.user.ID)
	require.NoError(t, err)
	membership.Role = db.ProjectOwner
	require.NoError(t, fixture.store.UpdateProjectUser(membership))
	_, err = fixture.store.GetConnection().Exec("update task set assignment_generation=assignment_generation+1 where id=?", task.ID)
	require.NoError(t, err)
	_, err = service.AppendWorkflowFileArtifact(context.Background(), pro_interfaces.WorkflowFileArtifactAppendRequest{
		ProjectID: fixture.projectID, WorkflowRunID: run.ID, ArtifactID: attemptArtifact.ID,
		ExpectedRevision: attemptArtifact.Revision, OffsetBytes: 0, Data: []byte("x"),
	}, &fixture.user)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowFileArtifactConflict)
}

type workflowFileArtifactFailingReadRepository struct {
	pro_interfaces.WorkflowFileArtifactRepository
	err error
}

func (repository workflowFileArtifactFailingReadRepository) GetWorkflowFileArtifact(int, int, int) (db.WorkflowFileArtifactMetadata, error) {
	return db.WorkflowFileArtifactMetadata{}, repository.err
}

func TestWorkflowFileArtifactServiceAuditsRepositoryReadFailureAsFailure(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "file-artifact-audit-failure")
	require.NoError(t, err)
	repositoryErr := errors.New("repository unavailable")
	service := NewWorkflowFileArtifactService(
		workflowFileArtifactFailingReadRepository{WorkflowFileArtifactRepository: workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection()), err: repositoryErr},
		fixture.repository,
		fixture.store,
	)
	audit := &workflowFileArtifactAuditStub{}
	service.(pro_interfaces.WorkflowFileArtifactAuditConfigurer).ConfigureWorkflowFileArtifactAudit(audit)
	_, err = service.AcquireWorkflowFileArtifactDownload(context.Background(), fixture.projectID, run.ID, 123, &fixture.user)
	require.ErrorIs(t, err, repositoryErr)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[0].Outcome)
	assert.Equal(t, pro_interfaces.AuditReasonOperationError, audit.events[0].Reason)
}

func artifactServiceSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
