package projects

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/schedules"
	"github.com/semaphoreui/semaphore/util"
	"time"
)

func configuredScheduleTimezone() string {
	if util.Config.Schedule == nil || util.Config.Schedule.Timezone == "" {
		return "UTC"
	}
	return util.Config.Schedule.Timezone
}

func applyScheduleTiming(schedule *db.Schedule, after time.Time) error {
	return schedules.ApplyScheduleTiming(schedule, configuredScheduleTimezone(), after)
}
