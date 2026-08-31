package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler

	tasksRunning prometheus.Gauge
	tasksTotal   *prometheus.CounterVec

	enhancedActions      *prometheus.CounterVec
	dependencyHealthy    *prometheus.GaugeVec
	dependencyFailure    *prometheus.CounterVec
	dependencyLatency    *prometheus.HistogramVec
	queueDepth           *prometheus.GaugeVec
	droppedRecords       *prometheus.CounterVec
	auditWebhookAge      prometheus.Gauge
	auditWebhookTries    prometheus.Counter
	auditWebhookOK       prometheus.Counter
	auditWebhookDead     prometheus.Counter
	auditWebhookDrops    prometheus.Counter
	dockerCPUUsage       *prometheus.GaugeVec
	dockerMemoryBytes    *prometheus.GaugeVec
	dockerPIDs           *prometheus.GaugeVec
	dockerPolicyDenials  *prometheus.CounterVec
	dockerPullDuration   *prometheus.HistogramVec
	dockerCleanupFailed  *prometheus.CounterVec
	dockerReconciliation *prometheus.CounterVec
	dockerOrphans        *prometheus.CounterVec
	dockerTelemetryDrops *prometheus.CounterVec
	kubernetesAPILatency *prometheus.HistogramVec
	kubernetesReconnects *prometheus.CounterVec
	kubernetesDenials    *prometheus.CounterVec
	kubernetesCleanup    *prometheus.CounterVec
	kubernetesReconcile  *prometheus.CounterVec
	kubernetesOrphans    prometheus.Counter
	kubernetesQuarantine prometheus.Counter
	kubernetesDrops      *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	tasksRunning := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semaphore_tasks_running",
		Help: "Number of tasks currently running.",
	})

	tasksTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semaphore_tasks_total",
		Help: "Total number of tasks that finished, by outcome.",
	}, []string{"status"})
	enhancedActions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semaphore_enhanced_actions_total",
		Help: "Enhanced actions by allowlisted action, outcome, and source.",
	}, []string{"action", "outcome", "source"})
	dependencyHealthy := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "semaphore_enhanced_dependency_healthy",
		Help: "Last observed health of an enhanced optional dependency.",
	}, []string{"dependency"})
	dependencyFailure := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semaphore_enhanced_dependency_failures_total",
		Help: "Enhanced optional dependency failures.",
	}, []string{"dependency"})
	dependencyLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "semaphore_enhanced_dependency_latency_seconds",
		Help:    "Enhanced optional dependency latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"dependency"})
	queueDepth := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "semaphore_enhanced_queue_depth",
		Help: "Current depth of an allowlisted enhanced queue.",
	}, []string{"queue"})
	droppedRecords := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semaphore_enhanced_dropped_records_total",
		Help: "Enhanced observability records dropped by sink and reason.",
	}, []string{"sink", "reason"})
	auditWebhookAge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semaphore_audit_webhook_oldest_queued_age_seconds",
		Help: "Age in seconds of the oldest queued audit webhook delivery.",
	})
	auditWebhookTries := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "semaphore_audit_webhook_attempts_total",
		Help: "Total audit webhook delivery attempts.",
	})
	auditWebhookOK := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "semaphore_audit_webhook_successes_total",
		Help: "Total successful audit webhook deliveries.",
	})
	auditWebhookDead := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "semaphore_audit_webhook_permanent_failures_total",
		Help: "Total audit webhook deliveries moved to terminal failure.",
	})
	auditWebhookDrops := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "semaphore_audit_webhook_redaction_failures_total",
		Help: "Total audit events rejected by the export allow-list.",
	})
	dockerCPUUsage := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "semaphore_docker_resource_cpu_usage_nanoseconds", Help: "Last one-shot Docker CPU usage sample by fixed container role."}, []string{"role"})
	dockerMemoryBytes := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "semaphore_docker_resource_memory_bytes", Help: "Last one-shot Docker memory sample by fixed container role."}, []string{"role"})
	dockerPIDs := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "semaphore_docker_resource_pids", Help: "Last one-shot Docker PID sample by fixed container role."}, []string{"role"})
	dockerPolicyDenials := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_docker_policy_denials_total", Help: "Docker policy denials by allow-listed rule."}, []string{"rule"})
	dockerPullDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "semaphore_docker_image_pull_duration_seconds", Help: "Docker image resolution duration by fixed pull source and role.", Buckets: prometheus.DefBuckets}, []string{"source", "role"})
	dockerCleanupFailed := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_docker_cleanup_failures_total", Help: "Docker cleanup failures by fixed resource role."}, []string{"resource"})
	dockerReconciliation := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_docker_reconciliation_total", Help: "Docker reconciliation observations by fixed state."}, []string{"state"})
	dockerOrphans := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_docker_orphans_total", Help: "Docker orphan lifecycle reports by fixed state."}, []string{"state"})
	dockerTelemetryDrops := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_docker_telemetry_dropped_events_total", Help: "Docker telemetry events dropped by the fixed runner queue reason."}, []string{"reason"})
	kubernetesAPILatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "semaphore_kubernetes_api_latency_seconds", Help: "Kubernetes API latency by fixed namespaced operation.", Buckets: prometheus.DefBuckets}, []string{"operation"})
	kubernetesReconnects := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_kubernetes_reconnects_total", Help: "Kubernetes watch and log reconnects by fixed stream type."}, []string{"stream"})
	kubernetesDenials := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_kubernetes_denials_total", Help: "Kubernetes policy and API denials by stable rule."}, []string{"rule"})
	kubernetesCleanup := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_kubernetes_cleanup_failures_total", Help: "Kubernetes cleanup failures by fixed resource."}, []string{"resource"})
	kubernetesReconcile := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_kubernetes_reconciliation_total", Help: "Kubernetes reconciliation observations by fixed state."}, []string{"state"})
	kubernetesOrphans := prometheus.NewCounter(prometheus.CounterOpts{Name: "semaphore_kubernetes_orphans_total", Help: "Kubernetes orphan candidates observed."})
	kubernetesQuarantine := prometheus.NewCounter(prometheus.CounterOpts{Name: "semaphore_kubernetes_quarantines_total", Help: "Kubernetes reconciliation quarantines observed."})
	kubernetesDrops := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "semaphore_kubernetes_telemetry_dropped_events_total", Help: "Kubernetes telemetry events dropped by fixed runner queue reason."}, []string{"reason"})

	registry.MustRegister(
		tasksRunning,
		tasksTotal,
		enhancedActions,
		dependencyHealthy,
		dependencyFailure,
		dependencyLatency,
		queueDepth,
		droppedRecords,
		auditWebhookAge,
		auditWebhookTries,
		auditWebhookOK,
		auditWebhookDead,
		auditWebhookDrops,
		dockerCPUUsage, dockerMemoryBytes, dockerPIDs, dockerPolicyDenials,
		dockerPullDuration, dockerCleanupFailed, dockerReconciliation, dockerOrphans, dockerTelemetryDrops,
		kubernetesAPILatency, kubernetesReconnects, kubernetesDenials, kubernetesCleanup, kubernetesReconcile, kubernetesOrphans, kubernetesQuarantine, kubernetesDrops,
	)
	dependencyHealthy.WithLabelValues(string(pro_interfaces.DependencyAuditDatabase)).Set(1)
	dependencyHealthy.WithLabelValues(string(pro_interfaces.DependencyAuditFile)).Set(1)
	dependencyHealthy.WithLabelValues(string(pro_interfaces.DependencyAuditWebhook)).Set(1)
	queueDepth.WithLabelValues(string(pro_interfaces.QueueEnhancedAudit)).Set(0)
	queueDepth.WithLabelValues(string(pro_interfaces.QueueAuditWebhook)).Set(0)

	return &Metrics{
		registry:          registry,
		handler:           promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		tasksRunning:      tasksRunning,
		tasksTotal:        tasksTotal,
		enhancedActions:   enhancedActions,
		dependencyHealthy: dependencyHealthy,
		dependencyFailure: dependencyFailure,
		dependencyLatency: dependencyLatency,
		queueDepth:        queueDepth,
		droppedRecords:    droppedRecords,
		auditWebhookAge:   auditWebhookAge,
		auditWebhookTries: auditWebhookTries,
		auditWebhookOK:    auditWebhookOK,
		auditWebhookDead:  auditWebhookDead,
		auditWebhookDrops: auditWebhookDrops,
		dockerCPUUsage:    dockerCPUUsage, dockerMemoryBytes: dockerMemoryBytes, dockerPIDs: dockerPIDs,
		dockerPolicyDenials: dockerPolicyDenials, dockerPullDuration: dockerPullDuration,
		dockerCleanupFailed: dockerCleanupFailed, dockerReconciliation: dockerReconciliation, dockerOrphans: dockerOrphans,
		dockerTelemetryDrops: dockerTelemetryDrops,
		kubernetesAPILatency: kubernetesAPILatency, kubernetesReconnects: kubernetesReconnects, kubernetesDenials: kubernetesDenials,
		kubernetesCleanup: kubernetesCleanup, kubernetesReconcile: kubernetesReconcile, kubernetesOrphans: kubernetesOrphans,
		kubernetesQuarantine: kubernetesQuarantine, kubernetesDrops: kubernetesDrops,
	}
}

