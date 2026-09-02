package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const workflowReconcileInterval = 2 * time.Second
const workflowReconcileQuarantineAfter = 3
const maxWorkflowReconciliationErrorBytes = 512

type workflowService struct {
	repository            db.WorkflowManager
	templateStore         db.WorkflowTemplateValidationStore
	resultStore           db.WorkflowNodeResultStore
	enqueuer              pro_interfaces.WorkflowTaskEnqueuer
	locker                pro_interfaces.WorkflowRunLocker
	progressionStore      pro_interfaces.WorkflowProgressionRepository
	versionStore          db.WorkflowVersionStore
	grantStore            db.CrossProjectTemplateGrantStore
	referenceStore        db.CrossProjectWorkflowReferenceStore
	resourceStore         db.WorkflowParameterValidationStore
	globalCredentialStore db.WorkflowGlobalCredentialValidationStore
	approvalIdentity      pro_interfaces.WorkflowApprovalIdentityStore
	authorizationStore    pro_interfaces.WorkflowAuthorizationIdentityStore
	audit                 pro_interfaces.AuditServiceFacade
	credentialReader      pro_interfaces.WorkflowCredentialReader
	localRunLocks         workflowLocalLocks
	localStartLocks       workflowLocalLocks
}

// NewWorkflowServiceWithAudit attaches the optional Enhanced audit facade to
// the workflow command service. Existing Community-compatible construction
// remains unchanged until the router wiring is updated.
func NewWorkflowServiceWithAudit(
	repository db.WorkflowManager,
	templateStore db.WorkflowTemplateValidationStore,
	enqueuer pro_interfaces.WorkflowTaskEnqueuer,
	locker pro_interfaces.WorkflowRunLocker,
	audit pro_interfaces.AuditServiceFacade,
	credentialReaders ...pro_interfaces.WorkflowCredentialReader,
) pro_interfaces.WorkflowService {
	service := NewWorkflowService(repository, templateStore, enqueuer, locker, credentialReaders...).(*workflowService)
	service.audit = audit
	return service
}

type workflowLocalLock struct {
	mutex sync.Mutex
	users int
}

type workflowLocalLocks struct {
	mutex   sync.Mutex
	entries map[string]*workflowLocalLock
}

var _ pro_interfaces.WorkflowService = (*workflowService)(nil)
var _ pro_interfaces.WorkflowAuditConfigurer = (*workflowService)(nil)

// ConfigureWorkflowAudit attaches the process-wide audit facade after route
// construction. It is intentionally optional at the interface boundary.
func (s *workflowService) ConfigureWorkflowAudit(audit pro_interfaces.AuditServiceFacade) {
	s.audit = audit
}

func NewWorkflowService(
	repository db.WorkflowManager,
	templateStore db.WorkflowTemplateValidationStore,
	enqueuer pro_interfaces.WorkflowTaskEnqueuer,
	locker pro_interfaces.WorkflowRunLocker,
	credentialReaders ...pro_interfaces.WorkflowCredentialReader,
) pro_interfaces.WorkflowService {
	resultStore, _ := templateStore.(db.WorkflowNodeResultStore)
	resourceStore, _ := templateStore.(db.WorkflowParameterValidationStore)
	globalCredentialStore, _ := templateStore.(db.WorkflowGlobalCredentialValidationStore)
	approvalIdentity, _ := templateStore.(pro_interfaces.WorkflowApprovalIdentityStore)
	authorizationStore, _ := templateStore.(pro_interfaces.WorkflowAuthorizationIdentityStore)
	progressionStore, _ := repository.(pro_interfaces.WorkflowProgressionRepository)
	versionStore, _ := repository.(db.WorkflowVersionStore)
	grantStore, _ := repository.(db.CrossProjectTemplateGrantStore)
	referenceStore, _ := repository.(db.CrossProjectWorkflowReferenceStore)
	var credentialReader pro_interfaces.WorkflowCredentialReader
	if len(credentialReaders) > 0 {
		credentialReader = credentialReaders[0]
	}
	if crossProjectStore, ok := repository.(pro_interfaces.CrossProjectWorkflowTaskStore); ok {
		if configurer, configured := enqueuer.(pro_interfaces.CrossProjectWorkflowTaskStoreConfigurer); configured {
			configurer.ConfigureCrossProjectWorkflowTaskStore(crossProjectStore)
		}
	}
	return &workflowService{
		repository: repository, templateStore: templateStore, resultStore: resultStore,
		resourceStore: resourceStore, globalCredentialStore: globalCredentialStore, approvalIdentity: approvalIdentity, authorizationStore: authorizationStore, credentialReader: credentialReader,
		enqueuer: enqueuer, locker: locker, progressionStore: progressionStore, versionStore: versionStore,
		grantStore: grantStore, referenceStore: referenceStore,
	}
}

func (s *workflowService) StartWorkflow(
	workflow db.WorkflowTemplate,
	user *db.User,
	correlationID string,
	inputs ...db.WorkflowRunInput,
) (db.WorkflowRun, error) {
	if user == nil || user.ID <= 0 {
		return db.WorkflowRun{}, common_errors.NewValidationError("workflow run actor is required")
	}
	if correlationID == "" {
		return db.WorkflowRun{}, common_errors.NewValidationError("workflow run correlation ID is required")
	}
	var result db.WorkflowRun
	err := s.withStartLock(workflow.ProjectID, workflow.ID, func() error {
		existing, err := s.repository.GetWorkflowRunByCorrelationID(workflow.ProjectID, workflow.ID, correlationID)
		if err == nil {
			result = existing
			return s.ProgressWorkflowRun(workflow.ProjectID, existing.ID, user)
		}
		if !errors.Is(err, db.ErrNotFound) {
			return err
		}
		if s.versionStore == nil {
			return errors.New("workflow version store is unavailable")
		}
		version, versionErr := s.versionStore.EnsureCurrentWorkflowVersion(workflow.ProjectID, workflow.ID)
		if versionErr != nil {
			return versionErr
		}
		fingerprint, fingerprintErr := pro_interfaces.WorkflowDefinitionFingerprint(workflow)
		if fingerprintErr != nil || version.ProjectID != workflow.ProjectID || version.WorkflowTemplateID != workflow.ID ||
			version.VersionNumber != workflow.Revision || fingerprint != version.ContentFingerprint {
			return pro_interfaces.ErrWorkflowRevisionConflict
		}
		workflow = version.DefinitionSnapshot
		if err = s.requireWorkflowAccess(workflow, user, pro_interfaces.PermissionStartWorkflow, true); err != nil {
			return err
		}
		crossProject, resolveErr := s.resolveCrossProjectTemplateProvenance(workflow, db.CrossProjectTemplateGrantRun)
		if resolveErr != nil {
			return resolveErr
		}
		templates := make(map[int]db.Template, len(workflow.Nodes))
		for _, node := range workflow.Nodes {
			if node.EffectiveKind() != db.WorkflowNodeTaskKind {
				continue
			}
			if node.CrossProjectTemplateReference != nil {
				continue
			}
			template, getErr := s.templateStore.GetTemplate(workflow.ProjectID, node.TemplateID)
			if getErr != nil {
				return getErr
			}
			templates[node.TemplateID] = template
		}
		snapshot, buildErr := workflowDB.BuildWorkflowRunSnapshotWithCrossProjectProvenance(workflow, templates, crossProject, user.ID, correlationID, tz.Now(), inputs...)
		if buildErr != nil {
			return buildErr
		}
		snapshot.WorkflowVersionID = version.ID
		if validateErr := s.validateWorkflowRunResources(snapshot); validateErr != nil {
			return validateErr
		}
		if validateErr := s.validateWorkflowParameterReferences(snapshot); validateErr != nil {
			return validateErr
		}
		if len(crossProject) > 0 {
			if s.referenceStore == nil {
				return errors.New("cross-project workflow reference store is unavailable")
			}
			result, err = s.referenceStore.CreateWorkflowRunWithCrossProjectReferences(snapshot)
		} else {
			result, err = s.repository.CreateWorkflowRun(snapshot)
		}
		if err != nil {
			return err
		}
		return s.ProgressWorkflowRun(workflow.ProjectID, result.ID, user)
	})
	if err != nil {
		return result, err
	}
	return s.repository.GetWorkflowRun(workflow.ProjectID, workflow.ID, result.ID)
}

func (s *workflowService) ProgressWorkflowRun(projectID int, runID int, user *db.User) error {
	return s.withRunLock(projectID, runID, func(lease *pro_interfaces.WorkflowReconciliationLease) error {
		run, err := s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		if run.Status.IsFinished() {
			return nil
		}
		if run.DesiredState == db.WorkflowRunDesiredStopping {
			_, err = s.stopWorkflowRunNow(run, lease)
			return err
		}
		if err = s.syncWorkflowTaskStates(run); err != nil {
			return err
		}
		run, err = s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		return s.progressReadyWorkflowNodes(run, user, lease)
	})
}

