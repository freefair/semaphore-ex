package server

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowServiceRunsTwoNodesInOrderFromImmutableSnapshot(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "successful-run")
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunQueued, run.Status)
	require.Len(t, fixture.enqueuer.tasks, 1)
	require.NotNil(t, run.RootTaskID)
	assert.Equal(t, fixture.workflow.Nodes[0].ID, *fixture.enqueuer.tasks[0].WorkflowNodeID)
	assert.Equal(t, db.WorkflowRunNodePending, run.Nodes[1].Status)

	duplicate, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "successful-run")
	require.NoError(t, err)
	assert.Equal(t, run.ID, duplicate.ID)
	assert.Len(t, fixture.enqueuer.tasks, 1, "a duplicate start must reuse the same root task")

	editedWorkflow := fixture.workflow
	editedWorkflow.Name = "Edited after start"
	_, err = fixture.repository.UpdateWorkflowTemplate(editedWorkflow)
	require.NoError(t, err)
	fixture.second.Playbook = "edited-second.yml"
	require.NoError(t, fixture.store.UpdateTemplate(fixture.second))

	rootTask := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	restartedService := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil)
	require.NoError(t, restartedService.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	require.Len(t, fixture.enqueuer.tasks, 2)
	assert.Equal(t, "second.yml", fixture.enqueuer.templates[1].Playbook)
	require.NoError(t, restartedService.HandleWorkflowTaskCompletion(rootTask))
	require.NoError(t, restartedService.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	assert.Len(t, fixture.enqueuer.tasks, 2, "retries and duplicate callbacks must not create another task")

	running, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "Linear", running.DefinitionSnapshot.Name)
	assert.Equal(t, "second.yml", running.Nodes[1].TemplateSnapshot.Playbook)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, running.Nodes[0].Status)
	assert.Equal(t, db.WorkflowRunNodeQueued, running.Nodes[1].Status)

	dependentTask := finishWorkflowTask(t, fixture.store, running.Nodes[1], task_logger.TaskSuccessStatus, "")
	require.NoError(t, restartedService.HandleWorkflowTaskCompletion(dependentTask))
	completed, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunSucceeded, completed.Status)
	assert.NotNil(t, completed.End)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, completed.Nodes[1].Status)
	assert.Len(t, fixture.enqueuer.tasks, 2)
}

func TestWorkflowServiceBlocksDependentNodeAfterFirstFailure(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "failed-run")
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.tasks, 1)

	rootTask := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskFailStatus, "first task failed")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))

	failed, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunFailed, failed.Status)
	assert.Equal(t, "first task failed", failed.Reason)
	assert.Equal(t, db.WorkflowRunNodeFailed, failed.Nodes[0].Status)
	assert.Equal(t, db.WorkflowRunNodeBlocked, failed.Nodes[1].Status)
	assert.Len(t, fixture.enqueuer.tasks, 1, "the dependent task must never be created after a root failure")
}

func TestWorkflowServiceRecoversAnEnqueueRetryWithoutDuplicatingTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.enqueuer.failAfterCreate = true

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "retry-run")
	require.NoError(t, err)
	require.NotNil(t, run.RootTaskID)
	assert.Len(t, fixture.enqueuer.tasks, 1)

	restartedService := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil)
	require.NoError(t, restartedService.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	assert.Len(t, fixture.enqueuer.tasks, 1)
}

func TestWorkflowReconcilerCanStopBeforeStart(t *testing.T) {
	reconciler := NewWorkflowReconciler(nil, nil)
	reconciler.Stop()
	reconciler.Start()
}

func TestWorkflowServiceSerializesAndReleasesLocalLocks(t *testing.T) {
	service := NewWorkflowService(nil, nil, nil, nil).(*workflowService)
	const calls = 64
	completed := 0
	var wait sync.WaitGroup
	errors := make(chan error, calls)
	wait.Add(calls)
	for range calls {
		go func() {
			defer wait.Done()
			errors <- service.withRunLock(1, 1, func() error {
				completed++
				return nil
			})
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	assert.Equal(t, calls, completed)
	assertWorkflowLockTableEmpty(t, &service.localRunLocks)
	for workflowID := 1; workflowID <= calls; workflowID++ {
		require.NoError(t, service.withStartLock(1, workflowID, func() error { return nil }))
	}
	assertWorkflowLockTableEmpty(t, &service.localStartLocks)
}

func assertWorkflowLockTableEmpty(t *testing.T, locks *workflowLocalLocks) {
	t.Helper()
	locks.mutex.Lock()
	defer locks.mutex.Unlock()
	assert.Empty(t, locks.entries)
}

type workflowServiceFixture struct {
	store      *coresql.SqlDb
	repository *workflowSQL.WorkflowStoreImpl
	service    pro_interfaces.WorkflowService
	enqueuer   *workflowTestEnqueuer
	projectID  int
	user       db.User
	workflow   db.WorkflowTemplate
	first      db.Template
	second     db.Template
}

func newWorkflowServiceFixture(t *testing.T) workflowServiceFixture {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "Workflow service test"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "workflow-actor", Name: "Workflow Actor", Email: "workflow-service@example.invalid",
	})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repositoryResource, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.invalid/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	first, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repositoryResource.ID, Name: "First", Playbook: "first.yml",
	})
	require.NoError(t, err)
	second, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repositoryResource.ID, Name: "Second", Playbook: "second.yml",
	})
	require.NoError(t, err)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	workflow, err := repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Linear", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: first.ID, DisplayName: "First"},
			{ID: -2, TemplateID: second.ID, DisplayName: "Second"},
		},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess,
		}},
	})
	require.NoError(t, err)
	enqueuer := &workflowTestEnqueuer{store: store}
	return workflowServiceFixture{
		store: store, repository: repository,
		service: NewWorkflowService(repository, store, enqueuer, nil), enqueuer: enqueuer,
		projectID: project.ID, user: user, workflow: workflow, first: first, second: second,
	}
}

func finishWorkflowTask(
	t *testing.T,
	store *coresql.SqlDb,
	node db.WorkflowRunNode,
	status task_logger.TaskStatus,
	message string,
) db.Task {
	t.Helper()
	require.NotNil(t, node.TaskID)
	task, err := store.GetTask(node.ProjectID, *node.TaskID)
	require.NoError(t, err)
	task.Status = status
	task.Message = message
	require.NoError(t, store.UpdateTask(task))
	return task
}

type workflowTestEnqueuer struct {
	store           *coresql.SqlDb
	tasks           []db.Task
	templates       []db.Template
	failAfterCreate bool
}

func (e *workflowTestEnqueuer) AddTask(
	task db.Task,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
) (db.Task, error) {
	return db.Task{}, errors.New("workflow tests must use AddWorkflowTask")
}

func (e *workflowTestEnqueuer) AddWorkflowTask(
	task db.Task,
	template db.Template,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
) (db.Task, error) {
	templateJSON, err := json.Marshal(template)
	if err != nil {
		return db.Task{}, err
	}
	snapshot := string(templateJSON)
	task.ProjectID = projectID
	task.Status = task_logger.TaskWaitingStatus
	task.WorkflowTemplateSnapshot = &snapshot
	created, err := e.store.CreateTask(task, 0)
	if err != nil {
		return db.Task{}, err
	}
	e.tasks = append(e.tasks, created)
	e.templates = append(e.templates, template)
	if e.failAfterCreate {
		e.failAfterCreate = false
		return created, errors.New("simulated enqueue interruption")
	}
	return created, nil
}

func (e *workflowTestEnqueuer) StopTasksByWorkflowRun(projectID int, runID int, forceStop bool) {}
