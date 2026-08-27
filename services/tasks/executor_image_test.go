package tasks

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createExecutorImageTaskFixture(
	t *testing.T,
	executorType db.RunnerExecutorType,
	capability bool,
) (*sql.SqlDb, TaskPool, db.User, db.Template) {
	t.Helper()
	setupReconcilerConfig(t)
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "executor image"})
	require.NoError(t, err)
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "image-author", Name: "Image Author", Email: "image-author@example.invalid",
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: project.ID, UserID: user.ID, Role: db.ProjectOwner})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.com/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	image := "registry.example.com/team/job:v1"
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID,
		Name: "image", Playbook: "site.yml", ExecutorImage: &image,
	})
	require.NoError(t, err)
	_, err = store.CreateRunner(db.Runner{
		ProjectID: &project.ID, Name: "runner", IsDefault: true,
		Token: db.GenerateRunnerToken(), Active: true, ExecutorType: executorType,
	})
	require.NoError(t, err)
	pool := CreateTaskPool(
		store, NewMemoryTaskStateStore(), nil, &InventoryServiceMock{},
		&EncryptionServiceMock{}, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil,
	)
	pool.register = make(chan *TaskRunner, 1)
	pool.SetExecutorImageCapabilityResolver(func(*db.User) bool { return capability })
	return store, pool, user, template
}

func TestAddTaskRejectsDisabledOrIncompatibleExecutorImageBeforeInsert(t *testing.T) {
	for _, tt := range []struct {
		name         string
		executorType db.RunnerExecutorType
		capability   bool
		expected     error
	}{
		{name: "disabled capability", executorType: db.RunnerExecutorDocker, expected: db.ErrExecutorImageCapabilityUnavailable},
		{name: "local executor", executorType: db.RunnerExecutorLocal, capability: true, expected: db.ErrExecutorImageIncompatible},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, pool, user, template := createExecutorImageTaskFixture(t, tt.executorType, tt.capability)

			_, err := pool.AddTask(db.Task{TemplateID: template.ID}, &user.ID, user.Username, template.ProjectID, false)

			assert.ErrorIs(t, err, tt.expected)
			tasks, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
			require.NoError(t, listErr)
			assert.Empty(t, tasks, "rejection must happen before task insertion and enqueue")
		})
	}
}

func TestAddTaskFreezesResolvedExecutorImageBeforeEnqueue(t *testing.T) {
	store, pool, user, template := createExecutorImageTaskFixture(t, db.RunnerExecutorDocker, true)

	created, err := pool.AddTask(db.Task{TemplateID: template.ID}, &user.ID, user.Username, template.ProjectID, false)

	require.NoError(t, err)
	require.NotNil(t, created.RequestedExecutorImage)
	require.NotNil(t, created.ResolvedExecutorImage)
	assert.Equal(t, "registry.example.com/team/job:v1", *created.ResolvedExecutorImage)
	queued := <-pool.register
	remote, ok := queued.job.(*RemoteJob)
	require.True(t, ok)
	require.NotNil(t, remote.ExecutorImage)
	assert.Equal(t, *created.ResolvedExecutorImage, *remote.ExecutorImage)

	newImage := "registry.example.com/team/job:v2"
	template.ExecutorImage = &newImage
	require.NoError(t, store.UpdateTemplate(template))
	stored, err := store.GetTask(template.ProjectID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "registry.example.com/team/job:v1", *stored.ResolvedExecutorImage)
}

func TestAddTaskRevalidatesPersistedExecutorImage(t *testing.T) {
	store, pool, user, template := createExecutorImageTaskFixture(t, db.RunnerExecutorDocker, true)
	_, err := store.Sql().Exec(
		store.PrepareQuery("update project__template set executor_image=? where id=?"),
		"https://user:secret@registry.example.com/team/job:v1", template.ID,
	)
	require.NoError(t, err)

	_, err = pool.AddTask(db.Task{TemplateID: template.ID}, &user.ID, user.Username, template.ProjectID, false)

	assert.ErrorIs(t, err, db.ErrExecutorImageInvalid)
	stored, listErr := store.GetProjectTasks(template.ProjectID, db.RetrieveQueryParams{})
	require.NoError(t, listErr)
	assert.Empty(t, stored)
}