func (s *workflowService) progressReadyWorkflowNodes(run db.WorkflowRun, user *db.User, lease *pro_interfaces.WorkflowReconciliationLease) error {
	for iteration := 0; iteration <= len(run.Nodes); iteration++ {
		approvalChanged, err := s.reconcileWorkflowApprovals(run, lease)
		if err != nil {
			return err
		}
		if approvalChanged {
			run, err = s.repository.GetWorkflowRunByID(run.ProjectID, run.ID)
			if err != nil {
				return err
			}
		}
		decisions, active, err := planWorkflowNodes(run)
		if err != nil {
			return err
		}
		changed := approvalChanged
		for _, decision := range decisions {
			if decision.kind != workflowNodeSkipped && decision.kind != workflowNodeBlocked {
				continue
			}
			status := db.WorkflowRunNodeSkipped
			if decision.kind == workflowNodeBlocked {
				status = db.WorkflowRunNodeBlocked
			}
			resultJSON, err := marshalWorkflowNodeResult(db.WorkflowNodeResult{
				Status: status, Successful: status == db.WorkflowRunNodeSucceeded,
			})
			if err != nil {
				return err
			}
			updated, err := s.finalizeWorkflowRunNode(
				run, decision.node.WorkflowNodeID, status, decision.reason, resultJSON, tz.Now(), lease,
			)
			if err != nil {
				return err
			}
			changed = changed || updated
		}

		slots := run.DefinitionSnapshot.MaxParallelTasks - active
		if slots < 0 {
			slots = 0
		}
		root, rootErr := workflowDB.WorkflowRootNode(run.DefinitionSnapshot)
		if rootErr != nil {
			return rootErr
		}
		for _, decision := range decisions {
			if decision.kind != workflowNodeReady {
				continue
			}
			definitionNode, definitionErr := workflowDefinitionNode(run.DefinitionSnapshot, decision.node.WorkflowNodeID)
			if definitionErr != nil {
				return definitionErr
			}
			if definitionNode.EffectiveKind() == db.WorkflowNodeApprovalKind {
				opened, openErr := s.openWorkflowApproval(run, definitionNode, lease)
				if openErr != nil {
					return openErr
				}
				changed = changed || opened
				continue
			}
			if slots == 0 {
				continue
			}
			if err := s.enqueueWorkflowNode(run, decision.node, user, decision.node.WorkflowNodeID == root.ID, lease); err != nil {
				return err
			}
			slots--
		}

		current, err := s.repository.GetWorkflowRunByID(run.ProjectID, run.ID)
		if err != nil {
			return err
		}
		if status, reason, terminal := workflowRunTerminalStatus(current); terminal {
			return s.finishRun(current, status, reason, lease)
		}
		if !changed {
			status := db.WorkflowRunQueued
			for _, node := range current.Nodes {
				if node.Status == db.WorkflowRunNodeApproval {
					status = db.WorkflowRunApproval
					break
				}
				if node.Status == db.WorkflowRunNodeRunning {
					status = db.WorkflowRunRunning
					break
				}
			}
			return s.setRunStatus(current, status, "", nil, lease)
		}
		run = current
	}
	return fmt.Errorf("workflow readiness did not converge")
}

func (s *workflowService) StopWorkflowRun(projectID int, runID int, user *db.User) (db.WorkflowRun, error) {
	run, err := s.repository.GetWorkflowRunByID(projectID, runID)
	if err != nil {
		return db.WorkflowRun{}, err
	}
	if err = s.requireWorkflowAccess(run.DefinitionSnapshot, user, pro_interfaces.PermissionStopWorkflow, true); err != nil {
		return db.WorkflowRun{}, err
	}
	if _, err := s.RequestWorkflowRunStop(projectID, runID, user); err != nil {
		return db.WorkflowRun{}, err
	}
	return s.ReconcileWorkflowRun(projectID, runID)
}

func (s *workflowService) requireWorkflowAccess(
	workflow db.WorkflowTemplate,
	user *db.User,
	permission pro_interfaces.PermissionID,
	requireView bool,
) error {
	if user != nil && user.Admin {
		return nil
	}
	if user == nil || user.ID <= 0 || s.authorizationStore == nil {
		return common_errors.NewValidationError("workflow access is unavailable")
	}
	state, err := pro_interfaces.ResolveWorkflowAuthorizationState(s.authorizationStore, workflow.ProjectID, user.ID)
	if err != nil {
		return common_errors.NewValidationError("workflow access is denied")
	}
	if requireView && permission != pro_interfaces.PermissionViewWorkflow &&
		!pro_interfaces.AuthorizeWorkflowRead(workflow, state.Identity, state.KnownRoles).Allowed {
		return common_errors.NewValidationError("workflow access is denied")
	}
	decision := pro_interfaces.EvaluateWorkflowAccess(pro_interfaces.WorkflowAccessRequest{
		Permission: permission, ProjectPermissions: state.Identity.Permissions,
		EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference},
		KnownRoleReferences:     state.KnownRoles, Policy: workflow.AccessPolicy,
	})
	if !decision.Allowed {
		return common_errors.NewValidationError("workflow access is denied")
	}
	return nil
}

func (s *workflowService) resolveCrossProjectTemplateProvenance(
	workflow db.WorkflowTemplate,
	operation db.CrossProjectTemplateGrantOperation,
) (map[int]db.CrossProjectTemplateProvenance, error) {
	resolved := make(map[int]db.CrossProjectTemplateProvenance)
	for _, node := range workflow.Nodes {
		if node.CrossProjectTemplateReference == nil {
			continue
		}
		if s.grantStore == nil {
			return nil, errors.New("cross-project template grant store is unavailable")
		}
		normalized, version, err := s.grantStore.ResolveActiveCrossProjectTemplateGrant(workflow.ProjectID, *node.CrossProjectTemplateReference, operation)
		if err != nil {
			return nil, err
		}
		if normalized != *node.CrossProjectTemplateReference {
			return nil, db.ErrNotFound
		}
		provenance := db.CrossProjectTemplateProvenance{Reference: normalized, TemplateSnapshot: version.Snapshot}
		if err = provenance.Validate(); err != nil {
			return nil, err
		}
		resolved[node.ID] = provenance
	}
	return resolved, nil
}

func (s *workflowService) RequestWorkflowRunStop(projectID int, runID int, user *db.User) (db.WorkflowRun, error) {
	run, err := s.repository.GetWorkflowRunByID(projectID, runID)
	if err != nil {
		return db.WorkflowRun{}, err
	}
	if err = s.requireWorkflowAccess(run.DefinitionSnapshot, user, pro_interfaces.PermissionStopWorkflow, true); err != nil {
		return db.WorkflowRun{}, err
	}
	if run.Status.IsFinished() {
		return run, nil
	}
	if _, err = s.repository.RequestWorkflowRunStop(projectID, runID); err != nil {
		return db.WorkflowRun{}, err
	}
	return s.repository.GetWorkflowRunByID(projectID, runID)
}

func (s *workflowService) ReconcileWorkflowRun(projectID int, runID int) (db.WorkflowRun, error) {
	var result db.WorkflowRun
	err := s.withRunLock(projectID, runID, func(lease *pro_interfaces.WorkflowReconciliationLease) error {
		run, err := s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		if run.DesiredState == db.WorkflowRunDesiredStopping {
			result, err = s.stopWorkflowRunNow(run, lease)
			return err
		}
		if run.Status.IsFinished() {
			result = run
			return nil
		}
		if err = s.syncWorkflowTaskStates(run); err != nil {
			return err
		}
		run, err = s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		if err = s.progressReadyWorkflowNodes(run, nil, lease); err != nil {
			return err
		}
		result, err = s.repository.GetWorkflowRunByID(projectID, runID)
		return err
	})
	if err != nil {
		return db.WorkflowRun{}, err
	}
	if result.ID == 0 {
		return result, nil
	}
	if result.ReconciliationState != db.WorkflowRunReconciliationHealthy || result.ReconciliationAttempts != 0 || result.ReconciliationLastError != "" || result.ReconciliationNextRetryAt != nil || result.ReconciliationQuarantinedAt != nil {
		result.ReconciliationState = db.WorkflowRunReconciliationHealthy
		result.ReconciliationAttempts = 0
		result.ReconciliationLastError = ""
		result.ReconciliationNextRetryAt = nil
		result.ReconciliationQuarantinedAt = nil
		if err = s.repository.UpdateWorkflowRunReconciliation(result); err != nil {
			return db.WorkflowRun{}, err
		}
		return s.repository.GetWorkflowRunByID(projectID, runID)
	}
	return result, nil
}

func (s *workflowService) RetryWorkflowRunReconciliation(projectID int, runID int, user *db.User) (db.WorkflowRun, error) {
	var result db.WorkflowRun
	err := s.withRunLock(projectID, runID, func(_ *pro_interfaces.WorkflowReconciliationLease) error {
		run, err := s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		if err = s.requireWorkflowAccess(run.DefinitionSnapshot, user, pro_interfaces.PermissionAdministerWorkflow, false); err != nil {
			return err
		}
		if run.Status.IsFinished() {
			result = run
			return nil
		}
		run.ReconciliationState = db.WorkflowRunReconciliationRecovering
		run.ReconciliationAttempts = 0
		run.ReconciliationLastError = ""
		run.ReconciliationNextRetryAt = nil
		run.ReconciliationQuarantinedAt = nil
		if err = s.repository.UpdateWorkflowRunReconciliation(run); err != nil {
			return err
		}
		result = run
		return nil
	})
	return result, err
}

