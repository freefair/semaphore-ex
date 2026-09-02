package sql

import (
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

func TestWorkflowReconciliationOwnershipTransfersFenceAndPublishesDiagnostics(t *testing.T) {
	database, repository, projectID, run := workflowReconciliationFixture(t, "ha-ownership")
	t.Cleanup(database.Close)
	ownership := NewWorkflowReconciliationStore(database.GetConnection())

	first, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.EqualValues(t, 1, first.FencingToken)

	blocked, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Equal(t, first.OwnerBootID, blocked.OwnerBootID)

	_, err = database.GetConnection().Exec(
		"update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=?",
		projectID, run.ID,
	)
	require.NoError(t, err)
	second, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.EqualValues(t, 2, second.FencingToken)
	assert.Equal(t, "boot-a", second.PreviousOwnerBootID)
	assert.EqualValues(t, 1, second.TransferCount)
	require.NotNil(t, second.OwnershipTransferredAt)

	current, err := ownership.IsCurrentWorkflowReconciliation(first)
	require.NoError(t, err)
	assert.False(t, current)
	released, err := ownership.ReleaseWorkflowReconciliation(first)
	require.NoError(t, err)
	assert.False(t, released)
	recorded, err := ownership.RecordWorkflowReconciled(second)
	require.NoError(t, err)
	assert.True(t, recorded)

	reloaded, err := repository.GetWorkflowRunByID(projectID, run.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ReconciliationOwnership)
	assert.True(t, reloaded.ReconciliationOwnership.Owned)
	assert.True(t, reloaded.ReconciliationOwnership.Recovered)
	assert.Equal(t, "boot-b", reloaded.ReconciliationOwnership.OwnerBootID)
	assert.Equal(t, "boot-a", reloaded.ReconciliationOwnership.PreviousOwnerBootID)
	assert.EqualValues(t, 2, reloaded.ReconciliationOwnership.FencingToken)
	assert.GreaterOrEqual(t, reloaded.ReconciliationOwnership.LeaseAgeSeconds, int64(0))
	assert.GreaterOrEqual(t, reloaded.ReconciliationOwnership.ReconciliationLagSeconds, int64(0))

	health, err := ownership.WorkflowProgressionHealth()
	require.NoError(t, err)
	assert.Equal(t, 1, health.CurrentOwnerships)
	assert.Equal(t, 1, health.TransferCount)
	assert.GreaterOrEqual(t, health.MaxLagSeconds, int64(0))
}

func TestWorkflowReconciliationSameOwnerReclaimAdvancesFenceWithoutTransfer(t *testing.T) {
	database, _, projectID, run := workflowReconciliationFixture(t, "ha-same-owner-reclaim")
	t.Cleanup(database.Close)
	ownership := NewWorkflowReconciliationStore(database.GetConnection())

	first, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec(
		"update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=?",
		projectID, run.ID,
	)
	require.NoError(t, err)
	second, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	assert.Greater(t, second.FencingToken, first.FencingToken)
	assert.Zero(t, second.TransferCount)
	assert.Empty(t, second.PreviousOwnerBootID)
	assert.Nil(t, second.OwnershipTransferredAt)
}

func TestWorkflowProgressionHealthExcludesTerminalRuns(t *testing.T) {
	database, _, projectID, run := workflowReconciliationFixture(t, "ha-terminal-health")
	t.Cleanup(database.Close)
	ownership := NewWorkflowReconciliationStore(database.GetConnection())
	_, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec(
		"update project__workflow_run set status=? where project_id=? and id=?",
		db.WorkflowRunSucceeded, projectID, run.ID,
	)
	require.NoError(t, err)

	health, err := ownership.WorkflowProgressionHealth()
	require.NoError(t, err)
	assert.Zero(t, health.CurrentOwnerships)
	assert.Zero(t, health.ExpiredOwnerships)
	assert.Zero(t, health.MaxLagSeconds)
}

func TestWorkflowNodeAttemptRejectsExpiredOwner(t *testing.T) {
	database, repository, projectID, run := workflowReconciliationFixture(t, "ha-stale-attempt")
	t.Cleanup(database.Close)
	node := run.Nodes[0]
	ownership := NewWorkflowReconciliationStore(database.GetConnection())

	first, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repository.ClaimWorkflowRunNodeFenced(first, node.WorkflowNodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)

	_, err = database.GetConnection().Exec(
		"update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=?",
		projectID, run.ID,
	)
	require.NoError(t, err)
	second, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repository.ClaimWorkflowRunNodeFenced(second, node.WorkflowNodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)

	task := db.Task{
		ProjectID: projectID, TemplateID: node.TemplateID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &run.ID, WorkflowNodeID: &node.WorkflowNodeID,
		WorkflowTemplateSnapshot: &node.TemplateSnapshotJSON,
	}
	_, err = database.CreateWorkflowTaskFenced(task, 0, first)
	require.ErrorContains(t, err, "stale workflow reconciliation owner")
	created, err := database.CreateWorkflowTaskFenced(task, 0, second)
	require.NoError(t, err)
	assert.Positive(t, created.ID)

	_, err = database.CreateWorkflowTaskFenced(task, 0, second)
	require.Error(t, err, "the unique run/node boundary must reject a duplicate logical task")
}

func TestFencedWorkflowTaskAtomicallyBindsDeploymentWindowDecision(t *testing.T) {
	database, repository, projectID, run := workflowReconciliationFixture(t, "ha-deployment-window")
	t.Cleanup(database.Close)
	node := run.Nodes[0]
	ownership := NewWorkflowReconciliationStore(database.GetConnection())
	lease, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "deployment-window", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repository.ClaimWorkflowRunNodeFenced(lease, node.WorkflowNodeID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed)

	admissions := NewDeploymentWindowStore(database.GetConnection())
	templateID, workflowID, runID, runNodeID := node.TemplateID, run.WorkflowTemplateID, run.ID, node.ID
	claim, err := admissions.ClaimDeploymentWindowAdmission(pro_interfaces.DeploymentWindowAdmissionRequest{
		ProjectID: projectID, DecisionKey: "fenced-node", Source: pro_interfaces.DeploymentWindowSourceWorkflowNode,
		Origin: pro_interfaces.DeploymentWindowOriginWorkflowNode, TemplateID: &templateID, WorkflowID: &workflowID,
		WorkflowRunID: &runID, WorkflowRunNodeID: &runNodeID,
	}, deploymentWindowEvaluator().Evaluate)
	require.NoError(t, err)
	decisionID := claim.Decision.ID
	task := db.Task{ProjectID: projectID, TemplateID: node.TemplateID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &run.ID, WorkflowNodeID: &node.WorkflowNodeID, WorkflowTemplateSnapshot: &node.TemplateSnapshotJSON,
		DeploymentWindowDecisionID: &decisionID}
	created, err := database.CreateWorkflowTaskFenced(task, 0, lease)
	require.NoError(t, err)
	var boundTaskID, nodeTaskID int
	require.NoError(t, database.Sql().SelectOne(&boundTaskID, "select task_id from project__deployment_window_decision where id=?", decisionID))
	require.NoError(t, database.Sql().SelectOne(&nodeTaskID, "select task_id from project__workflow_run_node where id=?", node.ID))
	assert.Equal(t, created.ID, boundTaskID)
	assert.Equal(t, created.ID, nodeTaskID)
}

func TestWorkflowApprovalAttemptRejectsExpiredOwner(t *testing.T) {
	database, repository, projectID, run := workflowReconciliationFixture(t, "ha-stale-approval")
	t.Cleanup(database.Close)
	ownership := NewWorkflowReconciliationStore(database.GetConnection())
	first, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec(
		"update cluster__workflow_reconciliation set lease_expires_at=CURRENT_TIMESTAMP where project_id=? and workflow_run_id=?",
		projectID, run.ID,
	)
	require.NoError(t, err)
	second, claimed, err := ownership.ClaimWorkflowReconciliation(projectID, run.ID, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	approvalNode := run.Nodes[1]
	approval := db.WorkflowApproval{
		ProjectID: projectID, WorkflowRunID: run.ID, WorkflowNodeID: approvalNode.WorkflowNodeID,
		Status: db.WorkflowApprovalPending, Created: time.Now().UTC(), Prompt: "Approve",
		EligiblePermission: db.CanManageProjectResources,
		RequestActorUserID: run.ActorUserID, TimeoutOutcome: db.WorkflowApprovalTimeoutReject,
		CorrelationID: run.CorrelationID + ":approval",
	}

	_, _, err = repository.OpenWorkflowApprovalFenced(first, approval)
	require.ErrorContains(t, err, "stale workflow reconciliation owner")
	created, opened, err := repository.OpenWorkflowApprovalFenced(second, approval)
	require.NoError(t, err)
	assert.True(t, opened)
	assert.Positive(t, created.ID)
	_, opened, err = repository.OpenWorkflowApprovalFenced(second, approval)
	require.NoError(t, err)
	assert.False(t, opened)
}

func TestFairWorkflowRunOrderInterleavesProjects(t *testing.T) {
	runs := []db.WorkflowRun{
		{ID: 11, ProjectID: 1}, {ID: 12, ProjectID: 1}, {ID: 13, ProjectID: 1},
		{ID: 21, ProjectID: 2}, {ID: 31, ProjectID: 3}, {ID: 32, ProjectID: 3},
	}
	fair := fairWorkflowRunOrder(runs)
	ids := make([]int, 0, len(fair))
	for _, run := range fair {
		ids = append(ids, run.ID)
	}
	assert.Equal(t, []int{11, 21, 31, 12, 32, 13}, ids)
}

func workflowReconciliationFixture(t *testing.T, correlationID string) (*coresql.SqlDb, *WorkflowStoreImpl, int, db.WorkflowRun) {
	t.Helper()
	database, repository, projectID := workflowRepositoryFixture(t)
	user, firstTemplate, secondTemplate := workflowRunResources(t, database, projectID)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, firstTemplate.ID, secondTemplate.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		firstTemplate.ID: firstTemplate, secondTemplate.ID: secondTemplate,
	}, user.ID, correlationID, time.Now().UTC())
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	return database, repository, projectID, run
}
