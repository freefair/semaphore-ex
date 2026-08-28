package sql

import (
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRunRepositoryPersistsImmutableSnapshotAndConditionalNodeState(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	rootNodeID := workflow.Nodes[0].ID
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "run-once", now)
	require.NoError(t, err)

	created, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Len(t, created.Nodes, 2)

	workflow.Name = "Edited later"
	templateOne.Playbook = "edited.yml"
	reloaded, err := repository.GetWorkflowRun(projectID, workflow.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Linear", reloaded.DefinitionSnapshot.Name)
	assert.Equal(t, "first.yml", reloaded.Nodes[0].TemplateSnapshot.Playbook)
	assert.Equal(t, user.ID, reloaded.ActorUserID)
	assert.Equal(t, "run-once", reloaded.CorrelationID)

	claimed, err := repository.ClaimWorkflowRunNode(projectID, created.ID, rootNodeID, now.Add(time.Second))
	require.NoError(t, err)
	assert.True(t, claimed)
	claimed, err = repository.ClaimWorkflowRunNode(projectID, created.ID, rootNodeID, now.Add(2*time.Second))
	require.NoError(t, err)
	assert.False(t, claimed)

	task, err := store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &created.ID, WorkflowNodeID: &rootNodeID,
		WorkflowTemplateSnapshot: &created.Nodes[0].TemplateSnapshotJSON,
	}, 0)
	require.NoError(t, err)
	attached, err := repository.AttachWorkflowRunNodeTask(projectID, created.ID, rootNodeID, task.ID)
	require.NoError(t, err)
	assert.True(t, attached)
	attached, err = repository.AttachWorkflowRunNodeTask(projectID, created.ID, rootNodeID, task.ID)
	require.NoError(t, err)
	assert.False(t, attached)

	_, err = store.CreateTask(db.Task{
		ProjectID: projectID, TemplateID: templateOne.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &created.ID, WorkflowNodeID: &rootNodeID,
	}, 0)
	require.Error(t, err, "one run attempt must not create two tasks for one node")

	updated, err := repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeRunning, "", "{}", now.Add(3*time.Second))
	require.NoError(t, err)
	assert.True(t, updated)
	updated, err = repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeSucceeded, "", `{"status":"succeeded","successful":true}`, now.Add(4*time.Second))
	require.NoError(t, err)
	assert.True(t, updated)
	updated, err = repository.UpdateWorkflowRunNodeFromTask(projectID, created.ID, rootNodeID, task.ID, db.WorkflowRunNodeFailed, "late duplicate", `{"status":"failed","successful":false}`, now.Add(5*time.Second))
	require.NoError(t, err)
	assert.False(t, updated)

	node, err := repository.GetWorkflowRunNode(projectID, created.ID, rootNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, node.Status)
	assert.Equal(t, db.WorkflowRunNodeSucceeded, node.Result.Status)
	assert.True(t, node.Result.Successful)
	require.NotNil(t, node.Start)
	require.NotNil(t, node.End)

	_, err = repository.GetWorkflowRun(projectID+1, workflow.ID, created.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = repository.GetWorkflowRunNode(projectID+1, created.ID, rootNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound)

	dependentNodeID := workflow.Nodes[1].ID
	finalized, err := repository.FinalizeWorkflowRunNode(
		projectID, created.ID, dependentNodeID, db.WorkflowRunNodeSkipped,
		"condition did not match", `{"status":"skipped","successful":false}`, now.Add(6*time.Second),
	)
	require.NoError(t, err)
	assert.True(t, finalized)
	finalized, err = repository.FinalizeWorkflowRunNode(
		projectID, created.ID, dependentNodeID, db.WorkflowRunNodeBlocked,
		"late duplicate", `{"status":"blocked","successful":false}`, now.Add(7*time.Second),
	)
	require.NoError(t, err)
	assert.False(t, finalized)
	dependent, err := repository.GetWorkflowRunNode(projectID, created.ID, dependentNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowRunNodeSkipped, dependent.Status)
	assert.Equal(t, db.WorkflowRunNodeSkipped, dependent.Result.Status)
}

func TestWorkflowRunRepositoryDeduplicatesCorrelationAndRollsBackNodes(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	user, templateOne, templateTwo := workflowRunResources(t, store, projectID)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	workflow, err := repository.CreateWorkflowTemplate(linearRepositoryWorkflow(projectID, templateOne.ID, templateTwo.ID))
	require.NoError(t, err)
	run, err := workflowDB.BuildWorkflowRunSnapshot(workflow, map[int]db.Template{
		templateOne.ID: templateOne, templateTwo.ID: templateTwo,
	}, user.ID, "deduplicated", now)
	require.NoError(t, err)
	first, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	second, err := repository.CreateWorkflowRun(run)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	broken := run
	broken.ID = 0
	broken.CorrelationID = "rollback"
	broken.Nodes = append([]db.WorkflowRunNode(nil), run.Nodes...)
	broken.Nodes[1].WorkflowNodeID = broken.Nodes[0].WorkflowNodeID
	_, err = repository.CreateWorkflowRun(broken)
	require.Error(t, err)
	_, err = repository.GetWorkflowRunByCorrelationID(projectID, workflow.ID, "rollback")
	assert.True(t, errors.Is(err, db.ErrNotFound))
}

func TestDecodeWorkflowRunNodeLoadsResultWithoutTemplateSnapshot(t *testing.T) {
	node := db.WorkflowRunNode{ResultJSON: `{"status":"skipped","successful":false}`}
	require.NoError(t, decodeWorkflowRunNode(&node))
	assert.Equal(t, db.WorkflowRunNodeSkipped, node.Result.Status)
	assert.False(t, node.Result.Successful)
}

func workflowRunResources(t *testing.T, store interface {
	CreateUserWithoutPassword(db.User) (db.User, error)
	CreateAccessKey(db.AccessKey) (db.AccessKey, error)
	CreateRepository(db.Repository) (db.Repository, error)
	CreateTemplate(db.Template) (db.Template, error)
}, projectID int) (db.User, db.Template, db.Template) {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-actor", Name: "Workflow Actor", Email: "workflow@example.invalid"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &projectID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: projectID, SSHKeyID: key.ID, Name: "repo", GitURL: "https://example.invalid/repo.git", GitBranch: "main"})
	require.NoError(t, err)
	first, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repository.ID, Name: "First", Playbook: "first.yml"})
	require.NoError(t, err)
	second, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repository.ID, Name: "Second", Playbook: "second.yml"})
	require.NoError(t, err)
	return user, first, second
}

func linearRepositoryWorkflow(projectID, firstTemplateID, secondTemplateID int) db.WorkflowTemplate {
	return db.WorkflowTemplate{
		ProjectID: projectID, Name: "Linear", DefinitionVersion: 1,
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: firstTemplateID}, {ID: -2, TemplateID: secondTemplateID}},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	}
}
