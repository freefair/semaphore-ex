package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditExportRepositoryTest struct {
	events                []db.AuditEvent
	getEntered            chan struct{}
	initialized           chan struct{}
	releaseInitialization chan struct{}
	initializeOnce        sync.Once
	advanceCalls          int
	initializedIDs        []string
	backlog               db.AuditExportBacklog
	backlogErr            error
	backlogCalls          map[string]int
	mu                    sync.Mutex
}

func (r *auditExportRepositoryTest) InitializeAuditExportState(_ context.Context, destinationID string) (int64, error) {
	r.mu.Lock()
	r.initializedIDs = append(r.initializedIDs, destinationID)
	r.mu.Unlock()
	if r.initialized != nil {
		r.initializeOnce.Do(func() {
			close(r.initialized)
			<-r.releaseInitialization
		})
	}
	return 0, nil
}

func (r *auditExportRepositoryTest) GetAuditEventsAfter(ctx context.Context, _ int64, _ int) ([]db.AuditEvent, error) {
	if r.getEntered != nil {
		close(r.getEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.events, nil
}

func (r *auditExportRepositoryTest) GetAuditExportBacklog(_ context.Context, destinationID string) (db.AuditExportBacklog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backlogCalls == nil {
		r.backlogCalls = make(map[string]int)
	}
	r.backlogCalls[destinationID]++
	return r.backlog, r.backlogErr
}

func (r *auditExportRepositoryTest) AdvanceAuditExportState(context.Context, string, int64, int64) (bool, error) {
	r.advanceCalls++
	return true, nil
}

type controllableAuditExportLease struct {
	lost  chan struct{}
	valid bool
}

func (l *controllableAuditExportLease) Lost() <-chan struct{} { return l.lost }
func (l *controllableAuditExportLease) Valid() bool           { return l.valid }
func (*controllableAuditExportLease) Release()                {}

type controllableAuditExportLeaser struct{ lease *controllableAuditExportLease }

func (l controllableAuditExportLeaser) TryAcquire(context.Context, string) (pro_interfaces.AuditExportLease, bool, error) {
	return l.lease, true, nil
}

type unavailableAuditExportLeaser struct{}

func (unavailableAuditExportLeaser) TryAcquire(context.Context, string) (pro_interfaces.AuditExportLease, bool, error) {
	return nil, false, nil
}

func scrapeAuditExportMetrics(m *metrics.Metrics) string {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	return w.Body.String()
}

func TestAuditSyslogDestinationDeliversVerifiedTLSRFC5424JSON(t *testing.T) {
	listener, certificate := auditSyslogTLSListener(t)
	messages := acceptAuditSyslogMessage(t, listener)
	certificatePath := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath,
		ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	})
	require.NoError(t, err)

	err = destination.Deliver(context.Background(), db.AuditEvent{
		Seq: 9, EventID: "event-9", Created: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		EventCode: "audit.lifecycle", InstanceID: "prod-eu", Metadata: "{}",
	})
	require.NoError(t, err)
	message := <-messages
	assert.Contains(t, message, "<14>1 2026-10-01T12:00:00Z prod-eu semaphore - audit.lifecycle - {\"event_id\":\"event-9\"")
	expected, err := audit.EnvelopeFromRow(db.AuditEvent{Seq: 9, EventID: "event-9", Created: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), EventCode: "audit.lifecycle", InstanceID: "prod-eu", Metadata: "{}"})
	require.NoError(t, err)
	expectedPayload, err := json.Marshal(expected)
	require.NoError(t, err)
	assert.Contains(t, message, string(expectedPayload))
}

