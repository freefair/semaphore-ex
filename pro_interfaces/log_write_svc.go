package pro_interfaces

import "github.com/semaphoreui/semaphore/pkg/task_logger"

type LogWriteService interface {
	WriteEventLog(event EventLogRecord) error
	WriteTaskLog(task TaskLogRecord) error
	WriteResult(task any) error
}

type EventLogRecord struct {
	Action        string  `json:"action"`
	UserID        *int    `json:"user,omitempty"`
	IntegrationID *int    `json:"integration,omitempty"`
	ProjectID     *int    `json:"project,omitempty"`
	Description   *string `json:"description,omitempty"`

	CorrelationID string          `json:"correlation_id,omitempty"`
	TargetType    AuditTargetType `json:"target_type,omitempty"`
	TargetID      string          `json:"target_id,omitempty"`
	Outcome       AuditOutcome    `json:"outcome,omitempty"`
	Source        AuditSource     `json:"source,omitempty"`
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
