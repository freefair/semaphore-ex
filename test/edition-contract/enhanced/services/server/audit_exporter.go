package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

const (
	auditExportPollInterval = time.Second
	auditExportBatchSize    = 100
	auditExportTimeout      = 10 * time.Second
)

type auditExporter struct {
	repository  db.AuditExportRepository
	destination *auditSyslogDestination
	leaser      pro_interfaces.AuditExportLeaser

	lifecycle   sync.Mutex
	started     bool
	stopped     bool
	startDone   chan struct{}
	startCancel context.CancelFunc
	cancel      context.CancelFunc
	wait        sync.WaitGroup
	startErr    error
}

type auditSyslogDestination struct {
	id      string
	address string
	timeout time.Duration
	tls     *tls.Config
}

type auditSyslogConnection struct {
	connection        *tls.Conn
	stopCloseOnCancel func() bool
}

var _ pro_interfaces.AuditExporter = (*auditExporter)(nil)

// NewAuditExporter provides the selected durable SIEM exporters. The audit
// webhook service remains a separate delivery channel with its own queue.
func NewAuditExporter(store db.Store, config *util.AuditConfig, leaser pro_interfaces.AuditExportLeaser) pro_interfaces.AuditExporter {
	exporter := &auditExporter{leaser: leaser}
	if repository, ok := store.(db.AuditExportRepository); ok {
		exporter.repository = repository
	}
	if config != nil && config.Syslog != nil && config.Syslog.IsConfigured() {
		exporter.destination, exporter.startErr = newAuditSyslogDestination(config.Syslog)
	}
	if config != nil && config.Syslog != nil && config.Syslog.IsConfigured() && exporter.repository == nil && exporter.startErr == nil {
		exporter.startErr = errors.New("audit syslog export requires an audit export repository")
	}
	if exporter.destination != nil && exporter.leaser == nil && exporter.startErr == nil {
		exporter.startErr = errors.New("audit syslog export requires an audit export leaser")
	}
	if config == nil || config.SplunkHEC == nil || !config.SplunkHEC.IsConfigured() {
		return exporter
	}
	hecExporter := newAuditHECExporter(store, config.SplunkHEC, leaser)
	if config.Syslog == nil || !config.Syslog.IsConfigured() {
		return hecExporter
	}
	return &auditCompositeExporter{exporters: []pro_interfaces.AuditExporter{exporter, hecExporter}}
}

func (e *auditExporter) Start() error {
	e.lifecycle.Lock()
	if e.started {
		startDone := e.startDone
		e.lifecycle.Unlock()
		if startDone != nil {
			<-startDone
		}
		e.lifecycle.Lock()
		defer e.lifecycle.Unlock()
		return e.startErr
	}
	e.started = true
	e.startDone = make(chan struct{})
	if e.stopped {
		e.startErr = errors.New("audit syslog exporter is stopped")
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	if e.startErr != nil || e.destination == nil {
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.destination.timeout)
	defer cancel()
	e.startCancel = cancel
	e.lifecycle.Unlock()
	if _, err := e.repository.InitializeAuditExportState(ctx, e.destination.id); err != nil {
		e.lifecycle.Lock()
		e.startCancel = nil
		e.startErr = fmt.Errorf("initialize audit export destination %q: %w", e.destination.id, err)
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	e.lifecycle.Lock()
	e.startCancel = nil
	if e.stopped {
		e.startErr = errors.New("audit syslog exporter is stopped")
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	loopContext, loopCancel := context.WithCancel(context.Background())
	e.cancel = loopCancel
	e.wait.Add(1)
	go e.run(loopContext)
	close(e.startDone)
	e.lifecycle.Unlock()
	return e.startErr
}

func (e *auditExporter) Stop() {
	e.lifecycle.Lock()
	if e.stopped {
		e.lifecycle.Unlock()
		return
	}
	e.stopped = true
	if e.startCancel != nil {
		e.startCancel()
	}
	if e.cancel != nil {
		e.cancel()
	}
	startDone := e.startDone
	e.lifecycle.Unlock()
	if startDone != nil {
		<-startDone
	}
	e.wait.Wait()
}

func (e *auditExporter) DestinationIDs() []string {
	if e.destination == nil {
		return []string{}
	}
	return []string{e.destination.id}
}

func (e *auditExporter) run(ctx context.Context) {
	defer e.wait.Done()
	ticker := time.NewTicker(auditExportPollInterval)
	defer ticker.Stop()
	for {
		fullBatch := e.process(ctx)
		select {
		case <-ctx.Done():
			return
		default:
		}
		if fullBatch {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *auditExporter) process(ctx context.Context) bool {
	if e.leaser == nil {
		log.WithField("destination_id", e.destination.id).Error("audit export leaser is unavailable; exporter cannot deliver")
		return false
	}
	leaseContext, cancel := context.WithTimeout(ctx, e.destination.timeout)
	defer cancel()
	lease, acquired, err := e.leaser.TryAcquire(leaseContext, e.destination.id)
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Warn("audit export lease is unavailable; export paused")
		return false
	}
	if !acquired || lease == nil {
		return false
	}
	defer lease.Release()
	activeContext, activeCancel := context.WithCancel(ctx)
	defer activeCancel()
	go func() {
		select {
		case <-lease.Lost():
			activeCancel()
		case <-activeContext.Done():
		}
	}()

	operationContext, operationCancel := context.WithTimeout(activeContext, e.destination.timeout)
	cursor, err := e.repository.InitializeAuditExportState(operationContext, e.destination.id)
	operationCancel()
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Error("failed to read audit export cursor")
		return false
	}
	operationContext, operationCancel = context.WithTimeout(activeContext, e.destination.timeout)
	events, err := e.repository.GetAuditEventsAfter(operationContext, cursor, auditExportBatchSize)
	operationCancel()
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Error("failed to read pending audit events")
		return false
	}
	if len(events) == 0 {
		return false
	}
	deliveryContext, deliveryCancel := context.WithTimeout(activeContext, e.destination.timeout)
	connection, err := e.destination.connect(deliveryContext)
	if err != nil {
		deliveryCancel()
		log.WithError(err).WithField("destination_id", e.destination.id).Warn("failed to connect audit syslog exporter")
		return false
	}
	defer deliveryCancel()
	defer connection.Close()
	for _, event := range events {
		if !lease.Valid() {
			return false
		}
		select {
		case <-lease.Lost():
			return false
		default:
		}
		err = e.destination.deliver(deliveryContext, connection, event)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{"destination_id": e.destination.id, "event_id": event.EventID, "seq": event.Seq}).Warn("failed to export audit event")
			return false
		}
		if !lease.Valid() {
			return false
		}
		if activeContext.Err() != nil || !lease.Valid() {
			return false
		}
		operationContext, operationCancel = context.WithTimeout(activeContext, e.destination.timeout)
		advanced, advanceErr := e.repository.AdvanceAuditExportState(operationContext, e.destination.id, cursor, event.Seq)
		operationCancel()
		if advanceErr != nil {
			log.WithError(advanceErr).WithField("destination_id", e.destination.id).Error("failed to advance audit export cursor")
			return false
		}
		if !advanced {
			return false
		}
		cursor = event.Seq
	}
	return len(events) == auditExportBatchSize
}

