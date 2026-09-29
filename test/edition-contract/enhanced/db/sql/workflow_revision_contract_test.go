package sql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRevisionAdapterPreservesHistoricalGraphsAndRunPins(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "revision history"})
	require.NoError(t, err)
	foreignProject, err := store.CreateProject(db.Project{Name: "foreign revision history"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.WorkflowVersionStore)

	v1Graph := db.WorkflowTemplate{
		ProjectID: project.ID,
		Name:      "Deploy v1",
		Nodes: []db.WorkflowNode{
			{ID: -1, Kind: db.WorkflowNodeNoteKind, Note: stringPointer("v1 note")},
		},
	}
	v1, revision1, err := versionStore.CreateWorkflowTemplateVersioned(v1Graph, db.WorkflowVersionMutation{
		AuthorUserID: 91, Message: "Initial graph", Created: time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	v1Snapshot, err := repository.GetWorkflowRevisionGraph(project.ID, revision1.ID)
	require.NoError(t, err)
	snapshotJSON, err := json.Marshal(v1Snapshot)
	require.NoError(t, err)
	_, err = repository.CreateWorkflowRun(db.WorkflowRun{
		ProjectID: project.ID, WorkflowTemplateID: v1.ID, Status: db.WorkflowRunPending,
		ActorUserID: 91, DefinitionVersion: v1.DefinitionVersion, DefinitionRevision: v1.Revision,
		WorkflowVersionID: revision1.ID, DefinitionSnapshotJSON: string(snapshotJSON),
		ParameterSnapshotJSON: "{}", TriggerSnapshotJSON: "{}", CorrelationID: "revision-v1-run",
		Created: time.Date(2026, 9, 29, 6, 1, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	v2Input := v1
	v2Input.Name = "Deploy v2"
	v2Input.Nodes[0].Note = stringPointer("v2 note")
	v2, revision2, err := versionStore.UpdateWorkflowTemplateVersioned(v2Input, db.WorkflowVersionMutation{
		AuthorUserID: 92, Message: "Change graph", Created: time.Date(2026, 9, 29, 6, 2, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Revision)

	historical, err := repository.GetWorkflowRevisionGraph(project.ID, revision1.ID)
	require.NoError(t, err)
	assert.Equal(t, "Deploy v1", historical.Name)
	require.Len(t, historical.Nodes, 1)
	require.NotNil(t, historical.Nodes[0].Note)
	assert.Equal(t, "v1 note", *historical.Nodes[0].Note)

	latest, err := repository.GetWorkflowRevisionGraph(project.ID, revision2.ID)
	require.NoError(t, err)
	assert.Equal(t, "Deploy v2", latest.Name)
	require.Len(t, latest.Nodes, 1)
	require.NotNil(t, latest.Nodes[0].Note)
	assert.Equal(t, "v2 note", *latest.Nodes[0].Note)

	revisions, err := repository.GetWorkflowRevisions(project.ID, v1.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	assert.Equal(t, []int{revision2.ID, revision1.ID}, []int{revisions[0].ID, revisions[1].ID})
	assert.False(t, revisions[0].HasRuns)
	assert.True(t, revisions[1].HasRuns)

	_, err = repository.GetWorkflowRevisionGraph(foreignProject.ID, revision1.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	foreignRevisions, err := repository.GetWorkflowRevisions(foreignProject.ID, v1.ID)
	require.NoError(t, err)
	assert.Empty(t, foreignRevisions)
}

func stringPointer(value string) *string { return &value }
