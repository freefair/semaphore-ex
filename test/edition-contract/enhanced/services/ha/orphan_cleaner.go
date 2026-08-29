package ha

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	log "github.com/sirupsen/logrus"
)

type taskRecoveryPool interface {
	GetOwnedRunningTasks() []*tasks.TaskRunner
	GetTask(int) (*tasks.TaskRunner, error)
	ApplyOrphanRecovery(*tasks.TaskRunner, pro_interfaces.TaskControlLease, pro_interfaces.TaskRecoveryAssessment) bool
	RevokeOrphanedTaskAssignment(*tasks.TaskRunner, pro_interfaces.TaskControlLease, string) bool
}

type managedOrphanCleaner struct {
	repository     pro_interfaces.TaskControlRecoveryRepository
	pool           taskRecoveryPool
	ownerBootID    string
	ready          func() (bool, error)
	interval       time.Duration
	leaseTTL       time.Duration
	maxEvidenceAge time.Duration

	mu       sync.Mutex
	leases   map[int]pro_interfaces.TaskControlLease
	draining bool
	stop     chan struct{}
	done     chan struct{}
	start    sync.Once
	shutdown sync.Once
}

var _ pro_interfaces.OrphanCleaner = (*managedOrphanCleaner)(nil)

func NewManagedOrphanCleaner(
	repository pro_interfaces.TaskControlRecoveryRepository,
	pool taskRecoveryPool,
	ownerBootID string,
	ready func() (bool, error),
	interval time.Duration,
	leaseTTL time.Duration,
	maxEvidenceAge time.Duration,
) *managedOrphanCleaner {
	return &managedOrphanCleaner{
		repository: repository, pool: pool, ownerBootID: ownerBootID, ready: ready,
		interval: interval, leaseTTL: leaseTTL, maxEvidenceAge: maxEvidenceAge,
		leases: make(map[int]pro_interfaces.TaskControlLease), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

func (c *managedOrphanCleaner) Start() {
	c.start.Do(func() { go c.run() })
}

func (c *managedOrphanCleaner) Stop() {
	c.shutdown.Do(func() {
		close(c.stop)
		c.start.Do(func() { close(c.done) })
		<-c.done
		if err := c.Drain(); err != nil {
			log.WithError(err).Warn("failed to relinquish task controls during shutdown")
		}
	})
}

func (c *managedOrphanCleaner) Drain() error {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()
	return c.releaseAll()
}

func (c *managedOrphanCleaner) Resume() {
	c.mu.Lock()
	c.draining = false
	c.mu.Unlock()
}

func (c *managedOrphanCleaner) TaskRecoveryDiagnostics(taskID int) (pro_interfaces.TaskRecoveryDiagnostics, bool, error) {
	record, found, err := c.repository.GetTaskControlRecovery(taskID)
	if err != nil || !found {
		return pro_interfaces.TaskRecoveryDiagnostics{}, found, err
	}
	diagnostics := pro_interfaces.TaskRecoveryDiagnostics{
		Controlled: true, OwnerBootID: record.Lease.OwnerBootID,
		PreviousOwnerBootID: record.PreviousOwnerBootID, FencingToken: record.Lease.FencingToken,
		LeaseExpiresAt: record.Lease.ExpiresAt, OwnershipTransferredAt: record.OwnershipTransferredAt,
		RunnerID: record.Lease.Execution.RunnerID, AssignmentGeneration: record.Lease.Execution.Generation,
		EvidenceState: record.Evidence.State, EvidenceTerminalStatus: record.Evidence.TerminalStatus,
		EvidenceObservedAt: record.EvidenceObservedAt, AssignmentRevokedAt: record.AssignmentRevokedAt,
		RecoveryDecidedAt: record.RecoveryDecidedAt,
	}
	if record.LastAssessment != nil {
		diagnostics.RecoveryDecision = record.LastAssessment.Decision
		diagnostics.RecoveryReason = record.LastAssessment.Reason
		diagnostics.Quarantined = record.LastAssessment.Decision == pro_interfaces.TaskRecoveryQuarantine
		if diagnostics.Quarantined {
			diagnostics.SafeAction = "retry_recovery"
		}
	}
	return diagnostics, true, nil
}

func (c *managedOrphanCleaner) RetryTaskRecovery(taskID int) error {
	c.mu.Lock()
	lease, tracked := c.leases[taskID]
	c.mu.Unlock()
	if !tracked {
		record, found, err := c.repository.GetTaskControlRecovery(taskID)
		if err != nil {
			return err
		}
		if !found || record.Lease.OwnerBootID != c.ownerBootID {
			return errors.New("task recovery is not owned by this server")
		}
		lease = record.Lease
	}
	record, found, err := c.repository.GetTaskControlRecovery(taskID)
	if err != nil {
		return err
	}
	if !found || record.LastAssessment == nil || record.LastAssessment.Decision != pro_interfaces.TaskRecoveryQuarantine {
		return errors.New("task recovery is not quarantined")
	}
	return c.recoverLease(lease, true)
}

func (c *managedOrphanCleaner) RegisterTaskControl(task db.Task) error {
	if task.RunnerID == nil {
		return errors.New("task control requires a runner assignment")
	}
	execution, err := pro_interfaces.NewTaskExecutionIdentity(task.ID, *task.RunnerID, task.AssignmentGeneration)
	if err != nil {
		return err
	}
	c.mu.Lock()
	draining := c.draining
	c.mu.Unlock()
	if draining {
		return errors.New("task control owner is draining")
	}
	ready, err := c.ready()
	if err != nil {
		return fmt.Errorf("check task control owner readiness: %w", err)
	}
	if !ready {
		return errors.New("task control owner is not ready")
	}
	lease, claimed, err := c.repository.ClaimTaskControl(task.ID, execution, c.ownerBootID, c.leaseTTL)
	if err != nil {
		return err
	}
	if !claimed || lease.OwnerBootID != c.ownerBootID {
		return errors.New("task control is owned by another server")
	}
	c.track(lease)
	return nil
}

func (c *managedOrphanCleaner) ReleaseTaskControl(taskID int) {
	c.mu.Lock()
	lease, exists := c.leases[taskID]
	delete(c.leases, taskID)
	c.mu.Unlock()
	if exists {
		if _, err := c.repository.ReleaseTaskControlLease(lease); err != nil {
			log.WithError(err).WithField("task_id", taskID).Warn("failed to release task control")
		}
	}
}

func (c *managedOrphanCleaner) run() {
	defer close(c.done)
	c.tick()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.tick()
		case <-c.stop:
			return
		}
	}
}

