package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

type workflowFileArtifactService struct {
	repository      pro_interfaces.WorkflowFileArtifactRepository
	workflowManager db.WorkflowManager
	identityStore   pro_interfaces.WorkflowFileArtifactIdentityStore
	audit           pro_interfaces.AuditServiceFacade
}

var _ pro_interfaces.WorkflowFileArtifactServiceFacade = (*workflowFileArtifactService)(nil)
var _ pro_interfaces.WorkflowFileArtifactAuditConfigurer = (*workflowFileArtifactService)(nil)

func NewWorkflowFileArtifactService(
	repository pro_interfaces.WorkflowFileArtifactRepository,
	workflowManager db.WorkflowManager,
	identityStore pro_interfaces.WorkflowFileArtifactIdentityStore,
) pro_interfaces.WorkflowFileArtifactServiceFacade {
	if repository == nil || workflowManager == nil || identityStore == nil {
		return nil
	}
	return &workflowFileArtifactService{
		repository: repository, workflowManager: workflowManager, identityStore: identityStore,
	}
}

func (service *workflowFileArtifactService) ConfigureWorkflowFileArtifactAudit(audit pro_interfaces.AuditServiceFacade) {
	service.audit = audit
}

func (service *workflowFileArtifactService) BeginWorkflowFileArtifact(ctx context.Context, projectID int, workflowRunID int, upload db.WorkflowFileArtifactUpload, user *db.User) (db.WorkflowFileArtifactMetadata, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || projectID < 1 || workflowRunID < 1 || upload.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	run, state, err := service.authorizeWorkflowRun(projectID, workflowRunID, user)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if !user.Admin && !state.Identity.Permissions.Can(db.CanRunProjectTasks) {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	task, err := service.identityStore.GetTask(projectID, upload.TaskID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	producerUserID := run.ActorUserID
	if task.UserID != nil {
		producerUserID = *task.UserID
	}
	if user == nil || !user.Admin && producerUserID != user.ID {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	knownRoles := state.KnownRoles
	if user.Admin {
		knownRoles, err = service.knownProjectRoles(projectID)
		if err != nil {
			return db.WorkflowFileArtifactMetadata{}, err
		}
	}
	for _, roleID := range upload.AccessPolicy.RoleIDs {
		if !knownRoles[roleID] {
			return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
		}
	}
	retention, err := service.effectiveRetention(projectID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	credentialProvenance, err := service.taskCredentialProvenance(projectID, upload.TaskID, upload.Attempt)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	metadata := db.WorkflowFileArtifactMetadata{
		ProjectID: projectID, WorkflowTemplateID: run.WorkflowTemplateID, WorkflowRunID: workflowRunID,
		WorkflowNodeID: upload.WorkflowNodeID, WorkflowDefinitionRevision: run.DefinitionRevision,
		TaskID: upload.TaskID, Attempt: upload.Attempt, LogicalName: upload.LogicalName,
		Filename: upload.Filename, MediaType: upload.MediaType, SizeBytes: upload.SizeBytes, SHA256: upload.SHA256,
		ProducerUserID: producerUserID, ProducerRunnerID: task.RunnerSnapshotID,
		ProducerTemplateID: task.TemplateID, ProducerVersion: util.Version(),
		CredentialProvenance: credentialProvenance, AccessPolicy: upload.AccessPolicy, Retention: retention,
	}
	return service.repository.CreateWorkflowFileArtifact(metadata)
}

func (service *workflowFileArtifactService) AppendWorkflowFileArtifact(ctx context.Context, request pro_interfaces.WorkflowFileArtifactAppendRequest, user *db.User) (db.WorkflowFileArtifactMetadata, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || request.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	if _, err := service.authorizeArtifactMutation(request.ProjectID, request.WorkflowRunID, request.ArtifactID, user); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return service.repository.AppendWorkflowFileArtifactChunk(request)
}

func (service *workflowFileArtifactService) FinalizeWorkflowFileArtifact(ctx context.Context, request pro_interfaces.WorkflowFileArtifactMutationRequest, user *db.User) (db.WorkflowFileArtifactMetadata, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || request.Validate() != nil {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	if _, err := service.authorizeArtifactMutation(request.ProjectID, request.WorkflowRunID, request.ArtifactID, user); err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	return service.repository.FinalizeWorkflowFileArtifact(request)
}

func (service *workflowFileArtifactService) GetWorkflowFileArtifact(ctx context.Context, projectID int, workflowRunID int, artifactID int, user *db.User) (db.WorkflowFileArtifactMetadata, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || projectID < 1 || workflowRunID < 1 || artifactID < 1 {
		return db.WorkflowFileArtifactMetadata{}, db.ErrInvalidOperation
	}
	_, state, err := service.authorizeWorkflowRun(projectID, workflowRunID, user)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	artifact, err := service.repository.GetWorkflowFileArtifact(projectID, workflowRunID, artifactID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if artifact.State != db.WorkflowFileArtifactAvailable {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactNotAvailable
	}
	if user == nil || !user.Admin && !pro_interfaces.AuthorizeWorkflowFileArtifactAccess(artifact.AccessPolicy, state.Identity, state.KnownRoles) {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	return artifact, nil
}

func (service *workflowFileArtifactService) GetWorkflowFileArtifacts(ctx context.Context, projectID int, workflowRunID int, params db.RetrieveQueryParams, user *db.User) ([]db.WorkflowFileArtifactMetadata, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || projectID < 1 || workflowRunID < 1 {
		return nil, db.ErrInvalidOperation
	}
	_, state, err := service.authorizeWorkflowRun(projectID, workflowRunID, user)
	if err != nil {
		return nil, err
	}
	artifacts, err := service.repository.GetWorkflowFileArtifacts(projectID, workflowRunID, params)
	if err != nil {
		return nil, err
	}
	if user != nil && user.Admin {
		return artifacts, nil
	}
	visible := make([]db.WorkflowFileArtifactMetadata, 0, len(artifacts))
	for _, artifact := range artifacts {
		if pro_interfaces.AuthorizeWorkflowFileArtifactAccess(artifact.AccessPolicy, state.Identity, state.KnownRoles) {
			visible = append(visible, artifact)
		}
	}
	return visible, nil
}

func (service *workflowFileArtifactService) AcquireWorkflowFileArtifactDownload(ctx context.Context, projectID int, workflowRunID int, artifactID int, user *db.User) (pro_interfaces.WorkflowFileArtifactDownload, error) {
	artifact, err := service.GetWorkflowFileArtifact(ctx, projectID, workflowRunID, artifactID, user)
	if err != nil {
		outcome, reason := pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError
		if errors.Is(err, pro_interfaces.ErrWorkflowPermissionDenied) || errors.Is(err, pro_interfaces.ErrWorkflowFileArtifactNotAvailable) {
			outcome, reason = pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonWorkflowFileArtifactAccessDenied
		}
		service.recordDownloadAudit(projectID, artifactID, user, outcome, reason)
		return pro_interfaces.WorkflowFileArtifactDownload{}, err
	}
	lease, err := service.repository.AcquireWorkflowFileArtifactDownloadLease(pro_interfaces.WorkflowFileArtifactLeaseRequest{
		ProjectID: projectID, WorkflowRunID: workflowRunID, ArtifactID: artifactID,
		TTL: db.MinWorkflowFileArtifactDownloadLease,
	})
	if err != nil {
		service.recordDownloadAudit(projectID, artifactID, user, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		return pro_interfaces.WorkflowFileArtifactDownload{}, err
	}
	download := pro_interfaces.WorkflowFileArtifactDownload{
		Metadata: artifact, Lease: lease, Deadline: lease.CreatedAt.Add(db.MaxWorkflowFileArtifactDownloadLease), ActorID: user.ID,
	}
	if err = download.Validate(); err != nil {
		_ = service.repository.ReleaseWorkflowFileArtifactDownloadLease(lease)
		return pro_interfaces.WorkflowFileArtifactDownload{}, err
	}
	return download, nil
}

func (service *workflowFileArtifactService) StreamWorkflowFileArtifactDownload(ctx context.Context, download pro_interfaces.WorkflowFileArtifactDownload, writer io.Writer) (int64, error) {
	if err := validateWorkflowFileArtifactServiceContext(ctx); err != nil || writer == nil || download.Validate() != nil {
		return 0, db.ErrInvalidOperation
	}
	written, err := service.repository.StreamWorkflowFileArtifactContent(ctx, download.Lease, writer)
	actor := &db.User{ID: download.ActorID}
	if err != nil {
		service.recordDownloadAudit(download.Metadata.ProjectID, download.Metadata.ID, actor, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		return written, err
	}
	service.recordDownloadAudit(download.Metadata.ProjectID, download.Metadata.ID, actor, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonWorkflowFileArtifactDownloaded)
	return written, nil
}

func (service *workflowFileArtifactService) recordDownloadAudit(projectID int, artifactID int, user *db.User, outcome pro_interfaces.AuditOutcome, reason string) {
	if service.audit == nil || projectID < 1 || artifactID < 1 || user == nil || user.ID < 1 {
		return
	}
	actorID := user.ID
	event := pro_interfaces.AuditEvent{
		CorrelationID: "internal", ActorID: &actorID, ProjectID: &projectID,
		Action:     pro_interfaces.AuditActionWorkflowFileArtifactDownload,
		TargetType: pro_interfaces.AuditTargetWorkflowFileArtifact,
		TargetID:   fmt.Sprintf("artifact:%d", artifactID), Outcome: outcome,
		Source: pro_interfaces.AuditSourceAPI, Reason: reason,
	}
	_ = service.audit.Record(context.Background(), event)
}

func (service *workflowFileArtifactService) ReleaseWorkflowFileArtifactDownload(download pro_interfaces.WorkflowFileArtifactDownload) error {
	if download.Validate() != nil {
		return db.ErrInvalidOperation
	}
	return service.repository.ReleaseWorkflowFileArtifactDownloadLease(download.Lease)
}

func (service *workflowFileArtifactService) authorizeArtifactMutation(projectID int, workflowRunID int, artifactID int, user *db.User) (db.WorkflowFileArtifactMetadata, error) {
	_, state, err := service.authorizeWorkflowRun(projectID, workflowRunID, user)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if !user.Admin && !state.Identity.Permissions.Can(db.CanRunProjectTasks) {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	artifact, err := service.repository.GetWorkflowFileArtifact(projectID, workflowRunID, artifactID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if user == nil || !user.Admin && artifact.ProducerUserID != user.ID {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	task, err := service.identityStore.GetTask(projectID, artifact.TaskID)
	if err != nil {
		return db.WorkflowFileArtifactMetadata{}, err
	}
	if task.WorkflowRunID == nil || *task.WorkflowRunID != workflowRunID ||
		task.WorkflowNodeID == nil || *task.WorkflowNodeID != artifact.WorkflowNodeID ||
		task.AssignmentGeneration != artifact.Attempt || task.Status.IsFinished() {
		return db.WorkflowFileArtifactMetadata{}, pro_interfaces.ErrWorkflowFileArtifactConflict
	}
	return artifact, nil
}

func (service *workflowFileArtifactService) authorizeWorkflowRun(projectID int, workflowRunID int, user *db.User) (db.WorkflowRun, pro_interfaces.WorkflowAuthorizationState, error) {
	if user == nil || user.ID < 1 {
		return db.WorkflowRun{}, pro_interfaces.WorkflowAuthorizationState{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	run, err := service.workflowManager.GetWorkflowRunByID(projectID, workflowRunID)
	if err != nil {
		return db.WorkflowRun{}, pro_interfaces.WorkflowAuthorizationState{}, err
	}
	if user.Admin {
		return run, pro_interfaces.WorkflowAuthorizationState{}, nil
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(service.identityStore, projectID, user.ID)
	if err != nil || !pro_interfaces.AuthorizeWorkflowRead(run.DefinitionSnapshot, state.Identity, state.KnownRoles).Allowed {
		return db.WorkflowRun{}, pro_interfaces.WorkflowAuthorizationState{}, pro_interfaces.ErrWorkflowPermissionDenied
	}
	return run, state, nil
}

func (service *workflowFileArtifactService) knownProjectRoles(projectID int) (map[db.ProjectRoleReference]bool, error) {
	roles, err := service.identityStore.GetProjectRoles(projectID)
	if err != nil {
		return nil, err
	}
	roleIDs := make([]db.ProjectRoleID, 0, len(roles))
	for _, role := range roles {
		if role.ProjectID == nil || *role.ProjectID != projectID || role.Revision < 1 {
			return nil, db.ErrProjectWorkflowRoleIdentityUnavailable
		}
		roleIDs = append(roleIDs, role.ID)
	}
	return db.KnownProjectRoleReferences(roleIDs), nil
}

func (service *workflowFileArtifactService) effectiveRetention(projectID int) (db.WorkflowArtifactRetentionSnapshot, error) {
	global, globalFound, err := service.repository.GetWorkflowArtifactRetentionPolicy(db.WorkflowArtifactRetentionGlobal, nil)
	if err != nil {
		return db.WorkflowArtifactRetentionSnapshot{}, err
	}
	project, projectFound, err := service.repository.GetWorkflowArtifactRetentionPolicy(db.WorkflowArtifactRetentionProject, &projectID)
	if err != nil {
		return db.WorkflowArtifactRetentionSnapshot{}, err
	}
	if globalFound {
		if projectFound {
			return db.ResolveWorkflowArtifactRetention(global, &project)
		}
		return db.ResolveWorkflowArtifactRetention(global, nil)
	}
	effective := db.DefaultWorkflowArtifactRetentionSnapshot()
	if !projectFound {
		return effective, nil
	}
	if project.RetentionSeconds > effective.RetentionSeconds || project.MaxArtifactBytes > effective.MaxArtifactBytes || project.MaxRunBytes > effective.MaxRunBytes {
		return db.WorkflowArtifactRetentionSnapshot{}, pro_interfaces.ErrWorkflowArtifactRetentionConflict
	}
	effective.ProjectRevision = project.Revision
	effective.RetentionSeconds = project.RetentionSeconds
	effective.MaxArtifactBytes = project.MaxArtifactBytes
	effective.MaxRunBytes = project.MaxRunBytes
	return effective, effective.Validate()
}

func (service *workflowFileArtifactService) taskCredentialProvenance(projectID int, taskID int, attempt int) ([]db.WorkflowFileArtifactCredentialProvenance, error) {
	allowed := string(pro_interfaces.GlobalCredentialResolutionAllowed)
	usage, err := service.identityStore.GetTaskGlobalCredentialUsage(projectID, taskID, db.GlobalCredentialUsageQuery{
		Count: db.MaxWorkflowFileArtifactCredentials + 1, Outcome: &allowed, DispatchGeneration: &attempt,
	})
	if err != nil {
		return nil, err
	}
	provenance := make([]db.WorkflowFileArtifactCredentialProvenance, 0, len(usage))
	seen := make(map[string]bool, len(usage))
	for _, record := range usage {
		if record.DispatchGeneration != attempt {
			continue
		}
		key := record.Target + ":" + strconv.Itoa(record.CredentialID)
		if seen[key] {
			continue
		}
		seen[key] = true
		provenance = append(provenance, db.WorkflowFileArtifactCredentialProvenance{
			CredentialID: record.CredentialID, GrantID: record.GrantID,
			CredentialVersion: record.CredentialVersion, VersionFingerprint: record.VersionFingerprint,
			ProviderVersion: record.ProviderVersion, Target: record.Target,
		})
		if len(provenance) > db.MaxWorkflowFileArtifactCredentials {
			return nil, db.ErrInvalidOperation
		}
	}
	sort.Slice(provenance, func(left int, right int) bool {
		if provenance[left].CredentialID != provenance[right].CredentialID {
			return provenance[left].CredentialID < provenance[right].CredentialID
		}
		return provenance[left].Target < provenance[right].Target
	})
	return provenance, nil
}

func validateWorkflowFileArtifactServiceContext(ctx context.Context) error {
	if ctx == nil {
		return db.ErrInvalidOperation
	}
	return ctx.Err()
}
