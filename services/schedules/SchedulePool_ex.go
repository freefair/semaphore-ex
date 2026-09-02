package schedules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"time"
)

// ScheduleOccurrence describes one intended run of a schedule definition.
// Revision is a deterministic digest of every setting that can change the
// work, so an edited schedule cannot reuse an older occurrence claim.
type ScheduleOccurrence struct {
	ScheduleID int
	Revision   string
	IntendedAt time.Time
}

// ScheduleExecutionLease is the scheduler-facing part of the durable HA
// lease. It deliberately exposes fencing checks rather than Redis details.
type ScheduleExecutionLease interface {
	OccurrenceKey() string
	IsCurrent() (bool, error)
	Complete(taskID int) (bool, error)
	Release() (bool, error)
}

// NewScheduleOccurrence derives the relevant revision and normalizes the
// intended fire instant before an HA implementation creates its durable key.
func NewScheduleOccurrence(schedule db.Schedule, intendedAt time.Time) (ScheduleOccurrence, error) {
	if schedule.ID <= 0 || intendedAt.IsZero() {
		return ScheduleOccurrence{}, errors.New("schedule occurrence identity is invalid")
	}
	revisionInput := struct {
		TemplateID     int
		CronFormat     string
		Timezone       *string
		Type           string
		RepositoryID   *int
		RunAt          *time.Time
		DeleteAfterRun bool
		TaskParams     *db.TaskParams
	}{
		TemplateID: schedule.TemplateID, CronFormat: schedule.CronFormat, Timezone: schedule.Timezone, Type: schedule.Type,
		RepositoryID: schedule.RepositoryID, RunAt: schedule.RunAt, DeleteAfterRun: schedule.DeleteAfterRun,
		TaskParams: schedule.TaskParams,
	}
	encoded, err := json.Marshal(revisionInput)
	if err != nil {
		return ScheduleOccurrence{}, err
	}
	digest := sha256.Sum256(encoded)
	return ScheduleOccurrence{
		ScheduleID: schedule.ID,
		Revision:   "sha256:" + hex.EncodeToString(digest[:]),
		IntendedAt: intendedAt.UTC(),
	}, nil
}
