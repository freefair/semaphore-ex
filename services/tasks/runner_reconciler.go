package tasks

import (
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// RunnerTaskAction is the reconciler's decision for one dispatched task.
type RunnerTaskAction int

const (
	// RunnerTaskKeep leaves the task alone.
	RunnerTaskKeep RunnerTaskAction = iota
	// RunnerTaskRequeue returns a not-yet-running task to the queue so the
	// normal dispatch loop assigns it to another runner.
	RunnerTaskRequeue
	// RunnerTaskFail fails a running task whose runner is lost.
	RunnerTaskFail
	// RunnerTaskStop completes a cancellation after its runner is lost.
	RunnerTaskStop
)

// DecideRunnerTaskAction classifies a dispatched, unfinished task against its
// runner's liveness. runner == nil means the runner row no longer exists.
//
// Two thresholds with distinct semantics:
//
//   - offlineTimeout (heartbeat staleness): the runner is offline — it gets no
//     new tasks and its "starting" tasks are requeued. Offline does NOT mean
//     its running jobs stopped.
//   - taskFailTimeout: the runner is presumed dead — its "running" tasks are
//     failed. A runner that reconnects within this window kept its in-memory
//     job pool and simply continues; nothing is failed.
//
// A restarted runner (started_at newer than the task's start) provably lost
// its job pool, so its running task is failed immediately — there is nothing
// to wait for. Its starting tasks self-heal: the restarted runner re-pulls
// them from NewJobs.
func DecideRunnerTaskAction(
	status task_logger.TaskStatus,
	taskStart *time.Time,
	runnerAssignedAt *time.Time,
	runner *db.Runner,
	now time.Time,
	offlineTimeout time.Duration,
	taskFailTimeout time.Duration,
) (RunnerTaskAction, string) {

	starting := status == task_logger.TaskStartingStatus || status == task_logger.TaskWaitingStatus
	running := status == task_logger.TaskRunningStatus ||
		status == task_logger.TaskWaitingConfirmation || status == task_logger.TaskConfirmed
	canceling := status == task_logger.TaskStoppingStatus || status == task_logger.TaskRejected

	if !starting && !running && !canceling {
		return RunnerTaskKeep, ""
	}

	if runner == nil {
		if starting {
			return RunnerTaskRequeue, "runner no longer exists"
		}
		if canceling {
			return RunnerTaskStop, "runner disappeared during cancellation"
		}
		return RunnerTaskFail, "runner no longer exists"
	}

	if (running || canceling) && runner.StartedAt != nil && taskStart != nil &&
		runner.StartedAt.After(*taskStart) {
		if canceling {
			return RunnerTaskStop, "runner restarted during cancellation"
		}
		return RunnerTaskFail, "runner restarted and lost the task"
	}

	// A webhook-driven runner may still be booting in response to the dispatch
	// webhook; its heartbeat history (if any) predates this task, so staleness
	// must not requeue a just-dispatched task. Once such a runner reports the
	// task running, its heartbeat is meaningful again and the fail check below
	// applies as usual.
	if starting && runner.Webhook != "" {
		if runnerAssignedAt != nil && now.Sub(*runnerAssignedAt) > taskFailTimeout {
			return RunnerTaskRequeue, "webhook runner did not start before the recovery timeout"
		}
		return RunnerTaskKeep, ""
	}

	if runner.Touched == nil {
		// Never polled. A poll-based runner cannot have been selected without
		// a fresh heartbeat, so a starting task here is safe to give back.
		if starting {
			return RunnerTaskRequeue, "runner never polled the server"
		}
		if runnerAssignedAt != nil && now.Sub(*runnerAssignedAt) > taskFailTimeout {
			if canceling {
				return RunnerTaskStop, "runner never acknowledged cancellation"
			}
			return RunnerTaskFail, "runner never reported the assigned task"
		}
		return RunnerTaskKeep, ""
	}

	silence := now.Sub(*runner.Touched)

	if starting && silence > offlineTimeout {
		return RunnerTaskRequeue, "runner is offline"
	}

	if running && silence > taskFailTimeout {
		return RunnerTaskFail, "runner stopped responding"
	}
	if canceling && silence > offlineTimeout {
		return RunnerTaskStop, "runner stopped responding during cancellation"
	}

	return RunnerTaskKeep, ""
}

type taskRecoveryFencedStore interface {
	UpdateTaskFenced(task db.Task, expectedFencingToken int64) (bool, error)
	UpdateTaskRunnerFenced(
		task db.Task,
		expectedStatus task_logger.TaskStatus,
		expectedRunnerID int,
		expectedGeneration int,
		expectedFencingToken int64,
		outcome db.RunnerAttemptOutcome,
		attemptReason string,
		transitionedAt time.Time,
	) (bool, error)
}

// ApplyOrphanRecovery applies an evidence-derived recovery decision through a
// task-row mutation guarded by the same fencing token as the control lease.
// This closes the pause-after-decision race: once a successor installs a newer
// fence, the former owner cannot change task state even if it resumes later.
func (p *TaskPool) ApplyOrphanRecovery(
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	assessment pro_interfaces.TaskRecoveryAssessment,
) bool {
	if tsk == nil || tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	store, ok := p.store.(taskRecoveryFencedStore)
	if !ok {
		log.WithField("task_id", tsk.Task.ID).Error("task store does not support fenced recovery writes")
		return false
	}
	switch assessment.Decision {
	case pro_interfaces.TaskRecoveryObserve:
		return true
	case pro_interfaces.TaskRecoveryQuarantine:
		return p.quarantineOrphanedTask(store, tsk, lease, assessment.Reason)
	case pro_interfaces.TaskRecoveryRecover:
		if assessment.EvidenceState == pro_interfaces.TaskExecutionTerminal {
			status := task_logger.TaskStatus(assessment.TerminalStatus)
			if !status.IsFinished() || !taskStatusTransitionAllowed(tsk.Task.Status, status) {
				return p.quarantineOrphanedTask(store, tsk, lease, "terminal execution evidence could not be reconciled")
			}
			return p.applyFencedRecoveryTransition(store, tsk, lease, status,
				runnerAttemptOutcomeForStatus(status), assessment.Reason, true, false)
		}
		if !assessment.SafeReplacement {
			return p.quarantineOrphanedTask(store, tsk, lease, "recovery was not proven safe")
		}
		switch tsk.Task.Status {
		case task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskWaitingStatus,
				db.RunnerAttemptActive, assessment.Reason, false, true)
		case task_logger.TaskStoppingStatus, task_logger.TaskRejected:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskStoppedStatus,
				db.RunnerAttemptStopped, assessment.Reason, true, false)
		default:
			return p.applyFencedRecoveryTransition(store, tsk, lease, task_logger.TaskFailStatus,
				db.RunnerAttemptFailed, assessment.Reason, true, false)
		}
	default:
		return p.quarantineOrphanedTask(store, tsk, lease, "recovery decision is invalid")
	}
}

