package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowMutationsPersistAppendOnlyVersions(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "version contract"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	versionStore := any(repository).(db.WorkflowVersionStore)

	created, initial, err := versionStore.CreateWorkflowTemplateVersioned(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Deploy", MaxParallelTasks: 4,
	}, db.WorkflowVersionMutation{AuthorUserID: 41, Message: "Initial definition"})
	require.NoError(t, err)
	assert.Equal(t, 1, initial.VersionNumber)
	assert.Equal(t, created.Revision, initial.VersionNumber)
	assert.NotEmpty(t, initial.ContentFingerprint)

	created.Name = "Release"
	updated, second, err := versionStore.UpdateWorkflowTemplateVersioned(created, db.WorkflowVersionMutation{
		AuthorUserID: 42, Message: "Rename workflow",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, second.VersionNumber)
	require.NotNil(t, second.ParentVersionID)
	assert.Equal(t, initial.ID, *second.ParentVersionID)
	assert.Equal(t, updated.Revision, second.VersionNumber)

	versions, err := versionStore.GetWorkflowVersions(project.ID, created.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, []int{2, 1}, []int{versions[0].VersionNumber, versions[1].VersionNumber})
}

func TestVersionedWorkflowMutationBackfillsOneLegacyBaseline(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "legacy baseline"})
	require.NoError(t, err)
	repository := NewWorkflowStore(store.GetConnection())
	legacy, err := repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: project.ID, Name: "Legacy", MaxParallelTasks: 4,
	})
	require.NoError(t, err)
	legacy.Name = "Versioned"

	_, current, err := repository.UpdateWorkflowTemplateVersioned(legacy, db.WorkflowVersionMutation{
		AuthorUserID: 51, Message: "First versioned edit",
	})
	require.NoError(t, err)
	versions, err := repository.GetWorkflowVersions(project.ID, legacy.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, current.ID, versions[0].ID)
	assert.Equal(t, 0, versions[1].AuthorUserID)
	assert.Equal(t, "Legacy", versions[1].DefinitionSnapshot.Name)
}