func (s *workflowService) stopWorkflowRunNow(run db.WorkflowRun, lease *pro_interfaces.WorkflowReconciliationLease) (db.WorkflowRun, error) {
	projectID, runID := run.ProjectID, run.ID
	now := tz.Now()
	approvals, err := s.repository.GetWorkflowApprovals(projectID, runID)
	if err != nil {
		return db.WorkflowRun{}, err
	}
	for _, approval := range approvals {
		if approval.Status != db.WorkflowApprovalPending {
			continue
		}
		approval.Status = db.WorkflowApprovalCanceled
		approval.DecisionSource = db.WorkflowApprovalDecisionSourceCancel
		approval.Resolved = &now
		approval.ResolvedByUserID = nil
		if _, resolveErr := s.repository.ResolveWorkflowApprovalIfPending(approval); resolveErr != nil {
			return db.WorkflowRun{}, resolveErr
		}
	}
	s.enqueuer.StopTasksByWorkflowRun(projectID, runID, true)
	for _, node := range run.Nodes {
		if node.Status.IsFinished() {
			continue
		}
		resultJSON, marshalErr := marshalWorkflowNodeResult(db.WorkflowNodeResult{Status: db.WorkflowRunNodeCanceled})
		if marshalErr != nil {
			return db.WorkflowRun{}, marshalErr
		}
		if node.Status == db.WorkflowRunNodeApproval {
			if _, cancelErr := s.finalizeWorkflowRunApprovalNode(run, node.WorkflowNodeID, db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, now, lease); cancelErr != nil {
				return db.WorkflowRun{}, cancelErr
			}
		} else if node.TaskID == nil {
			if _, cancelErr := s.finalizeWorkflowRunNode(run, node.WorkflowNodeID, db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, now, lease); cancelErr != nil {
				return db.WorkflowRun{}, cancelErr
			}
		} else {
			if _, cancelErr := s.repository.UpdateWorkflowRunNodeFromTask(projectID, runID, node.WorkflowNodeID, *node.TaskID, db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, now); cancelErr != nil {
				return db.WorkflowRun{}, cancelErr
			}
		}
	}
	run.DesiredState = db.WorkflowRunDesiredStopped
	if err = s.finishRun(run, db.WorkflowRunCanceled, "Canceled by user.", lease); err != nil {
		return db.WorkflowRun{}, err
	}
	stopped, err := s.repository.GetWorkflowRunByID(projectID, runID)
	if err != nil {
		return db.WorkflowRun{}, err
	}
	stopped.DesiredState = db.WorkflowRunDesiredStopped
	if err = s.repository.UpdateWorkflowRun(stopped); err != nil {
		return db.WorkflowRun{}, err
	}
	return s.repository.GetWorkflowRunByID(projectID, runID)
}

func (s *workflowService) GetWorkflowApprovalInbox(projectID int, user *db.User) ([]db.WorkflowApproval, error) {
	if user == nil || user.ID <= 0 {
		return nil, common_errors.NewValidationError("workflow approval actor is required")
	}
	approvals, err := s.repository.GetPendingWorkflowApprovals(projectID)
	if err != nil {
		return nil, err
	}
	inbox := make([]db.WorkflowApproval, 0, len(approvals))
	for _, approval := range approvals {
		if approval.RolePolicySnapshotRevision > 0 {
			contributionStore, supportsContributions := s.repository.(db.WorkflowApprovalContributionStore)
			if !supportsContributions {
				return nil, errors.New("workflow approval contribution store is unavailable")
			}
			contributions, contributionErr := contributionStore.GetWorkflowApprovalContributions(approval.ID)
			if contributionErr != nil {
				return nil, contributionErr
			}
			alreadyContributed := false
			for _, contribution := range contributions {
				if contribution.ActorUserID == user.ID {
					alreadyContributed = true
					break
				}
			}
			if alreadyContributed {
				continue
			}
			if s.authorizationStore == nil {
				return nil, errors.New("workflow authorization store is unavailable")
			}
			state, resolveErr := pro_interfaces.ResolveWorkflowAuthorizationState(s.authorizationStore, projectID, user.ID)
			if resolveErr != nil {
				continue
			}
			eligibility := pro_interfaces.EvaluateWorkflowApprovalEligibility(pro_interfaces.WorkflowApprovalEligibilityRequest{
				Policy: approval.RolePolicySnapshot.Policy, ActorUserID: user.ID,
				InitiatorUserID:         approval.RequestActorUserID,
				EffectiveRoleReferences: []db.ProjectRoleReference{state.Identity.Reference}, KnownRoleReferences: state.KnownRoles,
			})
			if eligibility.Allowed {
				inbox = append(inbox, approval)
			}
			continue
		}
		if err = s.authorizeWorkflowApproval(projectID, approval, user); err == nil {
			inbox = append(inbox, approval)
		}
	}
	return inbox, nil
}

func (s *workflowService) HandleWorkflowTaskCompletion(task db.Task) error {
	if task.WorkflowRunID == nil || task.WorkflowNodeID == nil || !task.Status.IsFinished() {
		return nil
	}
	status, reason, err := s.workflowTaskTerminalState(task)
	if err != nil {
		return err
	}
	resultJSON, err := s.workflowNodeResultJSON(task, status)
	if err != nil {
		return err
	}
	if _, err := s.repository.UpdateWorkflowRunNodeFromTask(
		task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, task.ID, status, reason, resultJSON, tz.Now(),
	); err != nil {
		return err
	}
	return s.ProgressWorkflowRun(task.ProjectID, *task.WorkflowRunID, nil)
}

func (s *workflowService) HandleWorkflowTaskOutputs(task db.Task, outputs map[string]json.RawMessage) error {
	if task.WorkflowRunID == nil || task.WorkflowNodeID == nil {
		return nil
	}
	run, err := s.repository.GetWorkflowRunByID(task.ProjectID, *task.WorkflowRunID)
	if err != nil {
		return err
	}
	definitionNode, err := workflowDefinitionNode(run.DefinitionSnapshot, *task.WorkflowNodeID)
	if err != nil {
		return err
	}
	if len(definitionNode.ArtifactOutputs) == 0 {
		return nil
	}
	artifacts := make([]db.WorkflowArtifact, 0, len(definitionNode.ArtifactOutputs))
	for _, declaration := range definitionNode.ArtifactOutputs {
		raw, exists := outputs[declaration.Name]
		artifact := workflowArtifactRecord(task, declaration)
		if !exists {
			artifact.Availability = db.WorkflowArtifactInvalid
			artifact.Diagnostic = "Declared workflow output was not produced."
			artifacts = append(artifacts, artifact)
			continue
		}
		artifact.SizeBytes = min(len(raw), db.MaxWorkflowArtifactObservedBytes)
		if err = db.ValidateWorkflowArtifactValue(declaration.Schema, raw, declaration.MaxBytes); err != nil {
			artifact.Availability = db.WorkflowArtifactInvalid
			artifact.Diagnostic = boundedWorkflowArtifactDiagnostic(err.Error())
			artifacts = append(artifacts, artifact)
			continue
		}
		canonical, compactErr := compactWorkflowArtifactJSON(raw)
		if compactErr != nil {
			return compactErr
		}
		artifact.Availability = db.WorkflowArtifactAvailable
		artifact.SizeBytes = len(canonical)
		if declaration.Sensitive {
			if !util.Config.AccessKeyEncryptionEnabled() {
				return errors.New("sensitive workflow outputs require access-key encryption")
			}
			artifact.EncryptedValue, err = util.Config.EncryptAccessSecret(canonical)
			if err != nil {
				return fmt.Errorf("encrypt sensitive workflow output: %w", err)
			}
		} else {
			artifact.ValueJSON = string(canonical)
		}
		artifacts = append(artifacts, artifact)
	}
	return s.repository.ReplaceWorkflowTaskArtifacts(
		task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, task.ID, task.AssignmentGeneration, artifacts,
	)
}

func (s *workflowService) ResolveWorkflowApproval(
	projectID int,
	workflowID int,
	runID int,
	nodeID int,
	decision db.WorkflowApprovalDecision,
	user *db.User,
) (db.WorkflowApproval, error) {
	if err := decision.Validate(); err != nil {
		return db.WorkflowApproval{}, err
	}
	if user == nil || user.ID <= 0 {
		return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approval actor is required")
	}
	run, err := s.repository.GetWorkflowRun(projectID, workflowID, runID)
	if err != nil {
		return db.WorkflowApproval{}, err
	}
	approval, err := s.repository.GetWorkflowApproval(projectID, run.ID, nodeID)
	if err != nil {
		return db.WorkflowApproval{}, err
	}
	if approval.Status != db.WorkflowApprovalPending {
		return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approval is already resolved")
	}
	var legacySnapshot *db.WorkflowApprovalRolePolicySnapshot
	if approval.RolePolicySnapshotRevision == 0 {
		node, nodeErr := workflowDefinitionNode(run.DefinitionSnapshot, nodeID)
		if nodeErr != nil {
			return db.WorkflowApproval{}, nodeErr
		}
		policy, policyErr := s.workflowApprovalPolicy(projectID, node)
		if policyErr != nil {
			return db.WorkflowApproval{}, policyErr
		}
		legacySnapshot = &db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: policy.Revision, Policy: policy}
	}
	return s.contributeWorkflowApproval(projectID, run, approval, decision, user, legacySnapshot)
}