// RevokeOrphanedTaskAssignment conditionally removes a not-yet-running runner
// assignment without enqueueing a replacement. Recovery must then wait for a
// complete runner snapshot observed after this revocation before requeueing.
func (p *TaskPool) RevokeOrphanedTaskAssignment(tsk *TaskRunner, lease pro_interfaces.TaskControlLease, reason string) bool {
	if tsk == nil || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	store, ok := p.store.(taskRecoveryFencedStore)
	if !ok {
		return false
	}
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status != task_logger.TaskStartingStatus && tsk.Task.Status != task_logger.TaskWaitingStatus {
		return false
	}
	if !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	if tsk.Task.RunnerID == nil {
		return true
	}
	if *tsk.Task.RunnerID != lease.Execution.RunnerID {
		return false
	}

	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.RunnerID = nil
	candidate.RunnerAssignedAt = nil
	candidate.RecoveryReason = reason
	candidate.Message = reason
	updated, err := store.UpdateTaskRunnerFenced(
		candidate, oldStatus, lease.Execution.RunnerID, lease.Execution.Generation, lease.FencingToken,
		db.RunnerAttemptRequeued, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to revoke orphaned task assignment")
		return false
	}
	if !updated {
		p.refreshTaskStatusFromDB(tsk)
		return false
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Log("Recovery revoked the runner assignment; waiting for post-revocation execution evidence.")
	return true
}

func (p *TaskPool) applyFencedRecoveryTransition(
	store taskRecoveryFencedStore,
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	status task_logger.TaskStatus,
	outcome db.RunnerAttemptOutcome,
	reason string,
	finalize bool,
	requeue bool,
) bool {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	if !requeue && !taskStatusTransitionAllowed(tsk.Task.Status, status) {
		return false
	}
	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.Status = status
	candidate.RecoveryReason = reason
	candidate.Message = reason
	if requeue {
		candidate.RunnerID = nil
		candidate.RunnerAssignedAt = nil
	}
	updated, err := store.UpdateTaskRunnerFenced(
		candidate, oldStatus, lease.Execution.RunnerID, lease.Execution.Generation, lease.FencingToken,
		outcome, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist fenced task recovery")
		return false
	}
	if !updated {
		return false
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	if requeue {
		tsk.Log("Recovery proved the revoked execution absent; returning task to queue.")
		p.state.Enqueue(tsk)
		p.queueEvents <- PoolEvent{EventTypeRequeued, tsk}
	} else if finalize {
		tsk.Log("Recovery reconciled the execution as " + string(status) + ".")
		p.finalizeRemoteTaskLocked(tsk, nil)
	}
	return true
}

func (p *TaskPool) quarantineOrphanedTask(
	store taskRecoveryFencedStore,
	tsk *TaskRunner,
	lease pro_interfaces.TaskControlLease,
	reason string,
) bool {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return false
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() || !taskMatchesRecoveryLease(tsk.Task, lease) {
		return false
	}
	reason = "Recovery quarantined: " + reason
	candidate := tsk.Task
	candidate.RecoveryReason = reason
	candidate.Message = reason
	updated, err := store.UpdateTaskFenced(candidate, lease.FencingToken)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist task recovery quarantine")
		return false
	}
	if !updated {
		return false
	}
	tsk.Task = candidate
	p.state.UpdateRuntimeFields(tsk)
	tsk.publishStatus()
	tsk.Log(reason)
	return true
}

func taskMatchesRecoveryLease(task db.Task, lease pro_interfaces.TaskControlLease) bool {
	return lease.TaskID == task.ID && lease.FencingToken > 0 &&
		lease.Execution.RunnerID == runnerIDFromSnapshot(task) &&
		lease.Execution.Generation == task.AssignmentGeneration
}

// runnerTasksReconcileLoop periodically reconciles dispatched tasks against
// runner liveness: tasks on an offline runner are requeued (starting) or
// failed (running, after the recovery window). Started from TaskPool.Run.
// It returns when p.stop is closed (a nil p.stop means run forever).
func (p *TaskPool) runnerTasksReconcileLoop() {
	ticker := time.NewTicker(util.Config.RunnersReconcileInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.reconcileRunnerTasks(tz.Now())
		case <-p.stop:
			return
		}
	}
}

