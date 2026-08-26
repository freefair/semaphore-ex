package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type EventRepository interface {
	CreateEvent(db.Event) (db.Event, error)
}

type serviceFacade struct {
	repository EventRepository
	logWriter  pro_interfaces.LogWriteService
	metrics    *metrics.Metrics
}

func NewServiceFacade(
	repository EventRepository,
	logWriter pro_interfaces.LogWriteService,
	appMetrics *metrics.Metrics,
) pro_interfaces.AuditServiceFacade {
	return &serviceFacade{repository: repository, logWriter: logWriter, metrics: appMetrics}
}

func (f *serviceFacade) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	if err := event.Validate(); err != nil {
		f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkDatabase, pro_interfaces.DroppedRecordInvalid)
		return fmt.Errorf("invalid audit record")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkDatabase, pro_interfaces.DroppedRecordInvalid)
		return fmt.Errorf("audit encoding failed")
	}
	description := string(payload)
	objectType := db.EventCapability
	if event.TargetType == pro_interfaces.AuditTargetProjectRunner {
		objectType = db.EventProjectRunnerAudit
	}

	databaseStart := time.Now()
	_, databaseErr := f.repository.CreateEvent(db.Event{
		UserID:      event.ActorID,
		ProjectID:   event.ProjectID,
		ObjectType:  &objectType,
		Description: &description,
	})
	f.metrics.ObserveDependency(pro_interfaces.DependencyAuditDatabase, time.Since(databaseStart), databaseErr == nil)
	if databaseErr != nil {
		f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkDatabase, pro_interfaces.DroppedRecordWriteFailure)
	}

	fileStart := time.Now()
	fileErr := f.logWriter.WriteEventLog(pro_interfaces.EventLogRecord{
		Action:        string(event.Action),
		UserID:        event.ActorID,
		ProjectID:     event.ProjectID,
		Description:   &description,
		CorrelationID: event.CorrelationID,
		TargetType:    event.TargetType,
		TargetID:      event.TargetID,
		Outcome:       event.Outcome,
		Source:        event.Source,
		Reason:        event.Reason,
	})
	f.metrics.ObserveDependency(pro_interfaces.DependencyAuditFile, time.Since(fileStart), fileErr == nil)
	if fileErr != nil {
		f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkFile, pro_interfaces.DroppedRecordWriteFailure)
	}

	f.metrics.RecordEnhancedAction(event)
	if databaseErr != nil || fileErr != nil {
		return fmt.Errorf("audit persistence failed")
	}
	return nil
}

var _ pro_interfaces.AuditServiceFacade = (*serviceFacade)(nil)
