package server

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type auditCompositeExporter struct {
	exporters []pro_interfaces.AuditExporter
}

func (e *auditCompositeExporter) Start() error {
	ids := make(map[string]struct{}, len(e.exporters))
	for _, exporter := range e.exporters {
		for _, id := range exporter.DestinationIDs() {
			if _, exists := ids[id]; exists {
				return fmt.Errorf("audit export destination ID %q is configured more than once", id)
			}
			ids[id] = struct{}{}
		}
	}
	started := make([]pro_interfaces.AuditExporter, 0, len(e.exporters))
	for _, exporter := range e.exporters {
		if err := exporter.Start(); err != nil {
			for _, previous := range started {
				previous.Stop()
			}
			return err
		}
		started = append(started, exporter)
	}
	return nil
}
func (e *auditCompositeExporter) Stop() {
	for _, exporter := range e.exporters {
		exporter.Stop()
	}
}
func (e *auditCompositeExporter) DestinationIDs() []string {
	ids := make([]string, 0, len(e.exporters))
	for _, exporter := range e.exporters {
		ids = append(ids, exporter.DestinationIDs()...)
	}
	return ids
}

type auditHECExporter struct {
	repository          db.AuditExportRepository
	destination         *auditHECDestination
	leaser              pro_interfaces.AuditExportLeaser
	lifecycle           sync.Mutex
	started, stopped    bool
	startDone           chan struct{}
	startCancel, cancel context.CancelFunc
	wait                sync.WaitGroup
	startErr            error
}

type auditHECDestination struct {
	id                        string
	timeout                   time.Duration
	url                       string
	token                     string
	index, source, sourcetype string
	client                    *http.Client
}

func newAuditHECExporter(store any, config *util.AuditSplunkHECConfig, leaser pro_interfaces.AuditExportLeaser) *auditHECExporter {
	e := &auditHECExporter{leaser: leaser}
	if repository, ok := store.(db.AuditExportRepository); ok {
		e.repository = repository
	}
	e.destination, e.startErr = newAuditHECDestination(config)
	if e.destination != nil && e.repository == nil && e.startErr == nil {
		e.startErr = errors.New("audit Splunk HEC export requires an audit export repository")
	}
	if e.destination != nil && e.leaser == nil && e.startErr == nil {
		e.startErr = errors.New("audit Splunk HEC export requires an audit export leaser")
	}
	return e
}

