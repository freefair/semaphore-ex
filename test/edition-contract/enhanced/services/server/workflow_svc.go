package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const workflowReconcileInterval = 2 * time.Second

type workflowService struct {
	repository      db.WorkflowManager
	templateStore   db.WorkflowTemplateValidationStore
	resultStore     db.WorkflowNodeResultStore
	enqueuer        pro_interfaces.WorkflowTaskEnqueuer
	locker          pro_interfaces.WorkflowRunLocker
	localRunLocks   workflowLocalLocks
	localStartLocks workflowLocalLocks
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

func NewWorkflowService(
	repository db.WorkflowManager,
	templateStore db.WorkflowTemplateValidationStore,
	enqueuer pro_interfaces.WorkflowTaskEnqueuer,
	locker pro_interfaces.WorkflowRunLocker,
) pro_interfaces.WorkflowService {
	resultStore, _ := templateStore.(db.WorkflowNodeResultStore)
	return &workflowService{
		repository: repository, templateStore: templateStore, resultStore: resultStore,
		enqueuer: enqueuer, locker: locker,
	}
}

func (s *workflowService) StartWorkflow(
	workflow db.WorkflowTemplate,
	user *db.User,
	correlationID string,
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
		templates := make(map[int]db.Template, len(workflow.Nodes))
		for _, node := range workflow.Nodes {
			if node.EffectiveKind() == db.WorkflowNodeNoteKind {
				continue
			}
			template, getErr := s.templateStore.GetTemplate(workflow.ProjectID, node.TemplateID)
			if getErr != nil {
				return getErr
			}
			templates[node.TemplateID] = template
		}
		snapshot, buildErr := workflowDB.BuildWorkflowRunSnapshot(workflow, templates, user.ID, correlationID, tz.Now())
		if buildErr != nil {
			return buildErr
		}
		result, err = s.repository.CreateWorkflowRun(snapshot)
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
	return s.withRunLock(projectID, runID, func() error {
		run, err := s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		if run.Status.IsFinished() {
			return nil
		}
		if err = s.syncWorkflowTaskStates(run); err != nil {
			return err
		}
		run, err = s.repository.GetWorkflowRunByID(projectID, runID)
		if err != nil {
			return err
		}
		return s.progressReadyWorkflowNodes(run, user)
	})
}

func (s *workflowService) progressReadyWorkflowNodes(run db.WorkflowRun, user *db.User) error {
	for iteration := 0; iteration <= len(run.Nodes); iteration++ {
		decisions, active, err := planWorkflowNodes(run)
		if err != nil {
			return err
		}
		changed := false
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
			updated, err := s.repository.FinalizeWorkflowRunNode(
				run.ProjectID, run.ID, decision.node.WorkflowNodeID, status,
				decision.reason, resultJSON, tz.Now(),
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
			if decision.kind != workflowNodeReady || slots == 0 {
				continue
			}
			if err := s.enqueueWorkflowNode(run, decision.node, user, decision.node.WorkflowNodeID == root.ID); err != nil {
				return err
			}
			slots--
		}

		current, err := s.repository.GetWorkflowRunByID(run.ProjectID, run.ID)
		if err != nil {
			return err
		}
		if status, reason, terminal := workflowRunTerminalStatus(current); terminal {
			return s.finishRun(current, status, reason)
		}
		if !changed {
			status := db.WorkflowRunQueued
			for _, node := range current.Nodes {
				if node.Status == db.WorkflowRunNodeRunning {
					status = db.WorkflowRunRunning
					break
				}
			}
			return s.setRunStatus(current, status, "", nil)
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
	if run.Status.IsFinished() {
		return run, nil
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
		if node.TaskID == nil {
			if _, cancelErr := s.repository.FinalizeWorkflowRunNode(projectID, runID, node.WorkflowNodeID, db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, tz.Now()); cancelErr != nil {
				return db.WorkflowRun{}, cancelErr
			}
		} else {
			if _, cancelErr := s.repository.UpdateWorkflowRunNodeFromTask(projectID, runID, node.WorkflowNodeID, *node.TaskID, db.WorkflowRunNodeCanceled, "Canceled because the workflow was stopped.", resultJSON, tz.Now()); cancelErr != nil {
				return db.WorkflowRun{}, cancelErr
			}
		}
	}
	if err = s.finishRun(run, db.WorkflowRunCanceled, "Canceled by user."); err != nil {
		return db.WorkflowRun{}, err
	}
	return s.repository.GetWorkflowRunByID(projectID, runID)
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
	int, int, int, int, db.WorkflowApprovalStatus, *db.User,
) (db.WorkflowApproval, error) {
	return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approvals are not enabled")
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
	if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeCanceled || status == db.WorkflowRunNodeStopped {
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
) error {
	now := tz.Now()
	if node.Status == db.WorkflowRunNodePending {
		claimed, err := s.repository.ClaimWorkflowRunNode(run.ProjectID, run.ID, node.WorkflowNodeID, now)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
	}
	existing, err := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID)
	if err == nil {
		return s.attachWorkflowTask(run, node, existing, root)
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
	created, enqueueErr := s.enqueuer.AddWorkflowTask(
		task, node.TemplateSnapshot, &actorID, username, run.ProjectID, node.TemplateSnapshot.App.NeedTaskAlias(),
	)
	if created.ID > 0 {
		if attachErr := s.attachWorkflowTask(run, node, created, root); attachErr != nil {
			return attachErr
		}
	}
	if enqueueErr != nil {
		existing, getErr := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, node.WorkflowNodeID)
		if getErr == nil {
			return s.attachWorkflowTask(run, node, existing, root)
		}
		return enqueueErr
	}
	return nil
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

func (s *workflowService) attachWorkflowTask(run db.WorkflowRun, node db.WorkflowRunNode, task db.Task, root bool) error {
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
	return s.setRunStatus(run, db.WorkflowRunQueued, "", nil)
}

func (s *workflowService) setRunStatus(run db.WorkflowRun, status db.WorkflowRunStatus, reason string, end *time.Time) error {
	run.Status = status
	run.Reason = reason
	run.End = end
	_, err := s.repository.UpdateWorkflowRunStatusUnless(run, terminalWorkflowRunStatuses())
	return err
}

func (s *workflowService) finishRun(run db.WorkflowRun, status db.WorkflowRunStatus, reason string) error {
	now := tz.Now()
	return s.setRunStatus(run, status, reason, &now)
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

func (s *workflowService) withRunLock(projectID, runID int, action func() error) error {
	if s.locker != nil {
		release, ok := s.locker.TryLockRun(projectID, runID)
		if !ok {
			return nil
		}
		defer release()
	}
	key := fmt.Sprintf("%d:%d", projectID, runID)
	return s.localRunLocks.withLock(key, action)
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
	runs, err := r.repository.GetActiveWorkflowRuns()
	if err != nil {
		return
	}
	for _, run := range runs {
		_ = r.service.ProgressWorkflowRun(run.ProjectID, run.ID, nil)
	}
}