func (s *workflowService) contributeWorkflowApproval(
	projectID int,
	run db.WorkflowRun,
	approval db.WorkflowApproval,
	decision db.WorkflowApprovalDecision,
	user *db.User,
	legacySnapshot *db.WorkflowApprovalRolePolicySnapshot,
) (db.WorkflowApproval, error) {
	if approval.RolePolicySnapshot.Policy.InitiatorSeparation && approval.RequestActorUserID == user.ID {
		s.recordWorkflowApprovalDeniedAudit(approval, &user.ID, pro_interfaces.AuditReasonWorkflowApprovalInitiatorSeparated)
		return db.WorkflowApproval{}, fmt.Errorf("%w: workflow approval cannot be self-approved", pro_interfaces.ErrWorkflowPermissionDenied)
	}
	if s.authorizationStore == nil {
		return db.WorkflowApproval{}, errors.New("workflow authorization store is unavailable")
	}
	store, ok := s.repository.(db.WorkflowApprovalContributionStore)
	if !ok {
		return db.WorkflowApproval{}, errors.New("workflow approval contribution store is unavailable")
	}
	result, err := store.SubmitWorkflowApprovalContribution(db.WorkflowApprovalContributionSubmission{
		ProjectID: projectID, WorkflowRunID: run.ID, WorkflowNodeID: approval.WorkflowNodeID,
		ActorUserID: user.ID, Decision: decision, CorrelationID: approval.CorrelationID,
		LegacySnapshot: legacySnapshot, At: tz.Now(),
	})
	if err != nil {
		return db.WorkflowApproval{}, err
	}
	if result.Committed && result.Contribution != nil {
		s.recordWorkflowApprovalAudit(approval, *result.Contribution)
	} else if result.TimedOut {
		s.recordWorkflowApprovalDeniedAudit(approval, nil, pro_interfaces.AuditReasonWorkflowApprovalTimedOut)
	} else if result.Denied {
		actorID := user.ID
		s.recordWorkflowApprovalDeniedAudit(approval, &actorID, pro_interfaces.AuditReasonWorkflowApprovalIneligible)
	}
	if result.Duplicate {
		return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approval already has a contribution from this user")
	}
	if result.TimedOut || !result.Committed && result.Terminal {
		return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approval is already resolved")
	}
	if result.Denied {
		return db.WorkflowApproval{}, fmt.Errorf("%w: workflow approval actor is not eligible", pro_interfaces.ErrWorkflowPermissionDenied)
	}
	if !result.Terminal {
		return result.Approval, nil
	}
	if err = s.ProgressWorkflowRun(projectID, run.ID, user); err != nil {
		return db.WorkflowApproval{}, err
	}
	return result.Approval, nil
}

func (s *workflowService) recordWorkflowApprovalDeniedAudit(
	approval db.WorkflowApproval,
	actorID *int,
	reason string,
) {
	if s.audit == nil {
		return
	}
	projectID := approval.ProjectID
	policyRevision := approval.RolePolicySnapshotRevision
	if policyRevision < 1 {
		policyRevision = 1
	}
	_ = s.audit.Record(context.Background(), pro_interfaces.AuditEvent{
		CorrelationID: workflowAuditCorrelationID(approval.CorrelationID), ActorID: actorID, ProjectID: &projectID,
		Action:     pro_interfaces.AuditActionWorkflowApprovalContribute,
		TargetType: pro_interfaces.AuditTargetWorkflowApproval,
		TargetID:   fmt.Sprintf("approval:%d", approval.ID), Outcome: pro_interfaces.AuditOutcomeDenied,
		Source: workflowApprovalAuditSource(actorID), Reason: reason,
		WorkflowPolicyRevision: policyRevision,
	})
}

func (s *workflowService) recordWorkflowApprovalAudit(
	approval db.WorkflowApproval,
	contribution db.WorkflowApprovalContribution,
) {
	if s.audit == nil {
		return
	}
	projectID := approval.ProjectID
	event := pro_interfaces.AuditEvent{
		CorrelationID: workflowAuditCorrelationID(approval.CorrelationID), ActorID: &contribution.ActorUserID, ProjectID: &projectID,
		Action:     pro_interfaces.AuditActionWorkflowApprovalContribute,
		TargetType: pro_interfaces.AuditTargetWorkflowApproval,
		TargetID:   fmt.Sprintf("approval:%d", approval.ID), Outcome: pro_interfaces.AuditOutcomeAllowed,
		Source: pro_interfaces.AuditSourceAPI,
		Reason: approvalAuditReason(contribution.Decision), WorkflowPolicyRevision: contribution.PolicyRevision,
		RoleProvenance: []pro_interfaces.AuditRoleProvenance{approvalAuditRoleProvenance(contribution)},
	}
	_ = s.audit.Record(context.Background(), event)
}

func workflowAuditCorrelationID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func workflowApprovalAuditSource(actorID *int) pro_interfaces.AuditSource {
	if actorID == nil {
		return pro_interfaces.AuditSourceWorker
	}
	return pro_interfaces.AuditSourceAPI
}

func approvalAuditReason(decision db.WorkflowApprovalStatus) string {
	if decision == db.WorkflowApprovalRejected {
		return pro_interfaces.AuditReasonWorkflowApprovalRejected
	}
	return pro_interfaces.AuditReasonWorkflowApprovalApproved
}

func approvalAuditRoleProvenance(contribution db.WorkflowApprovalContribution) pro_interfaces.AuditRoleProvenance {
	origin := pro_interfaces.AuditRoleOriginManual
	switch contribution.RoleOrigin {
	case db.WorkflowApprovalRoleOriginBuiltIn:
		origin = pro_interfaces.AuditRoleOriginBuiltin
	case db.WorkflowApprovalRoleOriginLDAP:
		origin = pro_interfaces.AuditRoleOriginLDAP
	case db.WorkflowApprovalRoleOriginOIDC:
		origin = pro_interfaces.AuditRoleOriginOIDC
	}
	return pro_interfaces.AuditRoleProvenance{
		RoleID: string(contribution.RoleID), RoleRevision: contribution.RoleRevision, Origin: origin,
		DirectoryProviderID: contribution.DirectoryProviderID, DirectoryMappingID: contribution.DirectoryMappingID,
		DirectoryMappingRevision: contribution.DirectoryMappingRevision, DirectoryRevisionFingerprint: contribution.DirectoryRevisionFingerprint,
	}
}

func contributionRoleOrigin(origin db.ProjectWorkflowRoleOrigin) db.WorkflowApprovalRoleOrigin {
	switch origin {
	case db.ProjectWorkflowRoleOriginBuiltIn:
		return db.WorkflowApprovalRoleOriginBuiltIn
	case db.ProjectWorkflowRoleOriginLDAP:
		return db.WorkflowApprovalRoleOriginLDAP
	case db.ProjectWorkflowRoleOriginOIDC:
		return db.WorkflowApprovalRoleOriginOIDC
	default:
		return db.WorkflowApprovalRoleOriginManual
	}
}

func (s *workflowService) openWorkflowApproval(run db.WorkflowRun, node db.WorkflowNode, lease *pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	now := tz.Now()
	prompt := "Approval required."
	if node.ApprovalMessage != nil && *node.ApprovalMessage != "" {
		prompt = *node.ApprovalMessage
	}
	var deadline *time.Time
	if node.ApprovalTimeout != nil {
		value := now.Add(time.Duration(*node.ApprovalTimeout) * time.Second)
		deadline = &value
	}
	policy, err := s.workflowApprovalPolicy(run.ProjectID, node)
	if err != nil {
		return false, err
	}
	snapshot := db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: policy.Revision, Policy: policy}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return false, fmt.Errorf("encode workflow approval role policy snapshot: %w", err)
	}
	approval := db.WorkflowApproval{
		ProjectID: run.ProjectID, WorkflowRunID: run.ID, WorkflowNodeID: node.ID,
		Status: db.WorkflowApprovalPending, Created: now, Deadline: deadline, Prompt: prompt,
		EligiblePermission: node.EffectiveApprovalPermission(), SeparationOfDuties: node.ApprovalSeparationOfDuties,
		RolePolicySnapshotJSON: string(snapshotJSON), RolePolicySnapshotRevision: snapshot.PolicyRevision,
		RolePolicySnapshot: snapshot,
		RequestActorUserID: run.ActorUserID, TimeoutOutcome: node.EffectiveApprovalTimeoutOutcome(),
		CorrelationID: fmt.Sprintf("%s:approval:%d", run.CorrelationID, node.ID),
	}
	var opened bool
	if lease != nil {
		if s.progressionStore == nil {
			return false, errors.New("workflow progression fencing is unavailable")
		}
		_, opened, err = s.progressionStore.OpenWorkflowApprovalFenced(*lease, approval)
	} else {
		_, opened, err = s.repository.OpenWorkflowApproval(approval)
	}
	return opened, err
}

func (s *workflowService) workflowApprovalPolicy(projectID int, node db.WorkflowNode) (db.WorkflowApprovalRolePolicy, error) {
	if len(node.ApprovalRolePolicy.RoleIDs) > 0 {
		policy := node.ApprovalRolePolicy
		if policy.Revision <= 0 {
			policy.Revision = node.ApprovalRolePolicyRevision
		}
		if err := policy.Validate(); err != nil {
			return db.WorkflowApprovalRolePolicy{}, err
		}
		return policy, nil
	}
	if s.authorizationStore == nil {
		return db.WorkflowApprovalRolePolicy{}, errors.New("workflow authorization store is unavailable")
	}
	roles, err := s.authorizationStore.GetProjectRoles(projectID)
	if err != nil {
		return db.WorkflowApprovalRolePolicy{}, err
	}
	references := make([]db.ProjectRoleReference, 0, len(roles)+4)
	for role, permissions := range db.BuiltInProjectRolePermissions() {
		if permissions.Can(node.EffectiveApprovalPermission()) {
			if reference, ok := db.ProjectRoleReferenceForBuiltInRole(role); ok {
				references = append(references, reference)
			}
		}
	}
	for _, role := range roles {
		if role.Permissions.Can(node.EffectiveApprovalPermission()) {
			references = append(references, db.ProjectRoleReferenceForCustomRole(role.ID))
		}
	}
	sort.Slice(references, func(i, j int) bool { return references[i] < references[j] })
	policy := db.WorkflowApprovalRolePolicy{
		Revision: 1, Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: references,
		MinimumDistinctApprovers: 1, InitiatorSeparation: node.ApprovalSeparationOfDuties,
	}
	if err := policy.Validate(); err != nil {
		return db.WorkflowApprovalRolePolicy{}, err
	}
	return policy, nil
}