func (c *managedOrphanCleaner) tick() {
	c.mu.Lock()
	draining := c.draining
	c.mu.Unlock()
	if draining {
		return
	}
	ready, err := c.ready()
	if err != nil || !ready {
		if releaseErr := c.releaseAll(); releaseErr != nil {
			log.WithError(releaseErr).Warn("failed to relinquish task controls while node is not ready")
		}
		return
	}
	c.renewOwned()
	c.backfillOwned()
	c.claimExpired()
}

func (c *managedOrphanCleaner) renewOwned() {
	c.mu.Lock()
	leases := make([]pro_interfaces.TaskControlLease, 0, len(c.leases))
	for _, lease := range c.leases {
		leases = append(leases, lease)
	}
	c.mu.Unlock()
	for _, lease := range leases {
		renewed, claimed, err := c.repository.ClaimTaskControl(lease.TaskID, lease.Execution, c.ownerBootID, c.leaseTTL)
		if err != nil || !claimed || renewed.OwnerBootID != c.ownerBootID {
			c.forget(lease.TaskID)
			continue
		}
		c.track(renewed)
		c.recover(renewed)
	}
}

func (c *managedOrphanCleaner) backfillOwned() {
	for _, task := range c.pool.GetOwnedRunningTasks() {
		if task == nil || task.Task.Status.IsFinished() || task.Task.RunnerID == nil || task.Task.AssignmentGeneration <= 0 {
			continue
		}
		c.mu.Lock()
		_, tracked := c.leases[task.Task.ID]
		c.mu.Unlock()
		if tracked {
			continue
		}
		if err := c.RegisterTaskControl(task.Task); err != nil {
			log.WithError(err).WithField("task_id", task.Task.ID).Debug("task control backfill was not claimed")
		}
	}
}

