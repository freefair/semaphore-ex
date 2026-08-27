package pro_interfaces

import (
	"time"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

type LogWriteService interface {
	WriteEventLog(event EventLogRecord) error
	WriteTaskLog(task TaskLogRecord) error
	WriteResult(task any) error
}

type LogWriteServiceLifecycle interface {
	LogWriteService
	DebugLogService
	Diagnostics() StructuredLogDiagnostics
	Close() error
}

type StructuredLogState string

const (
	StructuredLogDisabled StructuredLogState = "disabled"
	StructuredLogHealthy  StructuredLogState = "healthy"
	StructuredLogDropping StructuredLogState = "dropping"
	StructuredLogFailed   StructuredLogState = "failed"
)

type StructuredLogDestinationDiagnostics struct {
	Category         string `json:"category"`
	Enabled          bool   `json:"enabled"`
	Filename         string `json:"filename,omitempty"`
	MaxSizeMegabytes int    `json:"max_size_megabytes,omitempty"`
	MaxAgeDays       int    `json:"max_age_days,omitempty"`
	MaxBackups       int    `json:"max_backups,omitempty"`
	Compress         bool   `json:"compress,omitempty"`
}

type StructuredLogDiagnostics struct {
	Enabled             bool                                  `json:"enabled"`
	State               StructuredLogState                    `json:"state"`
	QueueDepth          int                                   `json:"queue_depth"`
	QueueCapacity       int                                   `json:"queue_capacity"`
	DroppedRecords      uint64                                `json:"dropped_records"`
	LastWriteError      string                                `json:"last_write_error,omitempty"`
	LastSuccessfulFlush *time.Time                            `json:"last_successful_flush,omitempty"`
	FlushInterval       string                                `json:"flush_interval,omitempty"`
	RotationInterval    string                                `json:"rotation_interval,omitempty"`
	Destinations        []StructuredLogDestinationDiagnostics `json:"destinations"`
}

type ResultLogRecord struct {
	TaskID        int    `json:"task"`
	ProjectID     int    `json:"project"`
	CorrelationID string `json:"correlation_id,omitempty"`
	EventType     string `json:"event_type"`
	Result        any    `json:"result"`
}

type StructuredLogEnvelope struct {
	Version       int       `json:"version"`
	Schema        string    `json:"schema"`
	Timestamp     time.Time `json:"timestamp"`
	Instance      string    `json:"instance"`
	CorrelationID string    `json:"correlation_id"`
	ProjectID     *int      `json:"project,omitempty"`
	EventType     string    `json:"event_type"`
	Payload       any       `json:"payload"`
}

type EventLogRecord struct {
	EventID       string    `json:"event_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at,omitempty"`
	Action        string    `json:"action"`
	UserID        *int      `json:"user,omitempty"`
	IntegrationID *int      `json:"integration,omitempty"`
	ProjectID     *int      `json:"project,omitempty"`
	Description   *string   `json:"description,omitempty"`

	CorrelationID string          `json:"correlation_id,omitempty"`
	TargetType    AuditTargetType `json:"target_type,omitempty"`
	TargetID      string          `json:"target_id,omitempty"`
	Outcome       AuditOutcome    `json:"outcome,omitempty"`
	Source        AuditSource     `json:"source,omitempty"`
	SourceIP      string          `json:"source_ip,omitempty"`
	UserAgent     string          `json:"user_agent,omitempty"`
	Reason        string          `json:"reason,omitempty"`
}

type TaskLogRecord struct {
	Username     string                 `json:"username,omitempty"`
	TaskID       int                    `json:"task"`
	ProjectID    int                    `json:"project"`
	TemplateID   int                    `json:"template"`
	TemplateName string                 `json:"template_name"`
	UserID       *int                   `json:"user,omitempty"`
	Description  *string                `json:"-"`
	RunnerID     *int                   `json:"runner,omitempty"`
	Status       task_logger.TaskStatus `json:"status"`
}
