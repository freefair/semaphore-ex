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
	CreateEventWithAuditWebhook(db.Event, db.AuditWebhookDelivery) (db.Event, error)
}

type serviceFacade struct {
	repository        EventRepository
	canonicalRecorder db.AuditEventManager
	logWriter         pro_interfaces.LogWriteService
	metrics           *metrics.Metrics
	webhook           pro_interfaces.AuditWebhookService
}

func NewServiceFacade(
	repository EventRepository,
	logWriter pro_interfaces.LogWriteService,
	appMetrics *metrics.Metrics,
	webhook ...pro_interfaces.AuditWebhookService,
) pro_interfaces.AuditServiceFacade {
	var webhookService pro_interfaces.AuditWebhookService
	if len(webhook) > 0 {
		webhookService = webhook[0]
	}
	canonicalRecorder, _ := repository.(db.AuditEventManager)
	return &serviceFacade{
		repository: repository, canonicalRecorder: canonicalRecorder, logWriter: logWriter,
		metrics: appMetrics, webhook: webhookService,
	}
}

func (f *serviceFacade) Record(ctx context.Context, event pro_interfaces.AuditEvent) error {
	event, err := event.EnsureDeliveryMetadata(time.Now())
	if err != nil {
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

	databaseEvent := db.Event{
		UserID:      event.ActorID,
		ProjectID:   event.ProjectID,
		ObjectType:  &objectType,
		Description: &description,
	}
	var delivery *db.AuditWebhookDelivery
	if f.webhook != nil {
		delivery, err = f.webhook.PrepareDelivery(ctx, event)
		if err != nil {
			f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkDatabase, pro_interfaces.DroppedRecordInvalid)
			return fmt.Errorf("audit persistence failed")
		}
	}
	databaseStart := time.Now()
	var databaseErr error
	if delivery == nil {
		_, databaseErr = f.repository.CreateEvent(databaseEvent)
	} else {
		_, databaseErr = f.repository.CreateEventWithAuditWebhook(databaseEvent, *delivery)
		if databaseErr == nil {
			f.webhook.Notify()
		}
	}
	if databaseErr == nil && f.canonicalRecorder != nil {
		_, databaseErr = f.canonicalRecorder.CreateAuditEvent(ctx, canonicalAuditRow(event, description))
	}
	f.metrics.ObserveDependency(pro_interfaces.DependencyAuditDatabase, time.Since(databaseStart), databaseErr == nil)
	if databaseErr != nil {
		f.metrics.RecordDroppedRecord(pro_interfaces.AuditSinkDatabase, pro_interfaces.DroppedRecordWriteFailure)
	}

	fileStart := time.Now()
	fileErr := f.logWriter.WriteEventLog(pro_interfaces.EventLogRecord{
		EventID:       event.EventID,
		OccurredAt:    event.OccurredAt,
		Action:        string(event.Action),
		UserID:        event.ActorID,
		ProjectID:     event.ProjectID,
		Description:   &description,
		CorrelationID: event.CorrelationID,
		TargetType:    event.TargetType,
		TargetID:      event.TargetID,
		Outcome:       event.Outcome,
		Source:        event.Source,
		SourceIP:      event.SourceIP,
		UserAgent:     event.UserAgent,
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

// canonicalAuditRow mirrors the Enhanced allowlisted event into the durable
// audit stream used by /api/audit-log. The legacy activity event remains for
// compatibility, but it is no longer the only database recording path.
func canonicalAuditRow(event pro_interfaces.AuditEvent, metadata string) db.AuditEvent {
	actorID := ""
	actorType := "system"
	if event.ActorID != nil {
		actorID = fmt.Sprintf("%d", *event.ActorID)
		actorType = "user"
	}
	return db.AuditEvent{
		EventID: event.EventID, SchemaVersion: SchemaVersion,
		Category: "enhanced", EventCode: "enhanced." + string(event.Action), Type: string(TypeInfo),
		Action: string(event.Action), Outcome: string(event.Outcome), Reason: string(event.Reason),
		ActorType: actorType, ActorID: actorID, SourceIP: event.SourceIP, UserAgent: event.UserAgent,
		TargetType: string(event.TargetType), TargetID: event.TargetID, ProjectID: event.ProjectID,
		RequestID: event.CorrelationID, InstanceID: "enhanced", Metadata: metadata,
	}
}

var _ pro_interfaces.AuditServiceFacade = (*serviceFacade)(nil)
