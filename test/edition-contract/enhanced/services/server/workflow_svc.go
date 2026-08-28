package server

import (
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
	return &workflowService{
		repository: repository, templateStore: templateStore, enqueuer: enqueuer, locker: locker,
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
		root, dependent, err := linearRunNodes(run)
		if err != nil {
			return err
		}
		switch root.Status {
		case db.WorkflowRunNodePending:
			return s.enqueueWorkflowNode(run, root, user, true)
		case db.WorkflowRunNodeQueued:
			if root.TaskID == nil {
				return s.enqueueWorkflowNode(run, root, user, true)
			}
			return s.setRunStatus(run, db.WorkflowRunQueued, "", nil)
		case db.WorkflowRunNodeRunning:
			return s.setRunStatus(run, db.WorkflowRunRunning, "", nil)
		case db.WorkflowRunNodeFailed:
			if _, err = s.repository.BlockWorkflowRunNode(projectID, runID, dependent.WorkflowNodeID, "Blocked because the preceding task failed.", tz.Now()); err != nil {
				return err
			}
			return s.finishRun(run, db.WorkflowRunFailed, taskFailureReason(root))
		case db.WorkflowRunNodeStopped:
			if _, err = s.repository.BlockWorkflowRunNode(projectID, runID, dependent.WorkflowNodeID, "Blocked because the preceding task stopped.", tz.Now()); err != nil {
				return err
			}
			return s.finishRun(run, db.WorkflowRunStopped, taskFailureReason(root))
		case db.WorkflowRunNodeSucceeded:
			switch dependent.Status {
			case db.WorkflowRunNodePending:
				return s.enqueueWorkflowNode(run, dependent, user, false)
			case db.WorkflowRunNodeQueued:
				if dependent.TaskID == nil {
					return s.enqueueWorkflowNode(run, dependent, user, false)
				}
				return s.setRunStatus(run, db.WorkflowRunQueued, "", nil)
			case db.WorkflowRunNodeRunning:
				return s.setRunStatus(run, db.WorkflowRunRunning, "", nil)
			case db.WorkflowRunNodeSucceeded:
				return s.finishRun(run, db.WorkflowRunSucceeded, "")
			case db.WorkflowRunNodeFailed:
				return s.finishRun(run, db.WorkflowRunFailed, taskFailureReason(dependent))
			case db.WorkflowRunNodeStopped:
				return s.finishRun(run, db.WorkflowRunStopped, taskFailureReason(dependent))
			}
		}
		return nil
	})
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
		if node.TaskID == nil && !node.Status.IsFinished() {
			if _, blockErr := s.repository.BlockWorkflowRunNode(projectID, runID, node.WorkflowNodeID, "Blocked because the workflow was stopped.", tz.Now()); blockErr != nil {
				return db.WorkflowRun{}, blockErr
			}
		}
	}
	if err = s.finishRun(run, db.WorkflowRunStopped, "Stopped by user."); err != nil {
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
	if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeStopped {
		reason = task.Message
	}
	if _, err := s.repository.UpdateWorkflowRunNodeFromTask(
		task.ProjectID, *task.WorkflowRunID, *task.WorkflowNodeID, task.ID, status, reason, tz.Now(),
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
		if status == db.WorkflowRunNodeFailed || status == db.WorkflowRunNodeStopped {
			reason = task.Message
		}
		if _, err = s.repository.UpdateWorkflowRunNodeFromTask(
			run.ProjectID, run.ID, node.WorkflowNodeID, task.ID, status, reason, tz.Now(),
		); err != nil {
			return err
		}
	}
	return nil
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
		db.WorkflowRunSucceeded, db.WorkflowRunSuccess, db.WorkflowRunFailed, db.WorkflowRunStopped, db.WorkflowRunBlocked,
	}
}

func linearRunNodes(run db.WorkflowRun) (db.WorkflowRunNode, db.WorkflowRunNode, error) {
	if len(run.DefinitionSnapshot.Edges) != 1 {
		return db.WorkflowRunNode{}, db.WorkflowRunNode{}, errors.New("workflow run snapshot is not linear")
	}
	edge := run.DefinitionSnapshot.Edges[0]
	var root, dependent *db.WorkflowRunNode
	for index := range run.Nodes {
		switch run.Nodes[index].WorkflowNodeID {
		case edge.SourceNodeID:
			root = &run.Nodes[index]
		case edge.DestinationNodeID:
			dependent = &run.Nodes[index]
		}
	}
	if root == nil || dependent == nil {
		return db.WorkflowRunNode{}, db.WorkflowRunNode{}, errors.New("workflow run node snapshot is incomplete")
	}
	return *root, *dependent, nil
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
