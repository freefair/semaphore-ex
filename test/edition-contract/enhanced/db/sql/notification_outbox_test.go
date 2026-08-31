package sql

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskTerminalNotificationIsAtomicIdempotentAndFenced(t *testing.T) {
	normalDatabase, normalTask, _ := controlledTaskFixture(t)
	t.Cleanup(normalDatabase.Close)
	notificationRoute(t, normalDatabase, &normalTask.ProjectID, "task", "trigger", "error")
	normalCandidate := normalTask
	normalCandidate.Status = task_logger.TaskFailStatus
	require.NoError(t, normalDatabase.UpdateTask(normalCandidate))
	assertNotificationTransition(t, notificationEvents(t, normalDatabase, &normalTask.ProjectID), 1, "task", fmt.Sprintf("task:%d", normalTask.ID), 1, "trigger")
	require.NoError(t, normalDatabase.UpdateTask(normalCandidate))
	assert.Len(t, notificationEvents(t, normalDatabase, &normalTask.ProjectID), 1)

	database, task, runnerID := controlledTaskFixture(t)
	t.Cleanup(database.Close)
	notificationRoute(t, database, &task.ProjectID, "task", "trigger", "error")

	stale, current := taskControlTakeover(t, database, task, runnerID)
	candidate := task
	candidate.Status = task_logger.TaskFailStatus
	updated, err := database.UpdateTaskFenced(candidate, stale)
	require.NoError(t, err)
	assert.False(t, updated)
	assert.Empty(t, notificationEvents(t, database, &task.ProjectID))

	updated, err = database.UpdateTaskFenced(candidate, current)
	require.NoError(t, err)
	require.True(t, updated)
	assertNotificationTransition(t, notificationEvents(t, database, &task.ProjectID), 1, "task", fmt.Sprintf("task:%d", task.ID), 1, "trigger")

	updated, err = database.UpdateTaskFenced(candidate, current)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Len(t, notificationEvents(t, database, &task.ProjectID), 1)
}

func TestRunnerTaskTerminalNotificationIsAtomicAndConditional(t *testing.T) {
	database, task, runnerID := controlledTaskFixture(t)
	t.Cleanup(database.Close)
	notificationRoute(t, database, &task.ProjectID, "task", "trigger", "error")

	candidate := task
	candidate.Status = task_logger.TaskFailStatus
	updated, err := database.UpdateTaskRunner(
		candidate, task.Status, runnerID, task.AssignmentGeneration,
		db.RunnerAttemptFailed, "runner reported failure", time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, updated)
	assertNotificationTransition(t, notificationEvents(t, database, &task.ProjectID), 1, "task", fmt.Sprintf("task:%d", task.ID), 1, "trigger")

	updated, err = database.UpdateTaskRunner(
		candidate, task.Status, runnerID, task.AssignmentGeneration,
		db.RunnerAttemptFailed, "late duplicate", time.Now().UTC(),
	)
	require.NoError(t, err)
	assert.False(t, updated)
	assert.Len(t, notificationEvents(t, database, &task.ProjectID), 1)
}

func TestTaskNotificationFailureRollsBackSourceMutation(t *testing.T) {
	database, task, _ := controlledTaskFixture(t)
	t.Cleanup(database.Close)
	candidate := task
	candidate.Status = task_logger.TaskFailStatus
	projectID, taskID, templateID := task.ProjectID, task.ID, task.TemplateID
	event, err := (pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeProject, ProjectID: &projectID,
		Source:      pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: fmt.Sprintf("task:%d", task.ID)},
		LifecycleID: fmt.Sprintf("template:%d", task.TemplateID), SourceRevision: 1,
		Severity: pro_interfaces.NotificationSeverityError, LifecycleAction: pro_interfaces.NotificationLifecycleTrigger,
		Details: pro_interfaces.NotificationDetails{TaskID: &taskID, TemplateID: &templateID, Status: "failed"},
	}).EnsureIdentity(time.Now().UTC())
	require.NoError(t, err)
	details, err := json.Marshal(event.Details)
	require.NoError(t, err)
	_, err = database.CreateNotificationEventWithRouting(db.NotificationEvent{
		SchemaVersion: event.SchemaVersion, EventID: event.EventID, SourceEventKey: event.SourceEventKey, SourceRevision: event.SourceRevision,
		ProjectID: event.ProjectID, SourceKind: string(event.Source.Kind), SourceID: event.Source.ID, LifecycleID: event.LifecycleID,
		Severity: string(event.Severity), LifecycleAction: string(event.LifecycleAction), IncidentKey: event.IncidentKey, Details: string(details),
		RoutingOutcome: db.NotificationRoutingFiltered, OccurredAt: event.OccurredAt,
	}, nil)
	require.NoError(t, err)

	err = database.UpdateTask(candidate)
	require.Error(t, err)
	persisted, err := database.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskStartingStatus, persisted.Status)
	assert.Equal(t, 0, persisted.NotificationRevision)
}

