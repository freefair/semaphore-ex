package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRunRepositoryPersistsTaskDerivedBlockedNodeTerminally(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, first, second := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, first.ID, second.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		first.ID: first, second.ID: second,
	}, user.ID, "blocked-task", now)
	require.NoError(t, err)
	run, err = repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	rootNodeID := run.Nodes[0].WorkflowNodeID

	claimed, err := repository.ClaimWorkflowRunNode(projectID, run.ID, rootNodeID, now)
	require.NoError(t, err)
	require.True(t, claimed)
	task, err := store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: first.ID, Status: task_logger.TaskBlockedStatus,
		Message: "A required global credential is unavailable.", WorkflowRunID: &run.ID, WorkflowNodeID: &rootNodeID,
	}, 0)
	require.NoError(t, err)
	attached, err := repository.AttachWorkflowRunNodeTask(projectID, run.ID, rootNodeID, task.ID)
	require.NoError(t, err)
	require.True(t, attached)

	updated, err := repository.UpdateWorkflowRunNodeFromTask(
		projectID, run.ID, rootNodeID, task.ID, db.WorkflowRunNodeBlocked, task.Message,
		`{"status":"blocked","successful":false}`, now.Add(time.Second),
	)
	require.NoError(t, err)
	assert.True(t, updated)

	node, err := repository.GetWorkflowRunNode(projectID, run.ID, rootNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeBlocked, node.Status)
	assert.Equal(t, task.Message, node.Reason)
	assert.Equal(t, db.WorkflowRunNodeBlocked, node.Result.Status)
	assert.False(t, node.Result.Successful)
	assert.NotNil(t, node.End)

	updated, err = repository.UpdateWorkflowRunNodeFromTask(
		projectID, run.ID, rootNodeID, task.ID, db.WorkflowRunNodeFailed, "late replacement",
		`{"status":"failed","successful":false}`, now.Add(2*time.Second),
	)
	require.NoError(t, err)
	assert.False(t, updated, "a terminal blocked node cannot be overwritten")
}
