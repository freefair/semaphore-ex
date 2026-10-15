package runners

import (
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	log "github.com/sirupsen/logrus"
)

func (p *JobPool) handleJobRunError(running *runningJob, err error) {
	// Preparation can return an error after an explicit stop. Keep termination
	// confirmation authoritative without presenting that cancellation as a fault.
	if running.getStatus() == task_logger.TaskStoppingStatus {
		log.WithFields(log.Fields{
			"context": "job_running",
			"task_id": running.taskID,
		}).WithError(err).Debug("Job returned after stop request")
		p.finishStoppedJob(running)
		return
	}

	log.WithFields(log.Fields{
		"context":     "job_running",
		"task_id":     running.taskID,
		"task_status": running.getStatus(),
	}).WithError(err).Error("launch job failed")
	running.Log("Unable to launch the application. Please contact your system administrator for assistance.")
	if running.getStatus() == task_logger.TaskStoppingStatus {
		p.finishStoppedJob(running)
	} else {
		running.SetStatus(task_logger.TaskFailStatus)
	}
}