func (c *managedOrphanCleaner) claimExpired() {
	records, err := c.repository.ListExpiredTaskControls(100)
	if err != nil {
		log.WithError(err).Warn("failed to list expired task controls")
		return
	}
	for _, expired := range records {
		lease, claimed, claimErr := c.repository.ClaimTaskControl(
			expired.Lease.TaskID, expired.Lease.Execution, c.ownerBootID, c.leaseTTL,
		)
		if claimErr != nil || !claimed || lease.OwnerBootID != c.ownerBootID {
			continue
		}
		c.track(lease)
		c.recover(lease)
	}
}

func (c *managedOrphanCleaner) recover(lease pro_interfaces.TaskControlLease) {
	_ = c.recoverLease(lease, false)
}

func (c *managedOrphanCleaner) recoverLease(lease pro_interfaces.TaskControlLease, retry bool) error {
	record, found, err := c.repository.GetTaskControlRecovery(lease.TaskID)
	if err != nil {
		return err
	}
	if !found || record.Lease.OwnerBootID != c.ownerBootID || record.Lease.FencingToken != lease.FencingToken {
		return errors.New("task recovery lease is no longer current")
	}
	if record.PreviousOwnerBootID == "" || record.PreviousOwnerBootID == c.ownerBootID {
		return nil
	}
	if !retry && record.LastAssessment != nil && record.LastAssessment.Decision == pro_interfaces.TaskRecoveryQuarantine {
		return nil
	}
	task, err := c.pool.GetTask(lease.TaskID)
	if err != nil || task == nil {
		if !c.recordAndApply(nil, lease, pro_interfaces.TaskRecoveryAssessment{
			Decision: pro_interfaces.TaskRecoveryQuarantine, EvidenceState: record.Evidence.State,
			Reason: "controlled task could not be loaded for recovery",
		}) {
			return errors.New("task recovery decision lost its fencing lease")
		}
		return nil
	}
	if task.Task.Status.IsFinished() {
		c.ReleaseTaskControl(task.Task.ID)
		return nil
	}
	if task.Task.AssignmentGeneration != lease.Execution.Generation || runnerIdentity(task.Task) != lease.Execution.RunnerID {
		if !c.recordAndApply(task, lease, pro_interfaces.TaskRecoveryAssessment{
			Decision: pro_interfaces.TaskRecoveryQuarantine, EvidenceState: record.Evidence.State,
			Reason: "task assignment no longer matches the controlled execution",
		}) {
			return errors.New("task recovery quarantine lost its fencing lease")
		}
		return nil
	}
	if (task.Task.Status == task_logger.TaskStartingStatus || task.Task.Status == task_logger.TaskWaitingStatus) && task.Task.RunnerID != nil {
		current, currentErr := c.repository.IsCurrentTaskControlLease(lease)
		if currentErr != nil || !current {
			return errors.New("task recovery lease is no longer current")
		}
		if !c.pool.RevokeOrphanedTaskAssignment(task, lease, "previous task-control owner expired") {
			return errors.New("task recovery assignment revocation was rejected")
		}
		recorded, recordErr := c.repository.RecordTaskAssignmentRevoked(lease)
		if recordErr != nil || !recorded {
			if recordErr != nil {
				return recordErr
			}
			return errors.New("task recovery revocation lost its fencing lease")
		}
		if !c.recordDecision(lease, pro_interfaces.TaskRecoveryAssessment{
			Decision: pro_interfaces.TaskRecoveryObserve, EvidenceState: pro_interfaces.TaskExecutionUnknown,
			Reason: "runner assignment revoked; waiting for a complete post-revocation snapshot",
		}) {
			return errors.New("task recovery observation lost its fencing lease")
		}
		return nil
	}
	assessment := c.assess(record)
	if !c.recordAndApply(task, lease, assessment) {
		return errors.New("task recovery action lost its fencing lease")
	}
	return nil
}