func TestAuditSyslogDestinationUsesRFC5425OctetCountingAndRFC5424Headers(t *testing.T) {
	listener, certificate := auditSyslogTLSListener(t)
	messages := acceptAuditSyslogMessage(t, listener)
	certificatePath := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath,
		ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	})
	require.NoError(t, err)

	err = destination.Deliver(context.Background(), db.AuditEvent{
		Seq: 1, EventID: "event-1", Created: time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC),
		EventCode: strings.Repeat("x", 40) + "\u00e9", InstanceID: "prod", NodeID: "host\u00e9\tname", Metadata: "{}",
	})
	require.NoError(t, err)
	message := <-messages

	length, syslogMessage, found := strings.Cut(message, " ")
	require.True(t, found)
	size, err := strconv.Atoi(length)
	require.NoError(t, err)
	assert.Equal(t, size, len(syslogMessage))
	assert.Contains(t, syslogMessage, "<14>1 2026-10-01T12:00:00.123456Z hostname semaphore - "+strings.Repeat("x", 32)+" - ")
}

func TestAuditExporterUsesOneTLSConnectionForAnOrderedBatch(t *testing.T) {
	listener, certificate := auditSyslogTLSListener(t)
	connections := make(chan struct{}, 2)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connections <- struct{}{}
			go func() {
				defer connection.Close()
				_, _ = io.ReadAll(connection)
			}()
		}
	}()
	certificatePath := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath,
		ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	})
	require.NoError(t, err)
	lease := &controllableAuditExportLease{lost: make(chan struct{}), valid: true}
	repository := &auditExportRepositoryTest{events: []db.AuditEvent{
		{Seq: 1, EventID: "event-1", Created: time.Now().UTC(), EventCode: "audit.one", Metadata: "{}"},
		{Seq: 2, EventID: "event-2", Created: time.Now().UTC(), EventCode: "audit.two", Metadata: "{}"},
	}}
	exporter := &auditExporter{repository: repository, destination: destination, leaser: controllableAuditExportLeaser{lease: lease}}

	exporter.process(context.Background())
	assert.Equal(t, 2, repository.advanceCalls)
	select {
	case <-connections:
	case <-time.After(time.Second):
		require.FailNow(t, "exporter did not connect")
	}
	select {
	case <-connections:
		require.FailNow(t, "exporter opened more than one connection for one batch")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAuditExportMetricsTracksSyslogFailureWithoutCredentialLabels(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{ID: "syslog-primary", Address: address})
	require.NoError(t, err)
	appMetrics := metrics.NewMetrics()
	exporterMetrics, err := newAuditExportMetrics(appMetrics)
	require.NoError(t, err)
	repository := &auditExportRepositoryTest{
		events:  []db.AuditEvent{{Seq: 1, EventID: "event-1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", Metadata: "{}"}},
		backlog: db.AuditExportBacklog{PendingEvents: 3, OldestPendingAge: 42 * time.Second},
	}
	exporter := &auditExporter{repository: repository, destination: destination, leaser: auditExportLeaserTest{}, metrics: exporterMetrics}

	exporter.process(context.Background())

	output := scrapeAuditExportMetrics(appMetrics)
	assert.Contains(t, output, `semaphore_audit_export_pending_events{destination="syslog-primary"} 3`)
	assert.Contains(t, output, `semaphore_audit_export_oldest_pending_seconds{destination="syslog-primary"} 42`)
	assert.Contains(t, output, `semaphore_audit_export_errors_total{destination="syslog-primary"} 1`)
	assert.NotContains(t, output, address)
	assert.NotContains(t, output, "token")
}

