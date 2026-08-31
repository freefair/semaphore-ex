package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
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