func TestWorkflowTerminalNotificationIsAtomicIdempotentAndFenced(t *testing.T) {
	normalDatabase, normalRepository, normalProjectID, normalRun := notificationWorkflowRunFixture(t)
	t.Cleanup(normalDatabase.Close)
	notificationRoute(t, normalDatabase, &normalProjectID, "workflow", "trigger", "error")
	normalCandidate := normalRun
	normalCandidate.Status = db.WorkflowRunFailed
	updated, err := normalRepository.UpdateWorkflowRunStatusUnless(normalCandidate, nil)
	require.NoError(t, err)
	require.True(t, updated)
	assertNotificationTransition(t, notificationEvents(t, normalDatabase, &normalProjectID), 1, "workflow", fmt.Sprintf("workflow_run:%d", normalRun.ID), 1, "trigger")
	updated, err = normalRepository.UpdateWorkflowRunStatusUnless(normalCandidate, nil)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Len(t, notificationEvents(t, normalDatabase, &normalProjectID), 1)

	database, repository, projectID, run := notificationWorkflowRunFixture(t)
	t.Cleanup(database.Close)
	notificationRoute(t, database, &projectID, "workflow", "trigger", "error")

	stale, current := workflowReconciliationTakeover(t, database, projectID, run.ID)
	candidate := run
	candidate.Status = db.WorkflowRunFailed
	updated, err = repository.UpdateWorkflowRunStatusUnlessFenced(stale, candidate, []db.WorkflowRunStatus{db.WorkflowRunSucceeded})
	require.NoError(t, err)
	assert.False(t, updated)
	assert.Empty(t, notificationEvents(t, database, &projectID))

	updated, err = repository.UpdateWorkflowRunStatusUnlessFenced(current, candidate, []db.WorkflowRunStatus{db.WorkflowRunSucceeded})
	require.NoError(t, err)
	require.True(t, updated)
	assertNotificationTransition(t, notificationEvents(t, database, &projectID), 1, "workflow", fmt.Sprintf("workflow_run:%d", run.ID), 1, "trigger")

	updated, err = repository.UpdateWorkflowRunStatusUnless(candidate, nil)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Len(t, notificationEvents(t, database, &projectID), 1)
}

func TestWorkflowApprovalOpenAndContributionResolutionShareIncident(t *testing.T) {
	database, repository, projectID := workflowRepositoryFixture(t)
	t.Cleanup(database.Close)
	notificationRoute(t, database, &projectID, "approval", "trigger,resolve", "info")
	user, first, second := workflowRunResources(t, database, projectID)
	_, err := database.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{first.ID: first, second.ID: second}, user.ID, "notification-approval", now)
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	policy := db.WorkflowApprovalRolePolicy{Revision: 1, Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}, MinimumDistinctApprovers: 1}
	payload, err := json.Marshal(db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy})
	require.NoError(t, err)
	deadline := now.Add(time.Minute)
	approval, opened, err := repository.OpenWorkflowApproval(db.WorkflowApproval{
		ProjectID: projectID, WorkflowRunID: run.ID, WorkflowNodeID: workflow.Nodes[0].ID, Status: db.WorkflowApprovalPending,
		Created: now, Deadline: &deadline, Prompt: "approve", EligiblePermission: db.CanRunProjectTasks,
		RolePolicySnapshotJSON: string(payload), RolePolicySnapshotRevision: 1,
		RolePolicySnapshot: db.WorkflowApprovalRolePolicySnapshot{PolicyRevision: 1, Policy: policy},
		RequestActorUserID: user.ID + 1, TimeoutOutcome: db.WorkflowApprovalTimeoutReject, CorrelationID: "notification-approval",
	})
	require.NoError(t, err)
	require.True(t, opened)
	events := notificationEvents(t, database, &projectID)
	assertNotificationTransition(t, events, 1, "approval", fmt.Sprintf("approval:%d", approval.ID), 1, "trigger")

	result, err := repository.SubmitWorkflowApprovalContribution(workflowApprovalSubmission(projectID, approval, user.ID, now.Add(time.Second)))
	require.NoError(t, err)
	require.True(t, result.Committed)
	require.True(t, result.Terminal)
	events = notificationEvents(t, database, &projectID)
	assertNotificationTransition(t, events, 2, "approval", fmt.Sprintf("approval:%d", approval.ID), 2, "resolve")
	assert.Equal(t, events[0].IncidentKey, events[1].IncidentKey)
	assert.NotEqual(t, events[0].SourceEventKey, events[1].SourceEventKey)
}

