package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const workflowTriggerCredentialBytes = 48
const workflowTriggerIdempotencyRetention = 24 * time.Hour

type workflowTriggerService struct {
	repository      db.WorkflowTriggerManager
	workflowStore   pro_interfaces.WorkflowTriggerWorkflowStore
	workflowService pro_interfaces.WorkflowService
	identityStore   pro_interfaces.WorkflowTriggerIdentityStore
	capability      pro_interfaces.CapabilityProvider
	audit           pro_interfaces.AuditServiceFacade
}

var _ pro_interfaces.WorkflowTriggerService = (*workflowTriggerService)(nil)
var _ pro_interfaces.DeploymentWindowAuditConfigurer = (*workflowTriggerService)(nil)

func NewWorkflowTriggerService(
	repository db.WorkflowTriggerManager,
	workflowStore pro_interfaces.WorkflowTriggerWorkflowStore,
	workflowService pro_interfaces.WorkflowService,
	identityStore pro_interfaces.WorkflowTriggerIdentityStore,
	capability pro_interfaces.CapabilityProvider,
) pro_interfaces.WorkflowTriggerService {
	return &workflowTriggerService{
		repository: repository, workflowStore: workflowStore, workflowService: workflowService,
		identityStore: identityStore, capability: capability,
	}
}

func (s *workflowTriggerService) ConfigureDeploymentWindowAudit(audit pro_interfaces.AuditServiceFacade) {
	s.audit = audit
}

func (s *workflowTriggerService) List(
	ctx context.Context,
	projectID int,
	workflowID int,
	params db.RetrieveQueryParams,
	actor *db.User,
) ([]db.WorkflowTrigger, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionViewWorkflow); err != nil {
		return nil, err
	}
	return s.repository.GetWorkflowTriggers(projectID, workflowID, params)
}

func (s *workflowTriggerService) Get(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	actor *db.User,
) (db.WorkflowTrigger, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionViewWorkflow); err != nil {
		return db.WorkflowTrigger{}, err
	}
	return s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
}

func (s *workflowTriggerService) Create(
	ctx context.Context,
	projectID int,
	workflowID int,
	trigger db.WorkflowTrigger,
	actor *db.User,
) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionAdministerWorkflow); err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	current, err := s.repository.GetWorkflowTriggers(projectID, workflowID, db.RetrieveQueryParams{Count: db.MaxWorkflowTriggers})
	if err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	if len(current) >= db.MaxWorkflowTriggers {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, common_errors.NewValidationError("workflow trigger limit reached")
	}
	workflow, err := s.workflowStore.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	trigger.ID = 0
	trigger.ProjectID = projectID
	trigger.WorkflowTemplateID = workflowID
	trigger.OwnerUserID = actor.ID
	trigger.Revision = 0
	trigger.CredentialHash = ""
	trigger.CredentialGeneration = 0
	trigger.Created = time.Now().UTC()
	trigger.Updated = trigger.Created
	credential := ""
	if trigger.UsesCredential() {
		credential = db.WorkflowTriggerCredentialPrefix + random.String(workflowTriggerCredentialBytes)
		trigger.CredentialHash = db.HashWorkflowTriggerCredential(credential)
		trigger.CredentialGeneration = 1
	}
	if err = validateWorkflowTriggerConfiguration(trigger, workflow); err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	created, err := s.repository.CreateWorkflowTrigger(trigger)
	if err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	return pro_interfaces.WorkflowTriggerCredentialResult{Trigger: created, Credential: credential}, nil
}

func (s *workflowTriggerService) Update(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	requested db.WorkflowTrigger,
	actor *db.User,
) (db.WorkflowTrigger, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionAdministerWorkflow); err != nil {
		return db.WorkflowTrigger{}, err
	}
	current, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	if requested.Revision <= 0 {
		return db.WorkflowTrigger{}, common_errors.NewValidationError("workflow trigger revision is required")
	}
	if requested.Type != current.Type {
		return db.WorkflowTrigger{}, common_errors.NewValidationError("workflow trigger type is immutable")
	}
	workflow, err := s.workflowStore.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	current.Name = requested.Name
	current.Enabled = requested.Enabled
	current.CronFormat = requested.CronFormat
	current.InputMappings = requested.InputMappings
	current.InputMappingsJSON = ""
	if err = validateWorkflowTriggerConfiguration(current, workflow); err != nil {
		return db.WorkflowTrigger{}, err
	}
	return s.repository.UpdateWorkflowTrigger(current, requested.Revision)
}

