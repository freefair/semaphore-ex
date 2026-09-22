package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/require"
	"testing"
)

type localStopProbeJob struct {
	successfulKilledJob
	killed bool
}

func (j *localStopProbeJob) Kill()          { j.killed = true }
func (j *localStopProbeJob) IsKilled() bool { return j.killed }
func TestTaskPoolStopsLocalTerraformInEveryActivePhase(t *testing.T) {
	for _, status := range []task_logger.TaskStatus{task_logger.TaskStartingStatus, task_logger.TaskRunningStatus, task_logger.TaskWaitingConfirmation, task_logger.TaskConfirmed} {
		t.Run(string(status), func(t *testing.T) {
			f := newTaskRunnerRunFixture(t)
			t.Cleanup(f.store.Close)
			f.task.Status = status
			require.NoError(t, f.store.UpdateTask(f.task))
			job := &localStopProbeJob{}
			runner := &TaskRunner{Task: f.task, Template: db.Template{App: db.AppTerraform}, pool: &f.pool, job: job}
			f.pool.stopTaskRunner(runner, false)
			require.True(t, job.killed, "stop request must reach executor outside running, too")
			require.Equal(t, task_logger.TaskStoppingStatus, runner.Task.Status)
			require.Nil(t, runner.Task.End)
		})
	}
}
func TestTaskRunnerFinalizesStoppingLocalJobAfterExit(t *testing.T) {
	f := newTaskRunnerRunFixture(t)
	t.Cleanup(f.store.Close)
	runner := TaskRunner{Task: f.task, Template: f.template, pool: &f.pool, keyInstaller: f.keyInstaller}
	runner.job = &localStopProbeJob{successfulKilledJob: successfulKilledJob{onRun: func() { runner.SetStatus(task_logger.TaskStoppingStatus) }}}
	runner.run()
	require.Equal(t, task_logger.TaskStoppedStatus, runner.Task.Status)
	stored, err := f.store.GetTaskByID(f.task.ID)
	require.NoError(t, err)
	require.Equal(t, task_logger.TaskStoppedStatus, stored.Status)
	require.NotNil(t, stored.End)
}
