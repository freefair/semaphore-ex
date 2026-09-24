package runners

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateRunner_ConfirmationDecisionAcceptsProgress(t *testing.T) {
	for _, test := range []struct{ decision, reported, expected task_logger.TaskStatus }{
		{task_logger.TaskConfirmed, task_logger.TaskWaitingConfirmation, task_logger.TaskConfirmed},
		{task_logger.TaskRejected, task_logger.TaskWaitingConfirmation, task_logger.TaskRejected},
		{task_logger.TaskConfirmed, task_logger.TaskConfirmed, task_logger.TaskConfirmed},
		{task_logger.TaskConfirmed, task_logger.TaskRunningStatus, task_logger.TaskRunningStatus},
		{task_logger.TaskConfirmed, task_logger.TaskSuccessStatus, task_logger.TaskSuccessStatus},
		{task_logger.TaskRejected, task_logger.TaskFailStatus, task_logger.TaskFailStatus},
		{task_logger.TaskRejected, task_logger.TaskStoppedStatus, task_logger.TaskStoppedStatus},
	} {
		for _, crossNode := range []bool{false, true} {
			name := string(test.decision) + "/" + string(test.reported) + "/same-node"
			if crossNode {
				name = string(test.decision) + "/" + string(test.reported) + "/cross-node"
			}
			t.Run(name, func(t *testing.T) {
				previous := util.Config
				t.Cleanup(func() { util.Config = previous })
				fixture := newRunnerMetadataAPIFixture(t)
				fixture.task.Status = task_logger.TaskWaitingConfirmation
				stale := fixture.task
				fixture.task.Status = test.decision
				require.NoError(t, fixture.store.UpdateTask(fixture.task))
				pool := tasks.CreateTaskPool(fixture.store, tasks.NewMemoryTaskStateStore(), nil, nil, nil, nil, &runnerAPILogWriter{}, nil, nil)
				local := fixture.task
				if crossNode {
					local = stale
				}
				tr := tasks.NewTaskRunner(local, &pool, "", nil)
				pool.StateStore().SetRunning(tr)
				controller := NewRunnerController(fixture.store, &pool, nil, nil)
				request := newProgressRequest(t, fixture.store, fixture.runner, runners.RunnerProgress{
					Jobs: []runners.JobProgress{{ID: fixture.task.ID, Generation: fixture.task.AssignmentGeneration,
						Status: test.reported,
						Commit: &runners.CommitInfo{Hash: "reviewed-commit", Message: "awaiting decision"},
					}},
				})
				response := httptest.NewRecorder()
				controller.UpdateRunner(response, request)
				require.Equal(t, http.StatusOK, response.Code)
				assert.Empty(t, decodeProgressResponse(t, response).TerminatedJobs)
				assert.Equal(t, test.expected, tr.Task.Status)
				if test.expected.IsFinished() {
					require.Eventually(t, func() bool {
						stored, err := fixture.store.GetTaskByID(fixture.task.ID)
						return err == nil && stored.End != nil
					}, time.Second, 10*time.Millisecond)
				}
				stored, err := fixture.store.GetTaskByID(fixture.task.ID)
				require.NoError(t, err)
				assert.Equal(t, test.expected, stored.Status)
				require.NotNil(t, stored.CommitHash)
				assert.Equal(t, "reviewed-commit", *stored.CommitHash)
			})
		}
	}
}
