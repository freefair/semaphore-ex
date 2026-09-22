package sql

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createGroupedTask(t *testing.T, store *SqlDb, template db.Template, keys ...string) db.Task {
	t.Helper()
	task, err := store.CreateTask(db.Task{
		ProjectID: template.ProjectID, TemplateID: template.ID,
		Status: task_logger.TaskWaitingStatus, Created: time.Now(),
		TaskGroupKeys: keys,
	}, 0)
	require.NoError(t, err)
	return task
}

func createTaskGroup(t *testing.T, store *SqlDb, projectID int, name string) db.TaskGroup {
	t.Helper()
	group, err := store.CreateTaskGroup(db.TaskGroup{
		ProjectID: projectID, Name: name, MaxParallelTasks: 1,
	})
	require.NoError(t, err)
	return group
}

func taskGroupKey(group db.TaskGroup) string {
	return "group/" + strconv.Itoa(group.ID)
}

func TestTaskGroupsAcquireEveryGroupAtomically(t *testing.T) {
	store, project, _, seed := createRunnerAttemptFixture(t)
	template, err := store.GetTemplate(project, seed.TemplateID)
	require.NoError(t, err)
	state := createTaskGroup(t, store, project, "state")
	credentials := createTaskGroup(t, store, project, "credentials")
	first := createGroupedTask(t, store, template, taskGroupKey(state))
	second := createGroupedTask(t, store, template, taskGroupKey(state), taskGroupKey(credentials))
	independent := createGroupedTask(t, store, template, taskGroupKey(credentials))
	_, started, err := store.ClaimTaskStart(project, first.ID, 0)
	require.NoError(t, err)
	require.True(t, started)
	_, started, err = store.ClaimTaskStart(project, second.ID, 0)
	assert.ErrorAs(t, err, new(taskGroupsBusyError))
	assert.False(t, started)
	row, err := store.GetTask(project, second.ID)
	require.NoError(t, err)
	assert.Equal(t, task_logger.TaskWaitingStatus, row.Status)
	_, started, err = store.ClaimTaskStart(project, independent.ID, 0)
	require.NoError(t, err)
	assert.True(t, started, "a blocked multi-group task must not reserve any subset")
}

func TestTaskGroupsRetainOccupancyUntilTerminalStatus(t *testing.T) {
	for _, status := range []task_logger.TaskStatus{
		task_logger.TaskStartingStatus, task_logger.TaskRunningStatus,
		task_logger.TaskWaitingConfirmation, task_logger.TaskConfirmed,
		task_logger.TaskRejected, task_logger.TaskStoppingStatus,
		task_logger.TaskSuccessStatus, task_logger.TaskFailStatus,
		task_logger.TaskStoppedStatus, task_logger.TaskBlockedStatus,
	} {
		t.Run(string(status), func(t *testing.T) {
			store, project, _, seed := createRunnerAttemptFixture(t)
			template, err := store.GetTemplate(project, seed.TemplateID)
			require.NoError(t, err)
			group := createTaskGroup(t, store, project, "state")
			first := createGroupedTask(t, store, template, taskGroupKey(group))
			second := createGroupedTask(t, store, template, taskGroupKey(group))
			_, err = store.exec("update task set status=? where id=?", status, first.ID)
			require.NoError(t, err)
			_, started, err := store.ClaimTaskStart(project, second.ID, 0)
			if status.IsFinished() {
				require.NoError(t, err)
				assert.True(t, started)
			} else {
				assert.ErrorAs(t, err, new(taskGroupsBusyError))
				assert.False(t, started)
			}
		})
	}
}

