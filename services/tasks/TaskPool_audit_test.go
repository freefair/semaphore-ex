package tasks

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/audit/audittest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreateMetadata(t *testing.T) {
	id := func(v int) *int { return &v }
	tests := []struct {
		name    string
		trigger string
		task    db.Task
		want    audit.TaskCreateMetadata
	}{
		{"api", audit.TriggerAPI, db.Task{TemplateID: 3, UserID: id(7)}, audit.TaskCreateMetadata{Trigger: audit.TriggerAPI, TemplateID: 3}},
		{"api ignores source IDs from the request", audit.TriggerAPI, db.Task{TemplateID: 3, UserID: id(7), ScheduleID: id(5), IntegrationID: id(6), WorkflowRunID: id(4)}, audit.TaskCreateMetadata{Trigger: audit.TriggerAPI, TemplateID: 3}},
		{"deploy started by a user", audit.TriggerAPI, db.Task{TemplateID: 3, UserID: id(7), BuildTaskID: id(9), ScheduleID: id(5)}, audit.TaskCreateMetadata{Trigger: audit.TriggerAPI, TemplateID: 3, ParentTaskID: 9}},
		{"schedule", audit.TriggerSchedule, db.Task{TemplateID: 3, ScheduleID: id(5)}, audit.TaskCreateMetadata{Trigger: audit.TriggerSchedule, TemplateID: 3, ScheduleID: 5}},
		{"integration", audit.TriggerIntegration, db.Task{TemplateID: 3, IntegrationID: id(6)}, audit.TaskCreateMetadata{Trigger: audit.TriggerIntegration, TemplateID: 3, IntegrationID: 6}},
		{"autorun", audit.TriggerAutorun, db.Task{TemplateID: 3, BuildTaskID: id(9)}, audit.TaskCreateMetadata{Trigger: audit.TriggerAutorun, TemplateID: 3, ParentTaskID: 9}},
		{"workflow", audit.TriggerWorkflow, db.Task{TemplateID: 3, WorkflowRunID: id(4), BuildTaskID: id(9)}, audit.TaskCreateMetadata{Trigger: audit.TriggerWorkflow, TemplateID: 3, WorkflowRunID: 4, ParentTaskID: 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, taskCreateMetadata(tt.trigger, tt.task))
		})
	}
}

func TestAddTaskFrom_RecordsTheCallerActor(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	ctx := audit.WithActor(context.Background(), audit.SystemActor(audit.ComponentScheduler))
	schedule, err := fixture.store.CreateSchedule(db.Schedule{ProjectID: fixture.template.ProjectID, TemplateID: fixture.template.ID, CronFormat: "* * * * *"})
	require.NoError(t, err)

	task, err := fixture.pool.AddTaskFrom(ctx, audit.TriggerSchedule, db.Task{TemplateID: fixture.template.ID, ScheduleID: &schedule.ID}, nil, "", fixture.template.ProjectID, false)
	require.NoError(t, err)

	got, err := rec.Only(audit.TaskExecutionCreate)
	require.NoError(t, err)
	assert.Equal(t, audit.SystemActor(audit.ComponentScheduler), got.Actor)
	assert.Equal(t, &audit.Target{Type: audit.TargetTask, ID: strconv.Itoa(task.ID), Name: fixture.template.Name}, got.Event.Target)
	assert.Equal(t, audit.TriggerSchedule, got.Event.Metadata.(audit.TaskCreateMetadata).Trigger)
}

func TestAddTask_ActsForTheWorkflowRunUser(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	user, err := fixture.store.CreateUserWithoutPassword(db.User{Username: "alice", Name: "Alice", Email: "alice@example.com"})
	require.NoError(t, err)

	_, err = fixture.pool.AddTask(db.Task{TemplateID: fixture.template.ID}, &user.ID, "alice", fixture.template.ProjectID, false)
	require.NoError(t, err)

	got, err := rec.Only(audit.TaskExecutionCreate)
	require.NoError(t, err)
	assert.Equal(t, audit.UserActor(user.ID, "alice", "", ""), got.Actor)
	assert.Equal(t, audit.TriggerWorkflow, got.Event.Metadata.(audit.TaskCreateMetadata).Trigger)
}

