package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

// SetExecutorImageCapabilityResolver injects the replaceable-edition entitlement decision.
func (p *TaskPool) SetExecutorImageCapabilityResolver(resolver func(*db.User) bool) {
	p.executorImageAvailable = resolver
}

func (p *TaskPool) writeStructuredDebug(record pro_interfaces.DebugLogRecord) {
	debugWriter, ok := p.logWriteService.(pro_interfaces.DebugLogService)
	if !ok {
		return
	}
	if err := debugWriter.WriteDebug(record); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"context": record.Component, "event_type": record.EventType,
		}).Warn("failed to enqueue structured debug log")
	}
}
