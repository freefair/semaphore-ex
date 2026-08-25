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

	enhancedActions   *prometheus.CounterVec
	dependencyHealthy *prometheus.GaugeVec
	dependencyFailure *prometheus.CounterVec
	dependencyLatency *prometheus.HistogramVec
	queueDepth        *prometheus.GaugeVec
	droppedRecords    *prometheus.CounterVec
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

	registry.MustRegister(
		tasksRunning,
		tasksTotal,
		enhancedActions,
		dependencyHealthy,
		dependencyFailure,
		dependencyLatency,
		queueDepth,
		droppedRecords,
	)
	dependencyHealthy.WithLabelValues(string(pro_interfaces.DependencyAuditDatabase)).Set(1)
	dependencyHealthy.WithLabelValues(string(pro_interfaces.DependencyAuditFile)).Set(1)
	queueDepth.WithLabelValues(string(pro_interfaces.QueueEnhancedAudit)).Set(0)

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
