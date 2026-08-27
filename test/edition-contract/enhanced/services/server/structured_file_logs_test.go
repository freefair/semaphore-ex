package server

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/natefinch/lumberjack.v2"
)

func TestStructuredLogSchemasAndRedaction(t *testing.T) {
	directory := realTempDir(t)
	eventPath := filepath.Join(directory, "events.jsonl")
	taskPath := filepath.Join(directory, "tasks.jsonl")
	resultPath := filepath.Join(directory, "results.jsonl")
	description := "token=event-secret Authorization: bearer-secret Bearer standalone-secret " +
		"https://user:url-secret@example.test -----BEGIN PRIVATE" +
		" KEY-----pem-secret-----END PRIVATE" +
		" KEY-----"
	service := NewLogWriteServiceWithConfig(&util.ConfigLog{
		QueueSize:        8,
		FlushInterval:    "10ms",
		RotationInterval: "1h",
		Events: &util.EventLogType{Enabled: true, Format: util.FileLogJSON,
			Logger: &lumberjack.Logger{Filename: eventPath}},
		Tasks: &util.TaskLogType{Enabled: true, Format: util.FileLogJSON,
			Logger:       &lumberjack.Logger{Filename: taskPath},
			ResultLogger: &lumberjack.Logger{Filename: resultPath}},
	}, "instance-a")

	require.NoError(t, service.WriteEventLog(pro_interfaces.EventLogRecord{
		Action: "project.created", CorrelationID: "correlation-a", Description: &description,
	}))
	require.NoError(t, service.WriteTaskLog(pro_interfaces.TaskLogRecord{
		TaskID: 11, ProjectID: 22, TemplateID: 33, Status: task_logger.TaskSuccessStatus,
	}))
	require.NoError(t, service.WriteResult(pro_interfaces.ResultLogRecord{
		TaskID: 11, ProjectID: 22, CorrelationID: "result-a", EventType: "host_summary",
		Result: map[string]any{
			"password": "result-secret",
			"nested":   []any{map[string]any{"api_key": "nested-secret"}, "cookie=session-secret"},
		},
	}))
	require.NoError(t, service.Close())

	assertEnvelope(t, eventPath, "semaphore.application.v1", "project.created", "correlation-a")
	assertEnvelope(t, taskPath, "semaphore.task.v1", string(task_logger.TaskSuccessStatus), "task-11")
	assertEnvelope(t, resultPath, "semaphore.result.v1", "host_summary", "result-a")
	for _, path := range []string{eventPath, resultPath} {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(content), "event-secret")
		assert.NotContains(t, string(content), "bearer-secret")
		assert.NotContains(t, string(content), "result-secret")
		assert.NotContains(t, string(content), "nested-secret")
		assert.NotContains(t, string(content), "session-secret")
		assert.NotContains(t, string(content), "standalone-secret")
		assert.NotContains(t, string(content), "url-secret")
		assert.NotContains(t, string(content), "pem-secret")
		assert.Contains(t, string(content), "[REDACTED]")
	}
}

func assertEnvelope(t *testing.T, path, schema, eventType, correlationID string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, content)
	require.Equal(t, byte('\n'), content[len(content)-1])
	var envelope pro_interfaces.StructuredLogEnvelope
	require.NoError(t, json.Unmarshal(content[:len(content)-1], &envelope))
	assert.Equal(t, structuredLogVersion, envelope.Version)
	assert.Equal(t, schema, envelope.Schema)
	assert.Equal(t, eventType, envelope.EventType)
	assert.Equal(t, correlationID, envelope.CorrelationID)
	assert.Equal(t, "instance-a", envelope.Instance)
	assert.False(t, envelope.Timestamp.IsZero())
}

func TestStructuredLogPathValidation(t *testing.T) {
	directory := realTempDir(t)
	require.ErrorContains(t, validateStructuredLogPath("relative.jsonl"), "absolute")
	require.ErrorContains(t, validateStructuredLogPath(directory+"/nested/../events.jsonl"), "traversal")

	target := filepath.Join(directory, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(directory, "link")
	require.NoError(t, os.Symlink(target, link))
	require.ErrorContains(t, validateStructuredLogPath(filepath.Join(link, "events.jsonl")), "symlink")
	require.ErrorContains(t, validateStructuredLogPath(target), "regular file")
}

type blockingDestination struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *blockingDestination) WriteLine([]byte) error {
	d.once.Do(func() { close(d.started) })
	<-d.release
	return nil
}
func (*blockingDestination) Flush() error { return nil }
func (*blockingDestination) Close() error { return nil }

