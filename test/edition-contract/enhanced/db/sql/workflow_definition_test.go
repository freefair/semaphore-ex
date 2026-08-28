package sql

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionRoundTripPreservesGraphIDsAndLayout(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow := repositoryWorkflow(projectID)
	workflow.MaxParallelTasks = 3
	workflow.Nodes[1].JoinMode = db.WorkflowJoinAllComplete
	workflow.Edges[0].Condition = db.WorkflowEdgeExpression
	workflow.Edges[0].Expression = `result.summary.failed_hosts == 0`
	program, err := workflowDB.CompileWorkflowCondition(workflow.Edges[0].Expression)
	require.NoError(t, err)
	encodedProgram, err := json.Marshal(program)
	require.NoError(t, err)
	workflow.Edges[0].ConditionProgram = program
	workflow.Edges[0].ConditionProgramJSON = string(encodedProgram)

	created, err := repository.CreateWorkflowTemplate(workflow)
	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Equal(t, 1, created.Revision)
	require.Positive(t, created.Nodes[0].ID)
	require.Positive(t, created.Nodes[1].ID)
	require.Positive(t, created.Edges[0].ID)
	nodeIDs := []int{created.Nodes[0].ID, created.Nodes[1].ID}
	edgeID := created.Edges[0].ID

	created.Name = "Deploy safely"
	created.Nodes[0].PositionX = 321
	created.Nodes[0].PositionY = 654
	created.Edges[0].Label = "promote"
	updated, err := repository.UpdateWorkflowTemplate(created)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Revision)
	assert.Equal(t, nodeIDs, []int{updated.Nodes[0].ID, updated.Nodes[1].ID})
	assert.Equal(t, edgeID, updated.Edges[0].ID)

	reloaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Deploy safely", reloaded.Name)
	assert.Equal(t, 321, reloaded.Nodes[0].PositionX)
	assert.Equal(t, 654, reloaded.Nodes[0].PositionY)
	assert.Equal(t, "promote", reloaded.Edges[0].Label)
	assert.Equal(t, 3, reloaded.MaxParallelTasks)
	assert.Equal(t, db.WorkflowJoinAllComplete, reloaded.Nodes[1].JoinMode)
	assert.Equal(t, db.WorkflowEdgeExpression, reloaded.Edges[0].Condition)
	assert.Equal(t, `result.summary.failed_hosts == 0`, reloaded.Edges[0].Expression)
	assert.Equal(t, program, reloaded.Edges[0].ConditionProgram)
	assert.Equal(t, nodeIDs, []int{reloaded.Nodes[0].ID, reloaded.Nodes[1].ID})
	assert.Equal(t, edgeID, reloaded.Edges[0].ID)
}

func TestWorkflowDefinitionUpdateRejectsStaleRevisionWithoutOverwrite(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	created, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	stale := created
	created.Name = "First editor"
	_, err = repository.UpdateWorkflowTemplate(created)
	require.NoError(t, err)

	stale.Name = "Stale editor"
	_, err = repository.UpdateWorkflowTemplate(stale)
	require.ErrorIs(t, err, pro_interfaces.ErrWorkflowRevisionConflict)

	reloaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "First editor", reloaded.Name)
	assert.Equal(t, 2, reloaded.Revision)
}

func TestWorkflowDefinitionUpdateRollsBackMetadataWhenGraphPersistenceFails(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	created, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)
	created.Name = "Must roll back"
	created.Nodes[0].ID = 987654

	_, err = repository.UpdateWorkflowTemplate(created)
	require.Error(t, err)

	reloaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Deploy", reloaded.Name)
	assert.Equal(t, 1, reloaded.Revision)
}

func TestWorkflowDefinitionRepositoryEnforcesProjectIsolation(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	created, err := repository.CreateWorkflowTemplate(repositoryWorkflow(projectID))
	require.NoError(t, err)

	_, err = repository.GetWorkflowTemplate(projectID+1, created.ID)
	assert.True(t, errors.Is(err, db.ErrNotFound))
	assert.ErrorIs(t, repository.DeleteWorkflowTemplate(projectID+1, created.ID), db.ErrNotFound)

	reloaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, reloaded.ID)
}

func TestWorkflowDefinitionRoundTripRemapsArtifactReferences(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow := db.WorkflowTemplate{
		ProjectID: projectID, Name: "Artifact pipeline", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{
				ID: -1, TemplateID: 100,
				ArtifactOutputs: []db.WorkflowArtifactDeclaration{{
					Name: "release", MaxBytes: 128,
					Schema: db.WorkflowArtifactSchema{Type: db.WorkflowArtifactString},
				}},
			},
			{
				ID: -2, TemplateID: 101,
				ArtifactInputs: []db.WorkflowArtifactReference{{
					Name: "release_name", SourceNodeID: -1, Output: "release", Required: true,
				}},
			},
		},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	}

	created, err := repository.CreateWorkflowTemplate(workflow)
	require.NoError(t, err)
	reloaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.Nodes, 2)
	require.Len(t, reloaded.Nodes[0].ArtifactOutputs, 1)
	require.Len(t, reloaded.Nodes[1].ArtifactInputs, 1)
	assert.Equal(t, "release", reloaded.Nodes[0].ArtifactOutputs[0].Name)
	assert.Equal(t, reloaded.Nodes[0].ID, reloaded.Nodes[1].ArtifactInputs[0].SourceNodeID)
	assert.NotContains(t, reloaded.Nodes[0].ArtifactOutputsJSON, "release-1")
}

func workflowRepositoryFixture(t *testing.T) (*coresql.SqlDb, *WorkflowStoreImpl, int) {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	project, err := store.CreateProject(db.Project{Name: "Workflow test"})
	require.NoError(t, err)
	return store, NewWorkflowStore(store.GetConnection()), project.ID
}

func repositoryWorkflow(projectID int) db.WorkflowTemplate {
	return db.WorkflowTemplate{
		ProjectID:         projectID,
		Name:              "Deploy",
		DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, Kind: db.WorkflowNodeTaskKind, ConvergenceMode: db.WorkflowConvergenceAll, TemplateID: 100, DisplayName: "Build", PositionX: 10, PositionY: 20},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, ConvergenceMode: db.WorkflowConvergenceAll, DisplayName: "Approve", PositionX: 200, PositionY: 20},
		},
		Edges: []db.WorkflowEdge{
			{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess},
		},
	}
}
