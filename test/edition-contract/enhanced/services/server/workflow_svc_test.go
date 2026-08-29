package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
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

func TestWorkflowApprovalPausesThenResumesOnlyForEligibleNonRequester(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	approver, err := fixture.store.CreateUserWithoutPassword(db.User{Username: "workflow-approver", Name: "Workflow Approver", Email: "workflow-approver@example.invalid"})
	require.NoError(t, err)
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, approver.ID, db.ProjectManager))

	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Approval", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Prepare"},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, DisplayName: "Approve", ApprovalSeparationOfDuties: true},
			{ID: -3, TemplateID: fixture.second.ID, DisplayName: "Deploy"},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnSuccess},
		},
	})
	require.NoError(t, err)

	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-pause")
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.tasks, 1)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))

	paused, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunApproval, paused.Status)
	require.Len(t, fixture.enqueuer.tasks, 1, "downstream task must not exist before approval")
	approvalNodeID := workflow.Nodes[1].ID
	pending, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, approvalNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowApprovalPending, pending.Status)
	assert.Equal(t, fixture.user.ID, pending.RequestActorUserID)
	assert.True(t, pending.SeparationOfDuties)

	_, err = fixture.service.ResolveWorkflowApproval(fixture.projectID, workflow.ID, run.ID, approvalNodeID, db.WorkflowApprovalDecision{
		Status: db.WorkflowApprovalApproved, Source: db.WorkflowApprovalDecisionSourceUser,
	}, &fixture.user)
	require.ErrorContains(t, err, "cannot be self-approved")

	resolved, err := fixture.service.ResolveWorkflowApproval(fixture.projectID, workflow.ID, run.ID, approvalNodeID, db.WorkflowApprovalDecision{
		Status: db.WorkflowApprovalApproved, Comment: "Reviewed", Source: db.WorkflowApprovalDecisionSourceUser,
	}, &approver)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowApprovalApproved, resolved.Status)
	assert.Equal(t, approver.ID, *resolved.ResolvedByUserID)
	assert.Equal(t, "Reviewed", resolved.DecisionComment)
	assert.Len(t, fixture.enqueuer.tasks, 2, "approval must permit the downstream task")
}

func TestWorkflowApprovalInboxOnlyReturnsPendingRequestsTheActorMayResolve(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	approver, err := fixture.store.CreateUserWithoutPassword(db.User{Username: "approval-inbox-user", Name: "Approval Inbox User", Email: "approval-inbox@example.invalid"})
	require.NoError(t, err)
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, approver.ID, db.ProjectManager))
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Inbox", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalSeparationOfDuties: true},
		},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	})
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-inbox")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))

	requesterInbox, err := fixture.service.GetWorkflowApprovalInbox(fixture.projectID, &fixture.user)
	require.NoError(t, err)
	assert.Empty(t, requesterInbox)
	approverInbox, err := fixture.service.GetWorkflowApprovalInbox(fixture.projectID, &approver)
	require.NoError(t, err)
	require.Len(t, approverInbox, 1)
	assert.Equal(t, run.ID, approverInbox[0].WorkflowRunID)
	assert.Equal(t, workflow.ID, approverInbox[0].WorkflowTemplateID)
	assert.Equal(t, db.WorkflowApprovalPending, approverInbox[0].Status)
}

func TestWorkflowApprovalTimeoutExpiresAndBlocksDownstreamTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	timeout := 60
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Approval timeout", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalTimeout: &timeout, ApprovalTimeoutOutcome: db.WorkflowApprovalTimeoutReject},
			{ID: -3, TemplateID: fixture.second.ID},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnSuccess},
		},
	})
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-timeout")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))
	approvalNodeID := workflow.Nodes[1].ID
	_, err = fixture.store.Sql().Exec(
		"update project__workflow_approval set deadline=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=?",
		fixture.projectID, run.ID, approvalNodeID,
	)
	require.NoError(t, err)

	require.NoError(t, fixture.service.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	expired, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, approvalNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowApprovalExpired, expired.Status)
	assert.Equal(t, db.WorkflowApprovalDecisionSourceTimeout, expired.DecisionSource)
	assert.NotNil(t, expired.Resolved)
	assert.Nil(t, expired.ResolvedByUserID)
	assert.Len(t, fixture.enqueuer.tasks, 1)
	updatedRun, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunBlocked, updatedRun.Status)
}

func TestWorkflowApprovalTimeoutApproveResumesAfterServiceRestart(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	timeout := 60
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Approval timeout permits", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalTimeout: &timeout, ApprovalTimeoutOutcome: db.WorkflowApprovalTimeoutApprove},
			{ID: -3, TemplateID: fixture.second.ID},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnSuccess},
		},
	})
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-timeout-approve")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))
	approvalNodeID := workflow.Nodes[1].ID
	_, err = fixture.store.Sql().Exec(
		"update project__workflow_approval set deadline=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=? and workflow_node_id=?",
		fixture.projectID, run.ID, approvalNodeID,
	)
	require.NoError(t, err)

	restartedService := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil)
	require.NoError(t, restartedService.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	expired, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, approvalNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowApprovalExpired, expired.Status)
	assert.Len(t, fixture.enqueuer.tasks, 2, "the persisted timeout outcome must resume downstream work after restart")
	updatedRun, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeQueued, updatedRun.Nodes[2].Status)
}

func TestWorkflowApprovalConcurrentDecisionsPersistExactlyOneTerminalOutcome(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Concurrent approval", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind},
		},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	})
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-concurrent")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, status := range []db.WorkflowApprovalStatus{db.WorkflowApprovalApproved, db.WorkflowApprovalRejected} {
		status := status
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, resolveErr := fixture.service.ResolveWorkflowApproval(
				fixture.projectID, workflow.ID, run.ID, workflow.Nodes[1].ID,
				db.WorkflowApprovalDecision{Status: status, Source: db.WorkflowApprovalDecisionSourceUser}, &fixture.user,
			)
			results <- resolveErr
		}()
	}
	wait.Wait()
	close(results)
	successfulDecisions := 0
	for resolveErr := range results {
		if resolveErr == nil {
			successfulDecisions++
		}
	}
	assert.Equal(t, 1, successfulDecisions)
	persisted, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, workflow.Nodes[1].ID)
	require.NoError(t, err)
	assert.Contains(t, []db.WorkflowApprovalStatus{db.WorkflowApprovalApproved, db.WorkflowApprovalRejected}, persisted.Status)
	assert.Equal(t, db.WorkflowApprovalDecisionSourceUser, persisted.DecisionSource)
	require.NotNil(t, persisted.ResolvedByUserID)
	assert.Equal(t, fixture.user.ID, *persisted.ResolvedByUserID)
}

func TestWorkflowApprovalStopCancelsRequestAndApprovalNode(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	require.NoError(t, ensureWorkflowApprovalMember(fixture.store, fixture.projectID, fixture.user.ID, db.ProjectOwner))
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Canceled approval", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind},
			{ID: -3, TemplateID: fixture.second.ID},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnSuccess},
		},
	})
	require.NoError(t, err)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approval-stop")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(root))

	canceled, err := fixture.service.StopWorkflowRun(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunCanceled, canceled.Status)
	approval, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, workflow.Nodes[1].ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowApprovalCanceled, approval.Status)
	assert.Equal(t, db.WorkflowApprovalDecisionSourceCancel, approval.DecisionSource)
	assert.Nil(t, approval.ResolvedByUserID)
	assert.Equal(t, db.WorkflowRunNodeCanceled, canceled.Nodes[1].Status)
	assert.Equal(t, db.WorkflowRunNodeCanceled, canceled.Nodes[2].Status)
}