func TestTaskPool_WithoutRecorderRecordsNothing(t *testing.T) {
	pool := TaskPool{}
	assert.Equal(t, audit.Nop{}, pool.recorder())
}

func TestFinishRun_RecordsCompletionOnce(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	userID := 7
	fixture.task.UserID = &userID
	taskRunner := TaskRunner{Task: fixture.task, Template: fixture.template, pool: &fixture.pool, keyInstaller: fixture.keyInstaller}
	taskRunner.job = &successfulKilledJob{onRun: func() {
		taskRunner.SetStatus(task_logger.TaskStoppingStatus)
	}}

	taskRunner.run()

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Equal(t, audit.SystemActor(audit.ComponentTaskRunner), got.Actor)
	meta := got.Event.Metadata.(audit.TaskCompleteMetadata)
	assert.Equal(t, "stopped", meta.Result)
	assert.Equal(t, 7, meta.InitiatorID)
	assert.Equal(t, fixture.template.ID, meta.TemplateID)
}

func TestFailTaskRunnerLost_RecordsTheReconciler(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := newReconcilerTestPool(store, NewMemoryTaskStateStore())
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	now := time.Now()
	newTask, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}
	pool.state.SetRunning(tsk)

	pool.failTaskRunnerLost(tsk, nil, "runner stopped responding")
	pool.failTaskRunnerLost(tsk, nil, "runner stopped responding")

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err, "a second finalize records nothing")
	assert.Equal(t, audit.SystemActor(audit.ComponentReconciler), got.Actor)
	meta := got.Event.Metadata.(audit.TaskCompleteMetadata)
	assert.Equal(t, "error", meta.Result)
	assert.Equal(t, audit.EndReasonRunnerLost, meta.EndReason)
	assert.Equal(t, audit.ReasonNone, got.Event.Reason)
}

func TestFinalizeRemoteTask_RecordsTheReportingRunner(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := newReconcilerTestPool(store, NewMemoryTaskStateStore())
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	now := time.Now()
	newTask, runnerID := createReconcilerTestTask(t, store, task_logger.TaskSuccessStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}

	pool.FinalizeRemoteTask(tsk, &db.Runner{ID: runnerID, Name: "r1"})

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Equal(t, audit.RunnerActor(runnerID, "r1"), got.Actor)
}

// failingSecretService makes populateDetails fail after the task row is stored.
type failingSecretService struct{ EncryptionServiceMock }

func (*failingSecretService) DeserializeSecret(*db.AccessKey) error { return errors.New("no key") }

type createSurveySecretFailure struct{ EncryptionServiceMock }

func (*createSurveySecretFailure) CreateTaskSurveySecrets(int, int, string, time.Time) error {
	return errors.New("survey key unavailable")
}

type casLosingTaskStore struct{ db.Store }

func (casLosingTaskStore) UpdateTaskRunner(db.Task, task_logger.TaskStatus, int, int, db.RunnerAttemptOutcome, string, time.Time) (bool, error) {
	return false, nil
}

func TestAddTaskFrom_FailedPreparationCompletesTheTask(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	fixture.pool.encryptionService = &failingSecretService{}

	_, err := fixture.pool.AddTaskFrom(context.Background(), audit.TriggerAPI, db.Task{TemplateID: fixture.template.ID}, nil, "", fixture.template.ProjectID, false)
	require.Error(t, err)

	kinds := []audit.Kind{}
	for _, got := range rec.All() {
		kinds = append(kinds, got.Event.Kind)
	}
	assert.Equal(t, []audit.Kind{audit.TaskExecutionCreate, audit.TaskExecutionComplete}, kinds)
	assert.Equal(t, "error", rec.All()[1].Event.Metadata.(audit.TaskCompleteMetadata).Result)
}

