package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const (
	workflowFileArtifactRetentionInterval = time.Minute
	workflowFileArtifactStagingMaxAge     = 24 * time.Hour
	workflowFileArtifactRetentionBatch    = 100
)

type workflowFileArtifactRetentionWorker struct {
	repository pro_interfaces.WorkflowFileArtifactRepository
	audit      pro_interfaces.AuditServiceFacade

	mutex  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

var _ pro_interfaces.WorkflowFileArtifactRetentionWorker = (*workflowFileArtifactRetentionWorker)(nil)

func NewWorkflowFileArtifactRetentionWorker(repository pro_interfaces.WorkflowFileArtifactRepository, audit pro_interfaces.AuditServiceFacade) pro_interfaces.WorkflowFileArtifactRetentionWorker {
	if repository == nil || audit == nil {
		return nil
	}
	return &workflowFileArtifactRetentionWorker{repository: repository, audit: audit}
}

func (worker *workflowFileArtifactRetentionWorker) Start() {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	if worker.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker.cancel = cancel
	worker.done = make(chan struct{})
	go worker.run(ctx, worker.done)
}

func (worker *workflowFileArtifactRetentionWorker) Stop() {
	worker.mutex.Lock()
	cancel := worker.cancel
	done := worker.done
	worker.cancel = nil
	worker.done = nil
	worker.mutex.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (worker *workflowFileArtifactRetentionWorker) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	if err := worker.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.WithError(err).Warn("workflow file artifact retention tick failed")
	}
	ticker := time.NewTicker(workflowFileArtifactRetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := worker.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.WithError(err).Warn("workflow file artifact retention tick failed")
			}
		}
	}
}

func (worker *workflowFileArtifactRetentionWorker) RunOnce(ctx context.Context) error {
	if worker == nil || worker.repository == nil || worker.audit == nil || ctx == nil {
		return db.ErrInvalidOperation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	candidates, err := worker.repository.GetWorkflowFileArtifactExpiryCandidates(workflowFileArtifactRetentionBatch)
	if err != nil {
		return err
	}
	var firstError error
	for _, reference := range candidates {
		if err = ctx.Err(); err != nil {
			return err
		}
		expired, expireErr := worker.repository.ExpireWorkflowFileArtifact(reference)
		if errors.Is(expireErr, pro_interfaces.ErrWorkflowFileArtifactDownloadActive) {
			continue
		}
		if expireErr != nil {
			if firstError == nil {
				firstError = expireErr
			}
			if auditErr := worker.record(reference, pro_interfaces.AuditActionWorkflowFileArtifactExpire, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError); auditErr != nil && firstError == nil {
				firstError = auditErr
			}
			continue
		}
		if expired {
			if auditErr := worker.record(reference, pro_interfaces.AuditActionWorkflowFileArtifactExpire, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonWorkflowFileArtifactExpired); auditErr != nil && firstError == nil {
				firstError = auditErr
			}
		}
	}
	cleanup, reconcileErr := worker.repository.ReconcileStaleWorkflowFileArtifactUploadsDetailed(
		workflowFileArtifactStagingMaxAge, workflowFileArtifactRetentionBatch,
	)
	for _, reference := range cleanup.Reconciled {
		if auditErr := worker.record(reference, pro_interfaces.AuditActionWorkflowFileArtifactCleanup, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonWorkflowFileArtifactUploadCleaned); auditErr != nil && firstError == nil {
			firstError = auditErr
		}
	}
	if cleanup.Failed != nil {
		if auditErr := worker.record(*cleanup.Failed, pro_interfaces.AuditActionWorkflowFileArtifactCleanup, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError); auditErr != nil && firstError == nil {
			firstError = auditErr
		}
	}
	if reconcileErr != nil && firstError == nil {
		firstError = reconcileErr
	}
	return firstError
}

func (worker *workflowFileArtifactRetentionWorker) record(reference db.WorkflowFileArtifactReference, action pro_interfaces.AuditAction, outcome pro_interfaces.AuditOutcome, reason string) error {
	projectID := reference.ProjectID
	event := pro_interfaces.AuditEvent{
		CorrelationID: "internal", ProjectID: &projectID, Action: action,
		TargetType: pro_interfaces.AuditTargetWorkflowFileArtifact,
		TargetID:   fmt.Sprintf("artifact:%d", reference.ArtifactID), Outcome: outcome,
		Source: pro_interfaces.AuditSourceWorker, Reason: reason,
	}
	return worker.audit.Record(context.Background(), event)
}
