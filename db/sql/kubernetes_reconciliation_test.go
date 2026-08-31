package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesReconciliationExpiredGCIsSessionFencedAndReplaySafe(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	assigned, ok, err := store.AssignTaskRunner(projectID, task.ID, runner.ID, runner.Name, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok)
	deadline := time.Now().UTC().Add(-time.Minute)
	metadata := db.RunnerExecutorMetadata{ExecutorType: db.RunnerExecutorK8s, RequestedImage: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResolvedImage: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sClusterAlias: "qa", K8sNamespace: "semaphore-jobs", K8sJobName: "semaphore-task-41-2", K8sJobUID: "job-uid", K8sPodName: "semaphore-task-41-2-pod", K8sPodUID: "pod-uid", K8sContainerName: "task", K8sLifecycle: "succeeded", K8sPolicyRevision: 1, K8sPolicyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sServiceAccount: "semaphore-task", K8sResourcePolicyID: "1", K8sResourcePolicyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sNetworkProfile: "deny-all", K8sNetworkEnforcement: "network_policy", K8sSecretName: "semaphore-bundle-41-2", K8sSecretUID: "secret-uid", K8sNetworkPolicyName: "semaphore-network-41-2", K8sNetworkPolicyUID: "network-uid", K8sRetentionState: "terminal", K8sRetentionDeadline: &deadline}
	ok, err = store.UpdateTaskRunnerAttemptMetadata(projectID, task.ID, assigned.AssignmentGeneration, runner.ID, metadata)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = store.exec("update task__runner_attempt set outcome=?,ended_at=? where project_id=? and task_id=? and generation=?", db.RunnerAttemptSucceeded, time.Now().UTC(), projectID, task.ID, assigned.AssignmentGeneration)
	require.NoError(t, err)

	session, err := store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", "", "")
	require.NoError(t, err)
	require.Len(t, session.Targets, 1)
	scan := db.KubernetesReconciliationScan{SessionID: session.SessionID, Fence: session.Fence, Revision: session.Revision, Observations: []db.KubernetesReconciliationObservation{{ProjectID: projectID, TaskID: task.ID, Generation: assigned.AssignmentGeneration, State: db.KubernetesReconciliationObserved, Revision: session.Revision}}}
	require.NoError(t, store.IngestKubernetesReconciliationScan(runner.ID, scan))
	request := db.KubernetesReconciliationRemediationRequest{Action: db.KubernetesReconciliationGarbageCollectExpired, IdempotencyKey: "gc-1", ProjectID: projectID, TaskID: task.ID, Generation: assigned.AssignmentGeneration, ExpectedRevision: session.Revision}
	command, err := store.RequestKubernetesReconciliationRemediation(runner.ID, request)
	require.NoError(t, err)
	assert.Equal(t, session.Targets[0], command.Target)
	commands, err := store.GetKubernetesReconciliationCommands(runner.ID, session.SessionID, session.Fence, 100)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, command, commands[0])
	result := db.KubernetesReconciliationRemediationResult{CommandID: command.CommandID, Status: db.KubernetesReconciliationRemediationSucceeded, Evidence: db.KubernetesReconciliationEvidenceRemoved}
	require.NoError(t, store.ReportKubernetesReconciliationRemediation(runner.ID, session.SessionID, session.Fence, result))
	require.NoError(t, store.ReportKubernetesReconciliationRemediation(runner.ID, session.SessionID, session.Fence, result))
	_, err = store.GetKubernetesReconciliationCommands(runner.ID, session.SessionID, session.Fence, 100)
	require.NoError(t, err)
	assert.ErrorIs(t, store.ReportKubernetesReconciliationRemediation(runner.ID+1, session.SessionID, session.Fence, result), db.ErrKubernetesReconciliationSessionStale)
	_, err = store.RequestKubernetesReconciliationRemediation(runner.ID, db.KubernetesReconciliationRemediationRequest{Action: db.KubernetesReconciliationGarbageCollectExpired, IdempotencyKey: "too-late", ProjectID: projectID, TaskID: task.ID, Generation: assigned.AssignmentGeneration, ExpectedRevision: session.Revision + 1})
	assert.Error(t, err)
}