func (s *workflowService) reconcileWorkflowApprovals(run db.WorkflowRun, lease *pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	approvals, err := s.repository.GetWorkflowApprovals(run.ProjectID, run.ID)
	if err != nil {
		return false, err
	}
	changed := false
	now := tz.Now()
	for _, approval := range approvals {
		if approval.Status == db.WorkflowApprovalPending && approval.Deadline != nil && !approval.Deadline.After(now) {
			approval.Status = db.WorkflowApprovalExpired
			approval.DecisionSource = db.WorkflowApprovalDecisionSourceTimeout
			approval.Resolved = &now
			approval.ResolvedByUserID = nil
			resolved, resolveErr := s.repository.ResolveWorkflowApprovalIfPending(approval)
			if resolveErr != nil {
				return false, resolveErr
			}
			if resolved {
				changed = true
				approval.Status = db.WorkflowApprovalExpired
			}
		}
		if approval.Status == db.WorkflowApprovalPending {
			continue
		}
		status, reason := workflowApprovalNodeOutcome(approval)
		resultJSON, marshalErr := marshalWorkflowNodeResult(db.WorkflowNodeResult{Status: status, Successful: status == db.WorkflowRunNodeSucceeded})
		if marshalErr != nil {
			return false, marshalErr
		}
		finalized, finalizeErr := s.finalizeWorkflowRunApprovalNode(run, approval.WorkflowNodeID, status, reason, resultJSON, now, lease)
		if finalizeErr != nil {
			return false, finalizeErr
		}
		changed = changed || finalized
	}
	return changed, nil
}

func workflowApprovalNodeOutcome(approval db.WorkflowApproval) (db.WorkflowRunNodeStatus, string) {
	switch approval.Status {
	case db.WorkflowApprovalApproved:
		return db.WorkflowRunNodeSucceeded, "Approved."
	case db.WorkflowApprovalExpired:
		if approval.TimeoutOutcome == db.WorkflowApprovalTimeoutApprove {
			return db.WorkflowRunNodeSucceeded, "Approved by timeout outcome."
		}
		return db.WorkflowRunNodeBlocked, "Approval expired."
	case db.WorkflowApprovalCanceled:
		return db.WorkflowRunNodeCanceled, "Approval canceled."
	default:
		return db.WorkflowRunNodeBlocked, "Approval rejected."
	}
}

func (s *workflowService) authorizeWorkflowApproval(projectID int, approval db.WorkflowApproval, user *db.User) error {
	if approval.SeparationOfDuties && approval.RequestActorUserID == user.ID {
		return common_errors.NewValidationError("workflow approval cannot be self-approved")
	}
	if user.Admin {
		return nil
	}
	if s.approvalIdentity == nil {
		return errors.New("workflow approval identity store is unavailable")
	}
	member, err := s.approvalIdentity.GetProjectUser(projectID, user.ID)
	if err != nil {
		return common_errors.NewValidationError("workflow approval actor is not eligible")
	}
	permissions := member.Role.GetPermissions()
	if !member.Role.IsValid() {
		role, roleErr := s.approvalIdentity.GetProjectOrGlobalRoleBySlug(projectID, string(member.Role))
		if roleErr != nil {
			return common_errors.NewValidationError("workflow approval actor is not eligible")
		}
		permissions = role.Permissions
	}
	if permissions&approval.EligiblePermission != approval.EligiblePermission {
		return common_errors.NewValidationError("workflow approval actor is not eligible")
	}
	return nil
}

func (s *workflowService) GetWorkflowRunArtifacts(projectID int, runID int, _ *int) ([]db.WorkflowArtifactMetadata, error) {
	run, err := s.repository.GetWorkflowRunByID(projectID, runID)
	if err != nil {
		return nil, err
	}
	stored, err := s.repository.GetWorkflowRunArtifacts(projectID, runID)
	if err != nil {
		return nil, err
	}
	result := make([]db.WorkflowArtifactMetadata, 0)
	for _, definitionNode := range run.DefinitionSnapshot.Nodes {
		if len(definitionNode.ArtifactOutputs) == 0 {
			continue
		}
		runNode, nodeErr := workflowRunNode(run, definitionNode.ID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		producerTaskID := 0
		producerAttempt := 0
		var taskID *int
		var attempt *int
		if runNode.TaskID != nil {
			producer, getErr := s.repository.GetWorkflowRunNodeTask(projectID, runID, definitionNode.ID)
			if getErr != nil {
				return nil, getErr
			}
			producerTaskID = producer.ID
			producerAttempt = producer.AssignmentGeneration
			taskID = &producerTaskID
			attempt = &producerAttempt
		}
		for _, declaration := range definitionNode.ArtifactOutputs {
			reference := db.WorkflowArtifactReference{
				Name: declaration.Name, SourceNodeID: definitionNode.ID, Output: declaration.Name, Required: true,
			}
			metadata := db.WorkflowArtifactMetadata{
				WorkflowNodeID: definitionNode.ID, Name: declaration.Name, Schema: declaration.Schema,
				Sensitive: declaration.Sensitive, Availability: db.WorkflowArtifactUnavailable,
				Fingerprint: db.WorkflowArtifactReferenceFingerprint(
					runID, definitionNode.ID, producerTaskID, producerAttempt, reference,
				),
				ProducerTaskID: taskID, ProducerAttempt: attempt,
			}
			if artifact, found := currentWorkflowArtifact(
				stored, definitionNode.ID, producerTaskID, producerAttempt, declaration.Name,
			); found {
				metadata.Availability = artifact.Availability
				metadata.SizeBytes = artifact.SizeBytes
				metadata.Fingerprint = artifact.Fingerprint
				metadata.Diagnostic = artifact.Diagnostic
			}
			result = append(result, metadata)
		}
	}
	return result, nil
}

func (s *workflowService) syncWorkflowTaskStates(run db.WorkflowRun) error {
	for _, node := range run.Nodes {
		if node.TaskID == nil || node.Status.IsFinished() {
			continue
		}
		task, err := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		status := workflowDB.WorkflowRunNodeStatusFromTaskStatus(task.Status)
		reason := ""
		if task.Status.IsFinished() {
			status, reason, err = s.workflowTaskTerminalState(task)
			if err != nil {
				return err
			}
		}
		resultJSON, resultErr := s.workflowNodeResultJSON(task, status)
		if resultErr != nil {
			return resultErr
		}
		if _, err = s.repository.UpdateWorkflowRunNodeFromTask(
			run.ProjectID, run.ID, node.WorkflowNodeID, task.ID, status, reason, resultJSON, tz.Now(),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *workflowService) workflowTaskTerminalState(task db.Task) (db.WorkflowRunNodeStatus, string, error) {
	status := workflowDB.WorkflowRunNodeStatusFromTaskStatus(task.Status)
	reason := ""
	if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeCanceled || status == db.WorkflowRunNodeStopped || status == db.WorkflowRunNodeBlocked {
		reason = task.Message
	}
	artifactFailure, err := s.ensureWorkflowTaskArtifacts(task, status == db.WorkflowRunNodeSucceeded)
	if err != nil {
		return "", "", err
	}
	if artifactFailure != "" && status == db.WorkflowRunNodeSucceeded {
		return db.WorkflowRunNodeFailed, artifactFailure, nil
	}
	return status, reason, nil
}

func (s *workflowService) ensureWorkflowTaskArtifacts(task db.Task, successful bool) (string, error) {
	if task.WorkflowRunID == nil || task.WorkflowNodeID == nil {
		return "", nil
	}
	run, err := s.repository.GetWorkflowRunByID(task.ProjectID, *task.WorkflowRunID)
	if err != nil {
		return "", err
	}
	definitionNode, err := workflowDefinitionNode(run.DefinitionSnapshot, *task.WorkflowNodeID)
	if err != nil || len(definitionNode.ArtifactOutputs) == 0 {
		return "", err
	}
	stored, err := s.repository.GetWorkflowRunArtifacts(task.ProjectID, run.ID)
	if err != nil {
		return "", err
	}
	byName := make(map[string]db.WorkflowArtifact, len(definitionNode.ArtifactOutputs))
	for _, artifact := range stored {
		if artifact.WorkflowNodeID == *task.WorkflowNodeID && artifact.TaskID == task.ID && artifact.Attempt == task.AssignmentGeneration {
			byName[artifact.Name] = artifact
		}
	}
	complete := make([]db.WorkflowArtifact, 0, len(definitionNode.ArtifactOutputs))
	failure := ""
	changed := false
	for _, declaration := range definitionNode.ArtifactOutputs {
		artifact, exists := byName[declaration.Name]
		if !exists {
			changed = true
			artifact = workflowArtifactRecord(task, declaration)
			artifact.Availability = db.WorkflowArtifactUnavailable
			artifact.Diagnostic = "Declared workflow output was not produced."
			if successful {
				artifact.Availability = db.WorkflowArtifactInvalid
			}
		}
		complete = append(complete, artifact)
		if successful && artifact.Availability != db.WorkflowArtifactAvailable && failure == "" {
			failure = fmt.Sprintf("Workflow output %q is %s.", declaration.Name, artifact.Availability)
		}
	}
	if changed {
		if err = s.repository.ReplaceWorkflowTaskArtifacts(
			task.ProjectID, run.ID, *task.WorkflowNodeID, task.ID, task.AssignmentGeneration, complete,
		); err != nil {
			return "", err
		}
	}
	return boundedWorkflowArtifactDiagnostic(failure), nil
}

func (s *workflowService) workflowNodeResultJSON(task db.Task, status db.WorkflowRunNodeStatus) (string, error) {
	result := db.WorkflowNodeResult{Status: status, Successful: status == db.WorkflowRunNodeSucceeded}
	if s.resultStore != nil {
		summary, err := s.resultStore.GetTaskSummary(task.ProjectID, task.ID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return "", fmt.Errorf("load workflow task summary: %w", err)
		}
		if err == nil {
			result.Summary = &db.WorkflowNodeResultSummary{
				State: summary.State, ExpectedHosts: summary.ExpectedHosts, TotalHosts: summary.TotalHosts,
				OkHosts: summary.OkHosts, FailedHosts: summary.FailedHosts,
			}
		}
	}
	return marshalWorkflowNodeResult(result)
}

func marshalWorkflowNodeResult(result db.WorkflowNodeResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode workflow node result: %w", err)
	}
	return string(encoded), nil
}

func (s *workflowService) enqueueWorkflowNode(
	run db.WorkflowRun,
	node db.WorkflowRunNode,
	user *db.User,
	root bool,
	lease *pro_interfaces.WorkflowReconciliationLease,
) error {
	now := tz.Now()
	if node.Status == db.WorkflowRunNodePending || lease != nil && node.Status == db.WorkflowRunNodeQueued && node.TaskID == nil {
		var claimed bool
		var err error
		if lease != nil {
			if s.progressionStore == nil {
				return errors.New("workflow progression fencing is unavailable")
			}
			claimed, err = s.progressionStore.ClaimWorkflowRunNodeFenced(*lease, node.WorkflowNodeID, now)
		} else {
			claimed, err = s.repository.ClaimWorkflowRunNode(run.ProjectID, run.ID, node.WorkflowNodeID, now)
		}
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
	}
	existing, err := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID)
	if err == nil {
		return s.attachWorkflowTask(run, node, existing, root, lease)
	}
	if !errors.Is(err, db.ErrNotFound) {
		return err
	}
	definitionNode, err := workflowDefinitionNode(run.DefinitionSnapshot, node.WorkflowNodeID)
	if err != nil {
		return err
	}
	task := db.Task{TemplateID: node.TemplateID}
	if definitionNode.TaskParams != nil {
		task = definitionNode.TaskParams.CreateTask(node.TemplateID)
	}
	task.WorkflowRunID = &run.ID
	task.WorkflowNodeID = &node.WorkflowNodeID
	applyWorkflowNodeOverride(&task, node.OverrideSnapshot)
	if err := s.applyWorkflowRunParameters(run, definitionNode.OverridePolicy, &task); err != nil {
		return err
	}
	if node.CrossProjectTemplateProvenance != nil && task.InventoryID != nil {
		return s.blockCrossProjectWorkflowNode(run, node, "Cross-project template resource overrides are unavailable.", lease)
	}
	blocked, err := s.resolveWorkflowTaskInputs(run, node, definitionNode, &task)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}
	actorID := run.ActorUserID
	username := ""
	if user != nil && user.ID == actorID {
		username = user.Username
	}
	var created db.Task
	var enqueueErr error
	if node.CrossProjectTemplateProvenance != nil {
		if accessErr := s.requireCrossProjectDispatchAccess(run, user); accessErr != nil {
			return s.blockCrossProjectWorkflowNode(run, node, "Cross-project template authorization is no longer available.", lease)
		}
		crossProjectEnqueuer, ok := s.enqueuer.(pro_interfaces.CrossProjectWorkflowTaskFencedEnqueuer)
		if !ok {
			return errors.New("cross-project workflow task fencing is unavailable")
		}
		created, enqueueErr = crossProjectEnqueuer.AddCrossProjectWorkflowTaskFenced(
			task, *node.CrossProjectTemplateProvenance, &actorID, username, run.ProjectID, lease,
		)
	} else if lease != nil {
		fencedEnqueuer, ok := s.enqueuer.(pro_interfaces.WorkflowTaskFencedEnqueuer)
		if !ok {
			return errors.New("workflow task fencing is unavailable")
		}
		created, enqueueErr = fencedEnqueuer.AddWorkflowTaskFenced(
			task, node.TemplateSnapshot, &actorID, username, run.ProjectID,
			node.TemplateSnapshot.App.NeedTaskAlias(), *lease,
		)
	} else {
		created, enqueueErr = s.enqueuer.AddWorkflowTask(
			task, node.TemplateSnapshot, &actorID, username, run.ProjectID, node.TemplateSnapshot.App.NeedTaskAlias(),
		)
	}
	if created.ID > 0 {
		if attachErr := s.attachWorkflowTask(run, node, created, root, lease); attachErr != nil {
			return attachErr
		}
	}
	if enqueueErr != nil {
		if node.CrossProjectTemplateProvenance != nil && errors.Is(enqueueErr, db.ErrNotFound) {
			return s.blockCrossProjectWorkflowNode(run, node, "Cross-project template grant is no longer active.", lease)
		}
		existing, getErr := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID)
		if getErr == nil {
			return s.attachWorkflowTask(run, node, existing, root, lease)
		}
		return enqueueErr
	}
	return nil
}