func (s *workflowTriggerService) Delete(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	actor *db.User,
) error {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionAdministerWorkflow); err != nil {
		return err
	}
	return s.repository.DeleteWorkflowTrigger(projectID, workflowID, triggerID)
}

func (s *workflowTriggerService) SetEnabled(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	expectedRevision int,
	enabled bool,
	actor *db.User,
) (db.WorkflowTrigger, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionAdministerWorkflow); err != nil {
		return db.WorkflowTrigger{}, err
	}
	trigger, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return db.WorkflowTrigger{}, err
	}
	trigger.Enabled = enabled
	return s.repository.UpdateWorkflowTrigger(trigger, expectedRevision)
}

func (s *workflowTriggerService) RotateCredential(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	expectedRevision int,
	actor *db.User,
) (pro_interfaces.WorkflowTriggerCredentialResult, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionAdministerWorkflow); err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	trigger, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	if !trigger.UsesCredential() {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, common_errors.NewValidationError("workflow trigger does not use a credential")
	}
	credential := db.WorkflowTriggerCredentialPrefix + random.String(workflowTriggerCredentialBytes)
	trigger.CredentialHash = db.HashWorkflowTriggerCredential(credential)
	trigger.CredentialGeneration++
	updated, err := s.repository.UpdateWorkflowTrigger(trigger, expectedRevision)
	if err != nil {
		return pro_interfaces.WorkflowTriggerCredentialResult{}, err
	}
	return pro_interfaces.WorkflowTriggerCredentialResult{Trigger: updated, Credential: credential}, nil
}

func (s *workflowTriggerService) Test(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	requestValues map[string]json.RawMessage,
	actor *db.User,
) (pro_interfaces.WorkflowTriggerFireResult, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionStartWorkflow); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	trigger, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	return s.fire(ctx, trigger, actor, requestValues, nil, nil, time.Now().UTC())
}