func TestWorkflowStopRequestPersistsDesiredStateBeforeReconciliation(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "durable-stop-request")
	require.NoError(t, err)
	requested, err := fixture.service.RequestWorkflowRunStop(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunDesiredStopping, requested.DesiredState)
	assert.Equal(t, db.WorkflowRunStopping, requested.Status)

	restartedService := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil)
	recovered, err := restartedService.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunDesiredStopped, recovered.DesiredState)
	assert.Equal(t, db.WorkflowRunCanceled, recovered.Status)
	assert.Equal(t, db.WorkflowRunNodeCanceled, recovered.Nodes[0].Status)
	assert.Len(t, fixture.enqueuer.tasks, 1, "a durable stop request must not create downstream tasks")
}

func TestWorkflowStopRequestPreventsCompletionCallbackFromPlanningDownstreamWork(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "stop-callback-race")
	require.NoError(t, err)
	_, err = fixture.service.RequestWorkflowRunStop(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)

	require.NoError(t, fixture.service.ProgressWorkflowRun(fixture.projectID, run.ID, nil))
	stopped, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunCanceled, stopped.Status)
	assert.Equal(t, db.WorkflowRunNodeCanceled, stopped.Nodes[0].Status)
	assert.Equal(t, db.WorkflowRunNodeCanceled, stopped.Nodes[1].Status)
	assert.Len(t, fixture.enqueuer.tasks, 1)
}

func TestWorkflowRunParametersAndNodeOverridesMapToFrozenTaskInputs(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	fixture.first.AllowOverrideArgsInTask = true
	require.NoError(t, fixture.store.UpdateTemplate(fixture.first))
	credential, err := fixture.store.CreateAccessKey(db.AccessKey{
		ProjectID: &fixture.projectID, Name: "Deployment token", Type: db.AccessKeyString,
		Owner: db.AccessKeyShared,
	})
	require.NoError(t, err)
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Parameterized", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions: []db.WorkflowParameterDeclaration{
			{Name: "region", Type: db.WorkflowParameterString, Default: json.RawMessage(`"eu"`)},
			{Name: "replicas", Type: db.WorkflowParameterInteger, Required: true},
			{Name: "token", Type: db.WorkflowParameterSecretReference, Required: true,
				SecretOptions: []db.WorkflowSecretOption{{AccessKeyID: credential.ID, Label: "Deployment token"}}},
		},
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: fixture.first.ID, DisplayName: "Deploy",
			TaskParams: &db.TaskParams{Environment: `{"region":"node"}`},
			OverridePolicy: db.WorkflowNodeOverridePolicy{
				AllowArguments: true, CredentialParameters: []string{"token"},
			},
		}},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	arguments := `["--check"]`
	reader := &workflowCredentialReaderStub{value: "resolved-secret"}
	service := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil, reader)
	run, err := service.StartWorkflow(workflow, &fixture.user, "parameterized", db.WorkflowRunInput{
		TriggerValues: map[string]json.RawMessage{"region": json.RawMessage(`"trigger"`)},
		UserValues: map[string]json.RawMessage{
			"region": json.RawMessage(`"user"`), "replicas": json.RawMessage(`3`),
			"token": json.RawMessage(fmt.Sprintf(`{"access_key_id":%d}`, credential.ID)),
		},
		NodeOverrides: map[int]db.WorkflowNodeOverride{
			workflow.Nodes[0].ID: {Arguments: &arguments},
		},
	})
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.inputTasks, 1)
	inputTask := fixture.enqueuer.inputTasks[0]
	assert.JSONEq(t, `{"region":"node","replicas":3}`, inputTask.Environment,
		"definition-level node environment must win over run values")
	assert.JSONEq(t, `{"token":"resolved-secret"}`, inputTask.Secret)
	require.NotNil(t, inputTask.Arguments)
	assert.Equal(t, arguments, *inputTask.Arguments)
	assert.Equal(t, 1, reader.calls)

	persisted, err := fixture.store.GetTask(fixture.projectID, fixture.enqueuer.tasks[0].ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.Secret)
	assert.NotContains(t, persisted.Environment, "resolved-secret")
	assert.NotContains(t, run.ParameterSnapshotJSON, "resolved-secret")
	assert.NotEmpty(t, run.ParameterSnapshot["token"].ReferenceFingerprint)
}

func TestWorkflowSecretParameterIsNotInjectedWithoutNodeApproval(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	credential, err := fixture.store.CreateAccessKey(db.AccessKey{
		ProjectID: &fixture.projectID, Name: "Scoped token", Type: db.AccessKeyString,
		Owner: db.AccessKeyShared,
	})
	require.NoError(t, err)
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Scoped secret", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions: []db.WorkflowParameterDeclaration{{
			Name: "token", Type: db.WorkflowParameterSecretReference, Required: true,
			SecretOptions: []db.WorkflowSecretOption{{AccessKeyID: credential.ID}},
		}},
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: fixture.first.ID}},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	reader := &workflowCredentialReaderStub{value: "must-not-leak"}
	service := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil, reader)
	_, err = service.StartWorkflow(workflow, &fixture.user, "scoped-secret", db.WorkflowRunInput{
		UserValues: map[string]json.RawMessage{
			"token": json.RawMessage(fmt.Sprintf(`{"access_key_id":%d}`, credential.ID)),
		},
	})
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.inputTasks, 1)
	assert.Empty(t, fixture.enqueuer.inputTasks[0].Secret)
	assert.Zero(t, reader.calls)
}

func TestWorkflowRunRejectsPlaintextAndForbiddenNodeOverrides(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	credential, err := fixture.store.CreateAccessKey(db.AccessKey{
		ProjectID: &fixture.projectID, Name: "Deployment token", Type: db.AccessKeyString,
		Owner: db.AccessKeyShared,
	})
	require.NoError(t, err)
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Restricted", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions: []db.WorkflowParameterDeclaration{{
			Name: "token", Type: db.WorkflowParameterSecretReference,
			SecretOptions: []db.WorkflowSecretOption{{AccessKeyID: credential.ID}},
		}},
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: fixture.first.ID}},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	reader := &workflowCredentialReaderStub{value: "must-not-be-used"}
	service := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil, reader)

	_, err = service.StartWorkflow(workflow, &fixture.user, "plaintext", db.WorkflowRunInput{
		UserValues: map[string]json.RawMessage{"token": json.RawMessage(`"plaintext"`)},
	})
	require.Error(t, err)
	branch := "main"
	_, err = service.StartWorkflow(workflow, &fixture.user, "forbidden-branch", db.WorkflowRunInput{
		NodeOverrides: map[int]db.WorkflowNodeOverride{workflow.Nodes[0].ID: {GitBranch: &branch}},
	})
	require.Error(t, err)
	assert.Zero(t, reader.calls)
	assert.Empty(t, fixture.enqueuer.tasks)
}

