package schedules

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

type mockScheduleLease struct{ key string }

func (l mockScheduleLease) OccurrenceKey() string { return l.key }

func (mockScheduleLease) IsCurrent() (bool, error) { return true, nil }

func (mockScheduleLease) Complete(int) (bool, error) { return true, nil }

func (mockScheduleLease) Release() (bool, error) { return true, nil }

func (m *mockDeduplicator) ClaimScheduleOccurrence(occurrence ScheduleOccurrence) (ScheduleExecutionLease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockAttempts[occurrence.ScheduleID]++
	if !m.allowExecution[occurrence.ScheduleID] {
		return nil, false, nil
	}
	return mockScheduleLease{key: occurrence.Revision}, true, nil
}

// TestScheduleSkippedWhenTryLockExecutionReturnsFalse verifies schedules are skipped when TryLockExecution returns false
func TestScheduleSkippedWhenOccurrenceClaimReturnsFalse(t *testing.T) {
	pool, _ := setupTestSchedulePool(t)

	// Set up deduplicator to deny execution
	dedup := newMockDeduplicator()
	scheduleID := 123
	dedup.setAllowExecution(scheduleID, false)
	pool.SetDeduplicator(dedup)

	// Simulate the occurrence claim that happens in ScheduleRunner.Run().
	_, claimed, err := pool.dedup.ClaimScheduleOccurrence(ScheduleOccurrence{ScheduleID: scheduleID})
	shouldSkip := err == nil && !claimed

	// Verify the deduplicator was called and returned false
	assert.True(t, shouldSkip, "schedule should be skipped when TryLockExecution returns false")
	assert.Equal(t, 1, dedup.getLockAttempts(scheduleID), "TryLockExecution should be called once")
}

// TestScheduleProceedsWhenTryLockExecutionReturnsTrue verifies schedules proceed when TryLockExecution returns true
func TestScheduleProceedsWhenOccurrenceClaimReturnsTrue(t *testing.T) {
	pool, _ := setupTestSchedulePool(t)

	// Set up deduplicator to allow execution
	dedup := newMockDeduplicator()
	scheduleID := 456
	dedup.setAllowExecution(scheduleID, true)
	pool.SetDeduplicator(dedup)

	// Simulate the occurrence claim that happens in ScheduleRunner.Run().
	_, claimed, err := pool.dedup.ClaimScheduleOccurrence(ScheduleOccurrence{ScheduleID: scheduleID})
	shouldSkip := err == nil && !claimed

	// Verify the deduplicator was called and returned true (schedule proceeds)
	assert.False(t, shouldSkip, "schedule should proceed when TryLockExecution returns true")
	assert.Equal(t, 1, dedup.getLockAttempts(scheduleID), "TryLockExecution should be called once")
}

func TestNewScheduleOccurrenceBindsMutableScheduleSettings(t *testing.T) {
	at := time.Date(2026, 8, 29, 12, 34, 0, 0, time.UTC)
	base := db.Schedule{ID: 42, TemplateID: 7, CronFormat: "0 * * * *"}
	first, err := NewScheduleOccurrence(base, at)
	assert.NoError(t, err)
	matching, err := NewScheduleOccurrence(base, at.In(time.FixedZone("offset", 2*60*60)))
	assert.NoError(t, err)
	assert.Equal(t, first, matching)

	changed := base
	changed.CronFormat = "30 * * * *"
	other, err := NewScheduleOccurrence(changed, at)
	assert.NoError(t, err)
	assert.NotEqual(t, first.Revision, other.Revision)
}