func TestWorkflowApprovalDirectResolutionKeepsNotificationLifecycle(t *testing.T) {
	database, repository, projectID, _, approval, now := workflowApprovalContributionFixture(t)
	t.Cleanup(database.Close)
	notificationRoute(t, database, &projectID, "approval", "update", "error")
	resolvedAt := now.Add(time.Second)
	candidate := approval
	candidate.Status = db.WorkflowApprovalRejected
	candidate.Resolved = &resolvedAt
	candidate.DecisionSource = db.WorkflowApprovalDecisionSourceUser
	updated, err := repository.ResolveWorkflowApprovalIfPending(candidate)
	require.NoError(t, err)
	require.True(t, updated)
	events := notificationEvents(t, database, &projectID)
	assertNotificationTransition(t, events, 2, "approval", fmt.Sprintf("approval:%d", approval.ID), 2, "update")
	assert.Equal(t, events[0].IncidentKey, events[1].IncidentKey)
}

func TestGlobalNotificationScopeDoesNotRouteToProjectDestinations(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	project, err := database.CreateProject(db.Project{Name: "notification scope"})
	require.NoError(t, err)
	notificationRoute(t, database, &project.ID, "system", "update", "warning")
	notificationRoute(t, database, nil, "system", "update", "warning")
	event := pro_interfaces.NotificationEvent{
		Scope: pro_interfaces.NotificationScopeGlobal, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: "system:node"},
		LifecycleID: "system:node", SourceRevision: 1, Severity: pro_interfaces.NotificationSeverityWarning,
		LifecycleAction: pro_interfaces.NotificationLifecycleUpdate, Details: pro_interfaces.NotificationDetails{Status: "degraded"},
	}
	require.NoError(t, database.RecordGlobalSystemNotification(event))
	projectDeliveries, err := database.GetNotificationDeliveries(&project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, projectDeliveries)
	globalDeliveries, err := database.GetNotificationDeliveries(nil, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Len(t, globalDeliveries, 1)
}

func notificationWorkflowRunFixture(t *testing.T) (*coresql.SqlDb, *WorkflowStoreImpl, int, db.WorkflowRun) {
	t.Helper()
	database, repository, projectID := workflowRepositoryFixture(t)
	user, first, second := workflowRunResources(t, database, projectID)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{first.ID: first, second.ID: second}, user.ID, "notification-workflow", time.Now().UTC())
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	return database, repository, projectID, run
}

func notificationRoute(t *testing.T, database *coresql.SqlDb, projectID *int, sourceKinds, actions, minimumSeverity string) {
	t.Helper()
	destination, err := database.CreateNotificationDestination(db.NotificationDestination{ProjectID: projectID, Name: "notification-test", Provider: "test", Enabled: true})
	require.NoError(t, err)
	_, err = database.CreateNotificationRule(db.NotificationRule{ProjectID: projectID, DestinationID: destination.ID, SourceKinds: sourceKinds, LifecycleActions: actions, MinimumSeverity: minimumSeverity, Enabled: true})
	require.NoError(t, err)
}

func taskControlTakeover(t *testing.T, database *coresql.SqlDb, task db.Task, runnerID int) (int64, int64) {
	t.Helper()
	repository := NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(task.ID, runnerID, task.AssignmentGeneration)
	require.NoError(t, err)
	first, claimed, err := repository.ClaimTaskControl(task.ID, execution, "notification-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", task.ID)
	require.NoError(t, err)
	second, claimed, err := repository.ClaimTaskControl(task.ID, execution, "notification-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	return first.FencingToken, second.FencingToken
}

func workflowReconciliationTakeover(t *testing.T, database *coresql.SqlDb, projectID, runID int) (pro_interfaces.WorkflowReconciliationLease, pro_interfaces.WorkflowReconciliationLease) {
	t.Helper()
	repository := NewWorkflowReconciliationStore(database.GetConnection())
	first, claimed, err := repository.ClaimWorkflowReconciliation(projectID, runID, "notification-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec("update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=?", projectID, runID)
	require.NoError(t, err)
	second, claimed, err := repository.ClaimWorkflowReconciliation(projectID, runID, "notification-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	return first, second
}

func notificationEvents(t *testing.T, database *coresql.SqlDb, projectID *int) []db.NotificationEvent {
	t.Helper()
	query := "select * from notification_event where project_id is null order by id"
	args := []any{}
	if projectID != nil {
		query = "select * from notification_event where project_id=? order by id"
		args = append(args, *projectID)
	}
	var events []db.NotificationEvent
	_, err := database.GetConnection().SelectAll(&events, query, args...)
	require.NoError(t, err)
	return events
}

func assertNotificationTransition(t *testing.T, events []db.NotificationEvent, wantLen int, sourceKind, sourceID string, revision int, action string) {
	t.Helper()
	require.Len(t, events, wantLen)
	event := events[wantLen-1]
	assert.Equal(t, sourceKind, event.SourceKind)
	assert.Equal(t, sourceID, event.SourceID)
	assert.Equal(t, revision, event.SourceRevision)
	assert.Equal(t, action, event.LifecycleAction)
	assert.Equal(t, db.NotificationRoutingRouted, event.RoutingOutcome)
}
