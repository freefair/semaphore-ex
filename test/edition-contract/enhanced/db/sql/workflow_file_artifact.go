package sql

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const maxWorkflowFileArtifactRepositoryBatch = 100

type WorkflowFileArtifactStore struct {
	connection *coresql.SqlDbConnection
}

var _ pro_interfaces.WorkflowFileArtifactRepository = (*WorkflowFileArtifactStore)(nil)

func NewWorkflowFileArtifactStore(connection *coresql.SqlDbConnection) *WorkflowFileArtifactStore {
	return &WorkflowFileArtifactStore{connection: connection}
}

func (store *WorkflowFileArtifactStore) GetWorkflowArtifactRetentionPolicy(scope db.WorkflowArtifactRetentionScope, projectID *int) (db.WorkflowArtifactRetentionPolicy, bool, error) {
	if err := validateWorkflowArtifactRetentionScope(scope, projectID); err != nil || store.invalid() {
		return db.WorkflowArtifactRetentionPolicy{}, false, db.ErrInvalidOperation
	}
	var policy db.WorkflowArtifactRetentionPolicy
	err := store.connection.SelectOne(&policy,
		"select id, scope, project_id, revision, retention_seconds, max_artifact_bytes, max_run_bytes, created_by_user_id, created_at from workflow_artifact_retention_policy where scope_key=? order by revision desc limit 1",
		workflowArtifactRetentionScopeKey(scope, projectID),
	)
	if errors.Is(err, db.ErrNotFound) {
		return db.WorkflowArtifactRetentionPolicy{}, false, nil
	}
	if err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, false, err
	}
	if err = policy.Validate(); err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, false, err
	}
	return policy, true, nil
}

