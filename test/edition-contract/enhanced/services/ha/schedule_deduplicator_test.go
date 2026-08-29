package ha

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedScheduleDeduplicatorMapsCoreOccurrenceToFencedSQLLease(t *testing.T) {
	repository := &scheduleLeaseRepositoryFake{}
	deduplicator := NewManagedScheduleDeduplicator(repository, "boot-a")
	occurrence := schedules.ScheduleOccurrence{
		ScheduleID:  42,
		Revision:    "schedule-revision-a",
		IntendedAt:  time.Date(2026, 8, 29, 12, 34, 0, 0, time.UTC),
	}

	lease, acquired, err := deduplicator.ClaimScheduleOccurrence(occurrence)
	require.NoError(t, err)
	require.True(t, acquired)
	assert.Equal(t, "boot-a", repository.claimedOwner)
	assert.Equal(t, occurrence.ScheduleID, repository.claimedOccurrence.ScheduleID)
	assert.NotEmpty(t, lease.OccurrenceKey())

	current, err := lease.IsCurrent()
	require.NoError(t, err)
	assert.True(t, current)
	completed, err := lease.Complete(7)
	require.NoError(t, err)
	assert.True(t, completed)
	released, err := lease.Release()
	require.NoError(t, err)
	assert.True(t, released)
}

type scheduleLeaseRepositoryFake struct {
	claimedOccurrence pro_interfaces.ScheduleOccurrence
	claimedOwner      string
}

func (f *scheduleLeaseRepositoryFake) ClaimScheduleOccurrence(
	occurrence pro_interfaces.ScheduleOccurrence,
	ownerBootID string,
	_ time.Duration,
) (pro_interfaces.ScheduleOccurrenceLease, bool, error) {
	f.claimedOccurrence = occurrence
	f.claimedOwner = ownerBootID
	return pro_interfaces.ScheduleOccurrenceLease{
		Occurrence: occurrence, OwnerBootID: ownerBootID, FencingToken: 1,
	}, true, nil
}

func (*scheduleLeaseRepositoryFake) IsCurrentScheduleLease(pro_interfaces.ScheduleOccurrenceLease) (bool, error) {
	return true, nil
}

func (*scheduleLeaseRepositoryFake) CompleteScheduleOccurrence(pro_interfaces.ScheduleOccurrenceLease, int) (bool, error) {
	return true, nil
}

func (*scheduleLeaseRepositoryFake) ReleaseScheduleOccurrenceLease(pro_interfaces.ScheduleOccurrenceLease) (bool, error) {
	return true, nil
}
