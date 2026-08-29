package server

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const workflowTriggerSchedulerInterval = 15 * time.Second

type workflowTriggerScheduler struct {
	repository db.WorkflowTriggerManager
	service    pro_interfaces.WorkflowTriggerService

	mutex    sync.Mutex
	changed  *sync.Cond
	cancel   context.CancelFunc
	done     chan struct{}
	draining bool
	inflight int
}

var _ pro_interfaces.WorkflowTriggerScheduler = (*workflowTriggerScheduler)(nil)
var _ pro_interfaces.ClusterDrainer = (*workflowTriggerScheduler)(nil)

func NewWorkflowTriggerScheduler(
	repository db.WorkflowTriggerManager,
	service pro_interfaces.WorkflowTriggerService,
) pro_interfaces.WorkflowTriggerScheduler {
	if repository == nil || service == nil {
		return nil
	}
	scheduler := &workflowTriggerScheduler{repository: repository, service: service}
	scheduler.changed = sync.NewCond(&scheduler.mutex)
	return scheduler
}

func (s *workflowTriggerScheduler) Start() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}

func (s *workflowTriggerScheduler) Stop() {
	s.mutex.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mutex.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *workflowTriggerScheduler) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	s.RunOnce(ctx, time.Now().UTC())
	ticker := time.NewTicker(workflowTriggerSchedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			s.RunOnce(ctx, at.UTC())
		}
	}
}

func (s *workflowTriggerScheduler) RunOnce(ctx context.Context, at time.Time) {
	if !s.beginRun() {
		return
	}
	defer s.endRun()
	if _, err := s.repository.DeleteExpiredWorkflowTriggerInvocations(at.UTC(), db.MaxWorkflowTriggerHistoryPage); err != nil {
		log.WithError(err).Warn("failed to prune workflow trigger invocation history")
	}
	triggers, err := s.repository.GetActiveWorkflowScheduleTriggers()
	if err != nil {
		log.WithError(err).Error("failed to load scheduled workflow triggers")
		return
	}
	scheduledAt := at.UTC().Truncate(time.Minute)
	for _, trigger := range triggers {
		schedule, parseErr := cron.ParseStandard(trigger.CronFormat)
		if parseErr != nil {
			log.WithError(parseErr).WithField("workflow_trigger_id", trigger.ID).Warn("ignored invalid scheduled workflow trigger")
			continue
		}
		if !schedule.Next(scheduledAt.Add(-time.Nanosecond)).Equal(scheduledAt) {
			continue
		}
		if _, fireErr := s.service.FireScheduled(ctx, trigger.ProjectID, trigger.WorkflowTemplateID, trigger.ID, scheduledAt); fireErr != nil {
			log.WithError(fireErr).WithField("workflow_trigger_id", trigger.ID).Error("scheduled workflow trigger failed")
		}
	}
}

func (s *workflowTriggerScheduler) Drain() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.draining = true
	for s.inflight > 0 {
		s.changed.Wait()
	}
	return nil
}

func (s *workflowTriggerScheduler) Resume() {
	s.mutex.Lock()
	s.draining = false
	s.changed.Broadcast()
	s.mutex.Unlock()
}

func (s *workflowTriggerScheduler) beginRun() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.draining {
		return false
	}
	s.inflight++
	return true
}

func (s *workflowTriggerScheduler) endRun() {
	s.mutex.Lock()
	s.inflight--
	s.changed.Broadcast()
	s.mutex.Unlock()
}
