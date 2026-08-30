package sql

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dockerReconciliationObservation(runnerID, projectID, taskID, generation int, sequence int64) db.DockerReconciliationObservation {
	return db.DockerReconciliationObservation{
		Sequence: sequence, RunnerID: runnerID, RunnerBoot: "boot-a", ProjectID: projectID, TaskID: taskID, Generation: generation,
		Resource: db.DockerReconciliationResourceTask, ContainerID: "container-a", State: db.DockerReconciliationRunning,
		ObservedAt: time.Now().UTC(), QuarantineStatus: db.DockerReconciliationQuarantineNone, Remediation: db.DockerReconciliationRemediationNone,
	}
}

func createDockerReconciliationAttempt(t *testing.T) (*SqlDb, int, db.Runner, db.Task) {
	t.Helper()
	store, projectID, runner, task := createRunnerAttemptFixture(t)
	assigned, ok, err := store.AssignTaskRunner(projectID, task.ID, runner.ID, runner.Name, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.UpdateTaskRunnerAttemptMetadata(projectID, task.ID, assigned.AssignmentGeneration, runner.ID, db.RunnerExecutorMetadata{ExecutorType: db.RunnerExecutorDocker})
	require.NoError(t, err)
	require.True(t, ok)
	return store, projectID, runner, assigned
}

func TestDockerReconciliationStoreCreatesOrdersAndReplays(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)

	first, replayed, err := store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1))
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, int64(1), first.LatestSequence)
	assert.Equal(t, int64(1), first.Revision)

	firstObservation, err := store.GetDockerReconciliationObservation(db.DockerReconciliationOwner{RunnerID: runner.ID, RunnerBoot: "boot-a"}, 1)
	require.NoError(t, err)
	replayedFirst, replayed, err := store.CreateDockerReconciliationObservation(runner.ID, firstObservation)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, first, replayedFirst)

	conflict := firstObservation
	conflict.State = db.DockerReconciliationExited
	_, _, err = store.CreateDockerReconciliationObservation(runner.ID, conflict)
	assert.ErrorIs(t, err, db.ErrDockerReconciliationSequenceConflict)

	third, replayed, err := store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 3))
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, int64(3), third.LatestSequence)
	_, _, err = store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 2))
	assert.ErrorIs(t, err, db.ErrDockerReconciliationSequenceConflict)

	owner := db.DockerReconciliationOwner{RunnerID: runner.ID, RunnerBoot: "boot-a"}
	observations, err := store.GetDockerReconciliationObservations(owner, db.DockerReconciliationQuery{AfterSequence: 1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, observations, 1)
	assert.Equal(t, int64(3), observations[0].Sequence)
	_, err = store.GetDockerReconciliationObservation(db.DockerReconciliationOwner{RunnerID: owner.RunnerID, RunnerBoot: "boot-b"}, 1)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestDockerReconciliationStoreFencesQuarantineUpdatesByOwnerAndRevision(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	created, _, err := store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1))
	require.NoError(t, err)
	owner := db.DockerReconciliationOwner{RunnerID: created.RunnerID, RunnerBoot: created.RunnerBoot}
	now := time.Now().UTC()
	created.State = db.DockerReconciliationQuarantine
	created.QuarantineStatus = db.DockerReconciliationQuarantinePending
	created.Remediation = db.DockerReconciliationRemediationInspect
	created.RemediationReason = "daemon-interrupted"
	created.QuarantinedAt = &now
	updated, err := store.UpdateDockerReconciliationState(created, int(created.Revision))
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Revision)
	assert.Equal(t, db.DockerReconciliationQuarantinePending, updated.QuarantineStatus)

	_, err = store.UpdateDockerReconciliationState(updated, 1)
	assert.ErrorIs(t, err, db.ErrDockerReconciliationRevisionConflict)
	_, _, err = store.CreateDockerReconciliationObservation(runner.ID+1, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 2))
	assert.Error(t, err, "server-authenticated runner ownership is required")
	unownedAttempt := dockerReconciliationObservation(runner.ID, projectID, task.ID+1, task.AssignmentGeneration, 2)
	_, _, err = store.CreateDockerReconciliationObservation(runner.ID, unownedAttempt)
	assert.Error(t, err, "a report must name the authenticated runner's Docker attempt")
	_, err = store.GetDockerReconciliationState(db.DockerReconciliationKey{DockerReconciliationOwner: db.DockerReconciliationOwner{RunnerID: owner.RunnerID, RunnerBoot: "boot-b"}, ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask})
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestDockerReconciliationRemediationCommandIsSessionAndRevisionFenced(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	state, _, err := store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1))
	require.NoError(t, err)
	now := time.Now().UTC()
	state.State, state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt = db.DockerReconciliationQuarantine, db.DockerReconciliationQuarantinePending, db.DockerReconciliationRemediationInspect, "stop_unconfirmed", &now
	state, err = store.UpdateDockerReconciliationState(state, int(state.Revision))
	require.NoError(t, err)
	session, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	command, err := store.RequestDockerReconciliationRemediation(runner.ID, db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "retry-1", ExpectedRevision: state.Revision, Target: db.DockerReconciliationRemediationTargetQuarantine, Quarantine: ptrDockerKey(state.Key())})
	require.NoError(t, err)
	commands, err := store.GetDockerReconciliationRemediationCommands(runner.ID, session.SessionID, session.Fence, 10)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, command.CommandID, commands[0].CommandID)
	result := db.DockerReconciliationRemediationResult{CommandID: command.CommandID, Fingerprint: command.Fingerprint, Status: db.DockerReconciliationRemediationSucceeded, Evidence: db.DockerReconciliationEvidenceRemoved}
	require.NoError(t, store.ReportDockerReconciliationRemediation(runner.ID, session.SessionID, session.Fence, result))
	require.NoError(t, store.ReportDockerReconciliationRemediation(runner.ID, session.SessionID, session.Fence, result), "exact result replay is idempotent")
	resolved, err := store.GetDockerReconciliationState(state.Key())
	require.NoError(t, err)
	assert.Equal(t, db.DockerReconciliationQuarantineRemediated, resolved.QuarantineStatus)
	assert.ErrorIs(t, store.ReportDockerReconciliationRemediation(runner.ID+1, session.SessionID, session.Fence, result), db.ErrDockerReconciliationSessionStale)
	_, err = store.RequestDockerReconciliationRemediation(runner.ID, db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "retry-stale", ExpectedRevision: state.Revision, Target: db.DockerReconciliationRemediationTargetQuarantine, Quarantine: ptrDockerKey(state.Key())})
	assert.ErrorIs(t, err, db.ErrDockerReconciliationCommandStale)
	// A retry after a lost 202 must return the original command even though its
	// target is now resolved and its expected revision is stale.
	replayed, err := store.RequestDockerReconciliationRemediation(runner.ID, db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "retry-1", ExpectedRevision: state.Revision, Target: db.DockerReconciliationRemediationTargetQuarantine, Quarantine: ptrDockerKey(state.Key())})
	require.NoError(t, err)
	assert.Equal(t, command.CommandID, replayed.CommandID)
	_, err = store.RequestDockerReconciliationRemediation(runner.ID, db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "retry-1", ExpectedRevision: state.Revision + 1, Target: db.DockerReconciliationRemediationTargetQuarantine, Quarantine: ptrDockerKey(state.Key())})
	assert.ErrorIs(t, err, db.ErrDockerReconciliationImmutableMutation)
}