func TestTaskGroupsConcurrentDispatchHasOneWinner(t *testing.T) {
	store, project, _, seed := createRunnerAttemptFixture(t)
	template, err := store.GetTemplate(project, seed.TemplateID)
	require.NoError(t, err)
	shared := createTaskGroup(t, store, project, "shared")
	firstOnly := createTaskGroup(t, store, project, "first")
	secondOnly := createTaskGroup(t, store, project, "second")
	first := createGroupedTask(t, store, template, taskGroupKey(shared), taskGroupKey(firstOnly))
	second := createGroupedTask(t, store, template, taskGroupKey(secondOnly), taskGroupKey(shared))
	start := make(chan struct{})
	type outcome struct {
		started bool
		err     error
	}
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for _, task := range []db.Task{first, second} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, started, err := store.ClaimTaskStart(project, task.ID, 0)
			results <- outcome{started, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.started {
			winners++
			require.NoError(t, result.err)
		} else {
			assert.ErrorAs(t, result.err, new(taskGroupsBusyError))
		}
	}
	assert.Equal(t, 1, winners)
}

func TestTaskGroupsKeepDistinctManagedIdentifiers(t *testing.T) {
	store, project, _, seed := createRunnerAttemptFixture(t)
	template, err := store.GetTemplate(project, seed.TemplateID)
	require.NoError(t, err)
	for _, group := range []db.TaskGroup{
		createTaskGroup(t, store, project, "one"),
		createTaskGroup(t, store, project, "two"),
		createTaskGroup(t, store, project, "three"),
	} {
		keys := []string{taskGroupKey(group)}
		task := createGroupedTask(t, store, template, keys...)
		_, started, err := store.ClaimTaskStart(project, task.ID, 0)
		require.NoError(t, err)
		assert.True(t, started)
	}
}

func TestResolveTaskGroupsRequiresExplicitConsumerProjectGrant(t *testing.T) {
	store, ownerProjectID, _, _ := createRunnerAttemptFixture(t)
	consumer, err := store.CreateProject(db.Project{Name: "task group consumer"})
	require.NoError(t, err)
	group := createTaskGroup(t, store, ownerProjectID, "shared-state")

	_, err = store.ResolveTaskGroups(consumer.ID, db.TaskGroupBindings{group.ID})
	assert.Error(t, err, "a template/cross-project grant must not imply group access")

	group.SharedProjectIDs = db.TaskGroupBindings{consumer.ID}
	updated, err := store.UpdateTaskGroup(group)
	require.NoError(t, err)
	assert.Equal(t, group.ID, updated.ID)
	resolved, err := store.ResolveTaskGroups(consumer.ID, db.TaskGroupBindings{group.ID})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, group.ID, resolved[0].ID)
}

func TestTaskGroupSnapshotSurvivesTemplateChanges(t *testing.T) {
	store, project, _, seed := createRunnerAttemptFixture(t)
	template, err := store.GetTemplate(project, seed.TemplateID)
	require.NoError(t, err)
	group := createTaskGroup(t, store, project, "state")
	template.TaskGroups = db.TaskGroupBindings{group.ID}
	require.NoError(t, store.UpdateTemplate(template))
	reloaded, err := store.GetTemplate(project, template.ID)
	require.NoError(t, err)
	assert.Equal(t, template.TaskGroups, reloaded.TaskGroups)
	task := createGroupedTask(t, store, template, taskGroupKey(group))
	template.TaskGroups = nil
	require.NoError(t, store.UpdateTemplate(template))
	reloadedTask, err := store.GetTask(project, task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.StringArrayField{taskGroupKey(group)}, reloadedTask.TaskGroupKeys)
}

func TestTaskGroupMigrationPreservesExistingRows(t *testing.T) {
	previous := "2.20.5-ex1.2"
	store := InitConfigCreateTestStoreAt(&previous)
	t.Cleanup(store.Close)
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteColumnNames(t, store, "task"), "task_group_keys")
	assert.Contains(t, sqliteColumnNames(t, store, "project__template"), "task_groups")
	var guard int
	require.NoError(t, store.selectOne(&guard, "select id from task_group_dispatch_guard"))
	assert.Equal(t, 1, guard)
	require.NoError(t, db.Rollback(store, previous))
	assert.NotContains(t, sqliteColumnNames(t, store, "task"), "task_group_keys")
}

