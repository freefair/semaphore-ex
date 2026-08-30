package runners

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/services/runners"
	log "github.com/sirupsen/logrus"
	"net/http"
)

// SetMetrics is additive so existing runner-controller call sites retain their
// evidence-sink contract while the API router wires the process metrics once.
func (c *RunnerController) SetMetrics(appMetrics *metrics.Metrics) { c.metrics = appMetrics }

func (c *RunnerController) persistTaskExecutionEvidence(w http.ResponseWriter, runnerID int, evidence []db.TaskExecutionEvidence) bool {
	if c.taskExecutionEvidenceSink == nil {
		return true
	}
	if err := c.taskExecutionEvidenceSink.RecordTaskExecutionSnapshot(runnerID, evidence); err != nil {
		log.WithError(err).WithField("runner_id", runnerID).Error("failed to persist runner execution evidence")
		helpers.WriteErrorStatus(w, "Failed to persist runner execution evidence", http.StatusInternalServerError)
		return false
	}
	return true
}

func taskExecutionEvidenceFromSnapshot(snapshot []runners.JobState) ([]db.TaskExecutionEvidence, error) {
	evidence := make([]db.TaskExecutionEvidence, 0, len(snapshot))
	seen := make(map[[2]int]struct{}, len(snapshot))
	for _, job := range snapshot {
		if job.ID <= 0 || job.Generation <= 0 || !job.Status.IsValid() {
			return nil, errors.New("Invalid runner execution snapshot")
		}
		key := [2]int{job.ID, job.Generation}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("Invalid runner execution snapshot")
		}
		seen[key] = struct{}{}
		state := db.TaskExecutionEvidenceRunning
		if job.Status.IsFinished() {
			state = db.TaskExecutionEvidenceTerminal
		}
		evidence = append(evidence, db.TaskExecutionEvidence{
			TaskID: job.ID, Generation: job.Generation, State: state, Status: job.Status,
		})
	}
	return evidence, nil
}

// normalizeReportedGeneration keeps rolling upgrades compatible without
// weakening reassignment safety. Generation-unaware runners may finish only a
// task's first assignment; any replacement assignment is generation 2 or newer.
func normalizeReportedGeneration(reported int, current int) int {
	if reported == 0 && current == 1 {
		return 1
	}
	return reported
}
