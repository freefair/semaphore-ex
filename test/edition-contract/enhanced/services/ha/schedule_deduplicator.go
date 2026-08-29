package ha

import (
	"errors"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
)

const scheduleOccurrenceLeaseTTL = 30 * time.Second

type managedScheduleDeduplicator struct {
	repository  pro_interfaces.ScheduleOccurrenceLeaseRepository
	ownerBootID string
	ttl         time.Duration
}

var _ schedules.ScheduleDeduplicator = (*managedScheduleDeduplicator)(nil)

func NewManagedScheduleDeduplicator(
	repository pro_interfaces.ScheduleOccurrenceLeaseRepository,
	ownerBootID string,
) schedules.ScheduleDeduplicator {
	return &managedScheduleDeduplicator{
		repository:  repository,
		ownerBootID: strings.TrimSpace(ownerBootID),
		ttl:         scheduleOccurrenceLeaseTTL,
	}
}

func (d *managedScheduleDeduplicator) ClaimScheduleOccurrence(
	occurrence schedules.ScheduleOccurrence,
) (schedules.ScheduleExecutionLease, bool, error) {
	if d.repository == nil || d.ownerBootID == "" {
		return nil, false, errors.New("schedule coordinator dependencies are required")
	}
	durableOccurrence, err := pro_interfaces.NewScheduleOccurrence(
		occurrence.ScheduleID,
		occurrence.Revision,
		occurrence.IntendedAt,
	)
	if err != nil {
		return nil, false, err
	}
	lease, acquired, err := d.repository.ClaimScheduleOccurrence(durableOccurrence, d.ownerBootID, d.ttl)
	if err != nil || !acquired {
		return nil, acquired, err
	}
	return scheduleExecutionLease{repository: d.repository, lease: lease}, true, nil
}

type scheduleExecutionLease struct {
	repository pro_interfaces.ScheduleOccurrenceLeaseRepository
	lease      pro_interfaces.ScheduleOccurrenceLease
}

var _ schedules.ScheduleExecutionLease = scheduleExecutionLease{}

func (l scheduleExecutionLease) OccurrenceKey() string {
	return l.lease.Occurrence.Key
}

func (l scheduleExecutionLease) IsCurrent() (bool, error) {
	return l.repository.IsCurrentScheduleLease(l.lease)
}

func (l scheduleExecutionLease) Complete(taskID int) (bool, error) {
	return l.repository.CompleteScheduleOccurrence(l.lease, taskID)
}

func (l scheduleExecutionLease) Release() (bool, error) {
	return l.repository.ReleaseScheduleOccurrenceLease(l.lease)
}