func TestDockerReconciliationCandidateCommandRebindsToCurrentSession(t *testing.T) {
	store, _, runner, _ := createDockerReconciliationAttempt(t)
	oldSession, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	candidate := db.DockerReconciliationOrphanCandidate{Resource: db.DockerReconciliationCandidateContainer, Identifier: "immutable-container", Identity: "immutable-container", Name: "orphan", Reason: db.DockerReconciliationCandidateMalformed, ObservedAt: time.Now().UTC()}
	require.NoError(t, store.RecordDockerReconciliationOrphanCandidates(runner.ID, oldSession.SessionID, oldSession.Fence, []db.DockerReconciliationOrphanCandidate{candidate}))
	page, err := store.GetDockerReconciliationPendingCandidates(runner.ID, oldSession.SessionID, db.DockerReconciliationCandidateQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Candidates, 1)
	current, err := store.OpenDockerReconciliationSession(runner.ID, "forged", "forged")
	require.NoError(t, err)
	command, err := store.RequestDockerReconciliationRemediation(runner.ID, db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "candidate-retry", ExpectedRevision: page.Candidates[0].Revision, Target: db.DockerReconciliationRemediationTargetCandidate, SessionID: oldSession.SessionID, Fingerprint: page.Candidates[0].Fingerprint})
	require.NoError(t, err)
	assert.Equal(t, current.SessionID, command.SessionID)
	assert.Equal(t, oldSession.SessionID, command.CandidateSessionID)
	commands, err := store.GetDockerReconciliationRemediationCommands(runner.ID, current.SessionID, current.Fence, 1)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.NoError(t, store.ReportDockerReconciliationRemediation(runner.ID, current.SessionID, current.Fence, db.DockerReconciliationRemediationResult{CommandID: command.CommandID, Fingerprint: command.Fingerprint, Status: db.DockerReconciliationRemediationSucceeded, Evidence: db.DockerReconciliationEvidenceRemoved}))
	resolved, err := store.GetDockerReconciliationCandidates(runner.ID, oldSession.SessionID, db.DockerReconciliationCandidateQuery{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, db.DockerReconciliationCandidateResolved, resolved[0].Status)
}

