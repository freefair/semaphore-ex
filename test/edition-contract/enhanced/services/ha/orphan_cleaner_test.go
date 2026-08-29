package ha

import (
	"testing"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	clusterSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orphanCleanerPoolFake struct {
	task        *tasks.TaskRunner
	owned       bool
	revocations int
	assessments []pro_interfaces.TaskRecoveryAssessment
}

func (f *orphanCleanerPoolFake) GetOwnedRunningTasks() []*tasks.TaskRunner {
	if f.task == nil || !f.owned {
		return nil
	}
	return []*tasks.TaskRunner{f.task}
}

func (f *orphanCleanerPoolFake) GetTask(int) (*tasks.TaskRunner, error) { return f.task, nil }

func (f *orphanCleanerPoolFake) ApplyOrphanRecovery(_ *tasks.TaskRunner, _ pro_interfaces.TaskControlLease, assessment pro_interfaces.TaskRecoveryAssessment) bool {
	f.assessments = append(f.assessments, assessment)
	return true
}

func (f *orphanCleanerPoolFake) RevokeOrphanedTaskAssignment(task *tasks.TaskRunner, _ pro_interfaces.TaskControlLease, _ string) bool {
	f.revocations++
	task.Task.RunnerID = nil
	return true
}

func TestManagedOrphanCleanerRelinquishesOwnershipWhenReadinessIsLost(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	repository := clusterSQL.NewTaskControlStore(database.GetConnection())
	ready := true
	pool := &orphanCleanerPoolFake{}
	cleaner := NewManagedOrphanCleaner(repository, pool, "boot-a", func() (bool, error) {
		return ready, nil
	}, time.Hour, time.Minute, time.Minute)
	runnerID := 7
	task := coredb.Task{ID: 17, RunnerID: &runnerID, AssignmentGeneration: 2}

	require.NoError(t, cleaner.RegisterTaskControl(task))
	record, found, err := repository.GetTaskControlRecovery(task.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "boot-a", record.Lease.OwnerBootID)

	ready = false
	cleaner.tick()
	current, err := repository.IsCurrentTaskControlLease(record.Lease)
	require.NoError(t, err)
	assert.False(t, current)
}

func TestManagedOrphanCleanerWaitsForPostRevocationAbsenceBeforeReplacement(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	repository := clusterSQL.NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
	require.NoError(t, err)
	_, claimed, err := repository.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17)
	require.NoError(t, err)
	runnerID := 7
	pool := &orphanCleanerPoolFake{task: &tasks.TaskRunner{Task: coredb.Task{
		ID: 17, Status: task_logger.TaskStartingStatus, RunnerID: &runnerID,
		RunnerSnapshotID: &runnerID, AssignmentGeneration: 2,
	}}}
	cleaner := NewManagedOrphanCleaner(repository, pool, "boot-b", func() (bool, error) {
		return true, nil
	}, time.Hour, time.Minute, time.Minute)

	cleaner.tick()
	assert.Equal(t, 1, pool.revocations)
	assert.Empty(t, pool.assessments, "revocation is not proof that the execution is absent")
	recovery, found, err := repository.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, recovery.AssignmentRevokedAt)

	require.NoError(t, repository.RecordTaskExecutionSnapshot(7, []coredb.TaskExecutionEvidence{}))
	_, err = database.GetConnection().Exec(
		"update cluster__task_control set last_observed_at=datetime(assignment_revoked_at, '+1 second') where task_id=?", 17,
	)
	require.NoError(t, err)
	recovery, found, err = repository.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	cleaner.recover(recovery.Lease)
	require.Len(t, pool.assessments, 1)
	assert.Equal(t, pro_interfaces.TaskRecoveryRecover, pool.assessments[0].Decision)
	assert.True(t, pool.assessments[0].SafeReplacement)
}

