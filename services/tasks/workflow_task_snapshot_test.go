package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskPoolWorkflowTaskUsesPersistedTemplateSnapshotAfterRestart(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Workflow task snapshot"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.invalid/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "Snapshot", Playbook: "snapshot.yml",
	})
	require.NoError(t, err)

	pool := CreateTaskPool(
		store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil,
	)
	pool.register = make(chan *TaskRunner, 1)
	runID, nodeID := 41, 73
	created, err := pool.AddWorkflowTask(db.Task{
		TemplateID: template.ID, WorkflowRunID: &runID, WorkflowNodeID: &nodeID,
	}, template, nil, "", project.ID, false)
	require.NoError(t, err)

	template.Playbook = "edited-live.yml"
	require.NoError(t, store.UpdateTemplate(template))
	queued := <-pool.register
	assert.Equal(t, "snapshot.yml", queued.Template.Playbook)
	require.NotNil(t, created.WorkflowTemplateSnapshot)

	restartedPool := CreateTaskPool(
		store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil,
	)
	restarted, err := restartedPool.HydrateTaskRunnerFromDB(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "snapshot.yml", restarted.Template.Playbook)
	assert.Equal(t, runID, *restarted.Task.WorkflowRunID)
	assert.Equal(t, nodeID, *restarted.Task.WorkflowNodeID)
}
