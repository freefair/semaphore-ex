package pro_interfaces

import (
	"fmt"
	"time"
)

const DebugComponentTaskPool = "task_pool"

// DebugFilter evaluates stable component names for one running instance.
// Implementations must be safe for concurrent reads while configuration reloads.
type DebugFilter interface {
	Enabled(component string) bool
	Diagnostics() DebugFilterDiagnostics
}

type DebugFilterRejectedEntry struct {
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

type DebugFilterDiagnostics struct {
	Instance    string                     `json:"instance"`
	Default     string                     `json:"default"`
	Configured  []string                   `json:"configured"`
	Effective   []string                   `json:"effective"`
	Rejected    []DebugFilterRejectedEntry `json:"rejected"`
	ReloadedAt  time.Time                  `json:"reloaded_at"`
	ReloadError string                     `json:"reload_error,omitempty"`
}

type DebugFieldsBuilder func() map[string]any

type DebugLogRecord struct {
	Component     string
	EventType     string
	CorrelationID string
	ProjectID     *int
	Fields        DebugFieldsBuilder
}

func (r DebugLogRecord) Validate() error {
	if !identifierPattern.MatchString(r.Component) || !identifierPattern.MatchString(r.EventType) {
		return fmt.Errorf("invalid debug log identifier")
	}
	return nil
}

// DebugLogService keeps filter evaluation ahead of field construction and
// structured serialization. Non-debug log methods are deliberately separate.
type DebugLogService interface {
	WriteDebug(DebugLogRecord) error
	DebugFilterDiagnostics() DebugFilterDiagnostics
}