func TestSetStatus_WaitingConfirmationRequestsApproval(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	runner, err := fixture.store.CreateRunner(db.Runner{Name: "r"})
	require.NoError(t, err)
	fixture.task.RunnerID = &runner.ID
	taskRunner := TaskRunner{Task: fixture.task, Template: fixture.template, pool: &fixture.pool}

	taskRunner.SetStatus(task_logger.TaskWaitingConfirmation)
	taskRunner.SetStatus(task_logger.TaskWaitingConfirmation)

	got, err := rec.Only(audit.TaskApprovalRequest)
	require.NoError(t, err, "an unchanged status requests nothing")
	assert.Equal(t, audit.RunnerActor(runner.ID, ""), got.Actor)
}

func TestPersistedWaitingConfirmationDoesNotRepeatApprovalRequest(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	taskRunner := TaskRunner{Task: fixture.task, Template: fixture.template, pool: &fixture.pool}
	taskRunner.Task.Status = task_logger.TaskWaitingConfirmation

	taskRunner.afterStatusChange(task_logger.TaskWaitingConfirmation, task_logger.TaskWaitingConfirmation)

	assert.Empty(t, rec.All())
}

func TestConfirmTaskCASLossDoesNotReportChange(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskWaitingConfirmation, &now)
	task.AssignmentGeneration = 1
	task.Status = task_logger.TaskRejected
	require.NoError(t, store.UpdateTask(task))
	stale := task
	stale.Status = task_logger.TaskWaitingConfirmation
	pool := newReconcilerTestPool(casLosingTaskStore{Store: store}, NewMemoryTaskStateStore())
	pool.state.SetRunning(&TaskRunner{Task: stale, Template: db.Template{}, pool: &pool})

	changed, err := pool.ConfirmTask(stale)

	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRejectTaskCASLossDoesNotReportChange(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskWaitingConfirmation, &now)
	task.AssignmentGeneration = 1
	task.Status = task_logger.TaskConfirmed
	require.NoError(t, store.UpdateTask(task))
	stale := task
	stale.Status = task_logger.TaskWaitingConfirmation
	pool := newReconcilerTestPool(casLosingTaskStore{Store: store}, NewMemoryTaskStateStore())
	pool.state.SetRunning(&TaskRunner{Task: stale, pool: &pool})

	changed, err := pool.RejectTask(stale)

	require.NoError(t, err)
	assert.False(t, changed)
}

func TestStopTaskCASLossDoesNotReportChange(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	task.AssignmentGeneration = 1
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, store.UpdateTask(task))
	stale := task
	stale.Status = task_logger.TaskRunningStatus
	pool := newReconcilerTestPool(casLosingTaskStore{Store: store}, NewMemoryTaskStateStore())
	pool.state.SetRunning(&TaskRunner{Task: stale, pool: &pool})

	changed, err := pool.StopTask(stale, false)

	require.NoError(t, err)
	assert.False(t, changed)
}

func TestStopTaskFallbackCASLossDoesNotReportChange(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	task.AssignmentGeneration = 1
	task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, store.UpdateTask(task))
	stale := task
	stale.Status = task_logger.TaskRunningStatus
	pool := CreateTaskPool(casLosingTaskStore{Store: store}, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	pool.queueEvents = make(chan PoolEvent, 1)

	changed, err := pool.StopTask(stale, false)

	require.NoError(t, err)
	assert.False(t, changed)
}

func TestFinalizeRemoteTask_DispatchFailureIsNotTheRunner(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := newReconcilerTestPool(store, NewMemoryTaskStateStore())
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	now := time.Now()
	newTask, runnerID := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	tsk := &TaskRunner{Task: newTask, pool: &pool}

	tsk.FailDispatch()
	pool.FinalizeRemoteTask(tsk, &db.Runner{ID: runnerID, Name: "r1"})

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Equal(t, audit.SystemActor(audit.ComponentTaskRunner), got.Actor)
	assert.Empty(t, got.Event.Metadata.(audit.TaskCompleteMetadata).EndReason)
}