func TestAuditExportMetricsCountsHECFailureButNotLeaseContention(t *testing.T) {
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer receiver.Close()
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{
		ID: "hec-primary", URL: receiver.URL, Token: "hec-secret-must-not-appear", CAFile: writeAuditCertificate(t, receiver.Certificate()),
	})
	require.NoError(t, err)
	appMetrics := metrics.NewMetrics()
	exporterMetrics, err := newAuditExportMetrics(appMetrics)
	require.NoError(t, err)
	repository := &auditExportRepositoryTest{
		events:  []db.AuditEvent{{Seq: 1, EventID: "event-1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", Metadata: "{}"}},
		backlog: db.AuditExportBacklog{PendingEvents: 1, OldestPendingAge: time.Second},
	}
	exporter := &auditHECExporter{repository: repository, destination: destination, leaser: auditExportLeaserTest{}, metrics: exporterMetrics}

	exporter.process(context.Background())

	output := scrapeAuditExportMetrics(appMetrics)
	assert.Contains(t, output, `semaphore_audit_export_errors_total{destination="hec-primary"} 1`)
	assert.NotContains(t, output, "hec-secret-must-not-appear")
	assert.NotContains(t, output, receiver.URL)

	contentionMetrics := metrics.NewMetrics()
	contentionExporterMetrics, err := newAuditExportMetrics(contentionMetrics)
	require.NoError(t, err)
	contention := &auditHECExporter{repository: repository, destination: destination, leaser: unavailableAuditExportLeaser{}, metrics: contentionExporterMetrics}
	contention.process(context.Background())
	assert.NotContains(t, scrapeAuditExportMetrics(contentionMetrics), "semaphore_audit_export_errors_total")

	shutdownMetrics := metrics.NewMetrics()
	shutdownExporterMetrics, err := newAuditExportMetrics(shutdownMetrics)
	require.NoError(t, err)
	shutdown := &auditHECExporter{repository: repository, destination: destination, leaser: auditExportLeaserTest{}, metrics: shutdownExporterMetrics}
	shutdownContext, cancel := context.WithCancel(context.Background())
	cancel()
	shutdown.process(shutdownContext)
	assert.NotContains(t, scrapeAuditExportMetrics(shutdownMetrics), "semaphore_audit_export_errors_total")
}

func TestAuditExportMetricsThrottlesBacklogQueriesPerDestination(t *testing.T) {
	appMetrics := metrics.NewMetrics()
	exporterMetrics, err := newAuditExportMetrics(appMetrics)
	require.NoError(t, err)
	now := time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)
	exporterMetrics.now = func() time.Time { return now }
	repository := &auditExportRepositoryTest{backlog: db.AuditExportBacklog{PendingEvents: 200, OldestPendingAge: time.Minute}}

	for range 3 { // Three full delivery batches must still issue one backlog query.
		exporterMetrics.updateBacklog(context.Background(), repository, "syslog-primary", time.Second)
	}
	exporterMetrics.updateBacklog(context.Background(), repository, "hec-primary", time.Second)
	repository.mu.Lock()
	assert.Equal(t, 1, repository.backlogCalls["syslog-primary"])
	assert.Equal(t, 1, repository.backlogCalls["hec-primary"])
	repository.mu.Unlock()

	now = now.Add(auditExportMetricsRefreshInterval)
	repository.backlog = db.AuditExportBacklog{}
	exporterMetrics.updateBacklog(context.Background(), repository, "syslog-primary", time.Second)
	repository.mu.Lock()
	assert.Equal(t, 2, repository.backlogCalls["syslog-primary"])
	repository.mu.Unlock()
	assert.Contains(t, scrapeAuditExportMetrics(appMetrics), `semaphore_audit_export_pending_events{destination="syslog-primary"} 0`)
}

func TestAuditExportMetricsThrottlesFailedBacklogQueries(t *testing.T) {
	appMetrics := metrics.NewMetrics()
	exporterMetrics, err := newAuditExportMetrics(appMetrics)
	require.NoError(t, err)
	now := time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)
	exporterMetrics.now = func() time.Time { return now }
	repository := &auditExportRepositoryTest{backlogErr: errors.New("database unavailable")}

	exporterMetrics.updateBacklog(context.Background(), repository, "syslog-primary", time.Second)
	exporterMetrics.updateBacklog(context.Background(), repository, "syslog-primary", time.Second)
	repository.mu.Lock()
	assert.Equal(t, 1, repository.backlogCalls["syslog-primary"])
	repository.mu.Unlock()

	now = now.Add(auditExportMetricsRefreshInterval)
	exporterMetrics.updateBacklog(context.Background(), repository, "syslog-primary", time.Second)
	repository.mu.Lock()
	assert.Equal(t, 2, repository.backlogCalls["syslog-primary"])
	repository.mu.Unlock()
}