// reconcileRunnerTasks applies DecideRunnerTaskAction to every dispatched,
// unfinished task this node owns (OwnedRunningRange: in HA, the tasks whose
// claim names this node; single-node, everything). Tasks of dead nodes have
// no live claim and are reconciled by the HA orphan cleaner instead, so the
// work is partitioned across the cluster rather than repeated on every node.
// The remaining cross-actor races (cleaner vs. owner around claim expiry)
// are covered by the state store's finalize lock and the DB re-checks in the
// action helpers.
func (p *TaskPool) reconcileRunnerTasks(now time.Time) {
	offlineTimeout := util.Config.RunnersOfflineTimeout()
	taskFailTimeout := util.Config.RunnersTaskFailTimeout()

	for _, tsk := range p.state.OwnedRunningRange() {
		if tsk == nil || tsk.Task.Status.IsFinished() {
			continue
		}

		if tsk.Task.RunnerID == nil {
			// An undispatched task this node still claims. Normally this is a task
			// being dispatched right now by a live goroutine of this process — leave
			// it. But if no such goroutine exists, the task is a "starting" stub
			// restored from Redis after this node restarted mid-dispatch: its
			// dispatch goroutine died with the previous process, so no runner will
			// ever be assigned and neither the runner-liveness branch below (no
			// runner to check) nor the HA orphan cleaner (claim is alive, owner is
			// this live node) will recover it. Return it to the queue.
			if !tsk.isDispatching() {
				p.requeueUndispatchedTask(tsk)
			}
			continue
		}

		runnerID := *tsk.Task.RunnerID

		var runnerPtr *db.Runner
		runner, err := p.store.GetRunner(tsk.Task.ProjectID, runnerID)
		if errors.Is(err, db.ErrNotFound) {
			runner, err = p.store.GetGlobalRunner(runnerID)
		}
		switch {
		case err == nil:
			runnerPtr = &runner
		case errors.Is(err, db.ErrNotFound):
			runnerPtr = nil
		default:
			log.WithError(err).WithFields(log.Fields{
				"task_id":   tsk.Task.ID,
				"runner_id": runnerID,
				"context":   "runner_reconciler",
			}).Warn("failed to load runner; skipping task")
			continue
		}

		action, reason := DecideRunnerTaskAction(
			tsk.Task.Status, tsk.Task.Start, tsk.Task.RunnerAssignedAt,
			runnerPtr, now, offlineTimeout, taskFailTimeout)

		switch action {
		case RunnerTaskRequeue:
			p.requeueTaskRunnerOffline(tsk, runnerID, reason)
		case RunnerTaskFail:
			p.failTaskRunnerLost(tsk, runnerPtr, reason)
		case RunnerTaskStop:
			p.stopTaskRunnerLost(tsk, runnerPtr, reason)
		case RunnerTaskKeep:
			// Do nothing
		}
	}
}

