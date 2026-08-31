package server

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionVersionsDiffAndRestoreRemainAppendOnly(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "workflow version service"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 71, Admin: true}

	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "Deploy", VersionMessage: "Initial definition",
		Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: templateID, DisplayName: "Build"},
			{ID: -2, TemplateID: templateID, DisplayName: "Deploy"},
		},
		Edges: []db.WorkflowEdge{{
			ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess,
		}},
	}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.NotZero(t, created.CurrentVersionID)

	requested := created
	requested.Name = "Release"
	requested.VersionMessage = "Rename workflow"
	updated, validation, err := service.Update(project.ID, created.ID, requested, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 2, updated.Revision)

	versions, err := service.ListVersions(project.ID, created.ID, db.RetrieveQueryParams{}, actor)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, []int{2, 1}, []int{versions[0].VersionNumber, versions[1].VersionNumber})
	assert.Equal(t, "Rename workflow", versions[0].Message)
	assert.Equal(t, "Initial definition", versions[1].Message)
	sourceNodeIDs := []int{
		versions[1].DefinitionSnapshot.Nodes[0].ID,
		versions[1].DefinitionSnapshot.Nodes[1].ID,
	}

	diff, err := service.DiffVersions(project.ID, created.ID, 1, 2, actor)
	require.NoError(t, err)
	assert.Contains(t, diff.ChangedSections(), "metadata")

	restored, validation, err := service.RestoreVersion(project.ID, created.ID, 1, "Restore initial", actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, "Deploy", restored.Name)
	assert.Equal(t, 3, restored.Revision)
	require.Len(t, restored.Nodes, 2)
	require.Len(t, restored.Edges, 1)
	assert.NotEqual(t, sourceNodeIDs, []int{restored.Nodes[0].ID, restored.Nodes[1].ID})
	assert.Equal(t, restored.Nodes[0].ID, restored.Edges[0].SourceNodeID)
	assert.Equal(t, restored.Nodes[1].ID, restored.Edges[0].DestinationNodeID)

	versions, err = service.ListVersions(project.ID, created.ID, db.RetrieveQueryParams{}, actor)
	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.NotNil(t, versions[0].RestoredFromVersionID)
	assert.Equal(t, versions[2].ID, *versions[0].RestoredFromVersionID)
	assert.Equal(t, "Deploy", versions[2].DefinitionSnapshot.Name)
	assert.Equal(t, "Release", versions[1].DefinitionSnapshot.Name)
	assert.Equal(t, sourceNodeIDs, []int{
		versions[2].DefinitionSnapshot.Nodes[0].ID,
		versions[2].DefinitionSnapshot.Nodes[1].ID,
	})
}

func TestWorkflowDefinitionVersionMessageIsBoundedAtServiceBoundary(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "workflow version message"})
	require.NoError(t, err)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 72, Admin: true}

	_, _, err = service.Create(project.ID, db.WorkflowTemplate{
		Name: "Deploy", VersionMessage: strings.Repeat("x", db.MaxWorkflowVersionMessageBytes+1),
	}, actor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not exceed 512 bytes")
}