func TestDockerReconciliationPendingStatePagesSkipResolvedHistory(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Now().UTC()
	for _, row := range []struct {
		sequence int64
		status   db.DockerReconciliationQuarantineStatus
	}{{1, db.DockerReconciliationQuarantineRemediated}, {2, db.DockerReconciliationQuarantinePending}, {3, db.DockerReconciliationQuarantinePending}} {
		_, err := store.exec("insert into docker_reconciliation_state (runner_id,runner_boot,project_id,task_id,generation,resource,revision,latest_sequence,container_id,container_name,state,reason,updated_at,quarantine_status,remediation,remediation_reason,quarantined_at,remediated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", 77, "page-boot", 1, int(row.sequence), 1, db.DockerReconciliationResourceTask, 1, row.sequence, "container", "container", db.DockerReconciliationQuarantine, "", now, row.status, db.DockerReconciliationRemediationRemove, "cleanup", now, now)
		require.NoError(t, err)
	}
	first, err := store.GetDockerReconciliationPendingStates(db.DockerReconciliationOwner{RunnerID: 77, RunnerBoot: "page-boot"}, db.DockerReconciliationQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.States, 1)
	assert.Equal(t, int64(2), first.States[0].LatestSequence)
	require.NotNil(t, first.NextCursor)
	second, err := store.GetDockerReconciliationPendingStates(db.DockerReconciliationOwner{RunnerID: 77, RunnerBoot: "page-boot"}, db.DockerReconciliationQuery{AfterSequence: *first.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.Len(t, second.States, 1)
	assert.Equal(t, int64(3), second.States[0].LatestSequence)
	assert.Nil(t, second.NextCursor)
}

func TestDockerRemediationRequestAndSessionRestartNeverStrandAcceptedCommand(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	state, _, err := store.CreateDockerReconciliationObservation(runner.ID, dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1))
	require.NoError(t, err)
	now := time.Now().UTC()
	state.State, state.QuarantineStatus, state.Remediation, state.RemediationReason, state.QuarantinedAt = db.DockerReconciliationQuarantine, db.DockerReconciliationQuarantinePending, db.DockerReconciliationRemediationInspect, "stop_unconfirmed", &now
	state, err = store.UpdateDockerReconciliationState(state, int(state.Revision))
	require.NoError(t, err)
	oldSession, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	request := db.DockerReconciliationRemediationRequest{Action: db.DockerReconciliationRemediationRetryStopAndCleanup, IdempotencyKey: "restart-race", ExpectedRevision: state.Revision, Target: db.DockerReconciliationRemediationTargetQuarantine, Quarantine: ptrDockerKey(state.Key())}

	// Release both operations together. The runner-row lock makes the outcome
	// equivalent to either serial order; after both commit the command must be
	// bound to whichever session remains active.
	start := make(chan struct{})
	var wait sync.WaitGroup
	var command db.DockerReconciliationRemediationCommand
	var requestErr, restartErr error
	var restarted db.DockerReconciliationSession
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		command, requestErr = store.RequestDockerReconciliationRemediation(runner.ID, request)
	}()
	go func() {
		defer wait.Done()
		<-start
		restarted, restartErr = store.OpenDockerReconciliationSession(runner.ID, "restart-forged", "restart-forged")
	}()
	close(start)
	wait.Wait()
	require.NoError(t, requestErr)
	require.NoError(t, restartErr)
	active, err := store.OpenDockerReconciliationSession(runner.ID, restarted.SessionID, restarted.Fence)
	require.NoError(t, err)
	commands, err := store.GetDockerReconciliationRemediationCommands(runner.ID, active.SessionID, active.Fence, 10)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, command.CommandID, commands[0].CommandID)
	assert.Equal(t, active.SessionID, commands[0].SessionID)
	assert.NotEqual(t, oldSession.SessionID, active.SessionID)
}