func TestAuditSyslogDestinationRejectsUntrustedCertificate(t *testing.T) {
	listener, certificate := auditSyslogTLSListener(t)
	_ = acceptAuditSyslogMessage(t, listener)
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	})
	require.NoError(t, err)
	err = destination.Deliver(context.Background(), db.AuditEvent{Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod-eu", Metadata: "{}"})
	assert.Error(t, err)
}

func TestAuditExporterSkipsHistoricalEventsAndAdvancesDurableCursor(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()

	historical, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "historical", SchemaVersion: "1", Created: time.Now().UTC(), Metadata: "{}"})
	require.NoError(t, err)

	listener, certificate := auditSyslogTLSListener(t)
	delivered := acceptAuditSyslogMessage(t, listener)
	certificatePath := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))

	appMetrics := metrics.NewMetrics()
	exporter := NewAuditExporter(store, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath,
		ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	}}, auditExportLeaserTest{}, appMetrics)
	require.NoError(t, exporter.Start())
	defer exporter.Stop()
	assert.Equal(t, []string{"siem-primary"}, exporter.DestinationIDs())

	later, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "later", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod-eu", Metadata: "{}"})
	require.NoError(t, err)
	var message string
	select {
	case message = <-delivered:
	case <-time.After(3 * time.Second):
	}
	require.NotEmpty(t, message, "timed out waiting for audit export")
	assert.Contains(t, message, "\"event_id\":\"later\"")
	assert.NotContains(t, message, historical.EventID)
	assert.Contains(t, scrapeAuditExportMetrics(appMetrics), `semaphore_audit_export_pending_events{destination="siem-primary"}`)
	cursor, err := store.InitializeAuditExportState(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.Equal(t, later.Seq, cursor)
}

func TestAuditExporterRejectsPartialSyslogConfiguration(t *testing.T) {
	exporter := NewAuditExporter(nil, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{ID: "siem-primary"}}, nil, nil)
	assert.Error(t, exporter.Start())
	assert.Empty(t, exporter.DestinationIDs())
}

func TestAuditExporterRejectsNilLeaserForConfiguredDestination(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	exporter := NewAuditExporter(store, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{ID: "siem-primary", Address: "127.0.0.1:6514"}}, nil, nil)
	assert.ErrorContains(t, exporter.Start(), "leaser")
}

func TestAuditExporterDoesNotAdvanceCursorAfterLeaseLoss(t *testing.T) {
	lease := &controllableAuditExportLease{lost: make(chan struct{}), valid: true}
	repository := &auditExportRepositoryTest{events: []db.AuditEvent{{Seq: 1, EventID: "event", Metadata: "{}"}}}
	exporter := &auditExporter{
		repository:  repository,
		destination: &auditSyslogDestination{id: "siem-primary", timeout: time.Second},
		leaser:      controllableAuditExportLeaser{lease: lease},
	}
	close(lease.lost)
	lease.valid = false
	exporter.process(context.Background())
	assert.Zero(t, repository.advanceCalls)
}

func TestAuditExporterStopCancelsBlockedRepositoryOperation(t *testing.T) {
	lease := &controllableAuditExportLease{lost: make(chan struct{}), valid: true}
	repository := &auditExportRepositoryTest{getEntered: make(chan struct{})}
	exporter := &auditExporter{
		repository:  repository,
		destination: &auditSyslogDestination{id: "siem-primary", timeout: time.Second},
		leaser:      controllableAuditExportLeaser{lease: lease},
	}
	loopContext, cancel := context.WithCancel(context.Background())
	exporter.cancel = cancel
	exporter.wait.Add(1)
	go exporter.run(loopContext)
	select {
	case <-repository.getEntered:
	case <-time.After(time.Second):
		require.FailNow(t, "exporter did not enter the blocking repository operation")
	}
	started := time.Now()
	exporter.Stop()
	assert.Less(t, time.Since(started), time.Second)
}

