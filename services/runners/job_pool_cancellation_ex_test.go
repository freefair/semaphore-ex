package runners

import (
	"context"
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
)

func TestJobRunErrorRespectsCancellationConfirmation(t *testing.T) {
	initConfig(t)
	for _, tc := range []struct {
		name         string
		status       task_logger.TaskStatus
		confirmation tasks.StopConfirmation
		err          error
		want         task_logger.TaskStatus
		launchError  bool
	}{
		{"confirmed preparation cancellation", task_logger.TaskStoppingStatus, tasks.StopConfirmed, context.Canceled, task_logger.TaskStoppedStatus, false},
		{"stop still pending", task_logger.TaskStoppingStatus, tasks.StopPending, context.Canceled, task_logger.TaskStoppingStatus, false},
		{"preparation returned after stop", task_logger.TaskStoppingStatus, tasks.StopConfirmed, errors.New("preparation interrupted"), task_logger.TaskStoppedStatus, false},
		{"unexpected cancellation", task_logger.TaskRunningStatus, tasks.StopConfirmed, context.Canceled, task_logger.TaskFailStatus, true},
		{"launch failure", task_logger.TaskRunningStatus, tasks.StopConfirmed, errors.New("preparation failed"), task_logger.TaskFailStatus, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewJobPool(nil)
			executor := &confirmedStopExecutor{LocalExecutor: &tasks.LocalExecutor{Task: db.Task{ID: 7}}, confirmation: tc.confirmation}
			running := &runningJob{job: executor, taskID: 7, status: tc.status}
			executor.Logger = running
			pool.handleJobRunError(running, tc.err)
			assert.Equal(t, tc.want, running.getStatus())
			_, logs, _, _, _ := running.getProgress()
			messages := make([]string, 0, len(logs))
			for _, entry := range logs {
				messages = append(messages, entry.Message)
			}
			message := "Unable to launch the application. Please contact your system administrator for assistance."
			if tc.launchError {
				assert.Contains(t, messages, message)
			} else {
				assert.NotContains(t, messages, message)
			}
		})
	}
}