func newAuditHECDestination(config *util.AuditSplunkHECConfig) (*auditHECDestination, error) {
	if config == nil || strings.TrimSpace(config.ID) == "" || strings.TrimSpace(config.URL) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("audit.splunk_hec.id, audit.splunk_hec.url and audit.splunk_hec.token are required together")
	}
	if strings.TrimSpace(config.Token) != config.Token {
		return nil, errors.New("audit.splunk_hec.token must not have leading or trailing whitespace")
	}
	if strings.ContainsFunc(config.Token, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, errors.New("audit.splunk_hec.token must not contain control characters")
	}
	if strings.Contains(config.URL, "#") {
		return nil, errors.New("audit.splunk_hec.url must not contain a fragment")
	}
	endpoint, err := url.ParseRequestURI(config.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("audit.splunk_hec.url must be an https URL without credentials")
	}
	timeout := auditExportTimeout
	if strings.TrimSpace(config.Timeout) != "" {
		timeout, err = time.ParseDuration(config.Timeout)
		if err != nil || timeout <= 0 {
			return nil, errors.New("audit.splunk_hec.timeout must be a positive duration")
		}
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if config.CAFile != "" {
		pem, readErr := os.ReadFile(config.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read audit.splunk_hec.ca_file: %w", readErr)
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("audit.splunk_hec.ca_file contains no PEM certificates")
		}
	}
	transport := newAuditHECTransport(endpoint, roots, config.ServerName)
	return &auditHECDestination{id: strings.TrimSpace(config.ID), timeout: timeout, url: endpoint.String(), token: config.Token, index: config.Index, source: valueOr(config.Source, "semaphore"), sourcetype: valueOr(config.Sourcetype, "semaphore:audit"), client: &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (e *auditHECExporter) Start() error {
	e.lifecycle.Lock()
	if e.started {
		done := e.startDone
		e.lifecycle.Unlock()
		if done != nil {
			<-done
		}
		e.lifecycle.Lock()
		defer e.lifecycle.Unlock()
		return e.startErr
	}
	e.started = true
	e.startDone = make(chan struct{})
	if e.stopped {
		e.startErr = errors.New("audit Splunk HEC exporter is stopped")
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
	e.startCancel = cancel
	e.lifecycle.Unlock()
	_, err := e.repository.InitializeAuditExportState(ctx, e.destination.id)
	cancel()
	e.lifecycle.Lock()
	e.startCancel = nil
	if err != nil {
		e.startErr = fmt.Errorf("initialize audit export destination %q: %w", e.destination.id, err)
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	if e.stopped {
		e.startErr = errors.New("audit Splunk HEC exporter is stopped")
		close(e.startDone)
		e.lifecycle.Unlock()
		return e.startErr
	}
	ctx, e.cancel = context.WithCancel(context.Background())
	e.wait.Add(1)
	go e.run(ctx)
	close(e.startDone)
	e.lifecycle.Unlock()
	return nil
}
func (e *auditHECExporter) Stop() {
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
	done := e.startDone
	e.lifecycle.Unlock()
	if done != nil {
		<-done
	}
	e.wait.Wait()
	if e.destination != nil {
		e.destination.client.CloseIdleConnections()
	}
}
func (e *auditHECExporter) DestinationIDs() []string {
	if e.destination == nil {
		return []string{}
	}
	return []string{e.destination.id}
}
func (e *auditHECExporter) run(ctx context.Context) {
	defer e.wait.Done()
	ticker := time.NewTicker(auditExportPollInterval)
	defer ticker.Stop()
	for {
		full := e.process(ctx)
		if ctx.Err() != nil {
			return
		}
		if full {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (e *auditHECExporter) process(ctx context.Context) bool {
	leaseCtx, cancel := context.WithTimeout(ctx, e.destination.timeout)
	lease, acquired, err := e.leaser.TryAcquire(leaseCtx, e.destination.id)
	cancel()
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Warn("audit export lease is unavailable; export paused")
		return false
	}
	if !acquired || lease == nil {
		return false
	}
	defer lease.Release()
	active, cancelActive := context.WithCancel(ctx)
	defer cancelActive()
	go func() {
		select {
		case <-lease.Lost():
			cancelActive()
		case <-active.Done():
		}
	}()
	cursor, err := e.cursor(active)
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Error("failed to read audit export cursor")
		return false
	}
	events, err := e.events(active, cursor)
	if err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Error("failed to read pending audit events")
		return false
	}
	if len(events) == 0 {
		return false
	}
	if !lease.Valid() || active.Err() != nil {
		return false
	}
	if err = e.destination.deliver(active, events); err != nil {
		log.WithError(err).WithField("destination_id", e.destination.id).Warn("failed to export audit HEC batch")
		return false
	}
	for _, event := range events {
		select {
		case <-lease.Lost():
			return false
		default:
		}
		if !lease.Valid() || active.Err() != nil {
			return false
		}
		operation, operationCancel := context.WithTimeout(active, e.destination.timeout)
		advanced, advanceErr := e.repository.AdvanceAuditExportState(operation, e.destination.id, cursor, event.Seq)
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
func (e *auditHECExporter) cursor(ctx context.Context) (int64, error) {
	operation, cancel := context.WithTimeout(ctx, e.destination.timeout)
	defer cancel()
	return e.repository.InitializeAuditExportState(operation, e.destination.id)
}
func (e *auditHECExporter) events(ctx context.Context, cursor int64) ([]db.AuditEvent, error) {
	operation, cancel := context.WithTimeout(ctx, e.destination.timeout)
	defer cancel()
	return e.repository.GetAuditEventsAfter(operation, cursor, auditExportBatchSize)
}

func (d *auditHECDestination) deliver(ctx context.Context, events []db.AuditEvent) error {
	var body bytes.Buffer
	for _, event := range events {
		envelope, err := audit.EnvelopeFromRow(event)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(hecEvent{Time: float64(event.Created.UnixNano()) / float64(time.Second), Host: valueOr(event.NodeID, event.InstanceID), Source: d.source, Sourcetype: d.sourcetype, Index: d.index, Event: envelope})
		if err != nil {
			return err
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Splunk "+d.token)
	response, err := d.client.Do(request)
	if err != nil {
		return sanitizeHECTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("audit Splunk HEC returned HTTP %d", response.StatusCode)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	if readErr != nil {
		return errors.New("read audit Splunk HEC response")
	}
	if len(responseBody) > 4096 {
		return errors.New("audit Splunk HEC response is too large")
	}
	var acknowledgement struct {
		Code *int `json:"code"`
	}
	if err = json.Unmarshal(responseBody, &acknowledgement); err != nil || acknowledgement.Code == nil {
		return errors.New("invalid audit Splunk HEC response")
	}
	if *acknowledgement.Code != 0 {
		return fmt.Errorf("audit Splunk HEC returned response code %d", *acknowledgement.Code)
	}
	return nil
}

func sanitizeHECTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("audit Splunk HEC request timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("audit Splunk HEC request cancelled")
	}
	var certificateErr x509.UnknownAuthorityError
	if errors.As(err, &certificateErr) {
		return errors.New("audit Splunk HEC certificate verification failed")
	}
	var networkErr interface{ Timeout() bool }
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return errors.New("audit Splunk HEC request timed out")
	}
	return errors.New("audit Splunk HEC network request failed")
}

type hecEvent struct {
	Time       float64        `json:"time"`
	Host       string         `json:"host"`
	Source     string         `json:"source"`
	Sourcetype string         `json:"sourcetype"`
	Index      string         `json:"index,omitempty"`
	Event      audit.Envelope `json:"event"`
}