func ptrDockerKey(value db.DockerReconciliationKey) *db.DockerReconciliationKey { return &value }

func TestDockerReconciliationMigrationAddsAndRollsBackSchema(t *testing.T) {
	legacy := "2.20.36"
	store := InitConfigCreateTestStoreAt(&legacy)
	t.Cleanup(store.Close)
	assert.NotContains(t, sqliteTableNames(t, store), "docker_reconciliation_observation")
	require.NoError(t, db.Migrate(store, nil))
	assert.Contains(t, sqliteTableNames(t, store), "docker_reconciliation_runner")
	assert.Contains(t, sqliteTableNames(t, store), "docker_reconciliation_state")
	assert.Contains(t, sqliteTableNames(t, store), "docker_reconciliation_remediation_command")
	columns := sqliteColumnNames(t, store, "docker_reconciliation_observation")
	for _, column := range []string{"revision", "quarantine_status", "remediation", "remediation_reason", "quarantined_at", "remediated_at"} {
		assert.Contains(t, columns, column)
	}
	require.NoError(t, db.Rollback(store, legacy))
	assert.NotContains(t, sqliteTableNames(t, store), "docker_reconciliation_observation")
	assert.NotContains(t, sqliteTableNames(t, store), "docker_reconciliation_runner")
	assert.NotContains(t, sqliteTableNames(t, store), "docker_reconciliation_state")
	assert.NotContains(t, sqliteTableNames(t, store), "docker_reconciliation_remediation_command")
}

func TestDockerReconciliationMigrationPreparesForSupportedDialects(t *testing.T) {
	for _, dialect := range []string{util.DbDriverSQLite, util.DbDriverMySQL, util.DbDriverPostgres} {
		t.Run(dialect, func(t *testing.T) {
			queries := getVersionSQL(dialect, "v2.20.37.sql", false)
			require.NotEmpty(t, queries)
			for _, query := range queries {
				assert.NotContains(t, query, "{{")
			}
		})
	}
}

func TestDockerReconciliationStoreRejectsInvalidBounds(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	invalid := dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1)
	invalid.RemediationReason = string(make([]byte, 129))
	_, _, err := store.CreateDockerReconciliationObservation(runner.ID, invalid)
	assert.False(t, errors.Is(err, db.ErrDockerReconciliationSequenceConflict))
	assert.Error(t, err)
}