// failTaskRunnerLost fails a dispatched task whose runner is lost and runs the
// usual finalization (finish webhook, autorun children, pool/Redis cleanup).
// Idempotent: it takes the state store's finalize lock before writing the
// failure so a concurrent terminal runner report can win without being
// overwritten by the reconciler.
func (p *TaskPool) failTaskRunnerLost(tsk *TaskRunner, runner *db.Runner, reason string) {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)

	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}

	if tsk.Task.Status.IsFinished() {
		return
	}

	fields := log.Fields{
		"task_id": tsk.Task.ID,
		"context": "runner_reconciler",
	}
	if tsk.Task.RunnerID != nil {
		fields["runner_id"] = *tsk.Task.RunnerID
	}
	log.WithFields(fields).Warn("Runner lost: marking task failed")

	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.Message = reason
	candidate.RecoveryReason = reason
	if runner == nil {
		// The runner row no longer exists and the DB has already nulled
		// task.runner_id (the FK is "on delete set null"); persisting the
		// stale ID would violate the FK and panic in saveStatus.
		candidate.RunnerID = nil
	}
	candidate.Status = task_logger.TaskFailStatus
	updated, err := p.store.UpdateTaskRunner(
		candidate, oldStatus, runnerIDFromSnapshot(tsk.Task), tsk.Task.AssignmentGeneration,
		db.RunnerAttemptFailed, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist lost-runner failure")
		return
	}
	if !updated {
		p.finalizeConcurrentRunnerWinner(tsk, runner)
		return
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Log("Runner lost: " + reason)

	p.finalizeRemoteTaskLocked(tsk, runner)
}

func runnerIDFromSnapshot(task db.Task) int {
	if task.RunnerID != nil {
		return *task.RunnerID
	}
	if task.RunnerSnapshotID != nil {
		return *task.RunnerSnapshotID
	}
	return 0
}

func (p *TaskPool) applyPersistedRunnerStatus(tsk *TaskRunner, oldStatus task_logger.TaskStatus) {
	p.state.UpdateRuntimeFields(tsk)
	tsk.publishStatus()
	tsk.afterStatusChange(oldStatus, tsk.Task.Status)
}

// finalizeConcurrentRunnerWinner handles the CAS-loser side of a terminal
// runner-report race. The reconciler already owns the finalize lock here; if
// the runner won SQL first, its own FinalizeRemoteTask call cannot acquire that
// lock. The lock owner must therefore finish the persisted winner instead.
func (p *TaskPool) finalizeConcurrentRunnerWinner(tsk *TaskRunner, runner *db.Runner) {
	p.refreshTaskStatusFromDB(tsk)
	if !tsk.Task.Status.IsFinished() {
		return
	}
	if tsk.Task.End != nil {
		p.onTaskStop(tsk)
		return
	}
	p.finalizeRemoteTaskLocked(tsk, runner)
}

func (p *TaskPool) stopTaskRunnerLost(tsk *TaskRunner, runner *db.Runner, reason string) {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)
	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}
	if tsk.Task.Status.IsFinished() {
		return
	}
	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.Status = task_logger.TaskStoppedStatus
	candidate.Message = reason
	candidate.RecoveryReason = reason
	if runner == nil {
		candidate.RunnerID = nil
	}
	updated, err := p.store.UpdateTaskRunner(
		candidate, oldStatus, runnerIDFromSnapshot(tsk.Task), tsk.Task.AssignmentGeneration,
		db.RunnerAttemptStopped, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithField("task_id", tsk.Task.ID).Error("failed to persist lost-runner cancellation")
		return
	}
	if !updated {
		p.finalizeConcurrentRunnerWinner(tsk, runner)
		return
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Log("Runner cancellation completed: " + reason)
	p.finalizeRemoteTaskLocked(tsk, runner)
}