func TestWorkflowRunAppliesApprovedResourceAndTemplateOverrides(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	defaultInventory, err := fixture.store.CreateInventory(db.Inventory{
		ProjectID: fixture.projectID, Name: "Default", Type: db.InventoryStatic, Inventory: "localhost",
	})
	require.NoError(t, err)
	approvedInventory, err := fixture.store.CreateInventory(db.Inventory{
		ProjectID: fixture.projectID, Name: "Approved", Type: db.InventoryStatic, Inventory: "localhost",
	})
	require.NoError(t, err)
	defaultEnvironment, err := fixture.store.CreateEnvironment(db.Environment{
		ProjectID: fixture.projectID, Name: "Default", JSON: `{}`, ENV: nil,
	})
	require.NoError(t, err)
	approvedEnvironment, err := fixture.store.CreateEnvironment(db.Environment{
		ProjectID: fixture.projectID, Name: "Approved", JSON: `{}`, ENV: nil,
	})
	require.NoError(t, err)
	fixture.first.InventoryID = &defaultInventory.ID
	fixture.first.EnvironmentIDs = []int{defaultEnvironment.ID}
	fixture.first.TaskParams = db.MapStringAnyField{"allow_override_inventory": true}
	fixture.first.AllowOverrideArgsInTask = true
	fixture.first.AllowOverrideBranchInTask = true
	require.NoError(t, fixture.store.UpdateTemplate(fixture.first))

	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Approved overrides", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: fixture.first.ID,
			OverridePolicy: db.WorkflowNodeOverridePolicy{
				InventoryIDs: []int{approvedInventory.ID}, EnvironmentIDs: []int{approvedEnvironment.ID},
				AllowArguments: true, AllowBranch: true,
			},
		}},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	arguments, branch := `["--check"]`, "release"
	environmentIDs := []int{approvedEnvironment.ID}
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "approved-overrides", db.WorkflowRunInput{
		NodeOverrides: map[int]db.WorkflowNodeOverride{workflow.Nodes[0].ID: {
			InventoryID: &approvedInventory.ID, EnvironmentIDs: &environmentIDs,
			Arguments: &arguments, GitBranch: &branch,
		}},
	})
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.inputTasks, 1)
	require.Len(t, fixture.enqueuer.templates, 1)
	require.NotNil(t, fixture.enqueuer.inputTasks[0].InventoryID)
	assert.Equal(t, approvedInventory.ID, *fixture.enqueuer.inputTasks[0].InventoryID)
	require.NotNil(t, fixture.enqueuer.inputTasks[0].Arguments)
	assert.Equal(t, arguments, *fixture.enqueuer.inputTasks[0].Arguments)
	require.NotNil(t, fixture.enqueuer.inputTasks[0].GitBranch)
	assert.Equal(t, branch, *fixture.enqueuer.inputTasks[0].GitBranch)
	assert.Equal(t, []int{approvedEnvironment.ID}, fixture.enqueuer.templates[0].EnvironmentIDs)
	assert.Equal(t, []int{approvedEnvironment.ID}, run.Nodes[0].TemplateSnapshot.EnvironmentIDs)
	assert.Equal(t, []int{approvedEnvironment.ID}, *run.Nodes[0].OverrideSnapshot.EnvironmentIDs)
}

func TestWorkflowRunRejectsDeletedApprovedResourceBeforePersistingRun(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	inventory, err := fixture.store.CreateInventory(db.Inventory{
		ProjectID: fixture.projectID, Name: "Ephemeral", Type: db.InventoryStatic, Inventory: "localhost",
	})
	require.NoError(t, err)
	fixture.first.TaskParams = db.MapStringAnyField{"allow_override_inventory": true}
	require.NoError(t, fixture.store.UpdateTemplate(fixture.first))
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Deleted resource", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: fixture.first.ID,
			OverridePolicy: db.WorkflowNodeOverridePolicy{InventoryIDs: []int{inventory.ID}},
		}},
	})
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	require.NoError(t, fixture.store.DeleteInventory(fixture.projectID, inventory.ID))

	_, err = fixture.service.StartWorkflow(workflow, &fixture.user, "deleted-resource", db.WorkflowRunInput{
		NodeOverrides: map[int]db.WorkflowNodeOverride{
			workflow.Nodes[0].ID: {InventoryID: &inventory.ID},
		},
	})
	require.Error(t, err)
	runs, listErr := fixture.repository.GetWorkflowRuns(
		fixture.projectID, workflow.ID, db.RetrieveQueryParams{},
	)
	require.NoError(t, listErr)
	assert.Empty(t, runs, "invalid runtime resources must fail before the run snapshot is persisted")
	assert.Empty(t, fixture.enqueuer.tasks)
}

func TestWorkflowRunResolvesSecretReferencesAtEachTaskDispatch(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	credential, err := fixture.store.CreateAccessKey(db.AccessKey{
		ProjectID: &fixture.projectID, Name: "Rotating token", Type: db.AccessKeyString, Owner: db.AccessKeyShared,
	})
	require.NoError(t, err)
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Rotating secret", DefinitionVersion: db.WorkflowDefinitionVersion,
		ParameterDefinitions: []db.WorkflowParameterDeclaration{{
			Name: "token", Type: db.WorkflowParameterSecretReference, Required: true,
			SecretOptions: []db.WorkflowSecretOption{{AccessKeyID: credential.ID, Label: "Rotating token"}},
		}},
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID, OverridePolicy: db.WorkflowNodeOverridePolicy{CredentialParameters: []string{"token"}}},
			{ID: -2, TemplateID: fixture.second.ID, OverridePolicy: db.WorkflowNodeOverridePolicy{CredentialParameters: []string{"token"}}},
		},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess,
		}},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	workflow, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	reader := &workflowCredentialReaderStub{values: []string{"first-version", "rotated-version"}}
	service := NewWorkflowService(fixture.repository, fixture.store, fixture.enqueuer, nil, reader)
	run, err := service.StartWorkflow(workflow, &fixture.user, "rotating-secret", db.WorkflowRunInput{
		UserValues: map[string]json.RawMessage{
			"token": json.RawMessage(fmt.Sprintf(`{"access_key_id":%d}`, credential.ID)),
		},
	})
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.inputTasks, 1)
	assert.JSONEq(t, `{"token":"first-version"}`, fixture.enqueuer.inputTasks[0].Secret)

	rootTask := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, service.HandleWorkflowTaskCompletion(rootTask))
	require.Len(t, fixture.enqueuer.inputTasks, 2)
	assert.JSONEq(t, `{"token":"rotated-version"}`, fixture.enqueuer.inputTasks[1].Secret)
	assert.Equal(t, 2, reader.calls)

	persisted, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, credential.ID, persisted.ParameterSnapshot["token"].SecretReference.AccessKeyID)
	assert.NotContains(t, persisted.ParameterSnapshotJSON, "first-version")
	assert.NotContains(t, persisted.ParameterSnapshotJSON, "rotated-version")
}

