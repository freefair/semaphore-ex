package server

import (
	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func NewLogWriteServiceWithFilter(filter pro_interfaces.DebugFilter) pro_interfaces.LogWriteServiceLifecycle {
	return &LogWriteServiceImpl{debugFilter: filter}
}

func (*LogWriteServiceImpl) WriteDebug(pro_interfaces.DebugLogRecord) error { return nil }

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
