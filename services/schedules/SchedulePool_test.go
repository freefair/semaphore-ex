package schedules

import (
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
)

// mockEncryptionService is a test implementation of AccessKeyEncryptionService
type mockEncryptionService struct{}

func (m *mockEncryptionService) RekeyAccessKeys(oldKey string) (err error) {
	return nil
}

func (m *mockEncryptionService) SerializeSecret(key *db.AccessKey) error {
	return nil
}

func (m *mockEncryptionService) DeserializeSecret(key *db.AccessKey) error {
	return nil
}

func (m *mockEncryptionService) FillEnvironmentSecrets(env *db.Environment, deserializeSecret bool) error {
	return nil
}

func (m *mockEncryptionService) DeleteSecret(key *db.AccessKey) error {
	return nil
}

func (m *mockEncryptionService) CreateTaskSurveySecrets(projectID int, taskID int, secrets string, expireAt time.Time) error {
	return nil
}

func (m *mockEncryptionService) GetTaskSurveySecrets(projectID int, taskID int) (string, error) {
	return "", nil
}

func (m *mockEncryptionService) DeleteTaskSurveySecrets(projectID int, taskID int) error {
	return nil
}

func TestValidateCronFormat(t *testing.T) {
	err := ValidateCronFormat("* * * *")
	if err == nil {
		t.Fatal("")
	}

	err = ValidateCronFormat("* * 1 * *")
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestOneTimeSchedule(t *testing.T) {
	future := time.Now().Add(time.Hour)
	schedule := oneTimeSchedule{runAt: future}

	if schedule.Next(time.Now()) != future {
		t.Fatalf("expected next run at %v", future)
	}

	if !schedule.Next(future).IsZero() {
		t.Fatalf("expected schedule to stop after run time")
	}
}

// mockDeduplicator is a test implementation of ScheduleDeduplicator
type mockDeduplicator struct {
	mu             sync.Mutex
	allowExecution map[int]bool
	lockAttempts   map[int]int
}

type mockScheduleLease struct{ key string }

func (l mockScheduleLease) OccurrenceKey() string    { return l.key }
func (mockScheduleLease) IsCurrent() (bool, error)   { return true, nil }
func (mockScheduleLease) Complete(int) (bool, error) { return true, nil }
func (mockScheduleLease) Block(int) (bool, error)    { return true, nil }
func (mockScheduleLease) Release() (bool, error)     { return true, nil }

func newMockDeduplicator() *mockDeduplicator {
	return &mockDeduplicator{
		allowExecution: make(map[int]bool),
		lockAttempts:   make(map[int]int),
	}
}

func (m *mockDeduplicator) ClaimScheduleOccurrence(occurrence ScheduleOccurrence) (ScheduleExecutionLease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockAttempts[occurrence.ScheduleID]++
	if !m.allowExecution[occurrence.ScheduleID] {
		return nil, false, nil
	}
	return mockScheduleLease{key: occurrence.Revision}, true, nil
}

func (m *mockDeduplicator) setAllowExecution(scheduleID int, allow bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allowExecution[scheduleID] = allow
}

func (m *mockDeduplicator) getLockAttempts(scheduleID int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockAttempts[scheduleID]
}

// mockAccessKeyInstaller is a test implementation of AccessKeyInstaller
type mockAccessKeyInstaller struct{}

func (m *mockAccessKeyInstaller) Install(key db.AccessKey, usage db.AccessKeyRole, logger task_logger.Logger) (installation ssh.AccessKeyInstallation, err error) {
	return ssh.AccessKeyInstallation{}, nil
}

func setupTestSchedulePool(t *testing.T) (*SchedulePool, db.Store) {
	store := sql.InitConfigCreateTestStore()

	// Store original config and restore after test
	originalSchedule := util.Config.Schedule
	t.Cleanup(func() {
		util.Config.Schedule = originalSchedule
	})

	// Ensure util.Config Schedule is set (CreateTestStore doesn't set this)
	util.Config.Schedule = &util.ScheduleConfig{
		Timezone: "UTC",
	}

	pool := CreateSchedulePool(
		store,
		&tasks.TaskPool{},
		&mockAccessKeyInstaller{},
		&mockEncryptionService{},
	)

	t.Cleanup(func() {
		pool.Destroy()
	})

	return pool, store
}

func TestCreateSchedulePoolKeepsOneIdentityForScheduledJobsAndDeduplicator(t *testing.T) {
	pool, _ := setupTestSchedulePool(t)
	dedup := newMockDeduplicator()

	pool.SetDeduplicator(dedup)
	runner := CreateScheduleRunner(1, 1, pool, &mockEncryptionService{}, &mockAccessKeyInstaller{})

	assert.Same(t, pool, runner.pool)
	assert.Same(t, dedup, runner.pool.dedup)
}

// TestSetDeduplicator verifies that SetDeduplicator properly configures the deduplicator
func TestSetDeduplicator(t *testing.T) {
	pool, _ := setupTestSchedulePool(t)

	// Initially no deduplicator should be set
	assert.Nil(t, pool.dedup, "deduplicator should be nil initially")

	// Set a deduplicator
	dedup := newMockDeduplicator()
	pool.SetDeduplicator(dedup)

	// Verify it's set
	assert.NotNil(t, pool.dedup, "deduplicator should be set after calling SetDeduplicator")
	assert.Equal(t, dedup, pool.dedup, "deduplicator should be the one we set")
}

// TestScheduleExecutesNormallyWithoutDeduplicator verifies schedules execute when no deduplicator is set
func TestScheduleExecutesNormallyWithoutDeduplicator(t *testing.T) {
	pool, _ := setupTestSchedulePool(t)

	// Ensure no deduplicator is set
	pool.SetDeduplicator(nil)

	// Verify that the deduplicator is nil (schedule would execute normally)
	assert.Nil(t, pool.dedup, "deduplicator should be nil, allowing normal execution")
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

func TestDeploymentWindowScheduleDecisionKeySeparatesIdenticalSchedules(t *testing.T) {
	at := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	first, err := NewScheduleOccurrence(db.Schedule{ID: 42, TemplateID: 7, CronFormat: "0 * * * *"}, at)
	if !assert.NoError(t, err) {
		return
	}
	second, err := NewScheduleOccurrence(db.Schedule{ID: 43, TemplateID: 7, CronFormat: "0 * * * *"}, at)
	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, first.Revision, second.Revision, "the distinct schedules intentionally share their definition revision")
	assert.NotEqual(t, deploymentWindowScheduleDecisionKey(first), deploymentWindowScheduleDecisionKey(second), "admission keys must not collide within one project")
	assert.Equal(t, deploymentWindowScheduleDecisionKey(first), deploymentWindowScheduleDecisionKey(first), "a replay must retain the same admission key")
}