func TestWorkflowServiceSkipsUnselectedDependentNodeAfterFirstFailure(t *testing.T) {
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
	assert.Equal(t, db.WorkflowRunNodeSkipped, failed.Nodes[1].Status)
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

func TestWorkflowServiceRunsDiamondForEveryJoinMode(t *testing.T) {
	tests := []struct {
		name              string
		joinMode          db.WorkflowJoinMode
		leftStatus        task_logger.TaskStatus
		rightStatus       task_logger.TaskStatus
		joinBeforeRight   bool
		expectedRunStatus db.WorkflowRunStatus
		expectedJoin      db.WorkflowRunNodeStatus
	}{
		{
			name: "all successful blocks the join after a branch failure", joinMode: db.WorkflowJoinAllSuccessful,
			leftStatus: task_logger.TaskSuccessStatus, rightStatus: task_logger.TaskFailStatus,
			expectedRunStatus: db.WorkflowRunFailed, expectedJoin: db.WorkflowRunNodeBlocked,
		},
		{
			name: "all complete runs the join after a branch failure", joinMode: db.WorkflowJoinAllComplete,
			leftStatus: task_logger.TaskSuccessStatus, rightStatus: task_logger.TaskFailStatus,
			expectedRunStatus: db.WorkflowRunFailed, expectedJoin: db.WorkflowRunNodeSucceeded,
		},
		{
			name: "any successful starts the join while another branch is active", joinMode: db.WorkflowJoinAnySuccessful,
			leftStatus: task_logger.TaskSuccessStatus, rightStatus: task_logger.TaskSuccessStatus,
			joinBeforeRight: true, expectedRunStatus: db.WorkflowRunSucceeded, expectedJoin: db.WorkflowRunNodeSucceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowServiceFixture(t)
			defer fixture.store.Close()
			workflow := createDiamondWorkflow(t, &fixture, test.joinMode, 2, db.WorkflowEdgeAlways)

			run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "diamond-"+string(test.joinMode))
			require.NoError(t, err)
			require.Len(t, fixture.enqueuer.tasks, 1)
			rootTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Root"), task_logger.TaskSuccessStatus, "")
			require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))

			run = loadWorkflowRun(t, &fixture, run.ID)
			assert.Equal(t, db.WorkflowRunNodeQueued, workflowRunNodeNamed(t, run, "Left").Status)
			assert.Equal(t, db.WorkflowRunNodeQueued, workflowRunNodeNamed(t, run, "Right").Status)
			require.Len(t, fixture.enqueuer.tasks, 3, "both independent branches must be enqueued")

			leftTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Left"), test.leftStatus, workflowTaskMessage(test.leftStatus, "left failed"))
			require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(leftTask))
			run = loadWorkflowRun(t, &fixture, run.ID)
			if test.joinBeforeRight {
				assert.Equal(t, db.WorkflowRunNodeQueued, workflowRunNodeNamed(t, run, "Join").Status)
				require.Len(t, fixture.enqueuer.tasks, 4)
			} else {
				assert.Equal(t, db.WorkflowRunNodePending, workflowRunNodeNamed(t, run, "Join").Status)
			}

			rightTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Right"), test.rightStatus, workflowTaskMessage(test.rightStatus, "right failed"))
			require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rightTask))
			run = loadWorkflowRun(t, &fixture, run.ID)
			join := workflowRunNodeNamed(t, run, "Join")
			if join.Status == db.WorkflowRunNodeQueued {
				joinTask := finishWorkflowTask(t, fixture.store, join, task_logger.TaskSuccessStatus, "")
				require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(joinTask))
				run = loadWorkflowRun(t, &fixture, run.ID)
				join = workflowRunNodeNamed(t, run, "Join")
			}
			assert.Equal(t, test.expectedRunStatus, run.Status)
			assert.Equal(t, test.expectedJoin, join.Status)
			assert.LessOrEqual(t, len(fixture.enqueuer.tasks), 4)
		})
	}
}

func TestWorkflowServiceConcurrentPredecessorCompletionEnqueuesJoinOnce(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	workflow := createDiamondWorkflow(t, &fixture, db.WorkflowJoinAllSuccessful, 2, db.WorkflowEdgeAlways)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "simultaneous-diamond")
	require.NoError(t, err)
	rootTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Root"), task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	leftTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Left"), task_logger.TaskSuccessStatus, "")
	rightTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Right"), task_logger.TaskSuccessStatus, "")

	errors := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for _, task := range []db.Task{leftTask, rightTask} {
		task := task
		go func() {
			defer wait.Done()
			errors <- fixture.service.HandleWorkflowTaskCompletion(task)
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	run = loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunNodeQueued, workflowRunNodeNamed(t, run, "Join").Status)
	assert.Len(t, fixture.enqueuer.tasks, 4, "the ready join must be enqueued at most once")
}