func TestAuditExporterStopDuringStartDoesNotLeaveTheRunLoop(t *testing.T) {
	initialized := make(chan struct{})
	releaseInitialization := make(chan struct{})
	lease := &controllableAuditExportLease{lost: make(chan struct{}), valid: true}
	repository := &auditExportRepositoryTest{initialized: initialized, releaseInitialization: releaseInitialization}
	exporter := &auditExporter{
		repository:  repository,
		destination: &auditSyslogDestination{id: "siem-primary", timeout: time.Second},
		leaser:      controllableAuditExportLeaser{lease: lease},
	}
	started := make(chan error, 1)
	go func() { started <- exporter.Start() }()
	select {
	case <-initialized:
	case <-time.After(time.Second):
		require.FailNow(t, "exporter did not initialize")
	}
	stopped := make(chan struct{})
	go func() {
		exporter.Stop()
		close(stopped)
	}()
	close(releaseInitialization)
	_ = <-started
	select {
	case <-stopped:
	case <-time.After(time.Second):
		require.FailNow(t, "Stop did not finish")
	}
	exporter.lifecycle.Lock()
	assert.True(t, exporter.stopped)
	exporter.lifecycle.Unlock()
}

func TestAuditSyslogDestinationCancelsInFlightWrite(t *testing.T) {
	listener, certificate := auditSyslogTLSListener(t)
	handshaken := make(chan struct{})
	release := make(chan struct{})
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		if tlsConnection, ok := connection.(*tls.Conn); ok && tlsConnection.Handshake() == nil {
			close(handshaken)
			<-release
		}
	}()
	certificatePath := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))
	destination, err := newAuditSyslogDestination(&util.AuditSyslogConfig{ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath, ServerName: certificate.DNSNames[0], Timeout: time.Second.String()})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- destination.Deliver(ctx, db.AuditEvent{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: `{"payload":"` + strings.Repeat("x", 8<<20) + `"}`})
	}()
	select {
	case <-handshaken:
	case <-time.After(time.Second):
		require.FailNow(t, "TLS handshake did not complete")
	}
	cancel()
	defer close(release)
	select {
	case err = <-result:
		assert.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "canceled TLS write did not stop promptly")
	}
}

func TestAuditHECDestinationDeliversTLSBatchWithCanonicalEnvelope(t *testing.T) {
	var authorization string
	var received []hecEvent
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		decoder := json.NewDecoder(r.Body)
		for decoder.More() {
			var event hecEvent
			require.NoError(t, decoder.Decode(&event))
			received = append(received, event)
		}
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()
	certificatePath := writeAuditCertificate(t, server.Certificate())
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec-primary", URL: server.URL, Token: "hec-secret", CAFile: certificatePath, Timeout: time.Second.String()})
	require.NoError(t, err)
	created := time.Date(2026, 10, 7, 12, 0, 0, 123000000, time.UTC)
	require.NoError(t, destination.deliver(context.Background(), []db.AuditEvent{{Seq: 1, EventID: "event-1", Created: created, EventCode: "audit.lifecycle", InstanceID: "prod-eu", NodeID: "node-a", Metadata: "{}"}}))
	require.Len(t, received, 1)
	assert.Equal(t, "Splunk hec-secret", authorization)
	assert.Equal(t, float64(created.UnixNano())/float64(time.Second), received[0].Time)
	assert.Equal(t, "node-a", received[0].Host)
	assert.Equal(t, "semaphore", received[0].Source)
	assert.Equal(t, "semaphore:audit", received[0].Sourcetype)
	assert.Equal(t, "event-1", received[0].Event.EventID)
}