func TestDockerReconciliationSessionFencesCoverageAndReplay(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)

	first, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.False(t, first.Ready)
	// A clean runner has no prior targets, so the explicit empty snapshot is a
	// valid boundary. It is not a client-provided readiness boolean.
	require.NoError(t, store.CompleteDockerReconciliationScan(runner.ID, db.DockerReconciliationScanComplete{
		SessionID: first.SessionID, Fence: first.Fence, HighestSequence: 0,
	}))
	first, err = store.OpenDockerReconciliationSession(runner.ID, first.SessionID, first.Fence)
	require.NoError(t, err)
	require.True(t, first.Ready)
	bound := db.DockerReconciliationScanTarget{TargetBoot: first.TargetBoot, ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task"}
	require.NoError(t, store.BindDockerReconciliationAttempt(runner.ID, first, bound))

	second, err := store.OpenDockerReconciliationSession(runner.ID, "forged", "forged")
	require.NoError(t, err)
	require.False(t, second.Ready)
	// The forged/empty coverage cannot omit the server-snapshotted old target.
	err = store.CompleteDockerReconciliationScan(runner.ID, db.DockerReconciliationScanComplete{
		SessionID: second.SessionID, Fence: second.Fence, HighestSequence: 0,
	})
	require.ErrorIs(t, err, db.ErrDockerReconciliationCoverageInvalid)
	wrongIdentity := bound
	wrongIdentity.ContainerName = "forged-container-name"
	require.ErrorIs(t, store.CompleteDockerReconciliationScan(runner.ID, db.DockerReconciliationScanComplete{SessionID: second.SessionID, Fence: second.Fence, CoveredTargets: []db.DockerReconciliationScanTarget{wrongIdentity}}), db.ErrDockerReconciliationCoverageInvalid)

	forged := dockerReconciliationObservation(runner.ID, projectID, task.ID, task.AssignmentGeneration, 1)
	forged.RunnerBoot = "forged-target"
	_, _, err = store.CreateDockerReconciliationSessionObservation(runner.ID, second.SessionID, second.Fence, forged)
	require.ErrorIs(t, err, db.ErrDockerReconciliationSessionStale)

	observation := dockerReconciliationObservation(0, projectID, task.ID, task.AssignmentGeneration, 1)
	observation.RunnerBoot = first.TargetBoot
	_, replayed, err := store.CreateDockerReconciliationSessionObservation(runner.ID, second.SessionID, second.Fence, observation)
	require.NoError(t, err)
	assert.False(t, replayed)
	complete := db.DockerReconciliationScanComplete{
		SessionID: second.SessionID, Fence: second.Fence, CoveredTargets: []db.DockerReconciliationScanTarget{bound}, HighestSequence: 1,
	}
	require.NoError(t, store.CompleteDockerReconciliationScan(runner.ID, complete))
	// A duplicate commit with the same evidence is an idempotent replay.
	require.NoError(t, store.CompleteDockerReconciliationScan(runner.ID, complete))
	complete.HighestSequence = 2
	require.ErrorIs(t, store.CompleteDockerReconciliationScan(runner.ID, complete), db.ErrDockerReconciliationCoverageInvalid)
	// The replaced session cannot become ready after a restart.
	require.ErrorIs(t, store.CompleteDockerReconciliationScan(runner.ID, db.DockerReconciliationScanComplete{SessionID: first.SessionID, Fence: first.Fence}), db.ErrDockerReconciliationSessionStale)
}

