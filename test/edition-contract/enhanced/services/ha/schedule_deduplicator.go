package ha

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
)

const scheduleOccurrenceLeaseTTL = 30 * time.Second

type managedScheduleDeduplicator struct {
	repository  pro_interfaces.ScheduleOccurrenceLeaseRepository
	ownerBootID string
	ttl         time.Duration
	mu          sync.Mutex
	changed     *sync.Cond
	draining    bool
	claiming    int
	active      int
}

var _ schedules.ScheduleDeduplicator = (*managedScheduleDeduplicator)(nil)
var _ pro_interfaces.ClusterDrainer = (*managedScheduleDeduplicator)(nil)

func NewManagedScheduleDeduplicator(
	repository pro_interfaces.ScheduleOccurrenceLeaseRepository,
	ownerBootID string,
) schedules.ScheduleDeduplicator {
	deduplicator := &managedScheduleDeduplicator{
		repository:  repository,
		ownerBootID: strings.TrimSpace(ownerBootID),
		ttl:         scheduleOccurrenceLeaseTTL,
	}
	deduplicator.changed = sync.NewCond(&deduplicator.mu)
	return deduplicator
}

func (d *managedScheduleDeduplicator) ClaimScheduleOccurrence(
	occurrence schedules.ScheduleOccurrence,
) (schedules.ScheduleExecutionLease, bool, error) {
	if d.repository == nil || d.ownerBootID == "" {
		return nil, false, errors.New("schedule coordinator dependencies are required")
	}
	d.mu.Lock()
	if d.draining {
		d.mu.Unlock()
		return nil, false, nil
	}
	d.claiming++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.claiming--
		d.changed.Broadcast()
		d.mu.Unlock()
	}()
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
	d.mu.Lock()
	d.active++
	d.mu.Unlock()
	return &scheduleExecutionLease{repository: d.repository, lease: lease, finished: d.finishLease}, true, nil
}

func (d *managedScheduleDeduplicator) Drain() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.draining = true
	for d.claiming > 0 || d.active > 0 {
		d.changed.Wait()
	}
	return nil
}

func (d *managedScheduleDeduplicator) Resume() {
	d.mu.Lock()
	d.draining = false
	d.changed.Broadcast()
	d.mu.Unlock()
}

func (d *managedScheduleDeduplicator) finishLease() {
	d.mu.Lock()
	if d.active > 0 {
		d.active--
	}
	d.changed.Broadcast()
	d.mu.Unlock()
}

type scheduleExecutionLease struct {
	repository pro_interfaces.ScheduleOccurrenceLeaseRepository
	lease      pro_interfaces.ScheduleOccurrenceLease
	finishOnce sync.Once
	finished   func()
}

var _ schedules.ScheduleExecutionLease = (*scheduleExecutionLease)(nil)

func (l *scheduleExecutionLease) OccurrenceKey() string {
	return l.lease.Occurrence.Key
}

func (l *scheduleExecutionLease) IsCurrent() (bool, error) {
	current, err := l.repository.IsCurrentScheduleLease(l.lease)
	if err != nil || !current {
		l.finish()
	}
	return current, err
}

func (l *scheduleExecutionLease) Complete(taskID int) (bool, error) {
	completed, err := l.repository.CompleteScheduleOccurrence(l.lease, taskID)
	l.finish()
	return completed, err
}

func (l *scheduleExecutionLease) Block(decisionID int) (bool, error) {
	blocked, err := l.repository.BlockScheduleOccurrence(l.lease, decisionID)
	l.finish()
	return blocked, err
}

func (l *scheduleExecutionLease) Release() (bool, error) {
	released, err := l.repository.ReleaseScheduleOccurrenceLease(l.lease)
	l.finish()
	return released, err
}

func (l *scheduleExecutionLease) finish() {
	l.finishOnce.Do(func() {
		if l.finished != nil {
			l.finished()
		}
	})
}
