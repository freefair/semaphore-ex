package server

import (
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (*LogWriteServiceImpl) Diagnostics() pro_interfaces.StructuredLogDiagnostics {
	return pro_interfaces.StructuredLogDiagnostics{
		State:        pro_interfaces.StructuredLogDisabled,
		Destinations: []pro_interfaces.StructuredLogDestinationDiagnostics{},
	}
}

func (*LogWriteServiceImpl) Close() error { return nil }
