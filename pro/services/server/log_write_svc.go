package server

import (
	"github.com/semaphoreui/semaphore/pkg/debuglog"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"os"
	"time"
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

func (*LogWriteServiceImpl) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (*LogWriteServiceImpl) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (*LogWriteServiceImpl) WriteResult(any) error                             { return nil }
