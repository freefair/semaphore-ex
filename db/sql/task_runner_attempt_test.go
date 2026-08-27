package sql

import (
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createRunnerAttemptFixture(t *testing.T) (*SqlDb, int, db.Runner, db.Task) {
	t.Helper()
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	template, err := store.CreateTemplate(db.Template{
		ProjectID:    projectID,
		RepositoryID: repositoryID,
		Name:         "runner reconciliation",
		Playbook:     "site.yml",
	})
	require.NoError(t, err)
	runner, err := store.CreateRunner(db.Runner{
		Name:      "reused runner",
		ProjectID: &projectID,
		Token:     db.GenerateRunnerToken(),
		Active:    true,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		TemplateID: template.ID,
		ProjectID:  projectID,
		Status:     task_logger.TaskStartingStatus,
		Playbook:   "site.yml",
		Created:    time.Now(),
	}, 0)
	require.NoError(t, err)
	return store, projectID, runner, task
}

func TestRunnerAssignmentGenerationRejectsLateSameRunnerCompletion(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	now := time.Now().UTC()

	first, assigned, err := store.AssignTaskRunner(projectID, task.ID, runner.ID, runner.Name, now)
	require.NoError(t, err)
	require.True(t, assigned)
	require.Equal(t, 1, first.AssignmentGeneration)
	assert.Nil(t, first.PlacementDecision, "legacy assignment callers must not create an empty decision")

	requeued := first
	requeued.Status = task_logger.TaskWaitingStatus
	requeued.RunnerID = nil
	requeued.RunnerAssignedAt = nil
	requeued.RecoveryReason = "runner stopped responding before execution"
	updated, err := store.UpdateTaskRunner(
		requeued, first.Status, runner.ID, first.AssignmentGeneration,
		db.RunnerAttemptRequeued, requeued.RecoveryReason, now.Add(time.Second),
	)
	require.NoError(t, err)
	require.True(t, updated)

	second, assigned, err := store.AssignTaskRunner(
		projectID, task.ID, runner.ID, runner.Name, now.Add(2*time.Second),
	)
	require.NoError(t, err)
	require.True(t, assigned)
	require.Equal(t, 2, second.AssignmentGeneration)
	require.Equal(t, requeued.RecoveryReason, second.RecoveryReason)

	lateFirst := first
	lateFirst.Status = task_logger.TaskSuccessStatus
	currentSecond := second
	currentSecond.Status = task_logger.TaskSuccessStatus

	type result struct {
		generation int
		updated    bool
		err        error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, update := range []struct {
		candidate      db.Task
		expectedStatus task_logger.TaskStatus
	}{
		{lateFirst, first.Status},
		{currentSecond, second.Status},
	} {
		update := update
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, updateErr := store.UpdateTaskRunner(
				update.candidate, update.expectedStatus, runner.ID,
				update.candidate.AssignmentGeneration, db.RunnerAttemptSucceeded, "", now.Add(3*time.Second),
			)
			results <- result{update.candidate.AssignmentGeneration, ok, updateErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := make([]int, 0, 1)
	for got := range results {
		require.NoError(t, got.err)
		if got.updated {
			winners = append(winners, got.generation)
		}
	}
	assert.Equal(t, []int{2}, winners)

	stored, err := store.GetTask(projectID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskSuccessStatus, stored.Status)
	assert.Equal(t, 2, stored.AssignmentGeneration)
	assert.Equal(t, requeued.RecoveryReason, stored.RecoveryReason)

	attempts, err := store.GetTaskRunnerAttempts(projectID, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	assert.Equal(t, db.RunnerAttemptRequeued, attempts[0].Outcome)
	assert.Equal(t, db.RunnerAttemptSucceeded, attempts[1].Outcome)
	assert.Empty(t, attempts[1].Reason)
	assert.NotNil(t, attempts[0].EndedAt)
	assert.NotNil(t, attempts[1].EndedAt)
}

func TestAssignTaskRunnerAllowsOnlyOneConcurrentAssignment(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	secondRunner, err := store.CreateRunner(db.Runner{
		Name: "other runner", ProjectID: &projectID, Token: db.GenerateRunnerToken(), Active: true,
	})
	require.NoError(t, err)

	type result struct {
		runnerID int
		assigned bool
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, candidate := range []db.Runner{runner, secondRunner} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, assigned, assignErr := store.AssignTaskRunner(
				projectID, task.ID, candidate.ID, candidate.Name, time.Now().UTC(),
			)
			results <- result{candidate.ID, assigned, assignErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := make([]int, 0, 1)
	for got := range results {
		require.NoError(t, got.err)
		if got.assigned {
			winners = append(winners, got.runnerID)
		}
	}
	require.Len(t, winners, 1)

	stored, err := store.GetTask(projectID, task.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.RunnerID)
	assert.Equal(t, winners[0], *stored.RunnerID)
	assert.Equal(t, 1, stored.AssignmentGeneration)
	attempts, err := store.GetTaskRunnerAttempts(projectID, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, winners[0], attempts[0].RunnerID)
}

func TestRunnerTransitionRollsBackWhenActiveAttemptIsMissing(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	assigned, ok, err := store.AssignTaskRunner(
		projectID, task.ID, runner.ID, runner.Name, time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = store.Sql().Exec(
		store.PrepareQuery("delete from task__runner_attempt where task_id=?"), task.ID,
	)
	require.NoError(t, err)
	candidate := assigned
	candidate.Status = task_logger.TaskSuccessStatus

	updated, err := store.UpdateTaskRunner(
		candidate, assigned.Status, runner.ID, assigned.AssignmentGeneration,
		db.RunnerAttemptSucceeded, "", time.Now().UTC(),
	)

	assert.False(t, updated)
	require.ErrorContains(t, err, "was not active")
	stored, err := store.GetTask(projectID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, assigned.Status, stored.Status)
}

func TestRunnerAttemptPersistsRedactedPlacementDecision(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	selectedID := runner.ID
	decision := db.RunnerPlacementDecision{
		RequestedTags:    []string{"gpu", "linux"},
		MatchMode:        db.RunnerTagMatchAll,
		SelectedRunnerID: &selectedID,
		SelectedName:     runner.Name,
		SelectedScope:    db.RunnerPlacementProject,
		Reason:           "selected project runner by deterministic placement",
	}

	assigned, ok, err := store.AssignTaskRunner(
		projectID, task.ID, runner.ID, runner.Name, time.Now().UTC(), decision,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, assigned.PlacementDecision)
	assert.Equal(t, decision.Reason, assigned.PlacementDecision.Reason)
	attempts, err := store.GetTaskRunnerAttempts(projectID, task.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, db.StringArrayField{"gpu", "linux"}, attempts[0].RequestedTags)
	assert.Equal(t, db.RunnerTagMatchAll, attempts[0].MatchMode)
	assert.Equal(t, decision.Reason, attempts[0].PlacementReason)
}

func assertOneConcurrentCapacityWinner(
	t *testing.T,
	store *SqlDb,
	runner db.Runner,
	tasks []db.Task,
) {
	t.Helper()
	type result struct {
		assigned bool
		err      error
	}
	results := make(chan result, len(tasks))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, assigned, err := store.AssignTaskRunner(
				task.ProjectID, task.ID, runner.ID, runner.Name, time.Now().UTC(),
			)
			results <- result{assigned: assigned, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.assigned {
			winners++
		}
	}
	assert.Equal(t, 1, winners)
}

func TestAssignTaskRunnerSerializesCapacityAcrossProjectAndGlobalScopes(t *testing.T) {
	t.Run("project runner", func(t *testing.T) {
		store, projectID, runner, firstTask := createRunnerAttemptFixture(t)
		_, err := store.Sql().Exec(
			store.PrepareQuery("update runner set max_parallel_tasks=1 where id=?"), runner.ID,
		)
		require.NoError(t, err)
		runner.MaxParallelTasks = 1
		secondTask, err := store.CreateTask(db.Task{
			ProjectID: projectID, TemplateID: firstTask.TemplateID,
			Status: task_logger.TaskStartingStatus, Created: time.Now(),
		}, 0)
		require.NoError(t, err)
		assertOneConcurrentCapacityWinner(t, store, runner, []db.Task{firstTask, secondTask})
	})

	t.Run("global runner across projects", func(t *testing.T) {
		store := InitConfigCreateTestStore()
		t.Cleanup(store.Close)
		projectOne, repositoryOne := newTemplateTestProject(t, store)
		templateOne, err := store.CreateTemplate(db.Template{
			ProjectID: projectOne, RepositoryID: repositoryOne, Name: "one", Playbook: "site.yml",
		})
		require.NoError(t, err)
		projectTwo, repositoryTwo := newTemplateTestProject(t, store)
		templateTwo, err := store.CreateTemplate(db.Template{
			ProjectID: projectTwo, RepositoryID: repositoryTwo, Name: "two", Playbook: "site.yml",
		})
		require.NoError(t, err)
		runner, err := store.CreateRunner(db.Runner{
			Name: "global capacity", Token: db.GenerateRunnerToken(), Active: true, MaxParallelTasks: 1,
		})
		require.NoError(t, err)
		firstTask, err := store.CreateTask(db.Task{
			ProjectID: projectOne, TemplateID: templateOne.ID,
			Status: task_logger.TaskStartingStatus, Created: time.Now(),
		}, 0)
		require.NoError(t, err)
		secondTask, err := store.CreateTask(db.Task{
			ProjectID: projectTwo, TemplateID: templateTwo.ID,
			Status: task_logger.TaskStartingStatus, Created: time.Now(),
		}, 0)
		require.NoError(t, err)
		assertOneConcurrentCapacityWinner(t, store, runner, []db.Task{firstTask, secondTask})
	})
}