func TestAuditHECDestinationRejectsRedirectAndNonzeroAcknowledgement(t *testing.T) {
	redirectTargetCalled := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargetCalled = true }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec-primary", URL: redirect.URL, Token: "hec-secret", CAFile: writeAuditCertificate(t, redirect.Certificate()), Timeout: time.Second.String()})
	require.NoError(t, err)
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.Error(t, err)
	assert.False(t, redirectTargetCalled)

	acknowledgement := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"code":10}`)) }))
	defer acknowledgement.Close()
	destination, err = newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec-primary", URL: acknowledgement.URL, Token: "hec-secret", CAFile: writeAuditCertificate(t, acknowledgement.Certificate()), Timeout: time.Second.String()})
	require.NoError(t, err)
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.ErrorContains(t, err, "response code 10")

	malformed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`not-json`)) }))
	defer malformed.Close()
	destination, err = newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec-primary", URL: malformed.URL, Token: "hec-secret", CAFile: writeAuditCertificate(t, malformed.Certificate()), Timeout: time.Second.String()})
	require.NoError(t, err)
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.ErrorContains(t, err, "invalid audit Splunk HEC response")
}

func TestAuditHECDestinationRequiresBoundedCodeZeroAcknowledgement(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "invalid audit Splunk HEC response"},
		{"missing code", `{}`, "invalid audit Splunk HEC response"},
		{"nonzero code", `{"code":7}`, "response code 7"},
		{"malformed", `not-json`, "invalid audit Splunk HEC response"},
		{"oversized valid prefix", `{"code":0}` + strings.Repeat(" ", 4097), "response is too large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec", URL: server.URL, Token: "token", CAFile: writeAuditCertificate(t, server.Certificate())})
			require.NoError(t, err)
			err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
			assert.ErrorContains(t, err, test.want)
		})
	}
}

func TestAuditHECDestinationRejectsResponseReadFailure(t *testing.T) {
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token"})
	require.NoError(t, err)
	destination.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{}}, nil
	})
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.ErrorContains(t, err, "read audit Splunk HEC response")
}

func TestAuditHECExporterRetriesAcknowledgementFailureWithoutAdvancingCursor(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	historical, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "historical", SchemaVersion: "1", Created: time.Now().UTC(), Metadata: "{}"})
	require.NoError(t, err)
	var success atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if success.Load() {
			_, _ = w.Write([]byte(`{"code":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":10}`))
	}))
	defer server.Close()
	exporter := newAuditHECExporter(store, &util.AuditSplunkHECConfig{ID: "hec", URL: server.URL, Token: "token", CAFile: writeAuditCertificate(t, server.Certificate())}, auditExportLeaserTest{}, nil)
	_, err = store.InitializeAuditExportState(context.Background(), "hec")
	require.NoError(t, err)
	pending, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "pending", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"})
	require.NoError(t, err)
	exporter.process(context.Background())
	cursor, err := store.InitializeAuditExportState(context.Background(), "hec")
	require.NoError(t, err)
	assert.Equal(t, historical.Seq, cursor)
	success.Store(true)
	exporter.process(context.Background())
	cursor, err = store.InitializeAuditExportState(context.Background(), "hec")
	require.NoError(t, err)
	assert.Equal(t, pending.Seq, cursor)
}

