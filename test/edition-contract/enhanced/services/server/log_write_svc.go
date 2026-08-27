package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	structuredLogVersion          = 1
	defaultStructuredLogQueueSize = 1024
	defaultStructuredLogMaxSize   = 100
	defaultStructuredLogFlush     = time.Second
	defaultStructuredLogRotation  = 24 * time.Hour
	maxDiagnosticErrorBytes       = 512
)

var (
	secretKeyPattern     = regexp.MustCompile(`(?i)(authorization|credential|password|passwd|secret|token|api[_-]?key|private[_-]?key|client[_-]?secret|cookie)`)
	inlineSecretPattern  = regexp.MustCompile(`(?i)((?:authorization|credential|password|passwd|secret|token|api[_-]?key|private[_-]?key|client[_-]?secret|cookie)\s*[=:]\s*)[^\s,;]+`)
	bearerSecretPattern  = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	urlCredentialPattern = regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`)
	keyBlockPattern      = regexp.MustCompile(`(?s)-----BEGIN [^-]*KEY-----.*?-----END [^-]*KEY-----`)
)

type structuredLogDestination interface {
	WriteLine([]byte) error
	Flush() error
	Close() error
}

type queuedStructuredLog struct {
	category string
	line     []byte
}

type structuredLogService struct {
	instance         string
	queue            chan queuedStructuredLog
	stop             chan struct{}
	done             chan struct{}
	destinations     map[string]structuredLogDestination
	destinationInfo  []pro_interfaces.StructuredLogDestinationDiagnostics
	configuredCount  int
	flushInterval    time.Duration
	rotationInterval time.Duration
	debugFilter      pro_interfaces.DebugFilter

	closeMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error

	diagnosticsMu        sync.RWMutex
	droppedRecords       uint64
	lastWriteError       string
	lastSuccessfulFlush  *time.Time
	configurationFailure bool
	writeFailures        map[string]bool
	flushFailure         bool
}

var _ pro_interfaces.LogWriteServiceLifecycle = (*structuredLogService)(nil)

// NewLogWriteService creates the enhanced structured writer from the effective
// process configuration. A completely disabled configuration remains a no-op.
func NewLogWriteService() pro_interfaces.LogWriteServiceLifecycle {
	return NewLogWriteServiceWithFilter(nil)
}

func NewLogWriteServiceWithFilter(filter pro_interfaces.DebugFilter) pro_interfaces.LogWriteServiceLifecycle {
	instance := "semaphore"
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		instance = hostname
	}
	if util.Config == nil {
		return NewLogWriteServiceWithConfigAndFilter(nil, instance, filter)
	}
	if util.Config.HA != nil && util.Config.HA.NodeID != "" {
		instance = util.Config.HA.NodeID
	}
	if util.Config.Log == nil {
		return NewLogWriteServiceWithConfigAndFilter(nil, instance, filter)
	}
	return NewLogWriteServiceWithConfigAndFilter(util.Config.Log, instance, filter)
}

// NewLogWriteServiceWithConfig creates a structured writer with an explicit
// configuration. It is exported so enhanced-module contract tests can exercise
// the implementation without mutating the global server configuration.
func NewLogWriteServiceWithConfig(config *util.ConfigLog, instance string) pro_interfaces.LogWriteServiceLifecycle {
	return NewLogWriteServiceWithConfigAndFilter(config, instance, nil)
}

func NewLogWriteServiceWithConfigAndFilter(
	config *util.ConfigLog,
	instance string,
	filter pro_interfaces.DebugFilter,
) pro_interfaces.LogWriteServiceLifecycle {
	if config == nil {
		config = &util.ConfigLog{}
	}
	if filter == nil {
		filter = debuglog.NewManager(instance, config.DebugFilter, time.Now().UTC())
	}
	queueSize := config.QueueSize
	if queueSize <= 0 {
		queueSize = defaultStructuredLogQueueSize
	}
	flushInterval := parsePositiveDuration(config.FlushInterval, defaultStructuredLogFlush)
	rotationInterval := parsePositiveDuration(config.RotationInterval, defaultStructuredLogRotation)
	service := &structuredLogService{
		instance:         instance,
		queue:            make(chan queuedStructuredLog, queueSize),
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		destinations:     map[string]structuredLogDestination{},
		writeFailures:    map[string]bool{},
		flushInterval:    flushInterval,
		rotationInterval: rotationInterval,
		debugFilter:      filter,
	}

	service.configureEventDestination(*config)
	service.configureTaskDestinations(*config)
	service.configureDebugDestination(*config)
	if len(service.destinations) == 0 {
		close(service.done)
		return service
	}
	go service.run()
	return service
}

func (s *structuredLogService) configureDebugDestination(config util.ConfigLog) {
	if config.Debug == nil || !config.Debug.Enabled {
		return
	}
	s.configuredCount++
	s.addDestination("debug", config.Debug.Format, config.Debug.Logger)
}

func parsePositiveDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (s *structuredLogService) configureEventDestination(config util.ConfigLog) {
	if config.Events == nil || !config.Events.Enabled {
		return
	}
	s.configuredCount++
	s.addDestination("application", config.Events.Format, config.Events.Logger)
}

func (s *structuredLogService) configureTaskDestinations(config util.ConfigLog) {
	if config.Tasks == nil || !config.Tasks.Enabled {
		return
	}
	if config.Tasks.Logger != nil {
		s.configuredCount++
		s.addDestination("task", config.Tasks.Format, config.Tasks.Logger)
	}
	if config.Tasks.ResultLogger != nil {
		s.configuredCount++
		s.addDestination("result", config.Tasks.Format, config.Tasks.ResultLogger)
	}
	if config.Tasks.Logger == nil && config.Tasks.ResultLogger == nil {
		s.configuredCount++
		s.recordConfigurationFailure("task logging is enabled without a destination")
	}
}

func (s *structuredLogService) addDestination(category, format string, logger *lumberjack.Logger) {
	if format != util.FileLogJSON {
		s.recordConfigurationFailure(fmt.Sprintf("%s log format %q is unsupported", category, format))
		return
	}
	if logger == nil || logger.Filename == "" {
		s.recordConfigurationFailure(fmt.Sprintf("%s log destination is missing", category))
		return
	}
	maxSize := logger.MaxSize
	if maxSize <= 0 {
		maxSize = defaultStructuredLogMaxSize
	}
	info := pro_interfaces.StructuredLogDestinationDiagnostics{
		Category:         category,
		Enabled:          true,
		Filename:         redactText(logger.Filename),
		MaxSizeMegabytes: maxSize,
		MaxAgeDays:       logger.MaxAge,
		MaxBackups:       logger.MaxBackups,
		Compress:         logger.Compress,
	}
	s.destinationInfo = append(s.destinationInfo, info)
	destination, err := newSafeRotatingFile(safeRotatingFileConfig{
		filename:         logger.Filename,
		maxSizeMegabytes: maxSize,
		maxAgeDays:       logger.MaxAge,
		maxBackups:       logger.MaxBackups,
		compress:         logger.Compress,
		localTime:        logger.LocalTime,
		rotationInterval: s.rotationInterval,
	})
	if err != nil {
		s.recordConfigurationFailure(fmt.Sprintf("%s destination: %v", category, err))
		return
	}
	s.destinations[category] = destination
}

func (s *structuredLogService) WriteEventLog(event pro_interfaces.EventLogRecord) error {
	return s.enqueue("application", pro_interfaces.StructuredLogEnvelope{
		Version:       structuredLogVersion,
		Schema:        "semaphore.application.v1",
		Timestamp:     time.Now().UTC(),
		Instance:      s.instance,
		CorrelationID: correlationID(event.CorrelationID, "application"),
		ProjectID:     event.ProjectID,
		EventType:     event.Action,
		Payload:       event,
	})
}

func (s *structuredLogService) WriteTaskLog(task pro_interfaces.TaskLogRecord) error {
	projectID := task.ProjectID
	return s.enqueue("task", pro_interfaces.StructuredLogEnvelope{
		Version:       structuredLogVersion,
		Schema:        "semaphore.task.v1",
		Timestamp:     time.Now().UTC(),
		Instance:      s.instance,
		CorrelationID: fmt.Sprintf("task-%d", task.TaskID),
		ProjectID:     &projectID,
		EventType:     string(task.Status),
		Payload:       task,
	})
}

func (s *structuredLogService) WriteResult(value any) error {
	record, ok := value.(pro_interfaces.ResultLogRecord)
	if !ok {
		return s.enqueue("result", pro_interfaces.StructuredLogEnvelope{
			Version:       structuredLogVersion,
			Schema:        "semaphore.result.v1",
			Timestamp:     time.Now().UTC(),
			Instance:      s.instance,
			CorrelationID: correlationID("", "result"),
			EventType:     "result",
			Payload:       value,
		})
	}
	projectID := record.ProjectID
	return s.enqueue("result", pro_interfaces.StructuredLogEnvelope{
		Version:       structuredLogVersion,
		Schema:        "semaphore.result.v1",
		Timestamp:     time.Now().UTC(),
		Instance:      s.instance,
		CorrelationID: correlationID(record.CorrelationID, fmt.Sprintf("task-%d", record.TaskID)),
		ProjectID:     &projectID,
		EventType:     record.EventType,
		Payload:       record.Result,
	})
}

func (s *structuredLogService) WriteDebug(record pro_interfaces.DebugLogRecord) error {
	if _, enabled := s.destinations["debug"]; !enabled {
		return nil
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if s.debugFilter == nil || !s.debugFilter.Enabled(record.Component) {
		return nil
	}
	fields := map[string]any{}
	if record.Fields != nil {
		fields = record.Fields()
	}
	payload := map[string]any{
		"component": record.Component,
		"fields":    fields,
	}
	return s.enqueue("debug", pro_interfaces.StructuredLogEnvelope{
		Version:       structuredLogVersion,
		Schema:        "semaphore.debug.v1",
		Timestamp:     time.Now().UTC(),
		Instance:      s.instance,
		CorrelationID: correlationID(record.CorrelationID, "debug"),
		ProjectID:     record.ProjectID,
		EventType:     record.EventType,
		Payload:       payload,
	})
}

func (s *structuredLogService) DebugFilterDiagnostics() pro_interfaces.DebugFilterDiagnostics {
	if s.debugFilter == nil {
		return pro_interfaces.DebugFilterDiagnostics{
			Default: debuglog.DebugFilterDefaultAll, Configured: []string{}, Effective: []string{"*"},
			Rejected: []pro_interfaces.DebugFilterRejectedEntry{},
		}
	}
	return s.debugFilter.Diagnostics()
}

func correlationID(value, prefix string) string {
	if value != "" {
		return value
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func (s *structuredLogService) enqueue(category string, envelope pro_interfaces.StructuredLogEnvelope) error {
	if _, enabled := s.destinations[category]; !enabled {
		return nil
	}
	payload, err := redactStructuredPayload(envelope)
	if err != nil {
		s.recordWriteFailure(category, err)
		return err
	}
	payload = append(payload, '\n')

	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed {
		return errors.New("structured log writer is closed")
	}
	select {
	case s.queue <- queuedStructuredLog{category: category, line: payload}:
		return nil
	default:
		s.diagnosticsMu.Lock()
		s.droppedRecords++
		s.diagnosticsMu.Unlock()
		return errors.New("structured log queue is full; record dropped")
	}
}

func redactStructuredPayload(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("serialize structured log: %w", err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, fmt.Errorf("normalize structured log: %w", err)
	}
	redactValue(generic)
	return json.Marshal(generic)
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if secretKeyPattern.MatchString(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			redactValue(child)
			if text, ok := child.(string); ok {
				typed[key] = redactText(text)
			}
		}
	case []any:
		for index, child := range typed {
			redactValue(child)
			if text, ok := child.(string); ok {
				typed[index] = redactText(text)
			}
		}
	}
}

func redactText(value string) string {
	value = inlineSecretPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = bearerSecretPattern.ReplaceAllString(value, "$1[REDACTED]")
	value = urlCredentialPattern.ReplaceAllString(value, "$1[REDACTED]@")
	return keyBlockPattern.ReplaceAllString(value, "[REDACTED KEY BLOCK]")
}

func (s *structuredLogService) run() {
	defer close(s.done)
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case record := <-s.queue:
			s.write(record)
		case <-ticker.C:
			s.flushAll()
		case <-s.stop:
			for {
				select {
				case record := <-s.queue:
					s.write(record)
				default:
					s.flushAll()
					return
				}
			}
		}
	}
}

func (s *structuredLogService) write(record queuedStructuredLog) {
	destination := s.destinations[record.category]
	if destination == nil {
		return
	}
	if err := destination.WriteLine(record.line); err != nil {
		s.recordWriteFailure(record.category, fmt.Errorf("%s destination write failed: %w", record.category, err))
		return
	}
	s.diagnosticsMu.Lock()
	delete(s.writeFailures, record.category)
	s.diagnosticsMu.Unlock()
}

func (s *structuredLogService) flushAll() {
	var flushErr error
	for category, destination := range s.destinations {
		if err := destination.Flush(); err != nil {
			flushErr = errors.Join(flushErr, fmt.Errorf("%s destination flush failed: %w", category, err))
		}
	}
	if flushErr != nil {
		s.recordFlushFailure(flushErr)
		return
	}
	if len(s.destinations) == 0 {
		return
	}
	now := time.Now().UTC()
	s.diagnosticsMu.Lock()
	s.lastSuccessfulFlush = &now
	s.flushFailure = false
	s.diagnosticsMu.Unlock()
}

func (s *structuredLogService) recordConfigurationFailure(message string) {
	s.diagnosticsMu.Lock()
	s.configurationFailure = true
	s.lastWriteError = sanitizeDiagnosticError(message)
	s.diagnosticsMu.Unlock()
}

func (s *structuredLogService) recordWriteFailure(category string, err error) {
	s.diagnosticsMu.Lock()
	if s.writeFailures == nil {
		s.writeFailures = map[string]bool{}
	}
	s.writeFailures[category] = true
	s.lastWriteError = sanitizeDiagnosticError(err.Error())
	s.diagnosticsMu.Unlock()
}

func (s *structuredLogService) recordFlushFailure(err error) {
	s.diagnosticsMu.Lock()
	s.flushFailure = true
	s.lastWriteError = sanitizeDiagnosticError(err.Error())
	s.diagnosticsMu.Unlock()
}

func sanitizeDiagnosticError(message string) string {
	redacted := redactText(message)
	redacted = strings.ReplaceAll(redacted, "\n", " ")
	if len(redacted) <= maxDiagnosticErrorBytes {
		return redacted
	}
	redacted = redacted[:maxDiagnosticErrorBytes]
	for !utf8.ValidString(redacted) {
		redacted = redacted[:len(redacted)-1]
	}
	return redacted + "…"
}

func (s *structuredLogService) Diagnostics() pro_interfaces.StructuredLogDiagnostics {
	s.diagnosticsMu.RLock()
	dropped := s.droppedRecords
	lastError := s.lastWriteError
	lastFlush := s.lastSuccessfulFlush
	failed := s.configurationFailure || len(s.writeFailures) > 0 || s.flushFailure
	s.diagnosticsMu.RUnlock()

	state := pro_interfaces.StructuredLogDisabled
	enabled := s.configuredCount > 0
	if enabled {
		state = pro_interfaces.StructuredLogHealthy
	}
	if dropped > 0 {
		state = pro_interfaces.StructuredLogDropping
	}
	if failed {
		state = pro_interfaces.StructuredLogFailed
	}
	return pro_interfaces.StructuredLogDiagnostics{
		Enabled:             enabled,
		State:               state,
		QueueDepth:          len(s.queue),
		QueueCapacity:       cap(s.queue),
		DroppedRecords:      dropped,
		LastWriteError:      lastError,
		LastSuccessfulFlush: lastFlush,
		FlushInterval:       s.flushInterval.String(),
		RotationInterval:    s.rotationInterval.String(),
		Destinations:        append([]pro_interfaces.StructuredLogDestinationDiagnostics(nil), s.destinationInfo...),
	}
}

func (s *structuredLogService) Close() error {
	s.closeOnce.Do(func() {
		s.closeMu.Lock()
		s.closed = true
		if len(s.destinations) > 0 {
			close(s.stop)
		}
		s.closeMu.Unlock()
		<-s.done
		for category, destination := range s.destinations {
			if err := destination.Close(); err != nil {
				s.closeErr = errors.Join(s.closeErr, fmt.Errorf("close %s destination: %w", category, err))
			}
		}
	})
	return s.closeErr
}
