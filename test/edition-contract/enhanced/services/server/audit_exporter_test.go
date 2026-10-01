package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
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
}

func (r *auditExportRepositoryTest) InitializeAuditExportState(context.Context, string) (int64, error) {
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

	exporter := NewAuditExporter(store, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{
		ID: "siem-primary", Address: listener.Addr().String(), CAFile: certificatePath,
		ServerName: certificate.DNSNames[0], Timeout: time.Second.String(),
	}}, auditExportLeaserTest{})
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
	cursor, err := store.InitializeAuditExportState(context.Background(), "siem-primary")
	require.NoError(t, err)
	assert.Equal(t, later.Seq, cursor)
}

func TestAuditExporterRejectsPartialSyslogConfiguration(t *testing.T) {
	exporter := NewAuditExporter(nil, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{ID: "siem-primary"}}, nil)
	assert.Error(t, exporter.Start())
	assert.Empty(t, exporter.DestinationIDs())
}

func TestAuditExporterRejectsNilLeaserForConfiguredDestination(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	exporter := NewAuditExporter(store, &util.AuditConfig{Syslog: &util.AuditSyslogConfig{ID: "siem-primary", Address: "127.0.0.1:6514"}}, nil)
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

var _ pro_interfaces.AuditExporter = NewAuditExporter(nil, nil, nil)

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