func TestManagedOrphanCleanerQuarantinesRunningTaskWithoutPostTransferEvidence(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	repository := clusterSQL.NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
	require.NoError(t, err)
	_, claimed, err := repository.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17)
	require.NoError(t, err)
	runnerID := 7
	pool := &orphanCleanerPoolFake{task: &tasks.TaskRunner{Task: coredb.Task{
		ID: 17, Status: task_logger.TaskRunningStatus, RunnerID: &runnerID,
		RunnerSnapshotID: &runnerID, AssignmentGeneration: 2,
	}}}
	cleaner := NewManagedOrphanCleaner(repository, pool, "boot-b", func() (bool, error) {
		return true, nil
	}, time.Hour, time.Minute, time.Minute)

	cleaner.tick()
	require.Len(t, pool.assessments, 1)
	assert.Equal(t, pro_interfaces.TaskRecoveryObserve, pool.assessments[0].Decision)
	assert.Contains(t, pool.assessments[0].Reason, "waiting")
	_, err = database.GetConnection().Exec(
		"update cluster__task_control set ownership_transferred_at=datetime('now', '-2 minutes') where task_id=?", 17,
	)
	require.NoError(t, err)
	recovery, found, err := repository.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	cleaner.recover(recovery.Lease)
	require.Len(t, pool.assessments, 2)
	assert.Equal(t, pro_interfaces.TaskRecoveryQuarantine, pool.assessments[1].Decision)
	assert.Contains(t, pool.assessments[1].Reason, "after ownership transfer")
	diagnostics, found, err := cleaner.TaskRecoveryDiagnostics(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, diagnostics.Quarantined)
	assert.Equal(t, "retry_recovery", diagnostics.SafeAction)
	assert.Equal(t, "boot-a", diagnostics.PreviousOwnerBootID)
	assert.Equal(t, "boot-b", diagnostics.OwnerBootID)

	require.NoError(t, cleaner.RetryTaskRecovery(17))
	require.Len(t, pool.assessments, 3)
	assert.Equal(t, pro_interfaces.TaskRecoveryQuarantine, pool.assessments[2].Decision)

	_, err = database.GetConnection().Exec(
		"update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17,
	)
	require.NoError(t, err)
	_, claimed, err = repository.ClaimTaskControl(17, execution, "boot-c", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.ErrorContains(t, cleaner.RetryTaskRecovery(17), "no longer current")
}

func TestManagedOrphanCleanerUsesOnlyPostTransferRunnerEvidence(t *testing.T) {
	tests := []struct {
		name        string
		snapshot    []coredb.TaskExecutionEvidence
		decision    pro_interfaces.TaskRecoveryDecision
		replacement bool
		terminal    string
	}{
		{name: "running execution remains observed", snapshot: []coredb.TaskExecutionEvidence{{
			TaskID: 17, Generation: 2, State: coredb.TaskExecutionEvidenceRunning,
			Status: task_logger.TaskRunningStatus,
		}}, decision: pro_interfaces.TaskRecoveryObserve},
		{name: "absent execution permits replacement", snapshot: []coredb.TaskExecutionEvidence{},
			decision: pro_interfaces.TaskRecoveryRecover, replacement: true},
		{name: "terminal execution preserves exact result", snapshot: []coredb.TaskExecutionEvidence{{
			TaskID: 17, Generation: 2, State: coredb.TaskExecutionEvidenceTerminal,
			Status: task_logger.TaskSuccessStatus,
		}}, decision: pro_interfaces.TaskRecoveryRecover, terminal: string(task_logger.TaskSuccessStatus)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := coresql.InitConfigCreateTestStore()
			t.Cleanup(database.Close)
			repository := clusterSQL.NewTaskControlStore(database.GetConnection())
			execution, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
			require.NoError(t, err)
			_, claimed, err := repository.ClaimTaskControl(17, execution, "boot-a", time.Minute)
			require.NoError(t, err)
			require.True(t, claimed)
			_, err = database.GetConnection().Exec(
				"update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17,
			)
			require.NoError(t, err)
			runnerID := 7
			pool := &orphanCleanerPoolFake{task: &tasks.TaskRunner{Task: coredb.Task{
				ID: 17, Status: task_logger.TaskRunningStatus, RunnerID: &runnerID,
				RunnerSnapshotID: &runnerID, AssignmentGeneration: 2,
			}}}
			cleaner := NewManagedOrphanCleaner(repository, pool, "boot-b", func() (bool, error) {
				return true, nil
			}, time.Hour, time.Minute, time.Minute)

			cleaner.tick()
			require.Len(t, pool.assessments, 1)
			assert.Equal(t, pro_interfaces.TaskRecoveryObserve, pool.assessments[0].Decision)
			require.NoError(t, repository.RecordTaskExecutionSnapshot(7, test.snapshot))
			_, err = database.GetConnection().Exec(
				"update cluster__task_control set last_observed_at=datetime(ownership_transferred_at, '+1 second') where task_id=?", 17,
			)
			require.NoError(t, err)
			recovery, found, err := repository.GetTaskControlRecovery(17)
			require.NoError(t, err)
			require.True(t, found)
			cleaner.recover(recovery.Lease)

			require.Len(t, pool.assessments, 2)
			assessment := pool.assessments[1]
			assert.Equal(t, test.decision, assessment.Decision)
			assert.Equal(t, test.replacement, assessment.SafeReplacement)
			assert.Equal(t, test.terminal, assessment.TerminalStatus)
		})
	}
}