func TestKubernetesTelemetrySessionFencesReplaysAndRejectsMutation(t *testing.T) {
	store, _, runner, _ := createRunnerAttemptFixture(t)
	session, err := store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", "", "")
	require.NoError(t, err)
	batch := db.KubernetesTelemetryBatch{Events: []db.KubernetesTelemetryEvent{{Sequence: 1, Kind: db.KubernetesTelemetryAPILatency, Operation: db.KubernetesTelemetryOperationCreateJob, DurationMilliseconds: 7}, {Sequence: 2, Kind: db.KubernetesTelemetryDenial, PolicyRule: db.KubernetesPolicyRuleRBACDenied}}}
	ack, accepted, err := store.IngestKubernetesTelemetry(runner.ID, session.SessionID, session.Fence, batch)
	require.NoError(t, err)
	assert.Equal(t, int64(2), ack.HighestSequence)
	assert.Equal(t, session.SessionID, ack.SessionID)
	assert.Len(t, accepted, 2)
	ack, accepted, err = store.IngestKubernetesTelemetry(runner.ID, session.SessionID, session.Fence, batch)
	require.NoError(t, err)
	assert.Equal(t, int64(2), ack.HighestSequence)
	assert.Empty(t, accepted)
	mutated := batch
	mutated.Events = append([]db.KubernetesTelemetryEvent(nil), batch.Events...)
	mutated.Events[0].DurationMilliseconds = 8
	_, _, err = store.IngestKubernetesTelemetry(runner.ID, session.SessionID, session.Fence, mutated)
	assert.ErrorIs(t, err, db.ErrKubernetesTelemetrySequenceConflict)
	_, err = store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", "forged", "forged")
	require.NoError(t, err)
	_, _, err = store.IngestKubernetesTelemetry(runner.ID, session.SessionID, session.Fence, batch)
	assert.ErrorIs(t, err, db.ErrKubernetesTelemetrySessionStale)
}

func TestKubernetesReconciliationExcludesUnexpiredTerminalAttemptAndRejectsActiveAbsence(t *testing.T) {
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	assigned, ok, err := store.AssignTaskRunner(projectID, task.ID, runner.ID, runner.Name, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok)
	deadline := time.Now().UTC().Add(time.Hour)
	metadata := kubernetesTerminalMetadata(deadline)
	ok, err = store.UpdateTaskRunnerAttemptMetadata(projectID, task.ID, assigned.AssignmentGeneration, runner.ID, metadata)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = store.exec("update task__runner_attempt set outcome=?,ended_at=? where project_id=? and task_id=? and generation=?", db.RunnerAttemptSucceeded, time.Now().UTC(), projectID, task.ID, assigned.AssignmentGeneration)
	require.NoError(t, err)

	clean, err := store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", "", "")
	require.NoError(t, err)
	assert.Empty(t, clean.Targets, "normal cleanup during the retention window must not gate restart dispatch")
	require.NoError(t, store.IngestKubernetesReconciliationScan(runner.ID, db.KubernetesReconciliationScan{SessionID: clean.SessionID, Fence: clean.Fence, Revision: clean.Revision}))
	ready, err := store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", clean.SessionID, clean.Fence)
	require.NoError(t, err)
	assert.True(t, ready.Ready)

	activeMetadata := kubernetesTerminalMetadata(deadline)
	activeMetadata.K8sLifecycle = "running"
	activeMetadata.K8sRetentionState = "active"
	activeMetadata.K8sRetentionDeadline = nil
	_, err = store.exec("update task__runner_attempt set outcome=?,ended_at=null where project_id=? and task_id=? and generation=?", db.RunnerAttemptActive, projectID, task.ID, assigned.AssignmentGeneration)
	require.NoError(t, err)
	ok, err = store.UpdateTaskRunnerAttemptMetadata(projectID, task.ID, assigned.AssignmentGeneration, runner.ID, activeMetadata)
	require.NoError(t, err)
	require.True(t, ok)
	activeSession, err := store.OpenKubernetesReconciliationSession(runner.ID, "qa", "semaphore-jobs", "", "")
	require.NoError(t, err)
	require.Len(t, activeSession.Targets, 1)
	err = store.IngestKubernetesReconciliationScan(runner.ID, db.KubernetesReconciliationScan{SessionID: activeSession.SessionID, Fence: activeSession.Fence, Revision: activeSession.Revision, Observations: []db.KubernetesReconciliationObservation{{ProjectID: projectID, TaskID: task.ID, Generation: assigned.AssignmentGeneration, State: db.KubernetesReconciliationAbsent, Revision: activeSession.Revision}}})
	assert.ErrorIs(t, err, db.ErrKubernetesReconciliationCoverageInvalid)
}

func kubernetesTerminalMetadata(deadline time.Time) db.RunnerExecutorMetadata {
	return db.RunnerExecutorMetadata{ExecutorType: db.RunnerExecutorK8s, RequestedImage: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResolvedImage: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sClusterAlias: "qa", K8sNamespace: "semaphore-jobs", K8sJobName: "semaphore-task-41-2", K8sJobUID: "job-uid", K8sPodName: "semaphore-task-41-2-pod", K8sPodUID: "pod-uid", K8sContainerName: "task", K8sLifecycle: "succeeded", K8sPolicyRevision: 1, K8sPolicyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sServiceAccount: "semaphore-task", K8sResourcePolicyID: "1", K8sResourcePolicyHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", K8sNetworkProfile: "deny-all", K8sNetworkEnforcement: "network_policy", K8sSecretName: "semaphore-bundle-41-2", K8sSecretUID: "secret-uid", K8sNetworkPolicyName: "semaphore-network-41-2", K8sNetworkPolicyUID: "network-uid", K8sRetentionState: "terminal", K8sRetentionDeadline: &deadline}
}
