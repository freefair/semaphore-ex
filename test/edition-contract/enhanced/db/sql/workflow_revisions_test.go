package sql

import (
	"encoding/json"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRevisionAdapterProjectsImmutableVersionSnapshot(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "revision adapter"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.WorkflowVersionStore)

	created, version, err := versionStore.CreateWorkflowTemplateVersioned(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", MaxParallelTasks: 4,
	}, db.WorkflowVersionMutation{AuthorUserID: 44, Message: "Initial"})
	require.NoError(t, err)

	revisions, err := repository.GetWorkflowRevisions(project.ID, created.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	assert.Equal(t, version.ID, revisions[0].ID)
	assert.Equal(t, version.VersionNumber, revisions[0].Number)
	assert.False(t, revisions[0].HasRuns)
	require.NotNil(t, revisions[0].CreatedByUserID)
	assert.Equal(t, 44, *revisions[0].CreatedByUserID)

	graph, err := repository.GetWorkflowRevisionGraph(project.ID, version.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Name, graph.Name)
	assert.Equal(t, version.ID, graph.RevisionID)
	assert.Equal(t, version.VersionNumber, graph.Revision)
	assert.Equal(t, version.ID, graph.CurrentVersionID)
}

func TestWorkflowRevisionGraphUsesVersionRowOwnership(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "revision row ownership"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.WorkflowVersionStore)
	created, version, err := versionStore.CreateWorkflowTemplateVersioned(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", MaxParallelTasks: 4,
	}, db.WorkflowVersionMutation{AuthorUserID: 45, Message: "Initial"})
	require.NoError(t, err)

	var snapshot db.WorkflowTemplate
	require.NoError(t, json.Unmarshal([]byte(version.DefinitionSnapshotJSON), &snapshot))
	snapshot.ID = created.ID + 100
	snapshot.ProjectID = project.ID + 100
	payload, err := json.Marshal(snapshot)
	require.NoError(t, err)
	_, err = store.Sql().Exec("update project__workflow_version set definition_snapshot=? where id=?", string(payload), version.ID)
	require.NoError(t, err)

	graph, err := repository.GetWorkflowRevisionGraph(project.ID, version.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, graph.ID)
	assert.Equal(t, project.ID, graph.ProjectID)
}

func TestWorkflowVersionSnapshotExcludesResponseRevisionID(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "revision response metadata"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.WorkflowVersionStore)

	_, version, err := versionStore.CreateWorkflowTemplateVersioned(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", MaxParallelTasks: 4, RevisionID: 999,
	}, db.WorkflowVersionMutation{AuthorUserID: 46, Message: "Initial"})
	require.NoError(t, err)

	assert.Zero(t, version.DefinitionSnapshot.RevisionID)
	assert.NotContains(t, version.DefinitionSnapshotJSON, `"revision_id":999`)
}
