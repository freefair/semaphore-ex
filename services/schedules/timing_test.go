package schedules

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scheduleTimezone(value string) *string { return &value }

func TestResolveScheduleTimingAppliesTimezonePrecedence(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name              string
		schedule          db.Schedule
		globalTimezone    string
		effectiveTimezone string
		nextRun           string
	}{
		{
			name: "global fallback", schedule: db.Schedule{CronFormat: "30 9 * * *"},
			globalTimezone: "America/New_York", effectiveTimezone: "America/New_York", nextRun: "2026-01-01T14:30:00Z",
		},
		{
			name: "schedule timezone", schedule: db.Schedule{CronFormat: "30 9 * * *", Timezone: scheduleTimezone("Europe/Berlin")},
			globalTimezone: "America/New_York", effectiveTimezone: "Europe/Berlin", nextRun: "2026-01-01T08:30:00Z",
		},
		{
			name: "expression timezone", schedule: db.Schedule{CronFormat: "CRON_TZ=Asia/Tokyo 30 9 * * *", Timezone: scheduleTimezone("Europe/Berlin")},
			globalTimezone: "America/New_York", effectiveTimezone: "Asia/Tokyo", nextRun: "2026-01-01T00:30:00Z",
		},
		{
			name: "legacy expression timezone", schedule: db.Schedule{CronFormat: "TZ=Etc/GMT+5 30 9 * * *", Timezone: scheduleTimezone("Europe/Berlin")},
			globalTimezone: "UTC", effectiveTimezone: "Etc/GMT+5", nextRun: "2026-01-01T14:30:00Z",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			timing, err := ResolveScheduleTiming(testCase.schedule, testCase.globalTimezone, from)
			require.NoError(t, err)
			assert.Equal(t, testCase.effectiveTimezone, timing.EffectiveTimezone)
			assert.Equal(t, testCase.nextRun, timing.NextRun.Format(time.RFC3339))
		})
	}
}

func TestResolveScheduleTimingUsesUTCWhenGlobalTimezoneIsEmpty(t *testing.T) {
	timing, err := ResolveScheduleTiming(
		db.Schedule{CronFormat: "0 12 * * *"}, "", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	assert.Equal(t, "UTC", timing.EffectiveTimezone)
	assert.Equal(t, "2026-01-01T12:00:00Z", timing.NextRun.Format(time.RFC3339))
}

func TestResolveScheduleTimingDocumentsDSTGapAndOverlap(t *testing.T) {
	schedule := db.Schedule{CronFormat: "30 2 * * *", Timezone: scheduleTimezone("Europe/Berlin")}

	spring, err := ResolveScheduleTiming(schedule, "UTC", time.Date(2026, 3, 28, 1, 31, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2026-03-30T00:30:00Z", spring.NextRun.Format(time.RFC3339))

	first, err := ResolveScheduleTiming(schedule, "UTC", time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "2026-10-25T00:30:00Z", first.NextRun.Format(time.RFC3339))

	second, err := ResolveScheduleTiming(schedule, "UTC", first.NextRun)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-25T01:30:00Z", second.NextRun.Format(time.RFC3339))
	assert.NotEqual(t, first.NextRun, second.NextRun)
}

func TestResolveScheduleTimingReturnsRunAtAsBackendNextRun(t *testing.T) {
	runAt := time.Date(2026, 1, 2, 12, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	timing, err := ResolveScheduleTiming(db.Schedule{
		Type: db.ScheduleTypeRunAt, RunAt: &runAt, Timezone: scheduleTimezone("Europe/Berlin"),
	}, "UTC", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "Europe/Berlin", timing.EffectiveTimezone)
	assert.Equal(t, "2026-01-02T10:00:00Z", timing.NextRun.Format(time.RFC3339))
}

func TestNewScheduleOccurrenceBindsTimezone(t *testing.T) {
	at := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	base := db.Schedule{ID: 42, TemplateID: 7, CronFormat: "30 2 * * *", Timezone: scheduleTimezone("Europe/Berlin")}
	first, err := NewScheduleOccurrence(base, at)
	require.NoError(t, err)

	changed := base
	changed.Timezone = scheduleTimezone("UTC")
	second, err := NewScheduleOccurrence(changed, at)
	require.NoError(t, err)
	assert.NotEqual(t, first.Revision, second.Revision)
	assert.Equal(t, at, first.IntendedAt)
}
