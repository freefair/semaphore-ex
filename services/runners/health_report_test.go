package runners

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHealthReportValidatesBoundsAndPreservesMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		header  http.Header
		wantErr string
	}{
		{name: "older runner", header: http.Header{}},
		{name: "valid", header: http.Header{
			RunnerVersionHeader:     []string{" 2.20.4 "},
			RunnerPlatformHeader:    []string{"linux/amd64"},
			RunnerCurrentLoadHeader: []string{"3"},
		}},
		{name: "negative load", header: http.Header{RunnerCurrentLoadHeader: []string{"-1"}}, wantErr: "between"},
		{name: "excessive load", header: http.Header{RunnerCurrentLoadHeader: []string{strconv.Itoa(maxRunnerReportedLoad + 1)}}, wantErr: "between"},
		{name: "long version", header: http.Header{RunnerVersionHeader: []string{strings.Repeat("v", maxRunnerReportTextBytes+1)}}, wantErr: "exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := ParseHealthReport(tt.header)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.name == "older runner" {
				runner := db.Runner{Version: "kept", Platform: "kept", CurrentLoad: 7}
				report.Apply(&runner)
				assert.Equal(t, db.Runner{Version: "kept", Platform: "kept", CurrentLoad: 7}, runner)
				return
			}
			require.NotNil(t, report.Version)
			require.NotNil(t, report.Platform)
			require.NotNil(t, report.CurrentLoad)
			assert.Equal(t, "2.20.4", *report.Version)
			assert.Equal(t, "linux/amd64", *report.Platform)
			assert.Equal(t, 3, *report.CurrentLoad)
		})
	}
}
