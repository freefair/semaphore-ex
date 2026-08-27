package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProjectRunnerIssuesOneTimeRegistrationMaterial(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "runner project"})
	require.NoError(t, err)
	service := NewRunnerService(store)

	runner, token, err := service.CreateProjectRunner(db.Runner{
		Name:      "isolated",
		ProjectID: &project.ID,
		Active:    true,
	})

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, RunnerRegistrationTokenPrefix))
	assert.Empty(t, runner.Token)
	assert.False(t, runner.Active)
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.RegistrationTokenHash)
	assert.Equal(t, HashRunnerRegistrationToken(token), *stored.RegistrationTokenHash)
	require.NotNil(t, stored.RegistrationTokenExpiresAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *stored.RegistrationTokenExpiresAt, 5*time.Second)
}

func createProjectRunnerServiceFixture(t *testing.T) (*sql.SqlDb, RunnerService, int, db.Runner) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "runner lifecycle"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name:             "project runner",
		ProjectID:        &project.ID,
		Token:            db.GenerateRunnerToken(),
		Active:           true,
		Tags:             []string{"old"},
		MaxParallelTasks: 1,
	})
	require.NoError(t, err)
	return store, NewRunnerService(store), project.ID, runner
}

func assignRunnerTask(t *testing.T, store *sql.SqlDb, projectID int, runnerID int, status task_logger.TaskStatus) db.Task {
	t.Helper()
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &projectID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: projectID,
		Name:      "repo",
		GitURL:    "https://example.com/repo.git",
		GitBranch: "main",
		SSHKeyID:  key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID:    projectID,
		RepositoryID: repository.ID,
		Name:         "runner lifecycle",
		Playbook:     "site.yml",
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		TemplateID: template.ID,
		ProjectID:  projectID,
		Status:     status,
		Playbook:   "site.yml",
		RunnerID:   &runnerID,
		Created:    time.Now(),
	}, 0)
	require.NoError(t, err)
	return task
}

func TestProjectRunnerActiveTransitions(t *testing.T) {
	t.Run("activate registered", func(t *testing.T) {
		store, service, projectID, runner := createProjectRunnerServiceFixture(t)
		require.NoError(t, store.SetProjectRunnerActive(projectID, runner.ID, false))
		runner.Active = false

		require.NoError(t, service.SetProjectRunnerActive(runner, true))
		stored, err := store.GetRunner(projectID, runner.ID)
		require.NoError(t, err)
		assert.True(t, stored.Active)
	})

	t.Run("reject activate unregistered", func(t *testing.T) {
		store, service, projectID, runner := createProjectRunnerServiceFixture(t)
		require.NoError(t, store.ResetProjectRunnerRegistration(runner.ID, projectID, "hash", time.Now().Add(time.Hour)))
		runner, err := store.GetRunner(projectID, runner.ID)
		require.NoError(t, err)

		err = service.SetProjectRunnerActive(runner, true)
		assert.ErrorIs(t, err, ErrProjectRunnerUnregistered)
	})

	t.Run("deactivate idle", func(t *testing.T) {
		store, service, projectID, runner := createProjectRunnerServiceFixture(t)

		require.NoError(t, service.SetProjectRunnerActive(runner, false))
		stored, err := store.GetRunner(projectID, runner.ID)
		require.NoError(t, err)
		assert.False(t, stored.Active)
	})

	t.Run("reject deactivate busy", func(t *testing.T) {
		store, service, projectID, runner := createProjectRunnerServiceFixture(t)
		task := assignRunnerTask(t, store, projectID, runner.ID, task_logger.TaskRunningStatus)

		err := service.SetProjectRunnerActive(runner, false)
		var conflict *db.RunnerLifecycleConflictError
		require.True(t, errors.As(err, &conflict))
		require.Len(t, conflict.Assignments, 1)
		assert.Equal(t, task.ID, conflict.Assignments[0].TaskID)
	})
}