func TestAuditHECDestinationValidation(t *testing.T) {
	tests := []struct {
		name   string
		config *util.AuditSplunkHECConfig
	}{
		{"missing token", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event"}},
		{"token tab", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token\tvalue"}},
		{"token NUL", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token\x00value"}},
		{"token DEL", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token\x7fvalue"}},
		{"token leading whitespace", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: " token"}},
		{"token trailing whitespace", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token "}},
		{"non HTTPS URL", &util.AuditSplunkHECConfig{ID: "hec", URL: "http://hec.example/event", Token: "token"}},
		{"URL credentials", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://user:password@hec.example/event", Token: "token"}},
		{"URL fragment", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event#fragment", Token: "token"}},
		{"invalid timeout", &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/event", Token: "token", Timeout: "0s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { _, err := newAuditHECDestination(test.config); assert.Error(t, err) })
	}
}

func TestAuditHECDestinationRejectsUntrustedCertificateAndTimesOut(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer untrusted.Close()
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec", URL: untrusted.URL, Token: "token", Timeout: time.Second.String()})
	require.NoError(t, err)
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.ErrorContains(t, err, "certificate verification failed")

	blocked := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer blocked.Close()
	destination, err = newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec", URL: blocked.URL, Token: "token", CAFile: writeAuditCertificate(t, blocked.Certificate()), Timeout: "25ms"})
	require.NoError(t, err)
	started := time.Now()
	err = destination.deliver(context.Background(), []db.AuditEvent{{EventID: "event", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}})
	assert.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(started), time.Second)
}

func TestAuditHECTransportErrorIsActionableWithoutSecretOrURL(t *testing.T) {
	err := sanitizeHECTransportError(&url.Error{Op: "Post", URL: "https://hec.example/event?token=must-not-leak", Err: context.DeadlineExceeded})
	assert.ErrorContains(t, err, "timed out")
	assert.NotContains(t, err.Error(), "must-not-leak")
	assert.NotContains(t, err.Error(), "hec.example")
}

func TestAuditCompositeExporterRejectsDuplicateDestinationIDs(t *testing.T) {
	exporter := NewAuditExporter(nil, &util.AuditConfig{
		Syslog:    &util.AuditSyslogConfig{ID: "duplicate", Address: "127.0.0.1:6514"},
		SplunkHEC: &util.AuditSplunkHECConfig{ID: "duplicate", URL: "https://hec.example/event", Token: "token"},
	}, auditExportLeaserTest{}, nil)
	assert.ErrorContains(t, exporter.Start(), "configured more than once")
}

func TestAuditHECExporterPersistsCursorAcrossRestartAndDestinations(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	_, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "historical", SchemaVersion: "1", Created: time.Now().UTC(), Metadata: "{}"})
	require.NoError(t, err)
	delivered := make(chan hecEvent, 2)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		for decoder.More() {
			var event hecEvent
			require.NoError(t, decoder.Decode(&event))
			delivered <- event
		}
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer receiver.Close()
	config := func(id string) *util.AuditConfig {
		return &util.AuditConfig{SplunkHEC: &util.AuditSplunkHECConfig{ID: id, URL: receiver.URL, Token: "token", CAFile: writeAuditCertificate(t, receiver.Certificate()), Timeout: time.Second.String()}}
	}
	first := NewAuditExporter(store, config("hec-a"), auditExportLeaserTest{}, nil)
	require.NoError(t, first.Start())
	defer first.Stop()
	later, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "later", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"})
	require.NoError(t, err)
	select {
	case event := <-delivered:
		assert.Equal(t, "later", event.Event.EventID)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "timed out waiting for HEC delivery")
	}
	require.Eventually(t, func() bool {
		cursor, cursorErr := store.InitializeAuditExportState(context.Background(), "hec-a")
		return cursorErr == nil && cursor == later.Seq
	}, 3*time.Second, 10*time.Millisecond)
	first.Stop()
	cursor, err := store.InitializeAuditExportState(context.Background(), "hec-a")
	require.NoError(t, err)
	assert.Equal(t, later.Seq, cursor)
	restarted := NewAuditExporter(store, config("hec-a"), auditExportLeaserTest{}, nil)
	require.NoError(t, restarted.Start())
	defer restarted.Stop()
	third, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "third", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"})
	require.NoError(t, err)
	select {
	case event := <-delivered:
		assert.Equal(t, "third", event.Event.EventID, "restart must resume after its durable cursor")
	case <-time.After(3 * time.Second):
		require.FailNow(t, "timed out waiting for restarted HEC delivery")
	}
	require.Eventually(t, func() bool {
		cursor, cursorErr := store.InitializeAuditExportState(context.Background(), "hec-a")
		return cursorErr == nil && cursor == third.Seq
	}, 3*time.Second, 10*time.Millisecond)
	restarted.Stop()
	other := NewAuditExporter(store, config("hec-b"), auditExportLeaserTest{}, nil)
	require.NoError(t, other.Start())
	defer other.Stop()
	fourth, err := store.CreateAuditEvent(context.Background(), db.AuditEvent{EventID: "fourth", SchemaVersion: "1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"})
	require.NoError(t, err)
	select {
	case event := <-delivered:
		assert.Equal(t, "fourth", event.Event.EventID)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "timed out waiting for independent HEC delivery")
	}
	require.Eventually(t, func() bool {
		cursor, cursorErr := store.InitializeAuditExportState(context.Background(), "hec-b")
		return cursorErr == nil && cursor == fourth.Seq
	}, 3*time.Second, 10*time.Millisecond)
	other.Stop()
	otherCursor, err := store.InitializeAuditExportState(context.Background(), "hec-b")
	require.NoError(t, err)
	assert.Equal(t, fourth.Seq, otherCursor)
}

func TestAuditHECExporterDoesNotAdvanceCursorAfterLeaseLoss(t *testing.T) {
	requestReceived := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestReceived)
		<-releaseResponse
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()
	destination, err := newAuditHECDestination(&util.AuditSplunkHECConfig{ID: "hec-primary", URL: server.URL, Token: "hec-secret", CAFile: writeAuditCertificate(t, server.Certificate()), Timeout: time.Second.String()})
	require.NoError(t, err)
	lease := &controllableAuditExportLease{lost: make(chan struct{}), valid: true}
	repository := &auditExportRepositoryTest{events: []db.AuditEvent{{Seq: 1, EventID: "event-1", Created: time.Now().UTC(), EventCode: "audit.lifecycle", InstanceID: "prod", Metadata: "{}"}}}
	appMetrics := metrics.NewMetrics()
	exporterMetrics, metricsErr := newAuditExportMetrics(appMetrics)
	require.NoError(t, metricsErr)
	exporter := &auditHECExporter{repository: repository, destination: destination, leaser: controllableAuditExportLeaser{lease: lease}, metrics: exporterMetrics}
	done := make(chan struct{})
	go func() { exporter.process(context.Background()); close(done) }()
	<-requestReceived
	close(lease.lost)
	close(releaseResponse)
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, "HEC export did not stop after lease loss")
	}
	assert.Zero(t, repository.advanceCalls)
	assert.NotContains(t, scrapeAuditExportMetrics(appMetrics), "semaphore_audit_export_errors_total")
}