func (s *workflowService) requireCrossProjectDispatchAccess(run db.WorkflowRun, _ *db.User) error {
	users, ok := s.templateStore.(interface {
		GetUser(int) (db.User, error)
	})
	if !ok {
		return errors.New("workflow dispatch actor lookup is unavailable")
	}
	actor, err := users.GetUser(run.ActorUserID)
	if err != nil {
		return err
	}
	return s.requireWorkflowAccess(run.DefinitionSnapshot, &actor, pro_interfaces.PermissionStartWorkflow, true)
}

func (s *workflowService) blockCrossProjectWorkflowNode(
	run db.WorkflowRun,
	node db.WorkflowRunNode,
	reason string,
	lease *pro_interfaces.WorkflowReconciliationLease,
) error {
	resultJSON, err := marshalWorkflowNodeResult(db.WorkflowNodeResult{Status: db.WorkflowRunNodeBlocked})
	if err != nil {
		return err
	}
	_, err = s.finalizeWorkflowRunNode(run, node.WorkflowNodeID, db.WorkflowRunNodeBlocked, reason, resultJSON, tz.Now(), lease)
	return err
}

func applyWorkflowNodeOverride(task *db.Task, override db.WorkflowNodeOverride) {
	if override.InventoryID != nil {
		value := *override.InventoryID
		task.InventoryID = &value
	}
	if override.Arguments != nil {
		value := *override.Arguments
		task.Arguments = &value
	}
	if override.GitBranch != nil {
		value := *override.GitBranch
		task.GitBranch = &value
	}
}

func (s *workflowService) validateWorkflowRunResources(run db.WorkflowRun) error {
	for _, node := range run.Nodes {
		override := node.OverrideSnapshot
		if override.InventoryID == nil && override.EnvironmentIDs == nil {
			continue
		}
		if s.resourceStore == nil {
			return common_errors.NewValidationError("workflow node override resources are unavailable")
		}
		if override.InventoryID != nil {
			inventory, err := s.resourceStore.GetInventory(run.ProjectID, *override.InventoryID)
			if errors.Is(err, db.ErrNotFound) || err == nil &&
				(inventory.ID != *override.InventoryID || inventory.ProjectID != run.ProjectID) {
				return common_errors.NewValidationError(fmt.Sprintf(
					"workflow node %d inventory override is unavailable", node.WorkflowNodeID,
				))
			}
			if err != nil {
				return fmt.Errorf("validate workflow inventory override: %w", err)
			}
		}
		if override.EnvironmentIDs != nil {
			for _, environmentID := range *override.EnvironmentIDs {
				environment, err := s.resourceStore.GetEnvironment(run.ProjectID, environmentID)
				if errors.Is(err, db.ErrNotFound) || err == nil &&
					(environment.ID != environmentID || environment.ProjectID != run.ProjectID) {
					return common_errors.NewValidationError(fmt.Sprintf(
						"workflow node %d environment override is unavailable", node.WorkflowNodeID,
					))
				}
				if err != nil {
					return fmt.Errorf("validate workflow environment override: %w", err)
				}
			}
		}
	}
	return nil
}

func (s *workflowService) validateWorkflowParameterReferences(run db.WorkflowRun) error {
	for _, name := range sortedWorkflowParameterNames(run.ParameterSnapshot) {
		snapshot := run.ParameterSnapshot[name]
		if snapshot.SecretReference == nil {
			continue
		}
		if snapshot.SecretReference.GlobalCredentialID > 0 {
			if _, _, err := s.workflowParameterGlobalCredential(
				run.ProjectID, *snapshot.SecretReference,
				db.GlobalCredentialGrantOperationReference|db.GlobalCredentialGrantOperationConsume,
			); err != nil {
				return err
			}
		} else if _, err := s.workflowParameterAccessKey(run.ProjectID, *snapshot.SecretReference); err != nil {
			return err
		}
	}
	return nil
}