// requeueTaskRunnerOffline returns a not-yet-running task dispatched to an
// offline runner back to the queue so the dispatch loop selects another
// runner. Clearing RunnerID removes the task from the old runner's NewJobs,
// and UpdateRunner's ownership check rejects its late progress reports.
//
// In HA mode the reconciler runs on every node over the shared running set,
// so several nodes can reach this point for the same task in the same tick.
// The shared queue is a plain RPUSH (no dedup) and a duplicate queue entry
// would eventually re-run the task, so requeue must happen exactly once:
// the cluster-wide finalize lock (Redis SETNX) serializes the attempts, and
// the DB re-check under the lock makes every loser observe the cleared
// RunnerID and bail.
func (p *TaskPool) requeueTaskRunnerOffline(tsk *TaskRunner, runnerID int, reason string) {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return // another node is requeueing or finalizing this task
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)

	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}

	// Only a task that has not started executing may be reassigned: the
	// runner may have just picked it up and reported "running" concurrently.
	if tsk.Task.Status != task_logger.TaskStartingStatus &&
		tsk.Task.Status != task_logger.TaskWaitingStatus {
		return
	}

	// Already reassigned (e.g. by another node).
	if tsk.Task.RunnerID == nil || *tsk.Task.RunnerID != runnerID {
		return
	}

	log.WithFields(log.Fields{
		"task_id":   tsk.Task.ID,
		"runner_id": runnerID,
		"context":   "runner_reconciler",
	}).Warn("Runner offline: returning task to queue")

	oldStatus := tsk.Task.Status
	candidate := tsk.Task
	candidate.RunnerID = nil
	candidate.RunnerAssignedAt = nil
	candidate.Status = task_logger.TaskWaitingStatus
	candidate.RecoveryReason = reason
	updated, err := p.store.UpdateTaskRunner(
		candidate, oldStatus, runnerID, tsk.Task.AssignmentGeneration,
		db.RunnerAttemptRequeued, reason, tz.Now(),
	)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"task_id": tsk.Task.ID,
			"context": "runner_reconciler",
		}).Error("failed to persist requeued task")
		return
	}
	if !updated {
		p.finalizeConcurrentRunnerWinner(tsk, nil)
		return
	}
	tsk.Task = candidate
	p.applyPersistedRunnerStatus(tsk, oldStatus)
	tsk.Logf("Runner #%d lost the task: %s. Returning task to queue.", runnerID, reason)

	// Same flow as the ErrAllRunnersBusy requeue in TaskRunner.run:
	// put the task back into the queue, then let the pool release its
	// running/active bookkeeping (EventTypeRequeued -> onTaskStop).
	p.state.Enqueue(tsk)
	p.queueEvents <- PoolEvent{EventTypeRequeued, tsk}
}

// requeueUndispatchedTask returns a task that this node claims and that sits in
// the running set in a not-yet-running state without an assigned runner, but
// for which this process holds no live dispatch goroutine. That happens when a
// node restarts mid-dispatch: the in-memory dispatch goroutine is lost while the
// task's Redis state (running-set membership + a still-refreshed claim) survives,
// leaving the task stuck in "starting" forever — invisible to both the
// runner-liveness reconcile (no runner to check) and the HA orphan cleaner (the
// claim is alive and owned by this live node).
//
// The cluster-wide finalize lock plus a DB re-check make this idempotent and
// safe against a concurrent dispatch that just assigned a runner.
func (p *TaskPool) requeueUndispatchedTask(tsk *TaskRunner) {
	if !p.state.TryFinalize(tsk.Task.ID) {
		return // another node/goroutine is requeueing or finalizing this task
	}
	defer p.state.DeleteFinalize(tsk.Task.ID)

	if util.HAEnabled() {
		p.refreshTaskStatusFromDB(tsk)
	}

	// Only a not-yet-running task without a runner may be reassigned; a
	// concurrent dispatch may have moved it to running or assigned a runner.
	if tsk.Task.Status != task_logger.TaskStartingStatus &&
		tsk.Task.Status != task_logger.TaskWaitingStatus {
		return
	}
	if tsk.Task.RunnerID != nil {
		return
	}

	log.WithFields(log.Fields{
		"task_id": tsk.Task.ID,
		"context": "runner_reconciler",
	}).Warn("Dispatch lost: returning undispatched task to queue")

	tsk.Log("Dispatch goroutine lost (node restarted mid-dispatch). Returning task to queue.")

	tsk.SetStatus(task_logger.TaskWaitingStatus)

	// SetStatus is a no-op when the status is already "waiting"; persist
	// explicitly so the cleared state is durable before re-enqueueing.
	if err := p.store.UpdateTask(tsk.Task); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"task_id": tsk.Task.ID,
			"context": "runner_reconciler",
		}).Error("failed to persist requeued task")
		return
	}

	// EventTypeRequeued -> onTaskStop releases the running/active bookkeeping and
	// the claim, so the task can be re-claimed from the queue by any live node.
	p.state.Enqueue(tsk)
	p.queueEvents <- PoolEvent{EventTypeRequeued, tsk}
}
