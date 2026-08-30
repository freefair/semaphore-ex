package metrics

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"time"
)

// RecordDockerTelemetry accepts only db-validated events. All labels are
// closed enums; no Docker object, runner, task, image, or error text can enter
// the Prometheus label set.
func (m *Metrics) RecordDockerTelemetry(event db.DockerTelemetryEvent) {
	if m == nil || event.Validate() != nil {
		return
	}
	switch event.Kind {
	case db.DockerTelemetryResourceUsage:
		role := string(event.Role)
		m.dockerCPUUsage.WithLabelValues(role).Set(float64(event.CPUUsageNanoseconds))
		m.dockerMemoryBytes.WithLabelValues(role).Set(float64(event.MemoryBytes))
		m.dockerPIDs.WithLabelValues(role).Set(float64(event.PIDs))
	case db.DockerTelemetryPolicyDenial:
		m.dockerPolicyDenials.WithLabelValues(event.PolicyRule).Inc()
	case db.DockerTelemetryImagePull:
		m.dockerPullDuration.WithLabelValues(string(event.PullSource), string(event.Role)).Observe(float64(event.DurationMilliseconds) / 1000)
	case db.DockerTelemetryCleanupFailure:
		m.dockerCleanupFailed.WithLabelValues(string(event.CleanupResource)).Inc()
	case db.DockerTelemetryReconciliation:
		m.dockerReconciliation.WithLabelValues(string(event.ReconciliationState)).Add(float64(event.Count))
	case db.DockerTelemetryOrphan:
		m.dockerOrphans.WithLabelValues(string(event.OrphanState)).Add(float64(event.Count))
	case db.DockerTelemetryDrop:
		m.dockerTelemetryDrops.WithLabelValues(string(event.DropReason)).Add(float64(event.Count))
	}
}

func (m *Metrics) RecordEnhancedAction(event pro_interfaces.AuditEvent) {
	if m == nil || event.Validate() != nil {
		return
	}
	m.enhancedActions.WithLabelValues(string(event.Action), string(event.Outcome), string(event.Source)).Inc()
}

func (m *Metrics) ObserveDependency(dependency pro_interfaces.DependencyID, latency time.Duration, healthy bool) {
	if m == nil || !validDependency(dependency) {
		return
	}
	label := string(dependency)
	m.dependencyLatency.WithLabelValues(label).Observe(latency.Seconds())
	if healthy {
		m.dependencyHealthy.WithLabelValues(label).Set(1)
		return
	}
	m.dependencyHealthy.WithLabelValues(label).Set(0)
	m.dependencyFailure.WithLabelValues(label).Inc()
}

func (m *Metrics) SetQueueDepth(queue pro_interfaces.QueueID, depth float64) {
	if m == nil || (queue != pro_interfaces.QueueEnhancedAudit && queue != pro_interfaces.QueueAuditWebhook) || depth < 0 {
		return
	}
	m.queueDepth.WithLabelValues(string(queue)).Set(depth)
}

func (m *Metrics) SetAuditWebhookQueueHealth(depth int, oldestAge time.Duration) {
	if m == nil || depth < 0 || oldestAge < 0 {
		return
	}
	m.SetQueueDepth(pro_interfaces.QueueAuditWebhook, float64(depth))
	m.auditWebhookAge.Set(oldestAge.Seconds())
}

func (m *Metrics) RecordAuditWebhookAttempt() {
	if m != nil {
		m.auditWebhookTries.Inc()
	}
}

func (m *Metrics) RecordAuditWebhookSuccess() {
	if m != nil {
		m.auditWebhookOK.Inc()
	}
}

func (m *Metrics) RecordAuditWebhookPermanentFailure() {
	if m != nil {
		m.auditWebhookDead.Inc()
	}
}

func (m *Metrics) RecordAuditWebhookRedactionFailure() {
	if m != nil {
		m.auditWebhookDrops.Inc()
	}
}

func (m *Metrics) RecordDroppedRecord(sink pro_interfaces.AuditSink, reason pro_interfaces.DroppedRecordReason) {
	if m == nil || !validSink(sink) || !validDropReason(reason) {
		return
	}
	m.droppedRecords.WithLabelValues(string(sink), string(reason)).Inc()
}

func validDependency(dependency pro_interfaces.DependencyID) bool {
	return dependency == pro_interfaces.DependencyAuditDatabase || dependency == pro_interfaces.DependencyAuditFile ||
		dependency == pro_interfaces.DependencyAuditWebhook
}

func validSink(sink pro_interfaces.AuditSink) bool {
	return sink == pro_interfaces.AuditSinkDatabase || sink == pro_interfaces.AuditSinkFile
}

func validDropReason(reason pro_interfaces.DroppedRecordReason) bool {
	return reason == pro_interfaces.DroppedRecordWriteFailure || reason == pro_interfaces.DroppedRecordInvalid
}