func TestAuditExporterCombinesDistinctSyslogAndHECDestinations(t *testing.T) {
	exporter := NewAuditExporter(nil, &util.AuditConfig{
		Syslog:    &util.AuditSyslogConfig{ID: "syslog", Address: "127.0.0.1:6514"},
		SplunkHEC: &util.AuditSplunkHECConfig{ID: "hec", URL: "https://hec.example/services/collector/event", Token: "token"},
	}, nil, nil)
	assert.Equal(t, []string{"syslog", "hec"}, exporter.DestinationIDs())
}

func writeAuditCertificate(t *testing.T, certificate *x509.Certificate) string {
	t.Helper()
	path := t.TempDir() + "/receiver-ca.pem"
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600))
	return path
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

var _ pro_interfaces.AuditExporter = NewAuditExporter(nil, nil, nil, nil)

type auditExportLeaserTest struct{}

func (auditExportLeaserTest) TryAcquire(context.Context, string) (pro_interfaces.AuditExportLease, bool, error) {
	return &auditExportLeaseTest{lost: make(chan struct{})}, true, nil
}

type auditExportLeaseTest struct {
	lost chan struct{}
	once sync.Once
}

func (l *auditExportLeaseTest) Lost() <-chan struct{} { return l.lost }
func (*auditExportLeaseTest) Valid() bool             { return true }
func (l *auditExportLeaseTest) Release()              { l.once.Do(func() { close(l.lost) }) }

func auditSyslogTLSListener(t *testing.T) (net.Listener, *x509.Certificate) {
	t.Helper()
	fixture := httptest.NewUnstartedServer(nil)
	fixture.StartTLS()
	t.Cleanup(fixture.Close)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", fixture.TLS.Clone())
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener, fixture.Certificate()
}

func acceptAuditSyslogMessage(t *testing.T, listener net.Listener) <-chan string {
	t.Helper()
	messages := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		payload, err := io.ReadAll(connection)
		if err == nil {
			messages <- string(payload)
		}
	}()
	return messages
}
