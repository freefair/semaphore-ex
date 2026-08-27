package runners

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

const (
	RunnerVersionHeader     = "X-Runner-Version"
	RunnerPlatformHeader    = "X-Runner-Platform"
	RunnerCurrentLoadHeader = "X-Runner-Current-Load"

	maxRunnerReportTextBytes = 128
	maxRunnerReportedLoad    = 100_000
)

// HealthReport contains optional metadata added by health-aware runners.
// Pointers preserve compatibility with older runners that send no such headers.
type HealthReport struct {
	Version     *string
	Platform    *string
	CurrentLoad *int
}

// ParseHealthReport validates the bounded metadata attached to a runner poll.
func ParseHealthReport(header http.Header) (HealthReport, error) {
	var report HealthReport
	for name, target := range map[string]**string{
		RunnerVersionHeader:  &report.Version,
		RunnerPlatformHeader: &report.Platform,
	} {
		raw, present := header[name]
		if !present {
			continue
		}
		value := strings.TrimSpace(strings.Join(raw, ","))
		if len(value) > maxRunnerReportTextBytes {
			return HealthReport{}, fmt.Errorf("%s exceeds %d bytes", name, maxRunnerReportTextBytes)
		}
		*target = &value
	}
	if raw, present := header[RunnerCurrentLoadHeader]; present {
		value, err := strconv.Atoi(strings.TrimSpace(strings.Join(raw, ",")))
		if err != nil || value < 0 || value > maxRunnerReportedLoad {
			return HealthReport{}, fmt.Errorf("%s must be between 0 and %d", RunnerCurrentLoadHeader, maxRunnerReportedLoad)
		}
		report.CurrentLoad = &value
	}
	return report, nil
}

// Apply updates only metadata present in the report.
func (report HealthReport) Apply(runner *db.Runner) {
	if report.Version != nil {
		runner.Version = *report.Version
	}
	if report.Platform != nil {
		runner.Platform = *report.Platform
	}
	if report.CurrentLoad != nil {
		runner.CurrentLoad = *report.CurrentLoad
	}
}
