package pro_interfaces

import (
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"time"
)

type LogWriteService interface {
	WriteEventLog(event EventLogRecord) error
	WriteTaskLog(task TaskLogRecord) error
	WriteResult(task any) error
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