func (store *WorkflowFileArtifactStore) PublishWorkflowArtifactRetentionPolicy(policy db.WorkflowArtifactRetentionPolicy, expectedRevision int) (db.WorkflowArtifactRetentionPolicy, error) {
	if store.invalid() || expectedRevision < 0 || policy.Revision != expectedRevision+1 || policy.CreatedByUserID < 1 ||
		validateWorkflowArtifactRetentionScope(policy.Scope, policy.ProjectID) != nil {
		return db.WorkflowArtifactRetentionPolicy{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = store.lockWorkflowArtifactRetentionGlobal(tx); err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	if policy.Scope == db.WorkflowArtifactRetentionProject {
		if err = store.lockProject(tx, *policy.ProjectID); err != nil {
			return db.WorkflowArtifactRetentionPolicy{}, err
		}
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	policy.ID = 0
	policy.CreatedAt = now
	if err = policy.Validate(); err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, db.ErrInvalidOperation
	}
	var global db.WorkflowArtifactRetentionPolicy
	var globalFound bool
	if policy.Scope == db.WorkflowArtifactRetentionProject {
		global, globalFound, err = store.getRetentionPolicyTx(tx, db.WorkflowArtifactRetentionGlobal, nil, true)
		if err != nil {
			return db.WorkflowArtifactRetentionPolicy{}, err
		}
	}
	current, found, err := store.getRetentionPolicyTx(tx, policy.Scope, policy.ProjectID, true)
	if err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	currentRevision := 0
	if found {
		currentRevision = current.Revision
	}
	if currentRevision != expectedRevision {
		return db.WorkflowArtifactRetentionPolicy{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
	}
	if policy.Scope == db.WorkflowArtifactRetentionProject {
		if globalFound {
			if _, err = db.ResolveWorkflowArtifactRetention(global, &policy); err != nil {
				return db.WorkflowArtifactRetentionPolicy{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
			}
		} else if workflowArtifactPolicyWidensSnapshot(policy, db.DefaultWorkflowArtifactRetentionSnapshot()) {
			return db.WorkflowArtifactRetentionPolicy{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
		}
	} else {
		var conflicts int
		err = tx.SelectOne(&conflicts, store.connection.PrepareQuery(
			"select count(1) from workflow_artifact_retention_policy candidate where candidate.scope='project' and candidate.revision=(select max(current.revision) from workflow_artifact_retention_policy current where current.scope_key=candidate.scope_key) and (candidate.retention_seconds>? or candidate.max_artifact_bytes>? or candidate.max_run_bytes>?)"),
			policy.RetentionSeconds, policy.MaxArtifactBytes, policy.MaxRunBytes,
		)
		if err != nil {
			return db.WorkflowArtifactRetentionPolicy{}, err
		}
		if conflicts > 0 {
			return db.WorkflowArtifactRetentionPolicy{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
		}
	}
	policy.ID, err = workflowFileArtifactInsertID(tx, store.connection,
		"insert into workflow_artifact_retention_policy(scope_key, scope, project_id, revision, retention_seconds, max_artifact_bytes, max_run_bytes, created_by_user_id, created_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		workflowArtifactRetentionScopeKey(policy.Scope, policy.ProjectID), policy.Scope, policy.ProjectID, policy.Revision,
		policy.RetentionSeconds, policy.MaxArtifactBytes, policy.MaxRunBytes, policy.CreatedByUserID, policy.CreatedAt,
	)
	if err != nil {
		if workflowFileArtifactUniqueConflict(err) {
			return db.WorkflowArtifactRetentionPolicy{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
		}
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, err
	}
	return policy, nil
}

func (store *WorkflowFileArtifactStore) CreateWorkflowFileArtifact(artifact db.WorkflowFileArtifactMetadata) (db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	artifact.ID = 1
	artifact.UploadedBytes = 0
	artifact.State = db.WorkflowFileArtifactStaging
	artifact.Revision = 1
	artifact.CreatedAt = now
	artifact.FinalizedAt = nil
	artifact.ExpiresAt = nil
	artifact.DeletedAt = nil
	if err = artifact.CanonicalizeForPersistence(); err != nil || artifact.SizeBytes > artifact.Retention.MaxArtifactBytes {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	if err = store.validateArtifactProducerTx(tx, artifact); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if err = store.validateArtifactRetentionTx(tx, artifact); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	usage, found, err := store.getRunUsageTx(tx, artifact.WorkflowRunID, true)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if !found {
		usage = db.WorkflowFileArtifactRunUsage{WorkflowRunID: artifact.WorkflowRunID, Revision: 1}
	}
	if usage.ReservedBytes+artifact.SizeBytes > artifact.Retention.MaxRunBytes || usage.ArtifactCount+1 > db.MaxWorkflowFileArtifactsPerRun {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactQuotaExceeded
	}
	artifact.ID, err = workflowFileArtifactInsertID(tx, store.connection,
		"insert into workflow_file_artifact(project_id, workflow_template_id, workflow_run_id, workflow_node_id, workflow_definition_revision, task_id, attempt, logical_name, filename, media_type, size_bytes, uploaded_bytes, sha256, state, revision, producer_user_id, producer_runner_id, producer_template_id, producer_version, credential_provenance, access_policy, retention_global_revision, retention_project_revision, retention_seconds, retention_max_artifact_bytes, retention_max_run_bytes, created_at, finalized_at, expires_at, deleted_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		artifact.ProjectID, artifact.WorkflowTemplateID, artifact.WorkflowRunID, artifact.WorkflowNodeID,
		artifact.WorkflowDefinitionRevision, artifact.TaskID, artifact.Attempt, artifact.LogicalName, artifact.Filename,
		artifact.MediaType, artifact.SizeBytes, artifact.UploadedBytes, artifact.SHA256, artifact.State, artifact.Revision,
		artifact.ProducerUserID, artifact.ProducerRunnerID, artifact.ProducerTemplateID, artifact.ProducerVersion,
		artifact.CredentialProvenanceJSON, artifact.AccessPolicyJSON, artifact.RetentionGlobalRevision,
		artifact.RetentionProjectRevision, artifact.RetentionSeconds, artifact.RetentionMaxArtifactBytes,
		artifact.RetentionMaxRunBytes, artifact.CreatedAt, artifact.FinalizedAt, artifact.ExpiresAt, artifact.DeletedAt,
	)
	if err != nil {
		if workflowFileArtifactUniqueConflict(err) {
			return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
		}
		return db.WorkflowFileArtifactMetadata{}, err
	}
	usage.ReservedBytes += artifact.SizeBytes
	usage.ArtifactCount++
	if found {
		result, updateErr := tx.Exec(store.connection.PrepareQuery(
			"update workflow_file_artifact_run_usage set reserved_bytes=?, artifact_count=?, revision=revision+1 where workflow_run_id=? and revision=?"),
			usage.ReservedBytes, usage.ArtifactCount, usage.WorkflowRunID, usage.Revision,
		)
		if updateErr != nil {
			return db.WorkflowFileArtifactMetadata{}, updateErr
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil || rows != 1 {
			return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
		}
		usage.Revision++
	} else if _, err = tx.Exec(store.connection.PrepareQuery(
		"insert into workflow_file_artifact_run_usage(workflow_run_id, reserved_bytes, artifact_count, revision) values (?, ?, ?, ?)"),
		usage.WorkflowRunID, usage.ReservedBytes, usage.ArtifactCount, usage.Revision,
	); err != nil {
		if workflowFileArtifactUniqueConflict(err) {
			return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
		}
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return store.GetWorkflowFileArtifact(artifact.ProjectID, artifact.WorkflowRunID, artifact.ID)
}

func (store *WorkflowFileArtifactStore) AppendWorkflowFileArtifactChunk(request pro_interfaces.WorkflowFileArtifactAppendRequest) (db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() || request.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	defer func() { _ = tx.Rollback() }()
	artifact, err := store.getArtifactTx(tx, request.ProjectID, request.WorkflowRunID, request.ArtifactID, true)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if artifact.State != db.WorkflowFileArtifactStaging || artifact.Revision != request.ExpectedRevision ||
		artifact.UploadedBytes != request.OffsetBytes || int64(len(request.Data)) > artifact.SizeBytes-artifact.UploadedBytes {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = store.validateArtifactAttemptTx(tx, artifact); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	var ordinal int
	if err = tx.SelectOne(&ordinal, store.connection.PrepareQuery(
		"select count(1) from workflow_file_artifact_chunk where artifact_id=?"), artifact.ID); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	chunk := db.WorkflowFileArtifactChunk{
		ArtifactID: artifact.ID, Ordinal: ordinal, OffsetBytes: request.OffsetBytes,
		SizeBytes: len(request.Data), Data: request.Data,
	}
	if err = chunk.Validate(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	if _, err = tx.Exec(store.connection.PrepareQuery(
		"insert into workflow_file_artifact_chunk(artifact_id, ordinal, offset_bytes, size_bytes, data) values (?, ?, ?, ?, ?)"),
		chunk.ArtifactID, chunk.Ordinal, chunk.OffsetBytes, chunk.SizeBytes, chunk.Data,
	); err != nil {
		if workflowFileArtifactUniqueConflict(err) {
			return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
		}
		return db.WorkflowFileArtifactMetadata{}, err
	}
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact set uploaded_bytes=uploaded_bytes+?, revision=revision+1 where id=? and project_id=? and workflow_run_id=? and state=? and revision=? and uploaded_bytes=?"),
		chunk.SizeBytes, artifact.ID, request.ProjectID, request.WorkflowRunID, db.WorkflowFileArtifactStaging,
		request.ExpectedRevision, request.OffsetBytes,
	)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return store.GetWorkflowFileArtifact(request.ProjectID, request.WorkflowRunID, request.ArtifactID)
}

func (store *WorkflowFileArtifactStore) GetWorkflowFileArtifact(projectID int, workflowRunID int, artifactID int) (db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() || projectID < 1 || workflowRunID < 1 || artifactID < 1 {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	var artifact db.WorkflowFileArtifactMetadata
	err := store.connection.SelectOne(&artifact,
		"select * from workflow_file_artifact where id=? and project_id=? and workflow_run_id=?",
		artifactID, projectID, workflowRunID,
	)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return hydrateWorkflowFileArtifact(artifact)
}

func (store *WorkflowFileArtifactStore) GetWorkflowFileArtifacts(projectID int, workflowRunID int, params db.RetrieveQueryParams) ([]db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() || projectID < 1 || workflowRunID < 1 || params.Offset < 0 || params.BeforeID < 0 || params.Count < 0 {
		return nil, db.ErrInvalidOperation
	}
	count := params.Count
	if count == 0 || count > maxWorkflowFileArtifactRepositoryBatch {
		count = maxWorkflowFileArtifactRepositoryBatch
	}
	query := "select * from workflow_file_artifact where project_id=? and workflow_run_id=? and state=?"
	args := []any{projectID, workflowRunID, db.WorkflowFileArtifactAvailable}
	if params.BeforeID > 0 {
		query += " and id<?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	args = append(args, count)
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	var artifacts []db.WorkflowFileArtifactMetadata
	if _, err := store.connection.SelectAll(&artifacts, query, args...); err != nil {
		return nil, err
	}
	for index := range artifacts {
		hydrated, err := hydrateWorkflowFileArtifact(artifacts[index])
		if err != nil {
			return nil, err
		}
		artifacts[index] = hydrated
	}
	return artifacts, nil
}

func (store *WorkflowFileArtifactStore) FinalizeWorkflowFileArtifact(request pro_interfaces.WorkflowFileArtifactMutationRequest) (db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() || request.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	artifact, err := store.GetWorkflowFileArtifact(request.ProjectID, request.WorkflowRunID, request.ArtifactID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if artifact.State != db.WorkflowFileArtifactStaging || artifact.Revision != request.ExpectedRevision {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if artifact.UploadedBytes != artifact.SizeBytes {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactIncomplete
	}
	hash := sha256.New()
	written, err := store.streamArtifactChunks(context.Background(), artifact, hash)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if written != artifact.SizeBytes {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactIncomplete
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactChecksum
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := store.getArtifactTx(tx, request.ProjectID, request.WorkflowRunID, request.ArtifactID, true)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if current.State != db.WorkflowFileArtifactStaging || current.Revision != request.ExpectedRevision ||
		current.UploadedBytes != current.SizeBytes || current.SHA256 != artifact.SHA256 {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = store.validateArtifactAttemptTx(tx, current); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	expiresAt := now.Add(time.Duration(current.Retention.RetentionSeconds) * time.Second)
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact set state=?, revision=revision+1, finalized_at=?, expires_at=? where id=? and project_id=? and workflow_run_id=? and state=? and revision=? and uploaded_bytes=size_bytes"),
		db.WorkflowFileArtifactAvailable, now, expiresAt, current.ID, current.ProjectID, current.WorkflowRunID,
		db.WorkflowFileArtifactStaging, request.ExpectedRevision,
	)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return store.GetWorkflowFileArtifact(request.ProjectID, request.WorkflowRunID, request.ArtifactID)
}

func (store *WorkflowFileArtifactStore) FailWorkflowFileArtifact(request pro_interfaces.WorkflowFileArtifactMutationRequest) (db.WorkflowFileArtifactMetadata, error) {
	if store.invalid() || request.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	defer func() { _ = tx.Rollback() }()
	artifact, err := store.getArtifactTx(tx, request.ProjectID, request.WorkflowRunID, request.ArtifactID, true)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if artifact.State != db.WorkflowFileArtifactStaging || artifact.Revision != request.ExpectedRevision {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if _, err = tx.Exec(store.connection.PrepareQuery("delete from workflow_file_artifact_chunk where artifact_id=?"), artifact.ID); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact set state=?, revision=revision+1, deleted_at=? where id=? and state=? and revision=?"),
		db.WorkflowFileArtifactFailed, now, artifact.ID, db.WorkflowFileArtifactStaging, request.ExpectedRevision,
	)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return store.GetWorkflowFileArtifact(request.ProjectID, request.WorkflowRunID, request.ArtifactID)
}

func (store *WorkflowFileArtifactStore) AcquireWorkflowFileArtifactDownloadLease(request pro_interfaces.WorkflowFileArtifactLeaseRequest) (db.WorkflowFileArtifactDownloadLease, error) {
	if store.invalid() || request.Validate() != nil {
		return db.WorkflowFileArtifactDownloadLease{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	artifact, err := store.getArtifactTx(tx, request.ProjectID, request.WorkflowRunID, request.ArtifactID, true)
	if err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	if artifact.State != db.WorkflowFileArtifactAvailable || artifact.ExpiresAt == nil || !artifact.ExpiresAt.After(now) {
		return db.WorkflowFileArtifactDownloadLease{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	if _, err = tx.Exec(store.connection.PrepareQuery(
		"delete from workflow_file_artifact_download_lease where artifact_id=? and expires_at<=?"), artifact.ID, now,
	); err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	token, err := workflowFileArtifactLeaseToken()
	if err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	lease := db.WorkflowFileArtifactDownloadLease{
		LeaseToken: token, ArtifactID: artifact.ID, CreatedAt: now, ExpiresAt: now.Add(request.TTL),
	}
	if err = lease.Validate(); err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	if _, err = tx.Exec(store.connection.PrepareQuery(
		"insert into workflow_file_artifact_download_lease(lease_token, artifact_id, expires_at, created_at) values (?, ?, ?, ?)"),
		lease.LeaseToken, lease.ArtifactID, lease.ExpiresAt, lease.CreatedAt,
	); err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.WorkflowFileArtifactDownloadLease{}, err
	}
	return lease, nil
}

func (store *WorkflowFileArtifactStore) StreamWorkflowFileArtifactContent(ctx context.Context, lease db.WorkflowFileArtifactDownloadLease, writer io.Writer) (int64, error) {
	if store.invalid() || ctx == nil || writer == nil || lease.Validate() != nil {
		return 0, db.ErrInvalidOperation
	}
	absoluteDeadline, err := store.renewWorkflowFileArtifactDownloadLease(lease)
	if err != nil {
		return 0, err
	}
	artifact, err := store.getArtifactByID(lease.ArtifactID)
	if err != nil {
		return 0, err
	}
	streamContext, cancelStream := context.WithDeadline(ctx, absoluteDeadline)
	defer cancelStream()
	hash := sha256.New()
	written, streamErr := store.streamArtifactChunks(streamContext, artifact, io.MultiWriter(writer, hash))
	if streamErr != nil {
		return written, streamErr
	}
	if written != artifact.SizeBytes {
		return written, pro_interfaces.ErrWorkflowFileArtifactIncomplete
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return written, pro_interfaces.ErrWorkflowFileArtifactChecksum
	}
	return written, nil
}

func (store *WorkflowFileArtifactStore) renewWorkflowFileArtifactDownloadLease(lease db.WorkflowFileArtifactDownloadLease) (time.Time, error) {
	tx, err := store.connection.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var state db.WorkflowFileArtifactState
	err = tx.SelectOne(&state, store.connection.PrepareQuery(
		"select state from workflow_file_artifact where id=?"+store.forUpdate()), lease.ArtifactID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, db.ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	if state != db.WorkflowFileArtifactAvailable {
		return time.Time{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return time.Time{}, err
	}
	var stored db.WorkflowFileArtifactDownloadLease
	err = tx.SelectOne(&stored, store.connection.PrepareQuery(
		"select lease_token, artifact_id, expires_at, created_at from workflow_file_artifact_download_lease where lease_token=? and artifact_id=?"+store.forUpdate()),
		lease.LeaseToken, lease.ArtifactID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	if err != nil {
		return time.Time{}, err
	}
	if stored.Validate() != nil || !stored.ExpiresAt.After(now) {
		return time.Time{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	absoluteDeadline := stored.CreatedAt.Add(db.MaxWorkflowFileArtifactDownloadLease)
	if !absoluteDeadline.After(now) {
		return time.Time{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact_download_lease set expires_at=? where lease_token=? and artifact_id=? and expires_at>?"),
		absoluteDeadline, lease.LeaseToken, lease.ArtifactID, now,
	)
	if err != nil {
		return time.Time{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if updated != 1 {
		return time.Time{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	if err = tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return absoluteDeadline, nil
}

func (store *WorkflowFileArtifactStore) ReleaseWorkflowFileArtifactDownloadLease(lease db.WorkflowFileArtifactDownloadLease) error {
	if store.invalid() || lease.Validate() != nil {
		return db.ErrInvalidOperation
	}
	_, err := store.connection.Exec(
		"delete from workflow_file_artifact_download_lease where lease_token=? and artifact_id=?",
		lease.LeaseToken, lease.ArtifactID,
	)
	return err
}

func (store *WorkflowFileArtifactStore) GetWorkflowFileArtifactExpiryCandidates(limit int) ([]db.WorkflowFileArtifactReference, error) {
	if store.invalid() || limit < 1 || limit > maxWorkflowFileArtifactRepositoryBatch {
		return nil, db.ErrInvalidOperation
	}
	var references []db.WorkflowFileArtifactReference
	_, err := store.connection.SelectAll(&references,
		"select project_id, workflow_run_id, id as artifact_id from workflow_file_artifact where state=? and expires_at<="+databaseCurrentTimestamp(store.connection)+" order by expires_at, id limit ?",
		db.WorkflowFileArtifactAvailable, limit,
	)
	return references, err
}

func (store *WorkflowFileArtifactStore) ExpireWorkflowFileArtifact(reference db.WorkflowFileArtifactReference) (bool, error) {
	if store.invalid() || reference.Validate() != nil {
		return false, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	artifact, err := store.getArtifactTx(tx, reference.ProjectID, reference.WorkflowRunID, reference.ArtifactID, true)
	if err != nil {
		return false, err
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return false, err
	}
	if artifact.State != db.WorkflowFileArtifactAvailable || artifact.ExpiresAt == nil || artifact.ExpiresAt.After(now) {
		return false, nil
	}
	var status db.WorkflowRunStatus
	if err = tx.SelectOne(&status, store.connection.PrepareQuery(
		"select status from project__workflow_run where id=? and project_id=?"+store.forUpdate()), reference.WorkflowRunID, reference.ProjectID,
	); errors.Is(err, sql.ErrNoRows) {
		return false, db.ErrNotFound
	} else if err != nil {
		return false, err
	}
	if !status.IsFinished() {
		return false, nil
	}
	if _, err = tx.Exec(store.connection.PrepareQuery(
		"delete from workflow_file_artifact_download_lease where artifact_id=? and expires_at<=?"), artifact.ID, now,
	); err != nil {
		return false, err
	}
	var activeLeases int
	if err = tx.SelectOne(&activeLeases, store.connection.PrepareQuery(
		"select count(1) from workflow_file_artifact_download_lease where artifact_id=? and expires_at>?"), artifact.ID, now,
	); err != nil {
		return false, err
	}
	if activeLeases > 0 {
		return false, pro_interfaces.ErrWorkflowFileArtifactDownloadActive
	}
	if _, err = tx.Exec(store.connection.PrepareQuery("delete from workflow_file_artifact_chunk where artifact_id=?"), artifact.ID); err != nil {
		return false, err
	}
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact set state=?, revision=revision+1, deleted_at=? where id=? and state=? and revision=?"),
		db.WorkflowFileArtifactExpired, now, artifact.ID, db.WorkflowFileArtifactAvailable, artifact.Revision,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return false, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (store *WorkflowFileArtifactStore) ReconcileStaleWorkflowFileArtifactUploads(olderThan time.Duration, limit int) (int, error) {
	result, err := store.ReconcileStaleWorkflowFileArtifactUploadsDetailed(olderThan, limit)
	return len(result.Reconciled), err
}

func (store *WorkflowFileArtifactStore) ReconcileStaleWorkflowFileArtifactUploadsDetailed(olderThan time.Duration, limit int) (pro_interfaces.WorkflowFileArtifactCleanupResult, error) {
	if store.invalid() || olderThan < time.Minute || olderThan > 30*24*time.Hour || limit < 1 || limit > maxWorkflowFileArtifactRepositoryBatch {
		return pro_interfaces.WorkflowFileArtifactCleanupResult{}, db.ErrInvalidOperation
	}
	tx, err := store.connection.Begin()
	if err != nil {
		return pro_interfaces.WorkflowFileArtifactCleanupResult{}, err
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	_ = tx.Rollback()
	if err != nil {
		return pro_interfaces.WorkflowFileArtifactCleanupResult{}, err
	}
	cutoff := now.Add(-olderThan)
	var candidates []db.WorkflowFileArtifactReference
	_, err = store.connection.SelectAll(&candidates,
		"select project_id, workflow_run_id, id as artifact_id from workflow_file_artifact where state=? and created_at<=? order by created_at, id limit ?",
		db.WorkflowFileArtifactStaging, cutoff, limit,
	)
	if err != nil {
		return pro_interfaces.WorkflowFileArtifactCleanupResult{}, err
	}
	result := pro_interfaces.WorkflowFileArtifactCleanupResult{
		Reconciled: make([]db.WorkflowFileArtifactReference, 0, len(candidates)),
	}
	for _, reference := range candidates {
		changed, reconcileErr := store.failStaleUpload(reference, cutoff)
		if reconcileErr != nil {
			result.Failed = &reference
			return result, reconcileErr
		}
		if changed {
			result.Reconciled = append(result.Reconciled, reference)
		}
	}
	return result, nil
}

func (store *WorkflowFileArtifactStore) failStaleUpload(reference db.WorkflowFileArtifactReference, cutoff time.Time) (bool, error) {
	tx, err := store.connection.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	artifact, err := store.getArtifactTx(tx, reference.ProjectID, reference.WorkflowRunID, reference.ArtifactID, true)
	if err != nil {
		return false, err
	}
	if artifact.State != db.WorkflowFileArtifactStaging || artifact.CreatedAt.After(cutoff) {
		return false, nil
	}
	now, err := workflowFileArtifactDatabaseNow(tx, store.connection)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(store.connection.PrepareQuery("delete from workflow_file_artifact_chunk where artifact_id=?"), artifact.ID); err != nil {
		return false, err
	}
	result, err := tx.Exec(store.connection.PrepareQuery(
		"update workflow_file_artifact set state=?, revision=revision+1, deleted_at=? where id=? and state=? and revision=? and created_at<=?"),
		db.WorkflowFileArtifactFailed, now, artifact.ID, db.WorkflowFileArtifactStaging, artifact.Revision, cutoff,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return false, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (store *WorkflowFileArtifactStore) validateArtifactProducerTx(tx *gorp.Transaction, artifact db.WorkflowFileArtifactMetadata) error {
	var run struct {
		WorkflowTemplateID int `db:"workflow_template_id"`
		DefinitionRevision int `db:"definition_revision"`
		ActorUserID        int `db:"actor_user_id"`
	}
	err := tx.SelectOne(&run, store.connection.PrepareQuery(
		"select workflow_template_id, definition_revision, actor_user_id from project__workflow_run where id=? and project_id=?"+store.forUpdate()),
		artifact.WorkflowRunID, artifact.ProjectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if run.WorkflowTemplateID != artifact.WorkflowTemplateID || run.DefinitionRevision != artifact.WorkflowDefinitionRevision {
		return db.ErrInvalidOperation
	}
	var task struct {
		ProjectID            int  `db:"project_id"`
		TemplateID           int  `db:"template_id"`
		WorkflowRunID        *int `db:"workflow_run_id"`
		WorkflowNodeID       *int `db:"workflow_node_id"`
		UserID               *int `db:"user_id"`
		RunnerSnapshotID     *int `db:"runner_id_snapshot"`
		AssignmentGeneration int  `db:"assignment_generation"`
	}
	err = tx.SelectOne(&task, store.connection.PrepareQuery(
		"select project_id, template_id, workflow_run_id, workflow_node_id, user_id, runner_id_snapshot, assignment_generation from task where id=? and project_id=?"+store.forUpdate()),
		artifact.TaskID, artifact.ProjectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	producerUserID := run.ActorUserID
	if task.UserID != nil {
		producerUserID = *task.UserID
	}
	if task.WorkflowRunID == nil || *task.WorkflowRunID != artifact.WorkflowRunID ||
		task.WorkflowNodeID == nil || *task.WorkflowNodeID != artifact.WorkflowNodeID ||
		task.TemplateID != artifact.ProducerTemplateID || task.AssignmentGeneration != artifact.Attempt ||
		producerUserID != artifact.ProducerUserID || !sameWorkflowFileArtifactIntPointer(task.RunnerSnapshotID, artifact.ProducerRunnerID) {
		return db.ErrInvalidOperation
	}
	for _, provenance := range artifact.CredentialProvenance {
		var count int
		err = tx.SelectOne(&count, store.connection.PrepareQuery(
			"select count(1) from global_credential_usage where project_id=? and task_id=? and dispatch_generation=? and target=? and credential_id=? and grant_id=? and credential_version=? and version_fingerprint=? and provider_version=? and outcome='allowed'"),
			artifact.ProjectID, artifact.TaskID, artifact.Attempt, provenance.Target, provenance.CredentialID,
			provenance.GrantID, provenance.CredentialVersion, provenance.VersionFingerprint, provenance.ProviderVersion,
		)
		if err != nil {
			return err
		}
		if count < 1 {
			return db.ErrInvalidOperation
		}
	}
	return nil
}

func (store *WorkflowFileArtifactStore) validateArtifactAttemptTx(tx *gorp.Transaction, artifact db.WorkflowFileArtifactMetadata) error {
	var task struct {
		WorkflowRunID        *int                   `db:"workflow_run_id"`
		WorkflowNodeID       *int                   `db:"workflow_node_id"`
		AssignmentGeneration int                    `db:"assignment_generation"`
		Status               task_logger.TaskStatus `db:"status"`
	}
	err := tx.SelectOne(&task, store.connection.PrepareQuery(
		"select workflow_run_id, workflow_node_id, assignment_generation, status from task where id=? and project_id=?"+store.forUpdate()),
		artifact.TaskID, artifact.ProjectID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	if err != nil {
		return err
	}
	if task.WorkflowRunID == nil || *task.WorkflowRunID != artifact.WorkflowRunID ||
		task.WorkflowNodeID == nil || *task.WorkflowNodeID != artifact.WorkflowNodeID ||
		task.AssignmentGeneration != artifact.Attempt || task.Status.IsFinished() {
		return pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	return nil
}

func (store *WorkflowFileArtifactStore) validateArtifactRetentionTx(tx *gorp.Transaction, artifact db.WorkflowFileArtifactMetadata) error {
	global, globalFound, err := store.getRetentionPolicyTx(tx, db.WorkflowArtifactRetentionGlobal, nil, true)
	if err != nil {
		return err
	}
	projectID := artifact.ProjectID
	project, projectFound, err := store.getRetentionPolicyTx(tx, db.WorkflowArtifactRetentionProject, &projectID, true)
	if err != nil {
		return err
	}
	var effective db.WorkflowArtifactRetentionSnapshot
	if globalFound {
		var projectPolicy *db.WorkflowArtifactRetentionPolicy
		if projectFound {
			projectPolicy = &project
		}
		effective, err = db.ResolveWorkflowArtifactRetention(global, projectPolicy)
		if err != nil {
			return err
		}
	} else {
		effective = db.DefaultWorkflowArtifactRetentionSnapshot()
		if projectFound {
			if workflowArtifactPolicyWidensSnapshot(project, effective) {
				return pro_interfaces.ErrWorkflowArtifactRetentionConflict
			}
			effective.ProjectRevision = project.Revision
			effective.RetentionSeconds = project.RetentionSeconds
			effective.MaxArtifactBytes = project.MaxArtifactBytes
			effective.MaxRunBytes = project.MaxRunBytes
		}
	}
	if artifact.Retention != effective {
		return db.ErrInvalidOperation
	}
	return nil
}

func (store *WorkflowFileArtifactStore) getRunUsageTx(tx *gorp.Transaction, workflowRunID int, lock bool) (db.WorkflowFileArtifactRunUsage, bool, error) {
	var usage db.WorkflowFileArtifactRunUsage
	query := "select * from workflow_file_artifact_run_usage where workflow_run_id=?"
	if lock {
		query += store.forUpdate()
	}
	err := tx.SelectOne(&usage, store.connection.PrepareQuery(query), workflowRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowFileArtifactRunUsage{}, false, nil
	}
	if err != nil {
		return db.WorkflowFileArtifactRunUsage{}, false, err
	}
	if err = usage.Validate(); err != nil {
		return db.WorkflowFileArtifactRunUsage{}, false, err
	}
	return usage, true, nil
}

func (store *WorkflowFileArtifactStore) getArtifactTx(tx *gorp.Transaction, projectID int, workflowRunID int, artifactID int, lock bool) (db.WorkflowFileArtifactMetadata, error) {
	query := "select * from workflow_file_artifact where id=? and project_id=? and workflow_run_id=?"
	if lock {
		query += store.forUpdate()
	}
	var artifact db.WorkflowFileArtifactMetadata
	err := tx.SelectOne(&artifact, store.connection.PrepareQuery(query), artifactID, projectID, workflowRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowFileArtifactMetadata{}, db.ErrNotFound
	}
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return hydrateWorkflowFileArtifact(artifact)
}

func (store *WorkflowFileArtifactStore) getArtifactByID(artifactID int) (db.WorkflowFileArtifactMetadata, error) {
	var artifact db.WorkflowFileArtifactMetadata
	if err := store.connection.SelectOne(&artifact, "select * from workflow_file_artifact where id=?", artifactID); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return hydrateWorkflowFileArtifact(artifact)
}

func (store *WorkflowFileArtifactStore) streamArtifactChunks(ctx context.Context, artifact db.WorkflowFileArtifactMetadata, writer io.Writer) (int64, error) {
	rows, err := store.connection.QueryContext(ctx,
		"select artifact_id, ordinal, offset_bytes, size_bytes, data from workflow_file_artifact_chunk where artifact_id=? order by ordinal",
		artifact.ID,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var written int64
	expectedOrdinal := 0
	for rows.Next() {
		var chunk db.WorkflowFileArtifactChunk
		if err = rows.Scan(&chunk.ArtifactID, &chunk.Ordinal, &chunk.OffsetBytes, &chunk.SizeBytes, &chunk.Data); err != nil {
			return written, err
		}
		if err = chunk.Validate(); err != nil || chunk.Ordinal != expectedOrdinal || chunk.OffsetBytes != written || written+int64(chunk.SizeBytes) > artifact.SizeBytes {
			return written, pro_interfaces.ErrWorkflowFileArtifactIncomplete
		}
		count, writeErr := writer.Write(chunk.Data)
		written += int64(count)
		if writeErr != nil {
			return written, writeErr
		}
		if count != chunk.SizeBytes {
			return written, io.ErrShortWrite
		}
		expectedOrdinal++
	}
	if err = rows.Err(); err != nil {
		return written, err
	}
	return written, nil
}

func (store *WorkflowFileArtifactStore) getRetentionPolicyTx(tx *gorp.Transaction, scope db.WorkflowArtifactRetentionScope, projectID *int, lock bool) (db.WorkflowArtifactRetentionPolicy, bool, error) {
	query := "select id, scope, project_id, revision, retention_seconds, max_artifact_bytes, max_run_bytes, created_by_user_id, created_at from workflow_artifact_retention_policy where scope_key=? order by revision desc limit 1"
	if lock {
		query += store.forUpdate()
	}
	var policy db.WorkflowArtifactRetentionPolicy
	err := tx.SelectOne(&policy, store.connection.PrepareQuery(query), workflowArtifactRetentionScopeKey(scope, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return db.WorkflowArtifactRetentionPolicy{}, false, nil
	}
	if err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, false, err
	}
	if err = policy.Validate(); err != nil {
		return db.WorkflowArtifactRetentionPolicy{}, false, err
	}
	return policy, true, nil
}

func (store *WorkflowFileArtifactStore) lockProject(tx *gorp.Transaction, projectID int) error {
	var id int
	err := tx.SelectOne(&id, store.connection.PrepareQuery("select id from project where id=?"+store.forUpdate()), projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	return err
}

// lockWorkflowArtifactRetentionGlobal is the durable serialization point for
// global and project policy publishes, including bootstrap before revision 1
// exists. Every publish takes it before an optional project-row lock.
func (store *WorkflowFileArtifactStore) lockWorkflowArtifactRetentionGlobal(tx *gorp.Transaction) error {
	var lockKey string
	err := tx.SelectOne(&lockKey, store.connection.PrepareQuery(
		"select lock_key from workflow_artifact_retention_lock where lock_key=?"+store.forUpdate(),
	), "global")
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrInvalidOperation
	}
	if err != nil {
		return err
	}
	if lockKey != "global" {
		return db.ErrInvalidOperation
	}
	return nil
}

func (store *WorkflowFileArtifactStore) forUpdate() string {
	if store.connection.GetDialect() == util.DbDriverSQLite {
		return ""
	}
	return " for update"
}

func (store *WorkflowFileArtifactStore) invalid() bool {
	return store == nil || store.connection == nil
}

func hydrateWorkflowFileArtifact(artifact db.WorkflowFileArtifactMetadata) (db.WorkflowFileArtifactMetadata, error) {
	if err := json.Unmarshal([]byte(artifact.CredentialProvenanceJSON), &artifact.CredentialProvenance); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if err := json.Unmarshal([]byte(artifact.AccessPolicyJSON), &artifact.AccessPolicy); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	artifact.Retention = db.WorkflowArtifactRetentionSnapshot{
		GlobalRevision: artifact.RetentionGlobalRevision, ProjectRevision: artifact.RetentionProjectRevision,
		RetentionSeconds: artifact.RetentionSeconds, MaxArtifactBytes: artifact.RetentionMaxArtifactBytes,
		MaxRunBytes: artifact.RetentionMaxRunBytes,
	}
	if err := artifact.Validate(); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return artifact, nil
}

func validateWorkflowArtifactRetentionScope(scope db.WorkflowArtifactRetentionScope, projectID *int) error {
	switch scope {
	case db.WorkflowArtifactRetentionGlobal:
		if projectID != nil {
			return db.ErrInvalidOperation
		}
	case db.WorkflowArtifactRetentionProject:
		if projectID == nil || *projectID < 1 {
			return db.ErrInvalidOperation
		}
	default:
		return db.ErrInvalidOperation
	}
	return nil
}

func workflowArtifactRetentionScopeKey(scope db.WorkflowArtifactRetentionScope, projectID *int) string {
	if scope == db.WorkflowArtifactRetentionGlobal {
		return "global"
	}
	return fmt.Sprintf("project:%d", *projectID)
}

func workflowArtifactPolicyWidensSnapshot(policy db.WorkflowArtifactRetentionPolicy, snapshot db.WorkflowArtifactRetentionSnapshot) bool {
	return policy.RetentionSeconds > snapshot.RetentionSeconds || policy.MaxArtifactBytes > snapshot.MaxArtifactBytes || policy.MaxRunBytes > snapshot.MaxRunBytes
}

func workflowFileArtifactDatabaseNow(tx *gorp.Transaction, connection *coresql.SqlDbConnection) (time.Time, error) {
	value, err := tx.SelectStr(connection.PrepareQuery("select " + databaseCurrentTimestamp(connection)))
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if parsed, parseErr := time.Parse(layout, value); parseErr == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid database timestamp")
}

func workflowFileArtifactInsertID(tx *gorp.Transaction, connection *coresql.SqlDbConnection, query string, args ...any) (int, error) {
	prepared := connection.PrepareQuery(query)
	if connection.GetDialect() == util.DbDriverPostgres {
		value, err := tx.SelectInt(prepared+" returning id", args...)
		return int(value), err
	}
	result, err := tx.Exec(prepared, args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

func workflowFileArtifactLeaseToken() (string, error) {
	value := make([]byte, sha256.Size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func workflowFileArtifactUniqueConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

func sameWorkflowFileArtifactIntPointer(left *int, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
