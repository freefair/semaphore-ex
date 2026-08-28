package stage_parsers

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/semaphoreui/semaphore/db"
)

const (
	taskSummaryLinePrefix = "SEMAPHORE_TASK_RESULT "
	maxSummaryErrorBytes  = 4096
)

var (
	ansiEscapePattern   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	secretValuePatterns = []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{regexp.MustCompile(`(?i)("?authorization"?\s*[:=]\s*)(?:bearer\s+)?[^\s,}\]]+`), `${1}[REDACTED]`},
		{regexp.MustCompile(`(?i)("?(?:password|passwd|token|secret|api[_-]?key|private[_-]?key)"?\s*[:=]\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,}\]]+)`), `${1}[REDACTED]`},
		{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`), `${1}[REDACTED]`},
		{regexp.MustCompile(`(?i)(https?://)[^/@\s:]+:[^/@\s]+@`), `${1}[REDACTED]@`},
		{regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`), `[REDACTED PRIVATE KEY]`},
	}
)

// ParseTaskSummaryEvent parses the private, versioned JSON Lines contract
// emitted by Semaphore's Ansible callback. Non-contract output is ignored.
func ParseTaskSummaryEvent(line string) (db.TaskSummaryEvent, bool, error) {
	line = strings.TrimSpace(ansiEscapePattern.ReplaceAllString(line, ""))
	if !strings.HasPrefix(line, taskSummaryLinePrefix) {
		return db.TaskSummaryEvent{}, false, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, taskSummaryLinePrefix))
	if len(payload) > db.MaxWorkflowArtifactEventBytes {
		return db.TaskSummaryEvent{}, true, errors.New("structured task result exceeds the supported size")
	}
	var event db.TaskSummaryEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return db.TaskSummaryEvent{}, true, fmt.Errorf("decode task summary event: %w", err)
	}

	if event.Version != db.TaskSummarySchemaVersion {
		return event, true, nil
	}

	event.EventID = bounded(strings.TrimSpace(event.EventID), 255)
	event.PlayID = bounded(strings.TrimSpace(event.PlayID), 255)
	event.StageID = bounded(strings.TrimSpace(event.StageID), 255)
	event.Stage = bounded(normalizeLabel(event.Stage), 255)
	event.Host = normalizeHost(event.Host)
	event.Status = strings.ToLower(bounded(strings.TrimSpace(event.Status), 32))
	event.Error = RedactTaskSummaryError(event.Error)

	if event.EventID == "" {
		return db.TaskSummaryEvent{}, true, errors.New("task summary event_id is required")
	}
	if event.Kind != db.TaskSummaryEventResult && event.Kind != db.TaskSummaryEventHost &&
		event.Kind != db.TaskSummaryEventComplete && event.Kind != db.TaskSummaryEventFailure &&
		event.Kind != db.TaskSummaryEventWorkflowOutputs {
		return db.TaskSummaryEvent{}, true, fmt.Errorf("unsupported task summary event kind %q", event.Kind)
	}
	if (event.Kind == db.TaskSummaryEventResult || event.Kind == db.TaskSummaryEventHost) && event.Host == "" {
		return db.TaskSummaryEvent{}, true, errors.New("task summary host is required")
	}
	if event.DurationMS < 0 || event.ExpectedHosts < 0 || hasNegativeCounter(event) {
		return db.TaskSummaryEvent{}, true, errors.New("task summary counters and timing must not be negative")
	}
	if event.Kind == db.TaskSummaryEventResult && !validResultStatus(event.Status) {
		return db.TaskSummaryEvent{}, true, fmt.Errorf("unsupported task result status %q", event.Status)
	}
	if event.Kind == db.TaskSummaryEventHost && !validHostStatus(event.Status) {
		return db.TaskSummaryEvent{}, true, fmt.Errorf("unsupported host summary status %q", event.Status)
	}
	if event.Kind == db.TaskSummaryEventWorkflowOutputs {
		if len(event.Outputs) > db.MaxWorkflowArtifactsPerNode {
			return db.TaskSummaryEvent{}, true, errors.New("structured workflow output count exceeds the supported limit")
		}
		for name, raw := range event.Outputs {
			if err := db.ValidateWorkflowArtifactName(name); err != nil {
				return db.TaskSummaryEvent{}, true, err
			}
			if len(raw) > db.MaxWorkflowArtifactBytes {
				return db.TaskSummaryEvent{}, true, fmt.Errorf("structured workflow output %q exceeds the supported size", name)
			}
		}
	}
	return event, true, nil
}

func IngestTaskSummaryOutput(
	repository db.AnsibleTaskRepository,
	projectID int,
	taskID int,
	line string,
	outputTime time.Time,
) (bool, error) {
	event, recognized, err := ParseTaskSummaryEvent(line)
	if err != nil || !recognized {
		return recognized, err
	}
	if repository == nil {
		return true, errTaskSummaryRepositoryMissing
	}
	return true, repository.IngestTaskSummaryEvent(projectID, taskID, event, outputTime)
}

var errTaskSummaryRepositoryMissing = errors.New("task summary repository is not configured")

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, ".")
	return bounded(host, 255)
}

func normalizeLabel(value string) string {
	return strings.Join(strings.FieldsFunc(strings.TrimSpace(value), unicode.IsSpace), " ")
}

func bounded(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && (value[maxBytes]&0xc0) == 0x80 {
		maxBytes--
	}
	return value[:maxBytes]
}

func hasNegativeCounter(event db.TaskSummaryEvent) bool {
	return event.Changed < 0 || event.Failed < 0 || event.Ignored < 0 || event.Ok < 0 ||
		event.Rescued < 0 || event.Skipped < 0 || event.Unreachable < 0
}

func validResultStatus(status string) bool {
	switch status {
	case "ok", "changed", "failed", "ignored", "rescued", "skipped", "unreachable":
		return true
	default:
		return false
	}
}

func validHostStatus(status string) bool {
	return status == "success" || status == "failed"
}

// RedactTaskSummaryError removes common secret-bearing values and bounds the
// persisted diagnostic without mutating the ordinary task log.
func RedactTaskSummaryError(value string) string {
	value = ansiEscapePattern.ReplaceAllString(value, "")
	for _, redaction := range secretValuePatterns {
		value = redaction.pattern.ReplaceAllString(value, redaction.replacement)
	}
	return bounded(strings.TrimSpace(value), maxSummaryErrorBytes)
}

// TaskSummaryCollectionFailureOutput creates a persisted, redacted diagnostic
// for failures that prevent the callback from collecting runner events.
func TaskSummaryCollectionFailureOutput(collectionError error) string {
	event := db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion,
		Kind:    db.TaskSummaryEventFailure,
		EventID: "collection:setup",
		Error:   RedactTaskSummaryError(collectionError.Error()),
	}
	payload, _ := json.Marshal(event)
	return taskSummaryLinePrefix + string(payload)
}

// TaskSummaryCallbackEnvironment installs the bundled aggregate callback and
// returns environment variables that preserve the user's stdout callback.
func TaskSummaryCallbackEnvironment(directory string, environment []string) ([]string, error) {
	if directory == "" {
		return nil, errors.New("task summary callback directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create task summary callback directory: %w", err)
	}
	pluginPath := filepath.Join(directory, "semaphore_task_summary.py")
	if err := writeCallbackIfChanged(pluginPath, []byte(ansibleTaskSummaryCallback)); err != nil {
		return nil, err
	}
	plugins := appendEnvironmentList(
		environmentValue(environment, "ANSIBLE_CALLBACK_PLUGINS"),
		directory,
		string(os.PathListSeparator),
	)
	callbacks := appendEnvironmentList(
		environmentValue(environment, "ANSIBLE_CALLBACKS_ENABLED"),
		"semaphore_task_summary",
		",",
	)
	environment = withoutEnvironmentKeys(
		environment,
		"ANSIBLE_CALLBACK_PLUGINS",
		"ANSIBLE_CALLBACKS_ENABLED",
	)
	return append(environment,
		"ANSIBLE_CALLBACK_PLUGINS="+plugins,
		"ANSIBLE_CALLBACKS_ENABLED="+callbacks,
	), nil
}

func writeCallbackIfChanged(path string, content []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && sha256.Sum256(existing) == sha256.Sum256(content) {
		return os.Chmod(path, 0o600)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read task summary callback: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".semaphore-task-summary-")
	if err != nil {
		return fmt.Errorf("create temporary task summary callback: %w", err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(content)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write task summary callback: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("install task summary callback: %w", err)
	}
	return nil
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func withoutEnvironmentKeys(environment []string, keys ...string) []string {
	result := make([]string, 0, len(environment))
	for _, value := range environment {
		matched := false
		for _, key := range keys {
			if strings.HasPrefix(value, key+"=") {
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, value)
		}
	}
	return result
}

func appendEnvironmentList(current string, value string, separator string) string {
	for _, item := range strings.Split(current, separator) {
		if strings.TrimSpace(item) == value {
			return current
		}
	}
	if strings.TrimSpace(current) == "" {
		return value
	}
	return current + separator + value
}

const ansibleTaskSummaryCallback = `# Semaphore task-summary callback contract version 1.
from __future__ import annotations

import datetime
import json
import time

from ansible.plugins.callback import CallbackBase


DOCUMENTATION = r'''---
callback: semaphore_task_summary
type: aggregate
short_description: Emits versioned Semaphore task result records
version_added: "2.11"
requirements:
  - Set as an enabled callback by Semaphore
'''


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = "aggregate"
    CALLBACK_NAME = "semaphore_task_summary"
    CALLBACK_NEEDS_ENABLED = True

    def __init__(self):
        super().__init__()
        self._started = {}
        self._attempts = {}
        self._play_id = ""

    def _now(self):
        return datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")

    def _emit(self, event):
        event["version"] = 1
        self._display.display("SEMAPHORE_TASK_RESULT " + json.dumps(event, separators=(",", ":"), sort_keys=True))

    def v2_playbook_on_play_start(self, play):
        self._play_id = str(getattr(play, "_uuid", ""))

    def v2_runner_on_start(self, host, task):
        self._started[(host.get_name(), str(getattr(task, "_uuid", "")))] = (time.monotonic(), self._now())

    def _result(self, result, status, ignore_errors=False):
        host = result._host.get_name()
        task = result._task
        task_id = str(getattr(task, "_uuid", ""))
        started = self._started.pop((host, task_id), None)
        ended = self._now()
        duration_ms = 0
        started_at = None
        if started:
            duration_ms = max(0, int((time.monotonic() - started[0]) * 1000))
            started_at = started[1]
        data = getattr(result, "_result", {}) or {}
        error = ""
        if status in ("failed", "unreachable"):
            for key in ("msg", "stderr", "module_stderr", "exception"):
                if data.get(key):
                    error = str(data[key])
                    break
        if status == "failed" and ignore_errors:
            status = "ignored"
        attempt_key = (host, task_id)
        attempt = self._attempts.get(attempt_key, 0) + 1
        self._attempts[attempt_key] = attempt
        event_id = "%s:%s:%d" % (task_id, host, attempt)
        self._emit({
            "event": "task_result", "event_id": event_id, "play_id": self._play_id,
            "stage_id": task_id, "stage": task.get_name().strip(), "host": host,
            "status": status, "changed": 1 if data.get("changed") else 0,
            "started_at": started_at, "ended_at": ended, "duration_ms": duration_ms,
            "error": error,
        })

    def v2_runner_on_ok(self, result):
        self._result(result, "changed" if (result._result or {}).get("changed") else "ok")

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._result(result, "failed", ignore_errors)

    def v2_runner_on_unreachable(self, result):
        self._result(result, "unreachable")

    def v2_runner_on_skipped(self, result):
        self._result(result, "skipped")

    def v2_playbook_on_stats(self, stats):
        custom = getattr(stats, "custom", {}) or {}
        run_stats = custom.get("_run", {}) or {}
        outputs = run_stats.get("semaphore_workflow_outputs")
        if isinstance(outputs, dict):
            self._emit({"event": "workflow_outputs", "event_id": "run:outputs", "outputs": outputs})
        hosts = sorted(stats.processed.keys())
        for host in hosts:
            summary = stats.summarize(host)
            failed = int(summary.get("failures", 0))
            unreachable = int(summary.get("unreachable", 0))
            self._emit({
                "event": "host_summary", "event_id": "host:%s" % host, "host": host,
                "status": "failed" if failed or unreachable else "success",
                "changed": int(summary.get("changed", 0)), "failed": failed,
                "ignored": int(summary.get("ignored", 0)), "ok": int(summary.get("ok", 0)),
                "rescued": int(summary.get("rescued", 0)), "skipped": int(summary.get("skipped", 0)),
                "unreachable": unreachable,
            })
        self._emit({"event": "run_complete", "event_id": "run:complete", "expected_hosts": len(hosts)})
`