func TestStopTasksByTemplate_CompletesOnlyTasksOutsideTheQueue(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	starting, err := fixture.store.CreateTask(db.Task{ProjectID: fixture.template.ProjectID, TemplateID: fixture.template.ID, Status: task_logger.TaskStartingStatus}, 0)
	require.NoError(t, err)
	waiting, err := fixture.store.CreateTask(db.Task{ProjectID: fixture.template.ProjectID, TemplateID: fixture.template.ID, Status: task_logger.TaskWaitingStatus}, 0)
	require.NoError(t, err)
	fixture.pool.state.Enqueue(NewTaskRunner(waiting, &fixture.pool, "", nil))

	fixture.pool.StopTasksByTemplate(fixture.template.ProjectID, fixture.template.ID, false)

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err, "a queued task gets no complete")
	assert.Equal(t, strconv.Itoa(starting.ID), got.Event.Target.ID)
	assert.Equal(t, "stopped", got.Event.Metadata.(audit.TaskCompleteMetadata).Result)
}

func TestStopTasksByTemplateDefersStartedLocalCompletionToOwner(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	started := time.Now()
	fixture.task.Status = task_logger.TaskRunningStatus
	fixture.task.Start = &started
	require.NoError(t, fixture.store.UpdateTask(fixture.task))
	owner := &TaskRunner{Task: fixture.task, Template: fixture.template, pool: &fixture.pool}
	fixture.pool.state.SetRunning(owner)
	fixture.pool.state.AddActive(fixture.task.ProjectID, owner)

	other := CreateTaskPool(fixture.store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{}, fixture.keyInstaller, &mockLogWriteService{}, nil, nil)
	other.queueEvents = make(chan PoolEvent, 1)
	other.SetAuditRecorder(rec)
	other.StopTasksByTemplate(fixture.template.ProjectID, fixture.template.ID, false)

	owner.Task.Status = task_logger.TaskStoppedStatus
	owner.finishRun()
	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(fixture.task.ID), got.Event.Target.ID)
}

func TestRecordComplete_EndReasonSetByTheTimeoutTimer(t *testing.T) {
	pool := TaskPool{}
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	tr := &TaskRunner{pool: &pool}
	done := make(chan struct{})
	go func() {
		tr.endReason.Store(audit.EndReasonTimeout)
		close(done)
	}()
	tr.recordComplete(audit.SystemActor(audit.ComponentTaskRunner))
	<-done

	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Contains(t, []string{"", audit.EndReasonTimeout}, got.Event.Metadata.(audit.TaskCompleteMetadata).EndReason)
	assert.Equal(t, audit.EndReasonTimeout, tr.endReason.Load())
}

func TestAddAutorunTaskRecordsAutorunTrigger(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	parentTask, err := fixture.store.CreateTask(db.Task{ProjectID: fixture.template.ProjectID, TemplateID: fixture.template.ID, Status: task_logger.TaskSuccessStatus}, 0)
	require.NoError(t, err)
	parent := TaskRunner{Task: parentTask, pool: &fixture.pool}

	_, err = parent.addAutorunTask(fixture.template)
	require.NoError(t, err)
	got, err := rec.Only(audit.TaskExecutionCreate)
	require.NoError(t, err)
	assert.Equal(t, audit.SystemActor(audit.ComponentTaskRunner), got.Actor)
	assert.Equal(t, audit.TriggerAutorun, got.Event.Metadata.(audit.TaskCreateMetadata).Trigger)
}

func TestAddWorkflowTaskRecordsRunUser(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	user, err := fixture.store.CreateUserWithoutPassword(db.User{Username: "workflow-user", Name: "Workflow User", Email: "workflow-user@example.invalid"})
	require.NoError(t, err)

	_, err = fixture.pool.AddWorkflowTask(db.Task{TemplateID: fixture.template.ID}, fixture.template, &user.ID, user.Username, fixture.template.ProjectID, false)
	require.NoError(t, err)
	got, err := rec.Only(audit.TaskExecutionCreate)
	require.NoError(t, err)
	assert.Equal(t, audit.UserActor(user.ID, user.Username, "", ""), got.Actor)
	assert.Equal(t, audit.TriggerWorkflow, got.Event.Metadata.(audit.TaskCreateMetadata).Trigger)
}

