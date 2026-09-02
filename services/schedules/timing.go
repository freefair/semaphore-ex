package schedules

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/semaphoreui/semaphore/db"
)

const defaultScheduleTimezone = "UTC"

// ScheduleTiming is the backend-authoritative interpretation of a schedule at
// a particular instant.
type ScheduleTiming struct {
	EffectiveTimezone string
	NextRun           time.Time
}

// ResolveScheduleTiming applies the documented expression, schedule, and
// global timezone precedence and calculates the next UTC occurrence.
func ResolveScheduleTiming(schedule db.Schedule, globalTimezone string, after time.Time) (ScheduleTiming, error) {
	if after.IsZero() {
		return ScheduleTiming{}, errors.New("schedule evaluation time is required")
	}

	effectiveTimezone, expression, err := resolveScheduleExpression(schedule, globalTimezone)
	if err != nil {
		return ScheduleTiming{}, err
	}

	if schedule.Type == db.ScheduleTypeRunAt {
		if schedule.RunAt == nil || !schedule.RunAt.After(after) {
			return ScheduleTiming{EffectiveTimezone: effectiveTimezone}, nil
		}
		return ScheduleTiming{
			EffectiveTimezone: effectiveTimezone,
			NextRun:           schedule.RunAt.UTC(),
		}, nil
	}

	parsed, err := cron.ParseStandard(expression)
	if err != nil {
		return ScheduleTiming{}, err
	}

	return ScheduleTiming{
		EffectiveTimezone: effectiveTimezone,
		NextRun:           parsed.Next(after).UTC(),
	}, nil
}

// ApplyScheduleTiming adds response-only timing fields without changing the
// persisted schedule definition.
func ApplyScheduleTiming(schedule *db.Schedule, globalTimezone string, after time.Time) error {
	timing, err := ResolveScheduleTiming(*schedule, globalTimezone, after)
	if err != nil {
		return err
	}

	schedule.EffectiveTimezone = timing.EffectiveTimezone
	if timing.NextRun.IsZero() {
		schedule.NextRun = nil
	} else {
		nextRun := timing.NextRun
		schedule.NextRun = &nextRun
	}
	return nil
}

func resolveScheduleExpression(schedule db.Schedule, globalTimezone string) (string, string, error) {
	fallback := globalTimezone
	if fallback == "" {
		fallback = defaultScheduleTimezone
	}
	if err := ValidateTimezone(fallback); err != nil {
		return "", "", err
	}

	effective := fallback
	if schedule.Timezone != nil {
		if err := ValidateTimezone(*schedule.Timezone); err != nil {
			return "", "", err
		}
		effective = *schedule.Timezone
	}

	if schedule.Type == db.ScheduleTypeRunAt {
		return effective, "", nil
	}

	expression := strings.Join(strings.Fields(schedule.CronFormat), " ")
	explicitTimezone, hasExplicitTimezone, err := cronExpressionTimezone(expression)
	if err != nil {
		return "", "", err
	}
	if hasExplicitTimezone {
		if err = ValidateTimezone(explicitTimezone); err != nil {
			return "", "", err
		}
		return explicitTimezone, expression, nil
	}

	return effective, "CRON_TZ=" + effective + " " + expression, nil
}

func cronExpressionTimezone(expression string) (string, bool, error) {
	fields := strings.Fields(expression)
	if len(fields) == 0 {
		return "", false, nil
	}
	for _, prefix := range []string{"CRON_TZ=", "TZ="} {
		if strings.HasPrefix(fields[0], prefix) {
			value := strings.TrimPrefix(fields[0], prefix)
			if value == "" {
				return "", true, errors.New("cron timezone is required")
			}
			return value, true, nil
		}
	}
	return "", false, nil
}