func TestStructuredLogQueueOverflowDoesNotBlockProducer(t *testing.T) {
	destination := &blockingDestination{started: make(chan struct{}), release: make(chan struct{})}
	service := &structuredLogService{
		instance: "instance-a", queue: make(chan queuedStructuredLog, 1), stop: make(chan struct{}), done: make(chan struct{}),
		destinations: map[string]structuredLogDestination{"application": destination}, configuredCount: 1,
		flushInterval: time.Hour, rotationInterval: time.Hour,
	}
	go service.run()
	require.NoError(t, service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "one"}))
	select {
	case <-destination.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start the blocking write")
	}
	require.NoError(t, service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "two"}))
	started := time.Now()
	err := service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "three"})
	assert.ErrorContains(t, err, "queue is full")
	assert.Less(t, time.Since(started), 100*time.Millisecond)
	diagnostics := service.Diagnostics()
	assert.Equal(t, pro_interfaces.StructuredLogDropping, diagnostics.State)
	assert.Equal(t, uint64(1), diagnostics.DroppedRecords)
	close(destination.release)
	require.NoError(t, service.Close())
}

func TestStructuredLogConcurrentWritesPermissionsAndShutdownFlush(t *testing.T) {
	path := filepath.Join(realTempDir(t), "events.jsonl")
	service := NewLogWriteServiceWithConfig(&util.ConfigLog{
		QueueSize: 128, FlushInterval: "1h", RotationInterval: "1h",
		Events: &util.EventLogType{Enabled: true, Format: util.FileLogJSON,
			Logger: &lumberjack.Logger{Filename: path}},
	}, "instance-a")

	const records = 64
	var group sync.WaitGroup
	writeErrors := make(chan error, records)
	for index := 0; index < records; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			writeErrors <- service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "concurrent"})
		}(index)
	}
	group.Wait()
	close(writeErrors)
	for err := range writeErrors {
		require.NoError(t, err)
	}
	require.NoError(t, service.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := splitJSONLines(content)
	require.Len(t, lines, records)
	for _, line := range lines {
		assert.True(t, json.Valid(line))
	}
	assert.NotNil(t, service.Diagnostics().LastSuccessfulFlush)
}

func splitJSONLines(content []byte) [][]byte {
	lines := [][]byte{}
	start := 0
	for index, value := range content {
		if value == '\n' {
			lines = append(lines, content[start:index])
			start = index + 1
		}
	}
	return lines
}

func TestStructuredLogTimeRotationRetentionAndTailRepair(t *testing.T) {
	directory := realTempDir(t)
	path := filepath.Join(directory, "events.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"valid\":true}\n{\"partial\":"), 0o600))

	writer, err := newSafeRotatingFile(safeRotatingFileConfig{
		filename: path, maxAgeDays: 1, maxBackups: 1, rotationInterval: time.Hour,
	})
	require.NoError(t, err)
	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	current := base
	writer.openedAt = base
	writer.now = func() time.Time { return current }
	require.NoError(t, writer.WriteLine([]byte("{\"first\":true}\n")))
	current = base.Add(2 * time.Hour)
	require.NoError(t, writer.WriteLine([]byte("{\"second\":true}\n")))
	require.NoError(t, writer.Flush())

	rotated, err := filepath.Glob(path + ".*")
	require.NoError(t, err)
	require.Len(t, rotated, 1)
	rotatedContent, err := os.ReadFile(rotated[0])
	require.NoError(t, err)
	assert.NotContains(t, string(rotatedContent), "partial")
	for _, line := range splitJSONLines(rotatedContent) {
		assert.True(t, json.Valid(line))
	}

	old := path + ".old"
	newer := path + ".newer"
	require.NoError(t, os.WriteFile(old, []byte("old\n"), 0o600))
	require.NoError(t, os.WriteFile(newer, []byte("new\n"), 0o600))
	require.NoError(t, os.Chtimes(old, base.Add(-48*time.Hour), base.Add(-48*time.Hour)))
	require.NoError(t, os.Chtimes(newer, base.Add(-time.Hour), base.Add(-time.Hour)))
	require.NoError(t, writer.applyRetention())
	_, oldErr := os.Stat(old)
	assert.True(t, os.IsNotExist(oldErr))
	remaining, err := filepath.Glob(path + ".*")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.NoError(t, writer.Close())
}

func TestStructuredLogSizeAndTimeRotationDecisions(t *testing.T) {
	writer := &safeRotatingFile{
		config:   safeRotatingFileConfig{maxSizeMegabytes: 1, rotationInterval: time.Hour},
		size:     1024 * 1024,
		now:      func() time.Time { return time.Date(2026, 8, 27, 13, 0, 0, 0, time.UTC) },
		openedAt: time.Date(2026, 8, 27, 12, 30, 0, 0, time.UTC),
	}
	assert.True(t, writer.shouldRotate(1), "size threshold must rotate")
	writer.size = 1
	assert.False(t, writer.shouldRotate(1), "neither threshold is reached")
	writer.openedAt = time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)
	assert.True(t, writer.shouldRotate(1), "time threshold must rotate")
}