func TestManagedOrphanCleanerRequiresEvidenceStrictlyAfterRevocation(t *testing.T) {
	boundary := time.Date(2026, time.August, 29, 15, 0, 0, 0, time.UTC)
	cleaner := &managedOrphanCleaner{maxEvidenceAge: time.Minute}
	record := pro_interfaces.TaskControlRecoveryRecord{
		Evidence:            pro_interfaces.TaskExecutionEvidence{State: pro_interfaces.TaskExecutionAbsent},
		EvidenceObservedAt:  &boundary,
		AssignmentRevokedAt: &boundary,
		DatabaseNow:         boundary.Add(10 * time.Second),
	}

	assessment := cleaner.assess(record)

	assert.Equal(t, pro_interfaces.TaskRecoveryObserve, assessment.Decision)
	assert.False(t, assessment.SafeReplacement)
}

func TestManagedClusterInspectorDrainsSelfBeforePersistingDrainState(t *testing.T) {
	repository := &clusterNodeRepositoryFake{nodes: []pro_interfaces.ClusterNodeRegistration{{
		ClusterNodeIdentity: pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"},
	}}}
	drainer := &orphanCleanerDrainerFake{repository: repository}
	inspector := NewManagedClusterInspector(repository, unavailableHeartbeatStore{},
		pro_interfaces.ClusterCompatibilityRequirements{},
		pro_interfaces.ClusterNodeIdentity{NodeID: "node-a", BootID: "boot-a"})
	inspector.drainer = drainer

	require.NoError(t, inspector.SetNodeDraining("boot-a", true))
	assert.True(t, drainer.drained)
	assert.False(t, drainer.sawPersistedDrain, "leases must be relinquished before SQL advertises draining")
	assert.True(t, repository.nodes[0].Draining)

	require.NoError(t, inspector.SetNodeDraining("boot-a", false))
	assert.True(t, drainer.resumed)
	assert.False(t, repository.nodes[0].Draining)
}

type orphanCleanerDrainerFake struct {
	repository        *clusterNodeRepositoryFake
	drained           bool
	resumed           bool
	sawPersistedDrain bool
}

func (*orphanCleanerDrainerFake) Start() {}
func (*orphanCleanerDrainerFake) Stop()  {}
func (f *orphanCleanerDrainerFake) Drain() error {
	f.drained = true
	f.sawPersistedDrain = f.repository.nodes[0].Draining
	return nil
}
func (f *orphanCleanerDrainerFake) Resume() { f.resumed = true }
func (*orphanCleanerDrainerFake) TaskRecoveryDiagnostics(int) (pro_interfaces.TaskRecoveryDiagnostics, bool, error) {
	return pro_interfaces.TaskRecoveryDiagnostics{}, false, nil
}
func (*orphanCleanerDrainerFake) RetryTaskRecovery(int) error { return nil }
