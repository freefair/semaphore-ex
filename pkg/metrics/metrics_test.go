package metrics

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
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
	m.SetAuditWebhookQueueHealth(2, 5*time.Second)
	m.RecordAuditWebhookAttempt()
	m.RecordAuditWebhookSuccess()
	m.RecordAuditWebhookPermanentFailure()
	m.RecordAuditWebhookRedactionFailure()

	body := scrape(m)
	assert.Contains(t, body, `semaphore_enhanced_actions_total{action="capability_write",outcome="denied",source="api"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_dependency_failures_total{dependency="audit_file"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_dependency_latency_seconds_count{dependency="audit_file"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_queue_depth{queue="enhanced_audit"} 3`)
	assert.Contains(t, body, `semaphore_enhanced_dropped_records_total{reason="write_failure",sink="file"} 1`)
	assert.Contains(t, body, `semaphore_enhanced_queue_depth{queue="audit_webhook"} 2`)
	assert.Contains(t, body, `semaphore_audit_webhook_oldest_queued_age_seconds 5`)
	assert.Contains(t, body, `semaphore_audit_webhook_attempts_total 1`)
	assert.Contains(t, body, `semaphore_audit_webhook_successes_total 1`)
	assert.Contains(t, body, `semaphore_audit_webhook_permanent_failures_total 1`)
	assert.Contains(t, body, `semaphore_audit_webhook_redaction_failures_total 1`)
	securityfixtures.AssertTripwiresAbsent(t, body)
}

func TestMetricsDockerTelemetryUsesOnlyFixedLabels(t *testing.T) {
	m := NewMetrics()
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 1, Kind: db.DockerTelemetryResourceUsage, Role: db.DockerTelemetryRoleTask, CPUUsageNanoseconds: 7, MemoryBytes: 8, PIDs: 2})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 2, Kind: db.DockerTelemetryPolicyDenial, PolicyRule: db.DockerPolicyRuleImageDenied})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 3, Kind: db.DockerTelemetryImagePull, Role: db.DockerTelemetryRoleHelper, PullSource: db.DockerTelemetryPullPulled, DurationMilliseconds: 100})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 4, Kind: db.DockerTelemetryCleanupFailure, CleanupResource: db.DockerTelemetryCleanupVolume})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 5, Kind: db.DockerTelemetryReconciliation, ReconciliationState: db.DockerReconciliationAbsent, Count: 2})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 6, Kind: db.DockerTelemetryOrphan, OrphanState: db.DockerTelemetryOrphanDetected, Count: 1})
	m.RecordDockerTelemetry(db.DockerTelemetryEvent{Sequence: 7, Kind: db.DockerTelemetryDrop, DropReason: db.DockerTelemetryDropQueueFull, Count: 4})
	body := scrape(m)
	assert.Contains(t, body, `semaphore_docker_resource_memory_bytes{role="task"} 8`)
	assert.Contains(t, body, `semaphore_docker_policy_denials_total{rule="DOCKER_POLICY_IMAGE_DENIED"} 1`)
	assert.Contains(t, body, `semaphore_docker_image_pull_duration_seconds_count{role="helper",source="pulled"} 1`)
	assert.Contains(t, body, `semaphore_docker_cleanup_failures_total{resource="volume"} 1`)
	assert.Contains(t, body, `semaphore_docker_reconciliation_total{state="absent"} 2`)
	assert.Contains(t, body, `semaphore_docker_orphans_total{state="detected"} 1`)
	assert.Contains(t, body, `semaphore_docker_telemetry_dropped_events_total{reason="queue_full"} 4`)
	assert.NotContains(t, body, "container_id")
	assert.NotContains(t, body, "image=")
}

func TestMetricsKubernetesTelemetryUsesOnlyFixedLabels(t *testing.T) {
	m := NewMetrics()
	m.RecordKubernetesTelemetry(db.KubernetesTelemetryEvent{Sequence: 1, Kind: db.KubernetesTelemetryAPILatency, Operation: db.KubernetesTelemetryOperationCreateJob, DurationMilliseconds: 10})
	m.RecordKubernetesTelemetry(db.KubernetesTelemetryEvent{Sequence: 2, Kind: db.KubernetesTelemetryDenial, PolicyRule: db.KubernetesPolicyRuleQuotaDenied})
	m.RecordKubernetesTelemetry(db.KubernetesTelemetryEvent{Sequence: 3, Kind: db.KubernetesTelemetryCleanupFailure, CleanupResource: db.KubernetesTelemetryResourceSecret})
	m.RecordKubernetesTelemetry(db.KubernetesTelemetryEvent{Sequence: 4, Kind: db.KubernetesTelemetryQuarantine, Count: 2})
	body := scrape(m)
	assert.Contains(t, body, `semaphore_kubernetes_api_latency_seconds_count{operation="create_job"} 1`)
	assert.Contains(t, body, `semaphore_kubernetes_denials_total{rule="K8S_API_QUOTA_DENIED"} 1`)
	assert.Contains(t, body, `semaphore_kubernetes_cleanup_failures_total{resource="secret"} 1`)
	assert.Contains(t, body, `semaphore_kubernetes_quarantines_total 2`)
	assert.NotContains(t, body, "namespace=")
	assert.NotContains(t, body, "error=")
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