func TestWorkflowServiceHonorsParallelismBoundAndJoinsSelectedBranch(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	workflow := createDiamondWorkflow(t, &fixture, db.WorkflowJoinAllSuccessful, 1, db.WorkflowEdgeExpression)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "bounded-conditional-diamond")
	require.NoError(t, err)
	rootTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Root"), task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))

	run = loadWorkflowRun(t, &fixture, run.ID)
	left := workflowRunNodeNamed(t, run, "Left")
	right := workflowRunNodeNamed(t, run, "Right")
	assert.Equal(t, db.WorkflowRunNodeQueued, left.Status)
	assert.Equal(t, db.WorkflowRunNodeSkipped, right.Status)
	assert.Len(t, fixture.enqueuer.tasks, 2, "parallelism one allows only one active branch")

	leftTask := finishWorkflowTask(t, fixture.store, left, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(leftTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	join := workflowRunNodeNamed(t, run, "Join")
	assert.Equal(t, db.WorkflowRunNodeQueued, join.Status, "a skipped branch must not poison the selected join")
	assert.Len(t, fixture.enqueuer.tasks, 3)

	joinTask := finishWorkflowTask(t, fixture.store, join, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(joinTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunSucceeded, run.Status)
}

func TestWorkflowServiceKeepsIndependentReadyNodesWithinParallelismBound(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	workflow := createDiamondWorkflow(t, &fixture, db.WorkflowJoinAllSuccessful, 1, db.WorkflowEdgeAlways)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "parallelism-one-diamond")
	require.NoError(t, err)
	rootTask := finishWorkflowTask(t, fixture.store, workflowRunNodeNamed(t, run, "Root"), task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))

	run = loadWorkflowRun(t, &fixture, run.ID)
	branches := []db.WorkflowRunNode{workflowRunNodeNamed(t, run, "Left"), workflowRunNodeNamed(t, run, "Right")}
	queued := make([]db.WorkflowRunNode, 0, 1)
	pending := 0
	for _, branch := range branches {
		if branch.Status == db.WorkflowRunNodeQueued {
			queued = append(queued, branch)
		}
		if branch.Status == db.WorkflowRunNodePending {
			pending++
		}
	}
	require.Len(t, queued, 1)
	assert.Equal(t, 1, pending)
	assert.Len(t, fixture.enqueuer.tasks, 2)

	firstBranchTask := finishWorkflowTask(t, fixture.store, queued[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(firstBranchTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, 1, workflowActiveNodeCount(run))
	assert.Len(t, fixture.enqueuer.tasks, 3, "the second branch starts only after the first completes")
}

func TestWorkflowServiceCapturesAndInjectsTypedWorkflowArtifacts(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	configureWorkflowArtifactEncryption(t)
	workflow := createArtifactWorkflow(t, &fixture)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "artifact-consumption")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	require.NotNil(t, producer.TaskID)
	producerTask, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)

	require.NoError(t, fixture.service.HandleWorkflowTaskOutputs(producerTask, map[string]json.RawMessage{
		"release": json.RawMessage(`"release-1"`),
		"token":   json.RawMessage(`"top-secret"`),
	}))
	stored, err := fixture.repository.GetWorkflowRunArtifacts(fixture.projectID, run.ID)
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.Equal(t, `"release-1"`, stored[0].ValueJSON)
	assert.Empty(t, stored[1].ValueJSON)
	assert.NotEmpty(t, stored[1].EncryptedValue)
	assert.NotContains(t, stored[1].EncryptedValue, "top-secret")
	metadata, err := fixture.service.GetWorkflowRunArtifacts(fixture.projectID, run.ID, nil)
	require.NoError(t, err)
	require.Len(t, metadata, 2)
	encodedMetadata, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedMetadata), "release-1")
	assert.NotContains(t, string(encodedMetadata), "top-secret")
	assert.NotContains(t, string(encodedMetadata), "encrypted_value")
	assert.True(t, metadata[1].Sensitive)
	_, err = fixture.service.GetWorkflowRunArtifacts(fixture.projectID+1, run.ID, nil)
	assert.ErrorIs(t, err, db.ErrNotFound)

	producerTask = finishWorkflowTask(t, fixture.store, producer, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(producerTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	consumer := workflowRunNodeNamed(t, run, "Consumer")
	assert.Equal(t, db.WorkflowRunNodeQueued, consumer.Status)
	require.Len(t, fixture.enqueuer.tasks, 2)
	consumerTask := fixture.enqueuer.tasks[1]
	plain, err := decodeWorkflowArtifactObject(consumerTask.Environment)
	require.NoError(t, err)
	secret, err := decodeWorkflowArtifactObject(consumerTask.Secret)
	require.NoError(t, err)
	assert.JSONEq(t, `"release-1"`, string(plain["release_name"]))
	assert.JSONEq(t, `"top-secret"`, string(secret["deployment_token"]))
	require.Len(t, consumer.ArtifactInputs, 2)
	for _, input := range consumer.ArtifactInputs {
		assert.Equal(t, db.WorkflowArtifactAvailable, input.Availability)
		assert.NotEmpty(t, input.ReferenceFingerprint)
		encoded, marshalErr := json.Marshal(input)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), "release-1")
		assert.NotContains(t, string(encoded), "top-secret")
	}
}

func TestWorkflowServiceFailsSuccessfulProducerWithInvalidDeclaredOutput(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	configureWorkflowArtifactEncryption(t)
	workflow := createArtifactWorkflow(t, &fixture)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "invalid-artifact")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	producerTask, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleWorkflowTaskOutputs(producerTask, map[string]json.RawMessage{
		"release": json.RawMessage(`123`),
		"token":   json.RawMessage(`"top-secret"`),
	}))

	producerTask = finishWorkflowTask(t, fixture.store, producer, task_logger.TaskSuccessStatus, "")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(producerTask))
	run = loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunFailed, run.Status)
	assert.Equal(t, db.WorkflowRunNodeFailed, workflowRunNodeNamed(t, run, "Producer").Status)
	assert.Contains(t, run.Reason, `Workflow output "release" is invalid`)
	assert.Equal(t, db.WorkflowRunNodeSkipped, workflowRunNodeNamed(t, run, "Consumer").Status)
	assert.Len(t, fixture.enqueuer.tasks, 1)
}

func TestWorkflowServiceRejectsSensitiveOutputWithoutEncryption(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	previous := util.Config.AccessKeyEncryption
	util.Config.AccessKeyEncryption = ""
	t.Cleanup(func() { util.Config.AccessKeyEncryption = previous })
	workflow := createArtifactWorkflow(t, &fixture)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "unencrypted-artifact")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	producerTask, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)

	err = fixture.service.HandleWorkflowTaskOutputs(producerTask, map[string]json.RawMessage{
		"release": json.RawMessage(`"release-1"`), "token": json.RawMessage(`"must-not-persist"`),
	})
	require.ErrorContains(t, err, "require access-key encryption")
	stored, getErr := fixture.repository.GetWorkflowRunArtifacts(fixture.projectID, run.ID)
	require.NoError(t, getErr)
	assert.Empty(t, stored)
}

func TestWorkflowServiceResolvesOnlyLatestProducerAttempt(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	configureWorkflowArtifactEncryption(t)
	workflow := createArtifactWorkflow(t, &fixture)
	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "artifact-retry")
	require.NoError(t, err)
	producer := workflowRunNodeNamed(t, run, "Producer")
	producerTask, err := fixture.store.GetTask(fixture.projectID, *producer.TaskID)
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleWorkflowTaskOutputs(producerTask, map[string]json.RawMessage{
		"release": json.RawMessage(`"release-1"`), "token": json.RawMessage(`"token-1"`),
	}))
	_, err = fixture.store.Sql().Exec("update task set assignment_generation=1 where id=?", producerTask.ID)
	require.NoError(t, err)
	producerTask, err = fixture.store.GetTask(fixture.projectID, producerTask.ID)
	require.NoError(t, err)
	require.NoError(t, fixture.service.HandleWorkflowTaskOutputs(producerTask, map[string]json.RawMessage{
		"release": json.RawMessage(`"release-2"`), "token": json.RawMessage(`"token-2"`),
	}))
	producerTask.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(producerTask))
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(producerTask))

	require.Len(t, fixture.enqueuer.tasks, 2)
	consumerTask := fixture.enqueuer.tasks[1]
	plain, err := decodeWorkflowArtifactObject(consumerTask.Environment)
	require.NoError(t, err)
	secret, err := decodeWorkflowArtifactObject(consumerTask.Secret)
	require.NoError(t, err)
	assert.JSONEq(t, `"release-2"`, string(plain["release_name"]))
	assert.JSONEq(t, `"token-2"`, string(secret["deployment_token"]))
	assert.NotContains(t, consumerTask.Environment, "release-1")
	assert.NotContains(t, consumerTask.Secret, "token-1")
}

