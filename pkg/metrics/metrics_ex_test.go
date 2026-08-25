package metrics

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/test/securityfixtures"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

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
