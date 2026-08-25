package metrics

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
)

// scrape renders the current metrics in Prometheus text exposition format,
// the same way a real scrape would, so tests assert on it exactly like an
// external Prometheus server would see it - no extra test-only dependency
// on prometheus/client_golang/prometheus/testutil required.
func scrape(m *Metrics) string {
	req := httptest.NewRequest("GET", "/api/metrics", nil)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	return w.Body.String()
}

func TestMetricsEnhancedSignalsUseBoundedLabels(t *testing.T) {
	m := NewMetrics()
	event := pro_interfaces.AuditEvent{
		CorrelationID: "0123456789abcdef0123456789abcdef",
		Action:        pro_interfaces.AuditActionCapabilityWrite,
		TargetType:    pro_interfaces.AuditTargetCapability,
		TargetID:      string(pro_interfaces.CapabilityLifecycleTest),
		Outcome:       pro_interfaces.AuditOutcomeDenied,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        string(pro_interfaces.CapabilityReasonDisabledByAdmin),
	}
	m.RecordEnhancedAction(event)
	m.ObserveDependency(pro_interfaces.DependencyAuditFile, 25*time.Millisecond, false)
	m.SetQueueDepth(pro_interfaces.QueueEnhancedAudit, 3)
	m.RecordDroppedRecord(pro_interfaces.AuditSinkFile, pro_interfaces.DroppedRecordWriteFailure)

	body := scrape(m)
	assert.Contains(t, body, `semaphore_enhanced_actions_total{action="capability_write",outcome="denied",source="api"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_dependency_failures_total{dependency="audit_file"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_dependency_latency_seconds_count{dependency="audit_file"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_queue_depth{queue="enhanced_audit"} 3`)
	assert.Contains(t, body, `semaphore_enhanced_dropped_records_total{reason="write_failure",sink="file"} 1`)
	securityfixtures.AssertTripwiresAbsent(t, body)
}

func TestMetrics_ServeHTTP(t *testing.T) {
	m := NewMetrics()

	req := httptest.NewRequest("GET", "/api/metrics", nil)
	w := httptest.NewRecorder()

	m.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "go_goroutines")
}

func TestMetrics_RecordTaskStatusChange_RunningGauge(t *testing.T) {
	m := NewMetrics()

	m.RecordTaskStatusChange(task_logger.TaskWaitingStatus, task_logger.TaskRunningStatus)
	assert.Contains(t, scrape(m), "semaphore_tasks_running 1")

	m.RecordTaskStatusChange(task_logger.TaskRunningStatus, task_logger.TaskSuccessStatus)
	assert.Contains(t, scrape(m), "semaphore_tasks_running 0")
}

func TestMetrics_RecordTaskStatusChange_SkipsRunningGaugeWhenNeverRan(t *testing.T) {
	m := NewMetrics()

	// A task rejected before ever running must not push the gauge negative.
	m.RecordTaskStatusChange(task_logger.TaskWaitingConfirmation, task_logger.TaskRejected)
	m.RecordTaskStatusChange(task_logger.TaskRejected, task_logger.TaskStoppedStatus)

	assert.Contains(t, scrape(m), "semaphore_tasks_running 0")
}

func TestMetrics_RecordTaskStatusChange_OutcomeCounter(t *testing.T) {
	tests := []struct {
		name   string
		status task_logger.TaskStatus
	}{
		{"success", task_logger.TaskSuccessStatus},
		{"error", task_logger.TaskFailStatus},
		{"stopped", task_logger.TaskStoppedStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetrics()

			m.RecordTaskStatusChange(task_logger.TaskRunningStatus, tt.status)

			assert.Contains(t, scrape(m), `semaphore_tasks_total{status="`+string(tt.status)+`"} 1`)
		})
	}
}

func TestMetrics_RecordTaskStatusChange_TerminalToTerminalNotDoubleCounted(t *testing.T) {
	m := NewMetrics()

	m.RecordTaskStatusChange(task_logger.TaskRunningStatus, task_logger.TaskSuccessStatus)
	m.RecordTaskStatusChange(task_logger.TaskSuccessStatus, task_logger.TaskFailStatus)

	body := scrape(m)
	assert.Contains(t, body, `semaphore_tasks_total{status="success"} 1`)
	assert.NotContains(t, body, `semaphore_tasks_total{status="error"}`)
}

func TestMetrics_RecordTaskStatusChange_NonFinishedStatusNotCounted(t *testing.T) {
	m := NewMetrics()

	m.RecordTaskStatusChange(task_logger.TaskWaitingStatus, task_logger.TaskRunningStatus)

	assert.NotContains(t, scrape(m), "semaphore_tasks_total{")
}

func TestMetrics_RecordTaskStatusChange_NilReceiverIsNoop(t *testing.T) {
	var m *Metrics

	assert.NotPanics(t, func() {
		m.RecordTaskStatusChange(task_logger.TaskWaitingStatus, task_logger.TaskRunningStatus)
	})
}
