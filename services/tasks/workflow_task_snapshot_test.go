package tasks

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workflowSecretEncryptionMock struct {
	EncryptionServiceMock
	projectID int
	taskID    int
	secret    string
}

func (m *workflowSecretEncryptionMock) CreateTaskSurveySecrets(projectID int, taskID int, secrets string, _ time.Time) error {
	m.projectID = projectID
	m.taskID = taskID
	m.secret = secrets
	return nil
}

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

func TestTaskPoolWorkflowArtifactSecretUsesTaskBoundSecretStorage(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Workflow artifact secret"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.invalid/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "Consumer", Playbook: "consumer.yml",
	})
	require.NoError(t, err)
	encryption := &workflowSecretEncryptionMock{}
	pool := CreateTaskPool(
		store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		encryption, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil,
	)
	pool.register = make(chan *TaskRunner, 1)
	runID, nodeID := 41, 73
	secret := `{"deployment_token":"must-stay-private"}`

	created, err := pool.AddWorkflowTask(db.Task{
		TemplateID: template.ID, WorkflowRunID: &runID, WorkflowNodeID: &nodeID, Secret: secret,
	}, template, nil, "", project.ID, false)
	require.NoError(t, err)
	assert.Empty(t, created.Secret)
	persisted, err := store.GetTask(project.ID, created.ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.Secret)
	assert.Equal(t, project.ID, encryption.projectID)
	assert.Equal(t, created.ID, encryption.taskID)
	assert.Equal(t, secret, encryption.secret)
	queued := <-pool.register
	local, ok := queued.job.(*LocalExecutor)
	require.True(t, ok)
	assert.Equal(t, secret, local.Secret)
	assert.NotContains(t, *created.WorkflowTemplateSnapshot, "must-stay-private")
}