func TestDockerReconciliationScanBatchRollsBackOnInvalidTuple(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	first, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.NoError(t, store.CompleteDockerReconciliationScan(runner.ID, db.DockerReconciliationScanComplete{SessionID: first.SessionID, Fence: first.Fence}))
	first, err = store.OpenDockerReconciliationSession(runner.ID, first.SessionID, first.Fence)
	require.NoError(t, err)
	target := db.DockerReconciliationScanTarget{TargetBoot: first.TargetBoot, ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task"}
	require.NoError(t, store.BindDockerReconciliationAttempt(runner.ID, first, target))
	second, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	observation := dockerReconciliationObservation(0, projectID, task.ID, task.AssignmentGeneration, 1)
	observation.RunnerBoot = first.TargetBoot
	observation.ContainerName = "semaphore-task"
	bad := observation
	bad.TaskID++
	err = store.IngestDockerReconciliationScan(runner.ID, db.DockerReconciliationScan{SessionID: second.SessionID, Fence: second.Fence, Observations: []db.DockerReconciliationObservation{observation, bad}, Complete: db.DockerReconciliationScanComplete{CoveredTargets: []db.DockerReconciliationScanTarget{target}, HighestSequence: 1}})
	require.Error(t, err)
	_, err = store.GetDockerReconciliationObservation(db.DockerReconciliationOwner{RunnerID: runner.ID, RunnerBoot: first.TargetBoot}, 1)
	require.ErrorIs(t, err, db.ErrNotFound)
}

func TestDockerReconciliationOrphanCandidatesAreSessionFencedAndIdempotent(t *testing.T) {
	store, _, runner, _ := createDockerReconciliationAttempt(t)
	session, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	candidate := db.DockerReconciliationOrphanCandidate{
		Resource: db.DockerReconciliationCandidateVolume, Identifier: "bundle-volume", Name: "bundle-volume",
		Reason: db.DockerReconciliationCandidateExtra, ObservedAt: time.Now().UTC(),
	}
	require.NoError(t, store.RecordDockerReconciliationOrphanCandidates(runner.ID, session.SessionID, session.Fence, []db.DockerReconciliationOrphanCandidate{candidate, candidate}))
	require.NoError(t, store.RecordDockerReconciliationOrphanCandidates(runner.ID, session.SessionID, session.Fence, []db.DockerReconciliationOrphanCandidate{candidate}))
	count, err := store.Sql().SelectInt(store.PrepareQuery("select count(1) from docker_reconciliation_orphan_candidate where session_id=?"), session.SessionID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	assert.False(t, session.Ready, "candidate reports must not influence scan readiness")
	assert.ErrorIs(t, store.RecordDockerReconciliationOrphanCandidates(runner.ID, session.SessionID, "forged", []db.DockerReconciliationOrphanCandidate{candidate}), db.ErrDockerReconciliationSessionStale)
}

func TestDockerReconciliationSessionPagesMoreThanOneHundredTargetsAndRejectsReplay(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	insert := func(index int, candidate db.Task) {
		_, err := store.Sql().Exec(store.PrepareQuery("insert into docker_reconciliation_attempt (runner_id,target_boot,project_id,task_id,generation,resource,container_name) values (?,?,?,?,?,?,?)"), runner.ID, "prior-boot", projectID, candidate.ID, candidate.AssignmentGeneration, db.DockerReconciliationResourceTask, fmt.Sprintf("semaphore-task-%d", index))
		require.NoError(t, err)
	}
	insert(0, task)
	for index := 1; index <= 100; index++ {
		candidate, err := store.CreateTask(db.Task{TemplateID: task.TemplateID, ProjectID: projectID, Status: task_logger.TaskStartingStatus, Playbook: "site.yml", Created: time.Now()}, 0)
		require.NoError(t, err)
		candidate, assigned, err := store.AssignTaskRunner(projectID, candidate.ID, runner.ID, runner.Name, time.Now())
		require.NoError(t, err)
		require.True(t, assigned)
		ok, err := store.UpdateTaskRunnerAttemptMetadata(projectID, candidate.ID, candidate.AssignmentGeneration, runner.ID, db.RunnerExecutorMetadata{ExecutorType: db.RunnerExecutorDocker})
		require.NoError(t, err)
		require.True(t, ok)
		insert(index, candidate)
	}
	session, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.Len(t, session.ScanTargets, 100)
	assert.Equal(t, 101, session.ScanTargetCount)
	first := dockerReconciliationPage(session)
	require.NoError(t, store.IngestDockerReconciliationScan(runner.ID, first))
	assert.False(t, session.Ready)
	assert.ErrorIs(t, store.IngestDockerReconciliationScan(runner.ID, first), db.ErrDockerReconciliationSessionStale)

	next, err := store.OpenDockerReconciliationSession(runner.ID, session.SessionID, session.Fence)
	require.NoError(t, err)
	assert.Equal(t, int64(100), next.ScanCursor)
	require.Len(t, next.ScanTargets, 1)
	require.NoError(t, store.IngestDockerReconciliationScan(runner.ID, dockerReconciliationPage(next)))

	ready, err := store.OpenDockerReconciliationSession(runner.ID, next.SessionID, next.Fence)
	require.NoError(t, err)
	assert.True(t, ready.Ready)
	assert.Equal(t, int64(101), ready.ScanCursor)
}

func TestDockerReconciliationAttemptBindsTaskAndHelperAtomically(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	first, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.NoError(t, store.IngestDockerReconciliationScan(runner.ID, dockerReconciliationPage(first)))
	ready, err := store.OpenDockerReconciliationSession(runner.ID, first.SessionID, first.Fence)
	require.NoError(t, err)
	base := "semaphore-task-helper-test"
	taskTarget := db.DockerReconciliationScanTarget{TargetBoot: ready.TargetBoot, ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: base}
	helperTarget := taskTarget
	helperTarget.Resource, helperTarget.ContainerName = db.DockerReconciliationResourceHelper, base+"-helper"
	require.NoError(t, store.BindDockerReconciliationAttempts(runner.ID, ready, []db.DockerReconciliationScanTarget{taskTarget, helperTarget}))

	next, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.Len(t, next.ScanTargets, 2)
	assert.ElementsMatch(t, []db.DockerReconciliationResource{db.DockerReconciliationResourceTask, db.DockerReconciliationResourceHelper}, []db.DockerReconciliationResource{next.ScanTargets[0].Resource, next.ScanTargets[1].Resource})
	mismatch := helperTarget
	mismatch.ContainerName = "different-helper"
	assert.ErrorIs(t, store.BindDockerReconciliationAttempts(runner.ID, ready, []db.DockerReconciliationScanTarget{taskTarget, mismatch}), db.ErrDockerReconciliationSessionStale)
}

func TestDockerReconciliationStopQuarantineIsIdempotent(t *testing.T) {
	store, projectID, runner, task := createDockerReconciliationAttempt(t)
	session, err := store.OpenDockerReconciliationSession(runner.ID, "", "")
	require.NoError(t, err)
	require.NoError(t, store.IngestDockerReconciliationScan(runner.ID, dockerReconciliationPage(session)))
	session, err = store.OpenDockerReconciliationSession(runner.ID, session.SessionID, session.Fence)
	require.NoError(t, err)
	target := db.DockerReconciliationScanTarget{TargetBoot: session.TargetBoot, ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task-stop"}
	require.NoError(t, store.BindDockerReconciliationAttempt(runner.ID, session, target))
	quarantine := db.DockerReconciliationStopQuarantine{ProjectID: projectID, TaskID: task.ID, Generation: task.AssignmentGeneration, Resource: db.DockerReconciliationResourceTask, ContainerName: target.ContainerName, Reason: "daemon stop state could not be confirmed"}
	require.NoError(t, store.QuarantineDockerReconciliationAttempt(runner.ID, session.SessionID, session.Fence, quarantine))
	require.NoError(t, store.QuarantineDockerReconciliationAttempt(runner.ID, session.SessionID, session.Fence, quarantine))
	count, err := store.Sql().SelectInt(store.PrepareQuery("select count(1) from docker_reconciliation_observation where runner_id=? and runner_boot=? and task_id=? and generation=?"), runner.ID, session.TargetBoot, task.ID, task.AssignmentGeneration)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	quarantine.Reason = "different"
	assert.ErrorIs(t, store.QuarantineDockerReconciliationAttempt(runner.ID, session.SessionID, session.Fence, quarantine), db.ErrDockerReconciliationSequenceConflict)
}

func dockerReconciliationPage(session db.DockerReconciliationSession) db.DockerReconciliationScan {
	observations := make([]db.DockerReconciliationObservation, 0, len(session.ScanTargets))
	var high int64
	for _, target := range session.ScanTargets {
		observations = append(observations, db.DockerReconciliationObservation{Sequence: target.Sequence, RunnerID: session.RunnerID, RunnerBoot: target.TargetBoot, ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation, Resource: target.Resource, ContainerName: target.ContainerName, State: db.DockerReconciliationAbsent, ObservedAt: time.Now().UTC(), QuarantineStatus: db.DockerReconciliationQuarantineNone, Remediation: db.DockerReconciliationRemediationNone})
		if target.Sequence > high {
			high = target.Sequence
		}
	}
	return db.DockerReconciliationScan{SessionID: session.SessionID, Fence: session.Fence, Observations: observations, Complete: db.DockerReconciliationScanComplete{SessionID: session.SessionID, Fence: session.Fence, ScanCursor: session.ScanCursor, CoveredTargets: session.ScanTargets, HighestSequence: high}}
}
