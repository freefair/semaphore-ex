package server

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	log "github.com/sirupsen/logrus"
)

const (
	auditExportMetricsRefreshInterval = 15 * time.Second
	maxAuditExportMetricDestinations  = 2
)

type auditExportMetrics struct {
	oldestPending *prometheus.GaugeVec
	pendingEvents *prometheus.GaugeVec
	errors        *prometheus.CounterVec
	mu            sync.Mutex
	lastUpdated   map[string]time.Time
	now           func() time.Time
}

func newAuditExportMetrics(appMetrics *metrics.Metrics) (*auditExportMetrics, error) {
	m := &auditExportMetrics{
		oldestPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "semaphore_audit_export_oldest_pending_seconds",
			Help: "Age of the oldest audit event pending export.",
		}, []string{"destination"}),
		pendingEvents: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "semaphore_audit_export_pending_events",
			Help: "Number of audit events pending export.",
		}, []string{"destination"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "semaphore_audit_export_errors_total",
			Help: "Failed audit export send attempts.",
		}, []string{"destination"}),
		lastUpdated: make(map[string]time.Time, 2),
		now:         time.Now,
	}
	if err := appMetrics.Register(m.oldestPending, m.pendingEvents, m.errors); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *auditExportMetrics) updateBacklog(ctx context.Context, repository db.AuditExportBacklogRepository, destinationID string, timeout time.Duration) {
	if m == nil || repository == nil {
		return
	}
	if !m.shouldRefresh(destinationID) {
		return
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	backlog, err := repository.GetAuditExportBacklog(operation, destinationID)
	if err != nil {
		log.WithError(err).WithField("destination_id", destinationID).Warn("failed to read audit export backlog metrics")
		return
	}
	m.pendingEvents.WithLabelValues(destinationID).Set(float64(backlog.PendingEvents))
	m.oldestPending.WithLabelValues(destinationID).Set(backlog.OldestPendingAge.Seconds())
}

func (m *auditExportMetrics) shouldRefresh(destinationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if updated, ok := m.lastUpdated[destinationID]; ok && now.Sub(updated) < auditExportMetricsRefreshInterval {
		return false
	}
	if _, ok := m.lastUpdated[destinationID]; !ok && len(m.lastUpdated) == maxAuditExportMetricDestinations {
		return false
	}
	m.lastUpdated[destinationID] = now
	return true
}

func (m *auditExportMetrics) recordError(destinationID string) {
	if m != nil {
		m.errors.WithLabelValues(destinationID).Inc()
	}
}

func (m *auditExportMetrics) recordDeliveryError(ctx context.Context, lease interface{ Valid() bool }, destinationID string) {
	if ctx.Err() == nil && lease != nil && lease.Valid() {
		m.recordError(destinationID)
	}
}
