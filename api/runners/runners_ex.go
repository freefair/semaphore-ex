package runners

import (
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/runners"
)

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
			TaskID: job.ID, Generation: job.Generation, State: state,
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
