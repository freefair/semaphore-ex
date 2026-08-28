package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
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
	status := workflowDB.WorkflowRunNodeStatusFromTaskStatus(task.Status)
	reason := ""
	if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeCanceled || status == db.WorkflowRunNodeStopped {
		reason = task.Message
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

func (s *workflowService) ResolveWorkflowApproval(
	int, int, int, int, db.WorkflowApprovalStatus, *db.User,
) (db.WorkflowApproval, error) {
	return db.WorkflowApproval{}, common_errors.NewValidationError("workflow approvals are not enabled")
}

func (s *workflowService) GetWorkflowRunArtifacts(int, int, *int) (map[string]any, error) {
	return map[string]any{}, nil
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
		if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeCanceled || status == db.WorkflowRunNodeStopped {
			reason = task.Message
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
