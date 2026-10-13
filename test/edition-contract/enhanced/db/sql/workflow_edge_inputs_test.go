package sql

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkflowDefinitionPersistsEdgeInputsWithoutLosingConditions(t *testing.T) {
	store, repository, projectID := workflowRepositoryFixture(t)
	defer store.Close()
	workflow := repositoryWorkflow(projectID)
	workflow.Edges[0].InputMode = db.WorkflowEdgeInputExplicit
	workflow.Edges[0].InputMappings = []db.WorkflowInputMapping{{Var: "release", Key: "image-tag"}}
	created, err := repository.CreateWorkflowTemplate(workflow)
	require.NoError(t, err)
	loaded, err := repository.GetWorkflowTemplate(projectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowEdgeInputExplicit, loaded.Edges[0].InputMode)
	assert.Equal(t, workflow.Edges[0].InputMappings, loaded.Edges[0].InputMappings)
	loaded.Edges[0].InputMode = db.WorkflowEdgeInputByName
	loaded.Edges[0].InputMappings = nil
	loaded.Edges[0].Label = "Keep EX identity"
	updated, err := repository.UpdateWorkflowTemplate(loaded)
	require.NoError(t, err)
	assert.Equal(t, created.Edges[0].ID, updated.Edges[0].ID)
	assert.Empty(t, updated.Edges[0].InputMappings)
	assert.Equal(t, "Keep EX identity", updated.Edges[0].Label)
}