func (s *workflowTriggerService) FireExternal(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	expectedType db.WorkflowTriggerType,
	credential string,
	idempotencyKey string,
	requestValues map[string]json.RawMessage,
) (pro_interfaces.WorkflowTriggerFireResult, error) {
	trigger, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if trigger.Type != expectedType || (expectedType != db.WorkflowTriggerAPI && expectedType != db.WorkflowTriggerWebhook) {
		return pro_interfaces.WorkflowTriggerFireResult{}, pro_interfaces.ErrWorkflowTriggerTypeMismatch
	}
	if !db.WorkflowTriggerCredentialMatches(trigger.CredentialHash, credential) {
		return pro_interfaces.WorkflowTriggerFireResult{}, pro_interfaces.ErrWorkflowTriggerCredentialRejected
	}
	requestHash := db.HashWorkflowTriggerRequestKey(trigger.ID, trigger.CredentialGeneration, idempotencyKey)
	if requestHash == "" {
		return pro_interfaces.WorkflowTriggerFireResult{}, common_errors.NewValidationError("workflow trigger idempotency key is invalid")
	}
	actor, err := s.identityStore.GetUser(trigger.OwnerUserID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if err = s.authorize(ctx, &actor, projectID, pro_interfaces.CapabilityAccessExecute, db.CanRunProjectTasks); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	return s.fire(ctx, trigger, &actor, requestValues, &requestHash, nil, time.Now().UTC())
}

func (s *workflowTriggerService) FireScheduled(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	scheduledAt time.Time,
) (pro_interfaces.WorkflowTriggerFireResult, error) {
	trigger, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if trigger.Type != db.WorkflowTriggerSchedule {
		return pro_interfaces.WorkflowTriggerFireResult{}, pro_interfaces.ErrWorkflowTriggerTypeMismatch
	}
	actor, err := s.identityStore.GetUser(trigger.OwnerUserID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if err = s.authorize(ctx, &actor, projectID, pro_interfaces.CapabilityAccessExecute, db.CanRunProjectTasks); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	workflow, err := s.workflowStore.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	identity := db.WorkflowTriggerScheduleOccurrenceIdentity(trigger.ID, trigger.Revision, workflow.Revision, scheduledAt)
	if identity == "" {
		return pro_interfaces.WorkflowTriggerFireResult{}, common_errors.NewValidationError("workflow trigger scheduled instant is invalid")
	}
	return s.fireWithWorkflow(ctx, trigger, workflow, &actor, nil, nil, &identity, scheduledAt.UTC(), scheduledAt.UTC())
}

func (s *workflowTriggerService) History(
	ctx context.Context,
	projectID int,
	workflowID int,
	triggerID int,
	params db.RetrieveQueryParams,
	actor *db.User,
) ([]db.WorkflowTriggerInvocation, error) {
	if err := s.authorizeWorkflow(ctx, actor, projectID, workflowID, pro_interfaces.PermissionViewWorkflow); err != nil {
		return nil, err
	}
	if _, err := s.repository.GetWorkflowTrigger(projectID, workflowID, triggerID); err != nil {
		return nil, err
	}
	return s.repository.GetWorkflowTriggerInvocations(projectID, triggerID, params)
}

func (s *workflowTriggerService) fire(
	ctx context.Context,
	trigger db.WorkflowTrigger,
	actor *db.User,
	requestValues map[string]json.RawMessage,
	requestHash *string,
	occurrenceIdentity *string,
	firedAt time.Time,
) (pro_interfaces.WorkflowTriggerFireResult, error) {
	workflow, err := s.workflowStore.GetWorkflowTemplate(trigger.ProjectID, trigger.WorkflowTemplateID)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	return s.fireWithWorkflow(ctx, trigger, workflow, actor, requestValues, requestHash, occurrenceIdentity, firedAt, time.Time{})
}

func (s *workflowTriggerService) fireWithWorkflow(
	_ context.Context,
	trigger db.WorkflowTrigger,
	workflow db.WorkflowTemplate,
	actor *db.User,
	requestValues map[string]json.RawMessage,
	requestHash *string,
	occurrenceIdentity *string,
	firedAt time.Time,
	scheduledAt time.Time,
) (pro_interfaces.WorkflowTriggerFireResult, error) {
	if err := db.ValidateWorkflowTriggerCanFire(trigger); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, common_errors.NewValidationError(err.Error())
	}
	if err := validateWorkflowTriggerConfiguration(trigger, workflow); err != nil {
		_ = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, firedAt, "rejected")
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	triggerValues, err := db.ResolveWorkflowTriggerValues(trigger, requestValues)
	if err != nil {
		_ = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, firedAt, "rejected")
		return pro_interfaces.WorkflowTriggerFireResult{}, common_errors.NewValidationError(err.Error())
	}
	effective, err := db.ResolveWorkflowParameters(workflow.ParameterDefinitions, triggerValues, nil)
	if err != nil {
		_ = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, firedAt, "rejected")
		return pro_interfaces.WorkflowTriggerFireResult{}, common_errors.NewValidationError(err.Error())
	}
	inputSnapshotJSON, err := json.Marshal(effective)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, fmt.Errorf("snapshot workflow trigger inputs: %w", err)
	}
	snapshot := db.WorkflowTriggerSnapshot{
		ID: trigger.ID, Revision: trigger.Revision, CredentialGeneration: trigger.CredentialGeneration,
		Name: trigger.Name, Type: trigger.Type, OwnerUserID: trigger.OwnerUserID, TriggeredAt: firedAt,
	}
	if !scheduledAt.IsZero() {
		scheduled := scheduledAt.UTC()
		snapshot.ScheduledAt = &scheduled
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, fmt.Errorf("snapshot workflow trigger: %w", err)
	}
	invocation := db.WorkflowTriggerInvocation{
		ProjectID: trigger.ProjectID, WorkflowTriggerID: trigger.ID,
		WorkflowTemplateID: trigger.WorkflowTemplateID, TriggerRevision: trigger.Revision,
		CredentialGeneration: trigger.CredentialGeneration, DefinitionRevision: workflow.Revision,
		RequestKeyHash: requestHash, OccurrenceIdentity: occurrenceIdentity,
		Status: db.WorkflowTriggerInvocationClaimed, ActorUserID: actor.ID,
		TriggerSnapshotJSON: string(snapshotJSON), InputSnapshotJSON: string(inputSnapshotJSON),
		Created: firedAt, Updated: firedAt,
	}
	if requestHash != nil {
		expires := firedAt.Add(workflowTriggerIdempotencyRetention)
		invocation.ExpiresAt = &expires
	}
	claimed, inserted, err := s.repository.ClaimWorkflowTriggerInvocation(invocation, firedAt)
	if err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if !inserted && claimed.RunID != nil {
		if existing, getErr := s.workflowStore.GetWorkflowRunByID(trigger.ProjectID, *claimed.RunID); getErr == nil {
			return pro_interfaces.WorkflowTriggerFireResult{Trigger: trigger, Invocation: claimed, Run: existing, Duplicate: true}, nil
		}
	}
	if !inserted && claimed.Status == db.WorkflowTriggerInvocationBlocked {
		return pro_interfaces.WorkflowTriggerFireResult{Trigger: trigger, Invocation: claimed, Duplicate: true}, nil
	}
	snapshot.InvocationID = claimed.ID
	correlationID := db.WorkflowTriggerInvocationCorrelationID(claimed)
	if correlationID == "" {
		correlationID = "wti_" + random.String(60)
	}
	run, startErr := s.workflowService.StartWorkflow(workflow, actor, correlationID, db.WorkflowRunInput{
		TriggerValues: triggerValues, TriggerSnapshot: &snapshot,
	})
	claimed.Updated = time.Now().UTC()
	if startErr != nil {
		var blocked *pro_interfaces.DeploymentWindowBlockedError
		if errors.As(startErr, &blocked) {
			blockedAt := claimed.Updated
			claimed.Status = db.WorkflowTriggerInvocationBlocked
			claimed.Result = "blocked"
			claimed.Reason = "deployment_window_blocked"
			claimed.DeploymentWindowDecisionID = &blocked.DecisionID
			claimed.NextEligibleAt = blocked.NextEligibleAt
			claimed.NextEligibleKnown = blocked.NextEligibleKnown
			claimed.BlockedAt = &blockedAt
			if err = s.repository.UpdateWorkflowTriggerInvocation(claimed); err != nil {
				return pro_interfaces.WorkflowTriggerFireResult{}, err
			}
			if err = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, claimed.Updated, "blocked"); err != nil {
				return pro_interfaces.WorkflowTriggerFireResult{}, err
			}
			s.recordBlockedDeploymentWindowInvocation(blocked)
			return pro_interfaces.WorkflowTriggerFireResult{Trigger: trigger, Invocation: claimed}, nil
		}
		claimed.Status = db.WorkflowTriggerInvocationFailed
		claimed.Result = "failed"
		claimed.Reason = startErr.Error()
		_ = s.repository.UpdateWorkflowTriggerInvocation(claimed)
		_ = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, claimed.Updated, "failed")
		return pro_interfaces.WorkflowTriggerFireResult{}, startErr
	}
	claimed.Status = db.WorkflowTriggerInvocationSucceeded
	claimed.Result = "started"
	claimed.RunID = &run.ID
	if err = s.repository.UpdateWorkflowTriggerInvocation(claimed); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	if err = s.repository.RecordWorkflowTriggerResult(trigger.ProjectID, trigger.ID, claimed.Updated, "started"); err != nil {
		return pro_interfaces.WorkflowTriggerFireResult{}, err
	}
	return pro_interfaces.WorkflowTriggerFireResult{Trigger: trigger, Invocation: claimed, Run: run, Duplicate: !inserted}, nil
}

