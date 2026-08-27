package server

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// LogWriteServiceImpl preserves the Community no-op structured log contract.
type LogWriteServiceImpl struct{}

var _ pro_interfaces.LogWriteServiceLifecycle = (*LogWriteServiceImpl)(nil)

func NewLogWriteService() pro_interfaces.LogWriteServiceLifecycle {
	return &LogWriteServiceImpl{}
}

func (*LogWriteServiceImpl) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (*LogWriteServiceImpl) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (*LogWriteServiceImpl) WriteResult(any) error                             { return nil }
