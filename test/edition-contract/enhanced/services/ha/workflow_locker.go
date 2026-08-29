package ha

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type managedWorkflowRunLocker struct {
	repository  pro_interfaces.WorkflowReconciliationRepository
	ownerBootID string
	ttl         time.Duration

	mu       sync.Mutex
	changed  *sync.Cond
	draining bool
	claiming int
	active   int
	held     map[string]struct{}
	starts   map[string]struct{}
}

var _ pro_interfaces.WorkflowRunLocker = (*managedWorkflowRunLocker)(nil)
var _ pro_interfaces.ClusterDrainer = (*managedWorkflowRunLocker)(nil)
var _ pro_interfaces.WorkflowProgressionHealthSource = (*managedWorkflowRunLocker)(nil)

func NewManagedWorkflowRunLocker(repository pro_interfaces.WorkflowReconciliationRepository, ownerBootID string, ttl time.Duration) *managedWorkflowRunLocker {
	locker := &managedWorkflowRunLocker{
		repository: repository, ownerBootID: strings.TrimSpace(ownerBootID), ttl: ttl,
		held: make(map[string]struct{}), starts: make(map[string]struct{}),
	}
	locker.changed = sync.NewCond(&locker.mu)
	return locker
}

func (l *managedWorkflowRunLocker) TryLockRun(projectID int, runID int) (pro_interfaces.WorkflowReconciliationLease, func(), bool, error) {
	if l.repository == nil || l.ownerBootID == "" || l.ttl <= 0 {
		return pro_interfaces.WorkflowReconciliationLease{}, nil, false, errors.New("workflow reconciliation locker is not configured")
	}
	key := fmt.Sprintf("%d:%d", projectID, runID)
	l.mu.Lock()
	if l.draining {
		l.mu.Unlock()
		return pro_interfaces.WorkflowReconciliationLease{}, nil, false, nil
	}
	if _, exists := l.held[key]; exists {
		l.mu.Unlock()
		return pro_interfaces.WorkflowReconciliationLease{}, nil, false, nil
	}
	l.held[key] = struct{}{}
	l.claiming++
	l.mu.Unlock()

	lease, claimed, err := l.repository.ClaimWorkflowReconciliation(projectID, runID, l.ownerBootID, l.ttl)
	l.mu.Lock()
	l.claiming--
	if err != nil || !claimed || lease.OwnerBootID != l.ownerBootID {
		delete(l.held, key)
		l.changed.Broadcast()
		l.mu.Unlock()
		return lease, nil, false, err
	}
	l.active++
	l.changed.Broadcast()
	l.mu.Unlock()

	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	go l.renewWhileHeld(lease, stopRenewal, renewalDone)
	var once sync.Once
	release := func() {
		once.Do(func() {
			close(stopRenewal)
			<-renewalDone
			_, _ = l.repository.ReleaseWorkflowReconciliation(lease)
			l.mu.Lock()
			delete(l.held, key)
			l.active--
			l.changed.Broadcast()
			l.mu.Unlock()
		})
	}
	return lease, release, true, nil
}

func (l *managedWorkflowRunLocker) renewWhileHeld(lease pro_interfaces.WorkflowReconciliationLease, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := l.ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, renewed, err := l.repository.RenewWorkflowReconciliation(lease, l.ttl); err != nil || !renewed {
				return
			}
		case <-stop:
			return
		}
	}
}

func (l *managedWorkflowRunLocker) TryLockStart(projectID int, templateID int) (func(), bool) {
	key := fmt.Sprintf("%d:%d", projectID, templateID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.draining {
		return nil, false
	}
	if _, exists := l.starts[key]; exists {
		return nil, false
	}
	l.starts[key] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.starts, key)
			l.changed.Broadcast()
			l.mu.Unlock()
		})
	}, true
}

func (l *managedWorkflowRunLocker) RecordReconciled(lease pro_interfaces.WorkflowReconciliationLease) error {
	recorded, err := l.repository.RecordWorkflowReconciled(lease)
	if err != nil {
		return err
	}
	if !recorded {
		return errors.New("stale workflow reconciliation owner")
	}
	return nil
}

func (l *managedWorkflowRunLocker) WorkflowProgressionHealth() (pro_interfaces.WorkflowProgressionHealth, error) {
	source, ok := l.repository.(pro_interfaces.WorkflowProgressionHealthSource)
	if !ok {
		return pro_interfaces.WorkflowProgressionHealth{}, errors.New("workflow progression health is unavailable")
	}
	return source.WorkflowProgressionHealth()
}

func (l *managedWorkflowRunLocker) Drain() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.draining = true
	for l.claiming > 0 || l.active > 0 || len(l.starts) > 0 {
		l.changed.Wait()
	}
	return nil
}

func (l *managedWorkflowRunLocker) Resume() {
	l.mu.Lock()
	l.draining = false
	l.changed.Broadcast()
	l.mu.Unlock()
}