func (s *workflowTriggerService) recordBlockedDeploymentWindowInvocation(blocked *pro_interfaces.DeploymentWindowBlockedError) {
	if s == nil || s.audit == nil || blocked == nil || !blocked.AuditInserted || blocked.AuditDecision == nil {
		return
	}
	event, err := pro_interfaces.NewDeploymentWindowAuditEvent(*blocked.AuditDecision, pro_interfaces.AuditActionDeploymentWindowBinding, "internal", blocked.AuditDecision.ActorUserID)
	if err != nil {
		return
	}
	if err = s.audit.Record(context.Background(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to record blocked workflow trigger deployment window audit event")
	}
}

func (s *workflowTriggerService) authorize(
	ctx context.Context,
	actor *db.User,
	projectID int,
	access pro_interfaces.CapabilityAccess,
	permission db.ProjectUserPermission,
) error {
	if actor == nil || actor.ID <= 0 {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	if s.capability == nil {
		return errors.New("workflow trigger capability provider is unavailable")
	}
	snapshot, err := s.capability.Resolve(ctx, pro_interfaces.CapabilityRequest{
		UserID: actor.ID, IsAdmin: actor.Admin, At: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err = snapshot.Require(pro_interfaces.CapabilityWorkflowTriggers, access); err != nil {
		return err
	}
	if permission == 0 || actor.Admin {
		return nil
	}
	member, err := s.identityStore.GetProjectUser(projectID, actor.ID)
	if err != nil {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	permissions := member.Role.GetPermissions()
	if !member.Role.IsValid() {
		role, getErr := s.identityStore.GetProjectOrGlobalRoleBySlug(projectID, string(member.Role))
		if getErr != nil {
			return pro_interfaces.ErrWorkflowTriggerPermissionDenied
		}
		permissions = role.Permissions
	}
	if permissions&permission != permission {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	return nil
}

func (s *workflowTriggerService) authorizeWorkflow(
	ctx context.Context,
	actor *db.User,
	projectID int,
	workflowID int,
	permission pro_interfaces.PermissionID,
) error {
	if err := s.authorize(ctx, actor, projectID, pro_interfaces.CapabilityAccessRead, 0); err != nil {
		return err
	}
	workflow, err := s.workflowStore.GetWorkflowTemplate(projectID, workflowID)
	if err != nil {
		return err
	}
	if actor != nil && actor.Admin {
		return nil
	}
	store, ok := s.identityStore.(pro_interfaces.WorkflowAuthorizationIdentityStore)
	if actor == nil {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	if !ok {
		// Narrow legacy test adapters do not expose the live resolver. Production
		// wiring supplies db.Store and therefore never takes this compatibility path.
		legacyPermission := db.ProjectUserPermission(0)
		if permission == pro_interfaces.PermissionStartWorkflow {
			legacyPermission = db.CanRunProjectTasks
		} else if permission == pro_interfaces.PermissionAdministerWorkflow {
			legacyPermission = db.CanManageProjectResources
		}
		return s.authorize(ctx, actor, projectID, pro_interfaces.CapabilityAccessRead, legacyPermission)
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(store, projectID, actor.ID)
	if err != nil {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	view := pro_interfaces.AuthorizeWorkflowTriggerRead(workflow, state.Identity, state.KnownRoles)
	if !view.Allowed {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	var decision pro_interfaces.WorkflowAccessDecision
	if permission == pro_interfaces.PermissionAdministerWorkflow {
		decision = pro_interfaces.AuthorizeWorkflowTriggerAdmin(workflow, state.Identity, state.KnownRoles)
	} else {
		decision = pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{
			Permission: permission, ProjectPermissions: state.Identity.Permissions,
			EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference}, KnownRoleReferences: state.KnownRoles,
			Policy: workflow.AccessPolicy,
		})
	}
	if !decision.Allowed {
		return pro_interfaces.ErrWorkflowTriggerPermissionDenied
	}
	return nil
}

func validateWorkflowTriggerConfiguration(trigger db.WorkflowTrigger, workflow db.WorkflowTemplate) error {
	if trigger.Type == db.WorkflowTriggerSchedule {
		if _, err := cron.ParseStandard(trigger.CronFormat); err != nil {
			return common_errors.NewValidationError("workflow trigger cron expression is invalid")
		}
	}
	if err := db.ValidateWorkflowTrigger(trigger, workflow.ParameterDefinitions); err != nil {
		return common_errors.NewValidationError(err.Error())
	}
	return nil
}
