package server

import (
	"os"
	"time"

	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

// LogWriteServiceImpl preserves the Community no-op structured log contract.
type LogWriteServiceImpl struct {
	debugFilter pro_interfaces.DebugFilter
}

var _ pro_interfaces.LogWriteServiceLifecycle = (*LogWriteServiceImpl)(nil)

func NewLogWriteService() pro_interfaces.LogWriteServiceLifecycle {
	instance := "semaphore"
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		instance = hostname
	}
	spec := ""
	if util.Config != nil && util.Config.Log != nil {
		spec = util.Config.Log.DebugFilter
	}
	return NewLogWriteServiceWithFilter(debuglog.NewManager(instance, spec, time.Now().UTC()))
}

func NewLogWriteServiceWithFilter(filter pro_interfaces.DebugFilter) pro_interfaces.LogWriteServiceLifecycle {
	return &LogWriteServiceImpl{debugFilter: filter}
}

func (*LogWriteServiceImpl) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (*LogWriteServiceImpl) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (*LogWriteServiceImpl) WriteResult(any) error                             { return nil }
func (*LogWriteServiceImpl) WriteDebug(pro_interfaces.DebugLogRecord) error    { return nil }

func (s *LogWriteServiceImpl) DebugFilterDiagnostics() pro_interfaces.DebugFilterDiagnostics {
	if s.debugFilter == nil {
		return pro_interfaces.DebugFilterDiagnostics{
			Default: debuglog.DebugFilterDefaultAll, Configured: []string{}, Effective: []string{"*"},
			Rejected: []pro_interfaces.DebugFilterRejectedEntry{},
		}
	}
	return s.debugFilter.Diagnostics()
}

func (*LogWriteServiceImpl) Diagnostics() pro_interfaces.StructuredLogDiagnostics {
	return pro_interfaces.StructuredLogDiagnostics{
		State:        pro_interfaces.StructuredLogDisabled,
		Destinations: []pro_interfaces.StructuredLogDestinationDiagnostics{},
	}
}

func (*LogWriteServiceImpl) Close() error { return nil }