func TestExecutionPreflightFromRecordsRequestActor(t *testing.T) {
	_, pool, actor, template, _ := createTaskPreflightFixture(t)
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	ctx := audit.WithActor(context.Background(), audit.UserActor(actor.ID, "request-actor", "", ""))

	_, _, err := pool.AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverrideFrom(ctx, db.Task{TemplateID: template.ID}, &actor, template.ProjectID, false, pro_interfaces.ExecutionPreflightReview{}, nil)
	require.NoError(t, err)
	got, err := rec.Only(audit.TaskExecutionCreate)
	require.NoError(t, err)
	assert.Equal(t, audit.UserActor(actor.ID, "request-actor", "", ""), got.Actor)
	assert.Equal(t, audit.TriggerAPI, got.Event.Metadata.(audit.TaskCreateMetadata).Trigger)
}

func TestFinalizeRemoteTimeoutIsServerAttributed(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := newReconcilerTestPool(store, NewMemoryTaskStateStore())
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	now := time.Now()
	task, runnerID := createReconcilerTestTask(t, store, task_logger.TaskFailStatus, &now)
	runner := &TaskRunner{Task: task, pool: &pool}
	runner.endReason.Store(audit.EndReasonTimeout)
	pool.FinalizeRemoteTask(runner, &db.Runner{ID: runnerID, Name: "r"})
	got, err := rec.Only(audit.TaskExecutionComplete)
	require.NoError(t, err)
	assert.Equal(t, audit.SystemActor(audit.ComponentTaskRunner), got.Actor)
}

func TestStopTaskProtectedFallbackReportsChangeWithoutCompletion(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	rec := &audittest.Recorder{}
	pool.SetAuditRecorder(rec)
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	task.TaskGroupKeys = db.StringArrayField{"global/production"}
	require.NoError(t, store.UpdateTask(task))

	changed, err := pool.StopTask(task, true)
	require.NoError(t, err)
	assert.True(t, changed)
	persisted, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppingStatus, persisted.Status)
	assert.Empty(t, rec.All())
}

func TestStopTaskProtectedFallbackReportsOnlyFirstTransition(t *testing.T) {
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	pool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{}, &EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	pool.queueEvents = make(chan PoolEvent, 1)
	now := time.Now()
	task, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
	task.TaskGroupKeys = db.StringArrayField{"global/production"}
	require.NoError(t, store.UpdateTask(task))

	first, err := pool.StopTask(task, true)
	require.NoError(t, err)
	task.Status = task_logger.TaskStoppingStatus
	second, err := pool.StopTask(task, true)

	require.NoError(t, err)
	assert.True(t, first)
	assert.False(t, second)
	stored, err := store.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStoppingStatus, stored.Status)
}

func TestSurveySecretFailureRecordsCreateBeforeComplete(t *testing.T) {
	fixture := newTaskRunnerRunFixture(t)
	rec := &audittest.Recorder{}
	fixture.pool.SetAuditRecorder(rec)
	fixture.pool.register = make(chan *TaskRunner, 1)
	fixture.pool.encryptionService = &createSurveySecretFailure{}

	_, err := fixture.pool.AddTaskFrom(context.Background(), audit.TriggerAPI, db.Task{TemplateID: fixture.template.ID, Secret: `{"token":"value"}`}, nil, "", fixture.template.ProjectID, false)
	require.Error(t, err)
	recorded := rec.All()
	require.Len(t, recorded, 2)
	assert.Equal(t, audit.TaskExecutionCreate, recorded[0].Event.Kind)
	assert.Equal(t, audit.TaskExecutionComplete, recorded[1].Event.Kind)
}
