package ha

import (
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedWorkflowRunLockerDrainWaitsAndRejectsNewClaimsUntilResume(t *testing.T) {
	repository := &workflowReconciliationRepositoryFake{}
	locker := NewManagedWorkflowRunLocker(repository, "boot-a", time.Minute)
	lease, release, claimed, err := locker.TryLockRun(7, 19)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Equal(t, "boot-a", lease.OwnerBootID)

	drained := make(chan error, 1)
	go func() { drained <- locker.Drain() }()
	select {
	case err = <-drained:
		require.Failf(t, "drain returned before active transition released", "error: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	release()
	require.NoError(t, <-drained)
	_, _, claimed, err = locker.TryLockRun(7, 20)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Equal(t, 1, repository.claims())

	locker.Resume()
	second, releaseSecond, claimed, err := locker.TryLockRun(7, 20)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, locker.RecordReconciled(second))
	releaseSecond()
	assert.Equal(t, 2, repository.claims())
	assert.Equal(t, 2, repository.releases())
	assert.Equal(t, 1, repository.reconciled())
}

func TestManagedWorkflowRunLockerSuppressesConcurrentLocalClaimForSameRun(t *testing.T) {
	repository := &workflowReconciliationRepositoryFake{}
	locker := NewManagedWorkflowRunLocker(repository, "boot-a", time.Minute)
	_, release, claimed, err := locker.TryLockRun(7, 19)
	require.NoError(t, err)
	require.True(t, claimed)
	_, _, claimed, err = locker.TryLockRun(7, 19)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Equal(t, 1, repository.claims())
	release()
}

func TestClusterCoordinatorHealthIncludesWorkflowOwnershipSummary(t *testing.T) {
	inspector := &managedClusterInspector{
		workflowHealth: workflowProgressionHealthFake{health: pro_interfaces.WorkflowProgressionHealth{
			CurrentOwnerships: 3, ExpiredOwnerships: 1, TransferCount: 4, MaxLagSeconds: 2,
		}},
	}
	health := inspector.CoordinatorHealth()
	require.NotNil(t, health.WorkflowProgression)
	assert.Equal(t, 3, health.WorkflowProgression.CurrentOwnerships)
	assert.Equal(t, 1, health.WorkflowProgression.ExpiredOwnerships)
	assert.Equal(t, 4, health.WorkflowProgression.TransferCount)
	assert.EqualValues(t, 2, health.WorkflowProgression.MaxLagSeconds)
}

type workflowReconciliationRepositoryFake struct {
	mu              sync.Mutex
	claimCount      int
	releaseCount    int
	reconciledCount int
}

func (r *workflowReconciliationRepositoryFake) ClaimWorkflowReconciliation(projectID int, runID int, ownerBootID string, ttl time.Duration) (pro_interfaces.WorkflowReconciliationLease, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCount++
	return pro_interfaces.WorkflowReconciliationLease{
		ProjectID: projectID, WorkflowRunID: runID, OwnerBootID: ownerBootID,
		FencingToken: int64(r.claimCount), LeaseExpiresAt: time.Now().Add(ttl), AcquiredAt: time.Now(),
	}, true, nil
}

func (r *workflowReconciliationRepositoryFake) RenewWorkflowReconciliation(lease pro_interfaces.WorkflowReconciliationLease, _ time.Duration) (pro_interfaces.WorkflowReconciliationLease, bool, error) {
	return lease, true, nil
}

func (r *workflowReconciliationRepositoryFake) IsCurrentWorkflowReconciliation(pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	return true, nil
}

func (r *workflowReconciliationRepositoryFake) ReleaseWorkflowReconciliation(pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseCount++
	return true, nil
}

func (r *workflowReconciliationRepositoryFake) RecordWorkflowReconciled(pro_interfaces.WorkflowReconciliationLease) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconciledCount++
	return true, nil
}

func (r *workflowReconciliationRepositoryFake) claims() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.claimCount
}

func (r *workflowReconciliationRepositoryFake) releases() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.releaseCount
}

func (r *workflowReconciliationRepositoryFake) reconciled() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconciledCount
}

type workflowProgressionHealthFake struct {
	health pro_interfaces.WorkflowProgressionHealth
}

func (f workflowProgressionHealthFake) WorkflowProgressionHealth() (pro_interfaces.WorkflowProgressionHealth, error) {
	return f.health, nil
}