func (s *workflowService) applyWorkflowRunParameters(
	run db.WorkflowRun,
	policy db.WorkflowNodeOverridePolicy,
	task *db.Task,
) error {
	nodePlain, err := decodeWorkflowArtifactObject(task.Environment)
	if err != nil {
		return fmt.Errorf("decode workflow node environment override: %w", err)
	}
	nodeSecret, err := decodeWorkflowArtifactObject(task.Secret)
	if err != nil {
		return fmt.Errorf("decode workflow node secret override: %w", err)
	}
	plain := make(map[string]json.RawMessage, len(run.ParameterSnapshot)+len(nodePlain))
	secret := make(map[string]json.RawMessage, len(run.ParameterSnapshot)+len(nodeSecret))
	bindings := make(map[string]int, len(task.GlobalCredentialBindings)+len(run.ParameterSnapshot))
	for name, credentialID := range task.GlobalCredentialBindings {
		bindings[name] = credentialID
	}
	for _, name := range sortedWorkflowParameterNames(run.ParameterSnapshot) {
		snapshot := run.ParameterSnapshot[name]
		if snapshot.SecretReference == nil {
			plain[name] = append(json.RawMessage(nil), snapshot.Value...)
			continue
		}
		if !workflowStringContains(policy.CredentialParameters, name) {
			continue
		}
		if snapshot.SecretReference.GlobalCredentialID > 0 {
			credential, _, credentialErr := s.workflowParameterGlobalCredential(
				run.ProjectID, *snapshot.SecretReference,
				db.GlobalCredentialGrantOperationReference|db.GlobalCredentialGrantOperationConsume,
			)
			if credentialErr != nil {
				return credentialErr
			}
			bindings[name] = credential.ID
			delete(secret, name)
			delete(plain, name)
			continue
		}
		key, keyErr := s.workflowParameterAccessKey(run.ProjectID, *snapshot.SecretReference)
		if keyErr != nil {
			return keyErr
		}
		if s.credentialReader == nil {
			return errors.New("workflow secret references cannot be resolved")
		}
		if err = s.credentialReader.DeserializeSecret(&key); err != nil {
			return errors.New("workflow secret reference could not be resolved")
		}
		if key.String == "" {
			return errors.New("workflow secret reference is empty")
		}
		encoded, marshalErr := json.Marshal(key.String)
		key.String = ""
		if marshalErr != nil {
			return errors.New("workflow secret reference could not be encoded")
		}
		secret[name] = encoded
		delete(bindings, name)
	}
	// Existing node TaskParams are the final definition-level override and
	// therefore win over workflow defaults, trigger values, and user values.
	for name, value := range nodePlain {
		plain[name] = value
		delete(secret, name)
		delete(bindings, name)
	}
	for name, value := range nodeSecret {
		secret[name] = value
		delete(plain, name)
		delete(bindings, name)
	}
	task.Environment, err = encodeWorkflowArtifactObject(plain)
	if err != nil {
		return err
	}
	task.Secret, err = encodeWorkflowArtifactObject(secret)
	if err != nil {
		return err
	}
	task.GlobalCredentialBindings = bindings
	return task.EncodeGlobalCredentialBindings()
}

func workflowStringContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *workflowService) workflowParameterAccessKey(
	projectID int,
	reference db.WorkflowSecretReference,
) (db.AccessKey, error) {
	if s.resourceStore == nil || reference.AccessKeyID <= 0 {
		return db.AccessKey{}, errors.New("workflow secret reference is unavailable")
	}
	key, err := s.resourceStore.GetAccessKey(projectID, reference.AccessKeyID)
	if err != nil || key.ProjectID == nil || *key.ProjectID != projectID || key.Type != db.AccessKeyString || key.Owner != db.AccessKeyShared {
		return db.AccessKey{}, errors.New("workflow secret reference is unavailable")
	}
	return key, nil
}

func (s *workflowService) workflowParameterGlobalCredential(
	projectID int,
	reference db.WorkflowSecretReference,
	operation db.GlobalCredentialGrantOperation,
) (db.GlobalCredential, db.GlobalCredentialGrant, error) {
	if s.globalCredentialStore == nil || reference.GlobalCredentialID <= 0 || reference.AccessKeyID > 0 {
		return db.GlobalCredential{}, db.GlobalCredentialGrant{}, errors.New("workflow global credential reference is unavailable")
	}
	credential, err := s.globalCredentialStore.GetGlobalCredential(reference.GlobalCredentialID)
	if err != nil || credential.Type != db.GlobalCredentialTypeString || !credential.Enabled {
		return db.GlobalCredential{}, db.GlobalCredentialGrant{}, errors.New("workflow global credential reference is unavailable")
	}
	grant, err := s.globalCredentialStore.GetGlobalCredentialGrantForProject(credential.ID, projectID)
	if err != nil || !grant.IsEffectiveAt(projectID, operation, credential.Enabled, time.Now().UTC()) {
		return db.GlobalCredential{}, db.GlobalCredentialGrant{}, errors.New("workflow global credential grant is unavailable")
	}
	return credential, grant, nil
}

func sortedWorkflowParameterNames(values map[string]db.WorkflowParameterSnapshot) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (s *workflowService) resolveWorkflowTaskInputs(
	run db.WorkflowRun,
	node db.WorkflowRunNode,
	definitionNode db.WorkflowNode,
	task *db.Task,
) (bool, error) {
	if len(definitionNode.ArtifactInputs) == 0 {
		return false, nil
	}
	artifacts, err := s.repository.GetWorkflowRunArtifacts(run.ProjectID, run.ID)
	if err != nil {
		return false, err
	}
	plain, err := decodeWorkflowArtifactObject(task.Environment)
	if err != nil {
		return false, fmt.Errorf("decode workflow task environment: %w", err)
	}
	secret, err := decodeWorkflowArtifactObject(task.Secret)
	if err != nil {
		return false, fmt.Errorf("decode workflow task secret inputs: %w", err)
	}
	snapshots := make([]db.WorkflowArtifactInputSnapshot, 0, len(definitionNode.ArtifactInputs))
	missingRequired := make([]string, 0)
	for _, reference := range definitionNode.ArtifactInputs {
		sourceDefinition, sourceErr := workflowDefinitionNode(run.DefinitionSnapshot, reference.SourceNodeID)
		if sourceErr != nil {
			return false, sourceErr
		}
		declaration, declarationErr := workflowArtifactDeclaration(sourceDefinition, reference.Output)
		if declarationErr != nil {
			return false, declarationErr
		}
		sourceRunNode, sourceErr := workflowRunNode(run, reference.SourceNodeID)
		if sourceErr != nil {
			return false, sourceErr
		}
		snapshot := db.WorkflowArtifactInputSnapshot{
			Name: reference.Name, SourceNodeID: reference.SourceNodeID, Output: reference.Output,
			Sensitive: declaration.Sensitive, Required: reference.Required,
			Availability: db.WorkflowArtifactUnavailable,
		}
		producerTaskID := 0
		producerAttempt := 0
		if sourceRunNode.TaskID != nil {
			producer, getErr := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, reference.SourceNodeID)
			if getErr != nil {
				return false, getErr
			}
			producerTaskID = producer.ID
			producerAttempt = producer.AssignmentGeneration
			snapshot.ProducerTaskID = &producerTaskID
			snapshot.ProducerAttempt = &producerAttempt
		}
		snapshot.ReferenceFingerprint = db.WorkflowArtifactReferenceFingerprint(
			run.ID, reference.SourceNodeID, producerTaskID, producerAttempt, reference,
		)
		artifact, available := currentWorkflowArtifact(
			artifacts, reference.SourceNodeID, producerTaskID, producerAttempt, reference.Output,
		)
		if available {
			snapshot.Availability = artifact.Availability
			if artifact.Sensitive != declaration.Sensitive || !reflect.DeepEqual(artifact.Schema, declaration.Schema) {
				return false, errors.New("stored workflow artifact metadata does not match the run definition")
			}
			if artifact.Availability == db.WorkflowArtifactAvailable {
				value := []byte(artifact.ValueJSON)
				if declaration.Sensitive {
					value, err = util.Config.DecryptAccessSecret(artifact.EncryptedValue)
					if err != nil {
						return false, fmt.Errorf("decrypt sensitive workflow input: %w", err)
					}
					secret[reference.Name] = append(json.RawMessage(nil), value...)
					delete(plain, reference.Name)
				} else {
					plain[reference.Name] = append(json.RawMessage(nil), value...)
					delete(secret, reference.Name)
				}
			}
		}
		if snapshot.Availability != db.WorkflowArtifactAvailable && reference.Required {
			missingRequired = append(missingRequired, reference.Name)
		}
		snapshots = append(snapshots, snapshot)
	}
	snapshotJSON, err := json.Marshal(snapshots)
	if err != nil {
		return false, fmt.Errorf("encode workflow artifact input snapshot: %w", err)
	}
	updated, err := s.repository.UpdateWorkflowRunNodeArtifactInputs(
		run.ProjectID, run.ID, node.WorkflowNodeID, string(snapshotJSON),
	)
	if err != nil {
		return false, err
	}
	if !updated {
		return false, errors.New("workflow artifact input snapshot update conflict")
	}
	if len(missingRequired) > 0 {
		reason := boundedWorkflowArtifactDiagnostic(fmt.Sprintf(
			"Required workflow inputs are unavailable: %v.", missingRequired,
		))
		resultJSON, marshalErr := marshalWorkflowNodeResult(db.WorkflowNodeResult{Status: db.WorkflowRunNodeBlocked})
		if marshalErr != nil {
			return false, marshalErr
		}
		blocked, blockErr := s.repository.FinalizeWorkflowRunNode(
			run.ProjectID, run.ID, node.WorkflowNodeID, db.WorkflowRunNodeBlocked, reason, resultJSON, tz.Now(),
		)
		if blockErr != nil {
			return false, blockErr
		}
		return blocked, nil
	}
	task.Environment, err = encodeWorkflowArtifactObject(plain)
	if err != nil {
		return false, err
	}
	task.Secret, err = encodeWorkflowArtifactObject(secret)
	return false, err
}

func workflowArtifactRecord(task db.Task, declaration db.WorkflowArtifactDeclaration) db.WorkflowArtifact {
	reference := db.WorkflowArtifactReference{
		Name: declaration.Name, SourceNodeID: *task.WorkflowNodeID, Output: declaration.Name, Required: true,
	}
	return db.WorkflowArtifact{
		Name: declaration.Name, Schema: declaration.Schema, Sensitive: declaration.Sensitive,
		Fingerprint: db.WorkflowArtifactReferenceFingerprint(
			*task.WorkflowRunID, *task.WorkflowNodeID, task.ID, task.AssignmentGeneration, reference,
		),
	}
}

func workflowArtifactDeclaration(node db.WorkflowNode, name string) (db.WorkflowArtifactDeclaration, error) {
	for _, declaration := range node.ArtifactOutputs {
		if declaration.Name == name {
			return declaration, nil
		}
	}
	return db.WorkflowArtifactDeclaration{}, db.ErrNotFound
}