func TestUpdateProjectRunnerPreservesLifecycleAndCredentials(t *testing.T) {
	store, service, projectID, runner := createProjectRunnerServiceFixture(t)
	otherProjectID := projectID + 1000
	registrationHash := "replacement"

	updated, err := service.UpdateProjectRunner(runner, db.Runner{
		Name:                       "  renamed runner  ",
		ProjectID:                  &otherProjectID,
		Token:                      "replacement-token",
		Active:                     false,
		Tags:                       []string{"new", "linux"},
		IsDefault:                  true,
		Webhook:                    "  https://example.com/hook  ",
		MaxParallelTasks:           4,
		RegistrationTokenHash:      &registrationHash,
		RegistrationTokenExpiresAt: ptrTime(time.Now().Add(time.Hour)),
	})

	require.NoError(t, err)
	assert.Equal(t, "renamed runner", updated.Name)
	assert.Equal(t, projectID, *updated.ProjectID)
	assert.Equal(t, runner.Token, updated.Token)
	assert.True(t, updated.Active)
	assert.Nil(t, updated.RegistrationTokenHash)
	assert.Equal(t, []string{"linux", "new"}, updated.Tags)
	assert.Equal(t, "https://example.com/hook", updated.Webhook)
	assert.Equal(t, 4, updated.MaxParallelTasks)
	stored, err := store.GetRunner(projectID, runner.ID)
	require.NoError(t, err)
	assert.Equal(t, updated.Name, stored.Name)
	assert.Equal(t, updated.Token, stored.Token)
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestUpdateProjectRunnerValidatesEditableFields(t *testing.T) {
	_, service, _, runner := createProjectRunnerServiceFixture(t)

	for name, changes := range map[string]db.Runner{
		"blank name":           {Name: "  "},
		"negative parallelism": {Name: "valid", MaxParallelTasks: -1},
		"oversized tag": {
			Name: "valid", Tags: []string{strings.Repeat("x", db.MaxRunnerTagLength+1)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.UpdateProjectRunner(runner, changes)
			assert.Error(t, err)
		})
	}
}

func TestRegenerateProjectRunnerRegistrationRejectsUnfinishedAssignment(t *testing.T) {
	store, service, projectID, runner := createProjectRunnerServiceFixture(t)
	assignRunnerTask(t, store, projectID, runner.ID, task_logger.TaskRunningStatus)

	token, err := service.RegenerateRegistrationToken(runner)

	assert.Empty(t, token)
	var conflict *db.RunnerLifecycleConflictError
	require.True(t, errors.As(err, &conflict))
	stored, getErr := store.GetRunner(projectID, runner.ID)
	require.NoError(t, getErr)
	assert.True(t, stored.Active)
	assert.NotEmpty(t, stored.Token)
}

func TestCreateProjectRunnerRejectsGlobalScope(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	service := NewRunnerService(store)
	invalidProjectID := 0
	for name, projectID := range map[string]*int{
		"missing project": nil,
		"invalid project": &invalidProjectID,
	} {
		t.Run(name, func(t *testing.T) {
			_, token, err := service.CreateProjectRunner(db.Runner{Name: "not project-bound", ProjectID: projectID})

			assert.ErrorIs(t, err, ErrProjectRunnerRequiresProject)
			assert.Empty(t, token)
		})
	}
}

func TestRegenerateProjectRunnerRegistrationDeactivatesRunner(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "registration reset"})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name: "registered", ProjectID: &project.ID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)

	token, err := NewRunnerService(store).RegenerateRegistrationToken(runner)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, RunnerRegistrationTokenPrefix))
	stored, err := store.GetRunner(project.ID, runner.ID)
	require.NoError(t, err)
	assert.False(t, stored.Active)
	assert.Empty(t, stored.Token)
	require.NotNil(t, stored.RegistrationTokenHash)
	assert.Equal(t, HashRunnerRegistrationToken(token), *stored.RegistrationTokenHash)
}