func (c *managedOrphanCleaner) assess(record pro_interfaces.TaskControlRecoveryRecord) pro_interfaces.TaskRecoveryAssessment {
	evidence := record.Evidence
	if evidence.State == pro_interfaces.TaskExecutionTerminal {
		return pro_interfaces.DecideTaskRecovery(record.Lease, evidence)
	}
	minimumObservation := record.OwnershipTransferredAt
	if record.AssignmentRevokedAt != nil {
		minimumObservation = record.AssignmentRevokedAt
	}
	if record.EvidenceObservedAt == nil || minimumObservation == nil || !record.EvidenceObservedAt.After(*minimumObservation) {
		if minimumObservation != nil && c.maxEvidenceAge > 0 && record.DatabaseNow.Sub(*minimumObservation) <= c.maxEvidenceAge {
			return pro_interfaces.TaskRecoveryAssessment{
				Decision: pro_interfaces.TaskRecoveryObserve, EvidenceState: evidence.State,
				Reason: "waiting for a complete runner snapshot after ownership transfer",
			}
		}
		return pro_interfaces.TaskRecoveryAssessment{
			Decision: pro_interfaces.TaskRecoveryQuarantine, EvidenceState: evidence.State,
			Reason: "no complete runner snapshot was observed after ownership transfer",
		}
	}
	if c.maxEvidenceAge > 0 && record.DatabaseNow.Sub(*record.EvidenceObservedAt) > c.maxEvidenceAge {
		return pro_interfaces.TaskRecoveryAssessment{
			Decision: pro_interfaces.TaskRecoveryQuarantine, EvidenceState: evidence.State,
			Reason: "runner execution evidence is stale",
		}
	}
	return pro_interfaces.DecideTaskRecovery(record.Lease, evidence)
}

func (c *managedOrphanCleaner) recordAndApply(task *tasks.TaskRunner, lease pro_interfaces.TaskControlLease, assessment pro_interfaces.TaskRecoveryAssessment) bool {
	if !c.recordDecision(lease, assessment) {
		return false
	}
	if task != nil && !c.pool.ApplyOrphanRecovery(task, lease, assessment) {
		return false
	}
	if assessment.Decision == pro_interfaces.TaskRecoveryRecover {
		c.ReleaseTaskControl(lease.TaskID)
	}
	return true
}

func (c *managedOrphanCleaner) recordDecision(lease pro_interfaces.TaskControlLease, assessment pro_interfaces.TaskRecoveryAssessment) bool {
	renewed, claimed, err := c.repository.ClaimTaskControl(lease.TaskID, lease.Execution, c.ownerBootID, c.leaseTTL)
	if err != nil || !claimed || renewed.OwnerBootID != c.ownerBootID || renewed.FencingToken != lease.FencingToken {
		return false
	}
	c.track(renewed)
	recorded, err := c.repository.RecordTaskRecoveryDecision(renewed, assessment)
	return err == nil && recorded
}

func (c *managedOrphanCleaner) releaseAll() error {
	c.mu.Lock()
	leases := make([]pro_interfaces.TaskControlLease, 0, len(c.leases))
	for _, lease := range c.leases {
		leases = append(leases, lease)
	}
	c.leases = make(map[int]pro_interfaces.TaskControlLease)
	c.mu.Unlock()
	var result error
	for _, lease := range leases {
		if _, err := c.repository.ReleaseTaskControlLease(lease); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (c *managedOrphanCleaner) track(lease pro_interfaces.TaskControlLease) {
	c.mu.Lock()
	c.leases[lease.TaskID] = lease
	c.mu.Unlock()
}

func (c *managedOrphanCleaner) forget(taskID int) {
	c.mu.Lock()
	delete(c.leases, taskID)
	c.mu.Unlock()
}

func runnerIdentity(task db.Task) int {
	if task.RunnerID != nil {
		return *task.RunnerID
	}
	if task.RunnerSnapshotID != nil {
		return *task.RunnerSnapshotID
	}
	return 0
}