// RecordKubernetesTelemetry exports only db-validated, bounded labels.
func (m *Metrics) RecordKubernetesTelemetry(event db.KubernetesTelemetryEvent) {
	if m == nil || event.Validate() != nil {
		return
	}
	switch event.Kind {
	case db.KubernetesTelemetryAPILatency:
		m.kubernetesAPILatency.WithLabelValues(string(event.Operation)).Observe(float64(event.DurationMilliseconds) / 1000)
	case db.KubernetesTelemetryWatchReconnect:
		m.kubernetesReconnects.WithLabelValues("watch").Inc()
	case db.KubernetesTelemetryLogReconnect:
		m.kubernetesReconnects.WithLabelValues("logs").Inc()
	case db.KubernetesTelemetryDenial:
		m.kubernetesDenials.WithLabelValues(event.PolicyRule).Inc()
	case db.KubernetesTelemetryCleanupFailure:
		m.kubernetesCleanup.WithLabelValues(string(event.CleanupResource)).Inc()
	case db.KubernetesTelemetryReconciliation:
		m.kubernetesReconcile.WithLabelValues(string(event.ReconciliationState)).Add(float64(event.Count))
	case db.KubernetesTelemetryOrphan:
		m.kubernetesOrphans.Add(float64(event.Count))
	case db.KubernetesTelemetryQuarantine:
		m.kubernetesQuarantine.Add(float64(event.Count))
	case db.KubernetesTelemetryDrop:
		m.kubernetesDrops.WithLabelValues(string(event.DropReason)).Add(float64(event.Count))
	}
}

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

// RecordTaskStatusChange updates task metrics for a task transitioning from
// oldStatus to newStatus. It must only be called for transitions that a
// TaskRunner has already accepted (see TaskRunner.SetStatus), so that
// entering/leaving TaskRunningStatus is counted exactly once per task.
func (m *Metrics) RecordTaskStatusChange(oldStatus, newStatus task_logger.TaskStatus) {
	if m == nil {
		return
	}

	if oldStatus == task_logger.TaskRunningStatus {
		m.tasksRunning.Dec()
	}

	if newStatus == task_logger.TaskRunningStatus {
		m.tasksRunning.Inc()
	}

	if newStatus.IsFinished() && !oldStatus.IsFinished() {
		m.tasksTotal.WithLabelValues(string(newStatus)).Inc()
	}
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m == nil || m.handler == nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	m.handler.ServeHTTP(w, r)
}