func newAuditSyslogDestination(config *util.AuditSyslogConfig) (*auditSyslogDestination, error) {
	if config == nil || strings.TrimSpace(config.ID) == "" || strings.TrimSpace(config.Address) == "" {
		return nil, errors.New("audit.syslog.id and audit.syslog.address are required together")
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("audit.syslog.address must include a host and port")
	}
	timeout := auditExportTimeout
	if strings.TrimSpace(config.Timeout) != "" {
		timeout, err = time.ParseDuration(config.Timeout)
		if err != nil || timeout <= 0 {
			return nil, errors.New("audit.syslog.timeout must be a positive duration")
		}
	}
	serverName := config.ServerName
	if serverName == "" {
		serverName = host
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if config.CAFile != "" {
		pem, readErr := os.ReadFile(config.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read audit.syslog.ca_file: %w", readErr)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("audit.syslog.ca_file contains no PEM certificates")
		}
	}
	return &auditSyslogDestination{
		id: strings.TrimSpace(config.ID), address: config.Address, timeout: timeout,
		tls: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName},
	}, nil
}

func (d *auditSyslogDestination) Deliver(ctx context.Context, event db.AuditEvent) error {
	connection, err := d.connect(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return d.deliver(ctx, connection, event)
}

func (d *auditSyslogDestination) connect(ctx context.Context) (*auditSyslogConnection, error) {
	dialer := &net.Dialer{Timeout: d.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", d.address)
	if err != nil {
		return nil, err
	}
	tlsConnection := tls.Client(connection, d.tls.Clone())
	stopCloseOnCancel := context.AfterFunc(ctx, func() { _ = tlsConnection.Close() })
	if err = tlsConnection.HandshakeContext(ctx); err != nil {
		stopCloseOnCancel()
		_ = tlsConnection.Close()
		return nil, err
	}
	return &auditSyslogConnection{connection: tlsConnection, stopCloseOnCancel: stopCloseOnCancel}, nil
}

func (c *auditSyslogConnection) Close() {
	c.stopCloseOnCancel()
	_ = c.connection.Close()
}

func (d *auditSyslogDestination) deliver(ctx context.Context, connection *auditSyslogConnection, event db.AuditEvent) error {
	envelope, err := audit.EnvelopeFromRow(event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err = connection.connection.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	hostname := event.NodeID
	if hostname == "" {
		hostname = event.InstanceID
	}
	message := fmt.Sprintf("<14>1 %s %s semaphore - %s - %s", event.Created.UTC().Format("2006-01-02T15:04:05.999999Z"), auditSyslogToken(hostname, 255), auditSyslogToken(event.EventCode, 32), payload)
	frame := fmt.Sprintf("%d %s", len(message), message)
	for data := []byte(frame); len(data) > 0; {
		written, writeErr := connection.connection.Write(data)
		if writeErr != nil {
			return writeErr
		}
		data = data[written:]
	}
	return nil
}

func auditSyslogToken(value string, maximum int) string {
	value = strings.Map(func(r rune) rune {
		if r < '!' || r > '~' {
			return -1
		}
		return r
	}, value)
	if len(value) > maximum {
		value = value[:maximum]
	}
	if value == "" {
		return "-"
	}
	return value
}