func workflowRunNode(run db.WorkflowRun, nodeID int) (db.WorkflowRunNode, error) {
	for _, node := range run.Nodes {
		if node.WorkflowNodeID == nodeID {
			return node, nil
		}
	}
	return db.WorkflowRunNode{}, db.ErrNotFound
}

func currentWorkflowArtifact(
	artifacts []db.WorkflowArtifact,
	nodeID int,
	taskID int,
	attempt int,
	name string,
) (db.WorkflowArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.WorkflowNodeID == nodeID && artifact.TaskID == taskID && artifact.Attempt == attempt && artifact.Name == name {
			return artifact, true
		}
	}
	return db.WorkflowArtifact{}, false
}

func decodeWorkflowArtifactObject(encoded string) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage)
	if encoded == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func encodeWorkflowArtifactObject(value map[string]json.RawMessage) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode workflow task inputs: %w", err)
	}
	return string(encoded), nil
}

func compactWorkflowArtifactJSON(raw json.RawMessage) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, raw); err != nil {
		return nil, errors.New("compact workflow artifact JSON")
	}
	return buffer.Bytes(), nil
}

func boundedWorkflowArtifactDiagnostic(value string) string {
	if len(value) <= db.MaxWorkflowArtifactDiagnostic {
		return value
	}
	return value[:db.MaxWorkflowArtifactDiagnostic]
}

func (s *workflowService) attachWorkflowTask(run db.WorkflowRun, node db.WorkflowRunNode, task db.Task, root bool, leases ...*pro_interfaces.WorkflowReconciliationLease) error {
	attached, err := s.repository.AttachWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID, task.ID)
	if err != nil {
		return err
	}
	if !attached {
		current, getErr := s.repository.GetWorkflowRunNode(run.ProjectID, run.ID, node.WorkflowNodeID)
		if getErr != nil {
			return getErr
		}
		if current.TaskID == nil || *current.TaskID != task.ID {
			return fmt.Errorf("workflow node task attachment conflict")
		}
	}
	if root {
		if _, err = s.repository.SetWorkflowRunRootTask(run.ProjectID, run.ID, task.ID); err != nil {
			return err
		}
	}
	var lease *pro_interfaces.WorkflowReconciliationLease
	if len(leases) > 0 {
		lease = leases[0]
	}
	return s.setRunStatus(run, db.WorkflowRunQueued, "", nil, lease)
}

func (s *workflowService) setRunStatus(run db.WorkflowRun, status db.WorkflowRunStatus, reason string, end *time.Time, leases ...*pro_interfaces.WorkflowReconciliationLease) error {
	run.Status = status
	run.Reason = reason
	run.End = end
	if len(leases) > 0 && leases[0] != nil {
		if s.progressionStore == nil {
			return errors.New("workflow progression fencing is unavailable")
		}
		_, err := s.progressionStore.UpdateWorkflowRunStatusUnlessFenced(*leases[0], run, terminalWorkflowRunStatuses())
		return err
	}
	_, err := s.repository.UpdateWorkflowRunStatusUnless(run, terminalWorkflowRunStatuses())
	return err
}

func (s *workflowService) finishRun(run db.WorkflowRun, status db.WorkflowRunStatus, reason string, leases ...*pro_interfaces.WorkflowReconciliationLease) error {
	now := tz.Now()
	return s.setRunStatus(run, status, reason, &now, leases...)
}

func (s *workflowService) finalizeWorkflowRunNode(run db.WorkflowRun, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time, lease *pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if lease != nil {
		if s.progressionStore == nil {
			return false, errors.New("workflow progression fencing is unavailable")
		}
		return s.progressionStore.FinalizeWorkflowRunNodeFenced(*lease, nodeID, status, reason, resultJSON, at)
	}
	return s.repository.FinalizeWorkflowRunNode(run.ProjectID, run.ID, nodeID, status, reason, resultJSON, at)
}

func (s *workflowService) finalizeWorkflowRunApprovalNode(run db.WorkflowRun, nodeID int, status db.WorkflowRunNodeStatus, reason string, resultJSON string, at time.Time, lease *pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	if lease != nil {
		if s.progressionStore == nil {
			return false, errors.New("workflow progression fencing is unavailable")
		}
		return s.progressionStore.FinalizeWorkflowRunApprovalNodeFenced(*lease, nodeID, status, reason, resultJSON, at)
	}
	return s.repository.FinalizeWorkflowRunApprovalNode(run.ProjectID, run.ID, nodeID, status, reason, resultJSON, at)
}

func terminalWorkflowRunStatuses() []db.WorkflowRunStatus {
	return []db.WorkflowRunStatus{
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunCanceled, db.WorkflowRunBlocked,
	}
}

func workflowDefinitionNode(workflow db.WorkflowTemplate, nodeID int) (db.WorkflowNode, error) {
	for _, node := range workflow.Nodes {
		if node.ID == nodeID {
			return node, nil
		}
	}
	return db.WorkflowNode{}, db.ErrNotFound
}

func taskFailureReason(node db.WorkflowRunNode) string {
	if node.Reason != "" {
		return node.Reason
	}
	return fmt.Sprintf("Workflow node %d ended with status %s.", node.WorkflowNodeID, node.Status)
}

func (s *workflowService) withRunLock(projectID, runID int, action func(*pro_interfaces.WorkflowReconciliationLease) error) error {
	var lease *pro_interfaces.WorkflowReconciliationLease
	if s.locker != nil {
		claimed, release, ok, err := s.locker.TryLockRun(projectID, runID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		defer release()
		lease = &claimed
	}
	key := fmt.Sprintf("%d:%d", projectID, runID)
	return s.localRunLocks.withLock(key, func() error {
		if err := action(lease); err != nil {
			return err
		}
		if lease != nil {
			return s.locker.RecordReconciled(*lease)
		}
		return nil
	})
}

func (s *workflowService) withStartLock(projectID, workflowID int, action func() error) error {
	if s.locker != nil {
		release, ok := s.locker.TryLockStart(projectID, workflowID)
		if !ok {
			return common_errors.NewValidationError("workflow start is already in progress")
		}
		defer release()
	}
	key := fmt.Sprintf("%d:%d", projectID, workflowID)
	return s.localStartLocks.withLock(key, action)
}

func (locks *workflowLocalLocks) withLock(key string, action func() error) error {
	locks.mutex.Lock()
	if locks.entries == nil {
		locks.entries = make(map[string]*workflowLocalLock)
	}
	entry := locks.entries[key]
	if entry == nil {
		entry = &workflowLocalLock{}
		locks.entries[key] = entry
	}
	entry.users++
	locks.mutex.Unlock()

	entry.mutex.Lock()
	defer func() {
		entry.mutex.Unlock()
		locks.mutex.Lock()
		entry.users--
		if entry.users == 0 {
			delete(locks.entries, key)
		}
		locks.mutex.Unlock()
	}()
	return action()
}

type workflowReconciler struct {
	repository db.WorkflowManager
	service    pro_interfaces.WorkflowService
	stop       chan struct{}
	done       chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
}

func NewWorkflowReconciler(repository db.WorkflowManager, service pro_interfaces.WorkflowService) pro_interfaces.WorkflowReconciler {
	return &workflowReconciler{repository: repository, service: service, stop: make(chan struct{}), done: make(chan struct{})}
}

func (r *workflowReconciler) Start() {
	r.startOnce.Do(func() {
		go func() {
			defer close(r.done)
			r.reconcile()
			ticker := time.NewTicker(workflowReconcileInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					r.reconcile()
				case <-r.stop:
					return
				}
			}
		}()
	})
}

func (r *workflowReconciler) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.startOnce.Do(func() { close(r.done) })
	<-r.done
}

func (r *workflowReconciler) reconcile() {
	var runs []db.WorkflowRun
	var err error
	if fairScanner, ok := r.repository.(pro_interfaces.WorkflowRunFairScanner); ok {
		runs, err = fairScanner.GetActiveWorkflowRunsFair()
	} else {
		runs, err = r.repository.GetActiveWorkflowRuns()
	}
	if err != nil {
		return
	}
	for _, run := range runs {
		if _, err = r.service.ReconcileWorkflowRun(run.ProjectID, run.ID); err != nil {
			r.recordFailure(run, err)
		}
	}
}

func (r *workflowReconciler) recordFailure(run db.WorkflowRun, reconcileErr error) {
	if run.ReconciliationState == db.WorkflowRunReconciliationQuarantined {
		return
	}
	now := tz.Now()
	run.ReconciliationAttempts++
	run.ReconciliationLastError = boundedWorkflowReconciliationError(reconcileErr.Error())
	if run.ReconciliationAttempts >= workflowReconcileQuarantineAfter {
		run.ReconciliationState = db.WorkflowRunReconciliationQuarantined
		run.ReconciliationQuarantinedAt = &now
		run.ReconciliationNextRetryAt = nil
	} else {
		run.ReconciliationState = db.WorkflowRunReconciliationRecovering
		delay := workflowReconcileInterval * time.Duration(1<<(run.ReconciliationAttempts-1))
		next := now.Add(delay)
		run.ReconciliationNextRetryAt = &next
	}
	_ = r.repository.UpdateWorkflowRunReconciliation(run)
}

func boundedWorkflowReconciliationError(value string) string {
	value = strings.Join(strings.Fields(redactText(value)), " ")
	if len(value) <= maxWorkflowReconciliationErrorBytes {
		return value
	}
	value = value[:maxWorkflowReconciliationErrorBytes]
	for !utf8.ValidString(value) {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}