func TestStructuredLogRotationCompressionProducesValidJSONLines(t *testing.T) {
	path := filepath.Join(realTempDir(t), "events[qa].jsonl")
	writer, err := newSafeRotatingFile(safeRotatingFileConfig{
		filename: path, compress: true, maxBackups: 1, rotationInterval: time.Hour,
	})
	require.NoError(t, err)
	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	current := base
	writer.openedAt = base
	writer.now = func() time.Time { return current }
	require.NoError(t, writer.WriteLine([]byte("{\"first\":true}\n")))
	current = base.Add(2 * time.Hour)
	require.NoError(t, writer.WriteLine([]byte("{\"second\":true}\n")))
	require.NoError(t, writer.Close())

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	var compressedPath string
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) && entry.Name() != filepath.Base(path)+".gz" &&
			len(entry.Name()) > len(filepath.Base(path)) && entry.Name()[len(entry.Name())-3:] == ".gz" {
			compressedPath = filepath.Join(filepath.Dir(path), entry.Name())
		}
	}
	require.NotEmpty(t, compressedPath)
	compressed, err := os.Open(compressedPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = compressed.Close() })
	reader, err := gzip.NewReader(compressed)
	require.NoError(t, err)
	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	for _, line := range splitJSONLines(content) {
		assert.True(t, json.Valid(line))
	}
}

type failingDestination struct{ message string }

func (d failingDestination) WriteLine([]byte) error { return errors.New(d.message) }
func (failingDestination) Flush() error             { return nil }
func (failingDestination) Close() error             { return nil }

type toggleDestination struct {
	mu  sync.Mutex
	err error
}

func (d *toggleDestination) WriteLine([]byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}
func (*toggleDestination) Flush() error { return nil }
func (*toggleDestination) Close() error { return nil }

func (d *toggleDestination) setError(err error) {
	d.mu.Lock()
	d.err = err
	d.mu.Unlock()
}

func TestStructuredLogDiskFailureIsRedactedAndVisible(t *testing.T) {
	service := &structuredLogService{
		instance: "instance-a", queue: make(chan queuedStructuredLog, 1), stop: make(chan struct{}), done: make(chan struct{}),
		destinations:    map[string]structuredLogDestination{"application": failingDestination{message: "token=disk-secret write denied"}},
		configuredCount: 1, flushInterval: time.Hour, rotationInterval: time.Hour,
	}
	service.write(queuedStructuredLog{category: "application", line: []byte("{}\n")})
	diagnostics := service.Diagnostics()
	assert.Equal(t, pro_interfaces.StructuredLogFailed, diagnostics.State)
	assert.Contains(t, diagnostics.LastWriteError, "token=[REDACTED]")
	assert.NotContains(t, diagnostics.LastWriteError, "disk-secret")
}

func TestStructuredLogDestinationFailureIsClearedOnlyByItsOwnRecovery(t *testing.T) {
	application := &toggleDestination{err: errors.New("application unavailable")}
	task := &toggleDestination{}
	service := &structuredLogService{
		destinations:  map[string]structuredLogDestination{"application": application, "task": task},
		writeFailures: map[string]bool{}, configuredCount: 2,
		queue: make(chan queuedStructuredLog, 1), flushInterval: time.Hour, rotationInterval: time.Hour,
	}
	service.write(queuedStructuredLog{category: "application", line: []byte("{}\n")})
	service.write(queuedStructuredLog{category: "task", line: []byte("{}\n")})
	assert.Equal(t, pro_interfaces.StructuredLogFailed, service.Diagnostics().State)

	application.setError(nil)
	service.write(queuedStructuredLog{category: "application", line: []byte("{}\n")})
	assert.Equal(t, pro_interfaces.StructuredLogHealthy, service.Diagnostics().State)
}

func TestStructuredLogDisabledConfigurationRemainsNoOp(t *testing.T) {
	service := NewLogWriteServiceWithConfig(nil, "instance-a")
	require.NoError(t, service.WriteEventLog(pro_interfaces.EventLogRecord{Action: "ignored"}))
	require.NoError(t, service.Close())
	diagnostics := service.Diagnostics()
	assert.False(t, diagnostics.Enabled)
	assert.Equal(t, pro_interfaces.StructuredLogDisabled, diagnostics.State)
}

func realTempDir(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return directory
}