func TestWorkflowServiceHandlesSkippedArtifactProducerByRequirement(t *testing.T) {
	for _, test := range []struct {
		name           string
		required       bool
		expectedStatus db.WorkflowRunNodeStatus
		expectedTasks  int
	}{
		{name: "required input blocks consumer", required: true, expectedStatus: db.WorkflowRunNodeBlocked, expectedTasks: 1},
		{name: "optional input lets consumer run", required: false, expectedStatus: db.WorkflowRunNodeQueued, expectedTasks: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowServiceFixture(t)
			defer fixture.store.Close()
			workflow := createSkippedArtifactWorkflow(t, &fixture, test.required)
			run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "skipped-artifact-"+test.name)
			require.NoError(t, err)
			root := workflowRunNodeNamed(t, run, "Root")
			rootTask := finishWorkflowTask(t, fixture.store, root, task_logger.TaskSuccessStatus, "")
			require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(rootTask))

			run = loadWorkflowRun(t, &fixture, run.ID)
			assert.Equal(t, db.WorkflowRunNodeSkipped, workflowRunNodeNamed(t, run, "Skipped producer").Status)
			consumer := workflowRunNodeNamed(t, run, "Consumer")
			assert.Equal(t, test.expectedStatus, consumer.Status)
			assert.Len(t, fixture.enqueuer.tasks, test.expectedTasks)
			require.Len(t, consumer.ArtifactInputs, 1)
			assert.Equal(t, db.WorkflowArtifactUnavailable, consumer.ArtifactInputs[0].Availability)
			assert.Nil(t, consumer.ArtifactInputs[0].ProducerTaskID)
			assert.NotEmpty(t, consumer.ArtifactInputs[0].ReferenceFingerprint)
			if test.required {
				assert.Contains(t, consumer.Reason, "Required workflow inputs are unavailable")
				assert.Equal(t, db.WorkflowRunBlocked, run.Status)
			} else {
				assert.NotContains(t, fixture.enqueuer.tasks[1].Environment, "release_name")
			}
		})
	}
}

func TestWorkflowReconcilerCanStopBeforeStart(t *testing.T) {
	reconciler := NewWorkflowReconciler(nil, nil)
	reconciler.Stop()
	reconciler.Start()
}

func TestWorkflowReconcilerScansImmediatelyOnStart(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	_, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "reconcile-on-start")
	require.NoError(t, err)

	signaled := &workflowReconcileSignalService{called: make(chan struct{}, 1)}
	reconciler := NewWorkflowReconciler(fixture.repository, signaled)
	reconciler.Start()
	t.Cleanup(reconciler.Stop)

	select {
	case <-signaled.called:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("workflow reconciliation did not run immediately on start")
	}
}

func TestWorkflowReconcilerQuarantinesRepeatedFailuresAndManualRetryClearsIt(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "reconcile-quarantine")
	require.NoError(t, err)
	failing := &workflowReconcileFailingService{}
	reconciler := NewWorkflowReconciler(fixture.repository, failing).(*workflowReconciler)
	for attempt := 0; attempt < workflowReconcileQuarantineAfter; attempt++ {
		reconciler.reconcile()
		if attempt+1 < workflowReconcileQuarantineAfter {
			_, err = fixture.store.Sql().Exec(
				"update project__workflow_run set reconciliation_next_retry_at=CURRENT_TIMESTAMP where project_id=? and id=?",
				fixture.projectID, run.ID,
			)
			require.NoError(t, err)
		}
	}

	quarantined, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunReconciliationQuarantined, quarantined.ReconciliationState)
	assert.NotNil(t, quarantined.ReconciliationQuarantinedAt)
	assert.Equal(t, workflowReconcileQuarantineAfter, quarantined.ReconciliationAttempts)

	retried, err := fixture.service.RetryWorkflowRunReconciliation(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunReconciliationRecovering, retried.ReconciliationState)
	assert.Zero(t, retried.ReconciliationAttempts)
	assert.Nil(t, retried.ReconciliationQuarantinedAt)
}

func TestWorkflowReconcilerFailureDoesNotOverwriteAConcurrentStopRequest(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "reconcile-stop-race")
	require.NoError(t, err)

	staleRun, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	_, err = fixture.service.RequestWorkflowRunStop(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)

	reconciler := NewWorkflowReconciler(fixture.repository, nil).(*workflowReconciler)
	reconciler.recordFailure(staleRun, errors.New("transient reconciliation failure"))

	persisted, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunDesiredStopping, persisted.DesiredState)
	assert.Equal(t, db.WorkflowRunStopping, persisted.Status)
	assert.Equal(t, db.WorkflowRunReconciliationRecovering, persisted.ReconciliationState)
}

func TestWorkflowReconciliationDiagnosticPreservesValidUTF8WithinByteLimit(t *testing.T) {
	diagnostic := boundedWorkflowReconciliationError(strings.Repeat("€", 171))
	assert.LessOrEqual(t, len(diagnostic), maxWorkflowReconciliationErrorBytes)
	assert.True(t, utf8.ValidString(diagnostic))
}

func TestWorkflowReconciliationDiagnosticRedactsSecretLikeErrors(t *testing.T) {
	diagnostic := boundedWorkflowReconciliationError("reconcile remote secret=do-not-expose")
	assert.Contains(t, diagnostic, "secret=[REDACTED]")
	assert.NotContains(t, diagnostic, "do-not-expose")
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
			errors <- service.withRunLock(1, 1, func(_ *pro_interfaces.WorkflowReconciliationLease) error {
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

func TestConcurrentWorkflowReconcilersConvergeFromSQLWithoutDuplicateTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	ownership := workflowSQL.NewWorkflowReconciliationStore(fixture.store.GetConnection())
	serviceA := NewWorkflowService(
		fixture.repository, fixture.store, fixture.enqueuer,
		&workflowSQLTestLocker{repository: ownership, ownerBootID: "boot-a"},
	)
	serviceB := NewWorkflowService(
		fixture.repository, fixture.store, fixture.enqueuer,
		&workflowSQLTestLocker{repository: ownership, ownerBootID: "boot-b"},
	)
	run, err := serviceA.StartWorkflow(fixture.workflow, &fixture.user, "ha-sql-replay")
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.tasks, 1)
	completedTask := fixture.enqueuer.tasks[0]
	completedTask.Status = task_logger.TaskSuccessStatus
	now := time.Now().UTC()
	completedTask.End = &now
	require.NoError(t, fixture.store.UpdateTask(completedTask))

	var wait sync.WaitGroup
	errors := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, reconcileErr := serviceA.ReconcileWorkflowRun(fixture.projectID, run.ID)
		errors <- reconcileErr
	}()
	go func() {
		defer wait.Done()
		errors <- serviceB.HandleWorkflowTaskCompletion(completedTask)
	}()
	wait.Wait()
	close(errors)
	for reconcileErr := range errors {
		require.NoError(t, reconcileErr)
	}
	_, err = serviceB.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)

	fixture.enqueuer.mutex.Lock()
	createdTasks := append([]db.Task(nil), fixture.enqueuer.tasks...)
	fixture.enqueuer.mutex.Unlock()
	require.Len(t, createdTasks, 2, "lost or duplicated live events must still produce one downstream logical task")
	assert.NotEqual(t, createdTasks[0].ID, createdTasks[1].ID)
	reloaded := loadWorkflowRun(t, &fixture, run.ID)
	assert.True(t, reloaded.ReconciliationOwnership.Recovered)
	assert.GreaterOrEqual(t, reloaded.ReconciliationOwnership.TransferCount, 1)
}