func TestTaskGroupsPreventDeletingLiveOwners(t *testing.T) {
	for _, target := range []string{"task", "template", "project"} {
		t.Run(target, func(t *testing.T) {
			store, project, _, seed := createRunnerAttemptFixture(t)
			template, err := store.GetTemplate(project, seed.TemplateID)
			require.NoError(t, err)
			group := createTaskGroup(t, store, project, "state")
			task := createGroupedTask(t, store, template, taskGroupKey(group))
			_, started, err := store.ClaimTaskStart(project, task.ID, 0)
			require.NoError(t, err)
			require.True(t, started)
			remove := func() error {
				switch target {
				case "task":
					return store.DeleteTaskWithOutputs(project, task.ID)
				case "template":
					return store.DeleteTemplate(project, template.ID)
				default:
					return store.DeleteProject(project)
				}
			}
			assert.ErrorIs(t, remove(), db.ErrInvalidOperation)
			_, err = store.GetTask(project, task.ID)
			require.NoError(t, err)
			_, err = store.exec("update task set status=? where id=?", task_logger.TaskStoppedStatus, task.ID)
			require.NoError(t, err)
			require.NoError(t, remove())
		})
	}
}

func TestDeleteProjectRejectsExternalTaskGroupReferences(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		addReferences func(t *testing.T, store *SqlDb, ownerProjectID int, group db.TaskGroup)
	}{
		{
			name: "template membership",
			addReferences: func(t *testing.T, store *SqlDb, _ int, group db.TaskGroup) {
				t.Helper()
				consumerProjectID, consumerRepositoryID := newTemplateTestProject(t, store)
				group.SharedProjectIDs = db.TaskGroupBindings{consumerProjectID}
				_, err := store.UpdateTaskGroup(group)
				require.NoError(t, err)
				template, err := store.CreateTemplate(db.Template{
					ProjectID: consumerProjectID, RepositoryID: consumerRepositoryID,
					Name: "consumer template", Playbook: "site.yml",
				})
				require.NoError(t, err)
				template.TaskGroups = db.TaskGroupBindings{group.ID}
				require.NoError(t, store.UpdateTemplate(template))
			},
		},
		{
			name: "queued task snapshot",
			addReferences: func(t *testing.T, store *SqlDb, _ int, group db.TaskGroup) {
				t.Helper()
				consumerProjectID, consumerRepositoryID := newTemplateTestProject(t, store)
				group.SharedProjectIDs = db.TaskGroupBindings{consumerProjectID}
				_, err := store.UpdateTaskGroup(group)
				require.NoError(t, err)
				template, err := store.CreateTemplate(db.Template{
					ProjectID: consumerProjectID, RepositoryID: consumerRepositoryID,
					Name: "consumer task", Playbook: "site.yml",
				})
				require.NoError(t, err)
				createGroupedTask(t, store, template, taskGroupKey(group))
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, ownerProjectID, _, _ := createRunnerAttemptFixture(t)
			group := createTaskGroup(t, store, ownerProjectID, "shared state")
			testCase.addReferences(t, store, ownerProjectID, group)

			assert.ErrorIs(t, store.DeleteProject(ownerProjectID), db.ErrInvalidOperation)
			_, err := store.GetProject(ownerProjectID)
			require.NoError(t, err)
			_, err = store.GetTaskGroup(ownerProjectID, group.ID)
			require.NoError(t, err)
		})
	}
}

func TestTaskGroupRetentionKeepsLiveOwner(t *testing.T) {
	store, project, _, seed := createRunnerAttemptFixture(t)
	template, err := store.GetTemplate(project, seed.TemplateID)
	require.NoError(t, err)
	group := createTaskGroup(t, store, project, "state")
	first := createGroupedTask(t, store, template, taskGroupKey(group))
	_, started, err := store.ClaimTaskStart(project, first.ID, 0)
	require.NoError(t, err)
	require.True(t, started)
	_, err = store.exec("update task set created=? where id=?", time.Now().Add(-time.Hour), first.ID)
	require.NoError(t, err)
	for range 3 {
		createGroupedTask(t, store, template, taskGroupKey(group))
	}
	store.clearTasks(project, template.ID, 1)
	_, err = store.GetTask(project, first.ID)
	require.NoError(t, err, "retention must preserve group occupancy until execution stops")
}
