package sql

import (
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createRunnerLifecycleFixture(t *testing.T, status task_logger.TaskStatus) (*SqlDb, int, db.Runner, db.Task) {
	t.Helper()

	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID:    projectID,
		RepositoryID: repositoryID,
		Name:         "runner lifecycle",
		Playbook:     "site.yml",
	})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name:      "project runner",
		ProjectID: &projectID,
		Token:     db.GenerateRunnerToken(),
		Active:    true,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		TemplateID: template.ID,
		ProjectID:  projectID,
		Status:     status,
		Playbook:   "site.yml",
		RunnerID:   &runner.ID,
		Created:    time.Now(),
	}, 0)
	require.NoError(t, err)

	return store, projectID, runner, task
}

func requireRunnerAssignmentConflict(t *testing.T, err error, runnerID int, task db.Task) {
	t.Helper()
	var conflict *db.RunnerLifecycleConflictError
	require.True(t, errors.As(err, &conflict))
	assert.Equal(t, runnerID, conflict.RunnerID)
	require.Len(t, conflict.Assignments, 1)
	assert.Equal(t, db.RunnerTaskAssignment{TaskID: task.ID, Status: task.Status}, conflict.Assignments[0])
}

func TestDeleteRunnerRejectsUnfinishedAssignmentWithoutDataLoss(t *testing.T) {
	store, projectID, runner, task := createRunnerLifecycleFixture(t, task_logger.TaskRunningStatus)

	err := store.DeleteRunner(projectID, runner.ID)

	requireRunnerAssignmentConflict(t, err, runner.ID, task)
	_, err = store.GetRunner(projectID, runner.ID)
	assert.NoError(t, err)
	storedTask, err := store.GetTask(projectID, task.ID)
	require.NoError(t, err)
	require.NotNil(t, storedTask.RunnerID)
	assert.Equal(t, runner.ID, *storedTask.RunnerID)
}

func TestDeleteRunnerPreservesNameInFinishedTaskHistory(t *testing.T) {
	store, projectID, runner, task := createRunnerLifecycleFixture(t, task_logger.TaskSuccessStatus)

	require.NoError(t, store.DeleteRunner(projectID, runner.ID))

	_, err := store.GetRunner(projectID, runner.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	tasks, err := store.GetProjectTasks(projectID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, task.ID, tasks[0].ID)
	require.NotNil(t, tasks[0].UsedRunnerID)
	assert.Equal(t, runner.ID, *tasks[0].UsedRunnerID)
	require.NotNil(t, tasks[0].UsedRunnerName)
	assert.Equal(t, runner.Name, *tasks[0].UsedRunnerName)
}

func TestSetProjectRunnerActiveRejectsDeactivationWithUnfinishedAssignment(t *testing.T) {
	store, projectID, runner, task := createRunnerLifecycleFixture(t, task_logger.TaskRunningStatus)

	err := store.SetProjectRunnerActive(projectID, runner.ID, false)

	requireRunnerAssignmentConflict(t, err, runner.ID, task)
	stored, err := store.GetRunner(projectID, runner.ID)
	require.NoError(t, err)
	assert.True(t, stored.Active)
}

func TestInactiveProjectRunnerIsExcludedFromDispatchCandidates(t *testing.T) {
	store, projectID, runner, _ := createRunnerLifecycleFixture(t, task_logger.TaskSuccessStatus)
	require.NoError(t, store.SetProjectRunnerActive(projectID, runner.ID, false))

	candidates, err := store.GetRunners(projectID, true, db.RunnerFilterIgnoreTags, nil)

	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestResetProjectRunnerRegistrationRejectsUnfinishedAssignment(t *testing.T) {
	store, projectID, runner, task := createRunnerLifecycleFixture(t, task_logger.TaskRunningStatus)
	expiresAt := time.Now().Add(time.Hour)

	err := store.ResetProjectRunnerRegistration(runner.ID, projectID, "new-hash", expiresAt)

	requireRunnerAssignmentConflict(t, err, runner.ID, task)
	stored, err := store.GetRunner(projectID, runner.ID)
	require.NoError(t, err)
	assert.True(t, stored.Active)
	assert.NotEmpty(t, stored.Token)
	assert.Nil(t, stored.RegistrationTokenHash)
}