func TestWorkflowOwnershipTransferReplaysBranchJoinExactlyOnce(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	workflow := createDiamondWorkflow(t, &fixture, db.WorkflowJoinAllComplete, 3, db.WorkflowEdgeAlways)
	serviceA, serviceB := workflowHATestServices(&fixture)

	run, err := serviceA.StartWorkflow(workflow, &fixture.user, "ha-branch-join")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, serviceB.HandleWorkflowTaskCompletion(root))
	running := loadWorkflowRun(t, &fixture, run.ID)
	left := workflowRunNodeNamed(t, running, "Left")
	right := workflowRunNodeNamed(t, running, "Right")
	leftTask := finishWorkflowTask(t, fixture.store, left, task_logger.TaskSuccessStatus, "")
	rightTask := finishWorkflowTask(t, fixture.store, right, task_logger.TaskSuccessStatus, "")

	require.NoError(t, serviceA.HandleWorkflowTaskCompletion(leftTask))
	require.NoError(t, serviceB.HandleWorkflowTaskCompletion(rightTask))
	require.NoError(t, serviceA.ProgressWorkflowRun(fixture.projectID, run.ID, nil))

	joined := loadWorkflowRun(t, &fixture, run.ID)
	join := workflowRunNodeNamed(t, joined, "Join")
	require.NotNil(t, join.TaskID)
	fixture.enqueuer.mutex.Lock()
	createdTasks := append([]db.Task(nil), fixture.enqueuer.tasks...)
	fixture.enqueuer.mutex.Unlock()
	assert.Len(t, createdTasks, 4, "branch replay must create one root, two branches, and one join task")
	assert.True(t, joined.ReconciliationOwnership.Recovered)
}

func TestWorkflowOwnershipTransferReplaysApprovalDecisionExactlyOnce(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "HA approval", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Prepare"},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, DisplayName: "Approve"},
			{ID: -3, TemplateID: fixture.second.ID, DisplayName: "Deploy"},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeOnSuccess},
		},
	})
	require.NoError(t, err)
	serviceA, serviceB := workflowHATestServices(&fixture)

	run, err := serviceA.StartWorkflow(workflow, &fixture.user, "ha-approval-replay")
	require.NoError(t, err)
	root := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskSuccessStatus, "")
	require.NoError(t, serviceB.HandleWorkflowTaskCompletion(root))
	approval, err := fixture.repository.GetWorkflowApproval(fixture.projectID, run.ID, workflow.Nodes[1].ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	approval.Status = db.WorkflowApprovalApproved
	approval.Resolved = &now
	approval.ResolvedByUserID = &fixture.user.ID
	approval.DecisionSource = db.WorkflowApprovalDecisionSourceUser
	resolved, err := fixture.repository.ResolveWorkflowApprovalIfPending(approval)
	require.NoError(t, err)
	require.True(t, resolved, "the approval fact must be durable before reconciliation wakes")

	_, err = serviceA.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)
	_, err = serviceB.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)

	resumed := loadWorkflowRun(t, &fixture, run.ID)
	deploy := workflowRunNodeNamed(t, resumed, "Deploy")
	require.NotNil(t, deploy.TaskID)
	fixture.enqueuer.mutex.Lock()
	createdTasks := append([]db.Task(nil), fixture.enqueuer.tasks...)
	fixture.enqueuer.mutex.Unlock()
	assert.Len(t, createdTasks, 2, "approval replay must create one prepare and one deploy task")
	assert.True(t, resumed.ReconciliationOwnership.Recovered)
}

func TestWorkflowOwnershipTransferReplaysStopWithoutDownstreamTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()
	serviceA, serviceB := workflowHATestServices(&fixture)

	run, err := serviceA.StartWorkflow(fixture.workflow, &fixture.user, "ha-stop-replay")
	require.NoError(t, err)
	_, err = serviceA.RequestWorkflowRunStop(fixture.projectID, run.ID, &fixture.user)
	require.NoError(t, err)
	_, err = serviceB.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)
	_, err = serviceA.ReconcileWorkflowRun(fixture.projectID, run.ID)
	require.NoError(t, err)

	stopped := loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunCanceled, stopped.Status)
	assert.Equal(t, db.WorkflowRunDesiredStopped, stopped.DesiredState)
	fixture.enqueuer.mutex.Lock()
	createdTasks := append([]db.Task(nil), fixture.enqueuer.tasks...)
	fixture.enqueuer.mutex.Unlock()
	assert.Len(t, createdTasks, 1, "stop replay must not create the pending downstream task")
	assert.True(t, stopped.ReconciliationOwnership.Recovered)
}

func workflowHATestServices(fixture *workflowServiceFixture) (pro_interfaces.WorkflowService, pro_interfaces.WorkflowService) {
	ownership := workflowSQL.NewWorkflowReconciliationStore(fixture.store.GetConnection())
	return NewWorkflowService(
			fixture.repository, fixture.store, fixture.enqueuer,
			&workflowSQLTestLocker{repository: ownership, ownerBootID: "boot-a"},
		), NewWorkflowService(
			fixture.repository, fixture.store, fixture.enqueuer,
			&workflowSQLTestLocker{repository: ownership, ownerBootID: "boot-b"},
		)
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

func ensureWorkflowApprovalMember(store *coresql.SqlDb, projectID int, userID int, role db.ProjectUserRole) error {
	if _, err := store.GetProjectUser(projectID, userID); err == nil {
		return nil
	}
	_, err := store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: userID, Role: role})
	return err
}

func configureWorkflowArtifactEncryption(t *testing.T) {
	t.Helper()
	previous := util.Config.AccessKeyEncryption
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32)))
	t.Cleanup(func() { util.Config.AccessKeyEncryption = previous })
}

func createArtifactWorkflow(t *testing.T, fixture *workflowServiceFixture) db.WorkflowTemplate {
	t.Helper()
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Artifact workflow", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{
				ID: -1, TemplateID: fixture.first.ID, DisplayName: "Producer",
				ArtifactOutputs: []db.WorkflowArtifactDeclaration{
					{Name: "release", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, MaxBytes: 128},
					{Name: "token", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, Sensitive: true, MaxBytes: 128},
				},
			},
			{
				ID: -2, TemplateID: fixture.second.ID, DisplayName: "Consumer",
				ArtifactInputs: []db.WorkflowArtifactReference{
					{Name: "release_name", SourceNodeID: -1, Output: "release", Required: true},
					{Name: "deployment_token", SourceNodeID: -1, Output: "token", Required: true},
				},
			},
		},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess,
		}},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	created, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	return created
}

func createSkippedArtifactWorkflow(t *testing.T, fixture *workflowServiceFixture, required bool) db.WorkflowTemplate {
	t.Helper()
	producer, err := fixture.store.CreateTemplate(db.Template{
		ProjectID: fixture.projectID, RepositoryID: fixture.first.RepositoryID,
		Name: "Skipped producer", Playbook: "skipped-producer.yml",
	})
	require.NoError(t, err)
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Skipped artifact producer", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Root"},
			{
				ID: -2, TemplateID: producer.ID, DisplayName: "Skipped producer",
				ArtifactOutputs: []db.WorkflowArtifactDeclaration{{
					Name: "release", Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString}, MaxBytes: 128,
				}},
			},
			{
				ID: -3, TemplateID: fixture.second.ID, DisplayName: "Consumer", JoinMode: db.WorkflowJoinAllComplete,
				ArtifactInputs: []db.WorkflowArtifactReference{{
					Name: "release_name", SourceNodeID: -2, Output: "release", Required: required,
				}},
			},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnFailure},
			{ID: -2, SourceNodeID: -2, DestinationNodeID: -3, Condition: db.WorkflowEdgeAlways},
			{ID: -3, SourceNodeID: -1, DestinationNodeID: -3, Condition: db.WorkflowEdgeAlways},
		},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	created, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	return created
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
	mutex           sync.Mutex
	store           *coresql.SqlDb
	tasks           []db.Task
	inputTasks      []db.Task
	templates       []db.Template
	failAfterCreate bool
}

