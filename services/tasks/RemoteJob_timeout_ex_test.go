package tasks

import (
	"strconv"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteJobTimeoutKeepsStopEvidenceTasksNonterminal(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		app     db.TemplateApp
		grouped bool
	}{
		{name: "grouped", app: db.AppBash, grouped: true},
		{name: "terraform", app: db.AppTerraform},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setupReconcilerConfig(t)
			store := sql.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			state := NewMemoryTaskStateStore()
			pool := newReconcilerTestPool(store, state)
			now := time.Now()
			task, _ := createReconcilerTestTask(t, store, task_logger.TaskRunningStatus, &now)
			if testCase.grouped {
				group, err := store.CreateTaskGroup(db.TaskGroup{
					ProjectID: task.ProjectID, Name: "production", MaxParallelTasks: 1,
				})
				require.NoError(t, err)
				task.TaskGroupKeys = db.StringArrayField{"group/" + strconv.Itoa(group.ID)}
				_, err = store.Sql().Exec(store.PrepareQuery("update task set task_group_keys=? where id=?"), task.TaskGroupKeys, task.ID)
				require.NoError(t, err)
				task, err = store.GetTaskByID(task.ID)
				require.NoError(t, err)
			}
			tsk := &TaskRunner{Task: task, Template: db.Template{App: testCase.app}, pool: &pool}
			state.SetRunning(tsk)
			job := RemoteJob{Task: task, taskPool: &pool}

			job.handleTimeout(task.ID, nil)

			assert.Equal(t, task_logger.TaskStoppingStatus, tsk.Task.Status)
			assert.Nil(t, tsk.Task.End)
			stored, err := store.GetTaskByID(task.ID)
			require.NoError(t, err)
			assert.Equal(t, task_logger.TaskStoppingStatus, stored.Status)
			assert.Nil(t, stored.End)
			if testCase.grouped {
				assert.Equal(t, task.TaskGroupKeys, stored.TaskGroupKeys)
			}

			if !testCase.grouped {
				return
			}
			groups, err := store.GetTaskGroups(task.ProjectID)
			require.NoError(t, err)
			require.Len(t, groups, 1)
			assert.Equal(t, 1, groups[0].MaxParallelTasks)
			template, err := store.GetTemplate(task.ProjectID, task.TemplateID)
			require.NoError(t, err)
			successor, err := store.CreateTask(db.Task{
				ProjectID: task.ProjectID, TemplateID: template.ID,
				Status: task_logger.TaskWaitingStatus, Created: time.Now(),
				TaskGroupKeys: task.TaskGroupKeys,
			}, 0)
			require.NoError(t, err)
			_, started, err := store.ClaimTaskStart(task.ProjectID, successor.ID, 0)
			assert.Error(t, err)
			assert.False(t, started, "timed-out grouped task must retain group capacity until terminal evidence")
		})
	}
}