type workflowReconcileFailingService struct {
	pro_interfaces.WorkflowService
}

func (*workflowReconcileFailingService) ReconcileWorkflowRun(int, int) (db.WorkflowRun, error) {
	return db.WorkflowRun{}, errors.New("transient reconciliation failure")
}

type workflowReconcileSignalService struct {
	pro_interfaces.WorkflowService
	called chan struct{}
}

func (s *workflowReconcileSignalService) ReconcileWorkflowRun(int, int) (db.WorkflowRun, error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	return db.WorkflowRun{}, nil
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
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.inputTasks = append(e.inputTasks, task)
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

func (e *workflowTestEnqueuer) AddWorkflowTaskFenced(
	task db.Task,
	template db.Template,
	userID *int,
	username string,
	projectID int,
	needAlias bool,
	lease pro_interfaces.WorkflowReconciliationLease,
) (db.Task, error) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.inputTasks = append(e.inputTasks, task)
	templateJSON, err := json.Marshal(template)
	if err != nil {
		return db.Task{}, err
	}
	snapshot := string(templateJSON)
	task.ProjectID = projectID
	task.Status = task_logger.TaskWaitingStatus
	task.WorkflowTemplateSnapshot = &snapshot
	created, err := e.store.CreateWorkflowTaskFenced(task, 0, lease)
	if err != nil {
		return db.Task{}, err
	}
	e.tasks = append(e.tasks, created)
	e.templates = append(e.templates, template)
	return created, nil
}

type workflowSQLTestLocker struct {
	repository  pro_interfaces.WorkflowReconciliationRepository
	ownerBootID string
}

func (l *workflowSQLTestLocker) TryLockRun(projectID int, runID int) (pro_interfaces.WorkflowReconciliationLease, func(), bool, error) {
	lease, claimed, err := l.repository.ClaimWorkflowReconciliation(projectID, runID, l.ownerBootID, time.Minute)
	if err != nil || !claimed {
		return lease, nil, false, err
	}
	return lease, func() { _, _ = l.repository.ReleaseWorkflowReconciliation(lease) }, true, nil
}

func (*workflowSQLTestLocker) TryLockStart(int, int) (func(), bool) {
	return func() {}, true
}

func (l *workflowSQLTestLocker) RecordReconciled(lease pro_interfaces.WorkflowReconciliationLease) error {
	recorded, err := l.repository.RecordWorkflowReconciled(lease)
	if err != nil {
		return err
	}
	if !recorded {
		return errors.New("stale workflow reconciliation owner")
	}
	return nil
}

func (*workflowSQLTestLocker) Drain() error { return nil }
func (*workflowSQLTestLocker) Resume()      {}

type workflowCredentialReaderStub struct {
	value  string
	values []string
	calls  int
}

func (r *workflowCredentialReaderStub) DeserializeSecret(key *db.AccessKey) error {
	r.calls++
	if len(r.values) >= r.calls {
		key.String = r.values[r.calls-1]
	} else {
		key.String = r.value
	}
	return nil
}

func (e *workflowTestEnqueuer) StopTasksByWorkflowRun(projectID int, runID int, forceStop bool) {}

func createDiamondWorkflow(
	t *testing.T,
	fixture *workflowServiceFixture,
	joinMode db.WorkflowJoinMode,
	maxParallel int,
	rightCondition db.WorkflowEdgeCondition,
) db.WorkflowTemplate {
	t.Helper()
	left, err := fixture.store.CreateTemplate(db.Template{
		ProjectID: fixture.projectID, RepositoryID: fixture.first.RepositoryID, Name: "Left", Playbook: "left.yml",
	})
	require.NoError(t, err)
	right, err := fixture.store.CreateTemplate(db.Template{
		ProjectID: fixture.projectID, RepositoryID: fixture.first.RepositoryID, Name: "Right", Playbook: "right.yml",
	})
	require.NoError(t, err)
	join, err := fixture.store.CreateTemplate(db.Template{
		ProjectID: fixture.projectID, RepositoryID: fixture.first.RepositoryID, Name: "Join", Playbook: "join.yml",
	})
	require.NoError(t, err)
	rightExpression := ""
	if rightCondition == db.WorkflowEdgeExpression {
		rightExpression = `result.status == "failed"`
	}
	raw := db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Diamond", DefinitionVersion: db.WorkflowDefinitionVersion,
		MaxParallelTasks: maxParallel,
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: fixture.first.ID, DisplayName: "Root"},
			{ID: -2, TemplateID: left.ID, DisplayName: "Left"},
			{ID: -3, TemplateID: right.ID, DisplayName: "Right"},
			{ID: -4, TemplateID: join.ID, DisplayName: "Join", JoinMode: joinMode},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeAlways},
			{ID: -2, SourceNodeID: -1, DestinationNodeID: -3, Condition: rightCondition, Expression: rightExpression},
			{ID: -3, SourceNodeID: -2, DestinationNodeID: -4, Condition: db.WorkflowEdgeAlways},
			{ID: -4, SourceNodeID: -3, DestinationNodeID: -4, Condition: db.WorkflowEdgeAlways},
		},
	}
	prepared, validation, err := workflowDB.PrepareWorkflowTemplate(fixture.store, raw)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	created, err := fixture.repository.CreateWorkflowTemplate(prepared)
	require.NoError(t, err)
	return created
}

func loadWorkflowRun(t *testing.T, fixture *workflowServiceFixture, runID int) db.WorkflowRun {
	t.Helper()
	run, err := fixture.repository.GetWorkflowRunByID(fixture.projectID, runID)
	require.NoError(t, err)
	return run
}

func workflowRunNodeNamed(t *testing.T, run db.WorkflowRun, name string) db.WorkflowRunNode {
	t.Helper()
	nodeID := 0
	for _, node := range run.DefinitionSnapshot.Nodes {
		if node.DisplayName == name {
			nodeID = node.ID
			break
		}
	}
	require.NotZero(t, nodeID, "workflow definition node %q is missing", name)
	for _, node := range run.Nodes {
		if node.WorkflowNodeID == nodeID {
			return node
		}
	}
	require.FailNow(t, "workflow run node is missing", name)
	return db.WorkflowRunNode{}
}

func workflowTaskMessage(status task_logger.TaskStatus, message string) string {
	if status == task_logger.TaskFailStatus || status == task_logger.TaskRejected || status == task_logger.TaskStoppedStatus {
		return message
	}
	return ""
}

func workflowActiveNodeCount(run db.WorkflowRun) int {
	active := 0
	for _, node := range run.Nodes {
		if node.Status == db.WorkflowRunNodeQueued || node.Status == db.WorkflowRunNodeRunning {
			active++
		}
	}
	return active
}
