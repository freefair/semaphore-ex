package docker

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReduceDockerReconciliationScanMapsRequiredTuplesAndQuarantinesCandidates(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	target := db.DockerReconciliationScanTarget{TargetBoot: "prior-boot", ProjectID: 7, TaskID: 11, Generation: 3, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task-11-g3-prior-boot"}
	session := db.DockerReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: 19, TargetBoot: "new-boot", ScanTargets: []db.DockerReconciliationScanTarget{target}}
	labels := managedLabels(19, target)
	resources := []ManagedResource{
		{Kind: ManagedContainer, ID: "running", Name: target.ContainerName, Labels: labels, Running: true, StateKnown: true},
		{Kind: ManagedVolume, ID: "volume", Name: target.ContainerName + "-bundle", Labels: labels, StateKnown: true},
		{Kind: ManagedContainer, ID: "bad", Name: "bad", Labels: map[string]string{"io.semaphore.managed": "v1"}, StateKnown: true},
	}
	observations, complete, candidates, err := reduceDockerReconciliationScan(session, resources, now)
	require.NoError(t, err)
	require.Len(t, observations, 1)
	assert.Equal(t, db.DockerReconciliationRunning, observations[0].State)
	assert.Equal(t, "running", observations[0].ContainerID)
	assert.Equal(t, []db.DockerReconciliationScanTarget{target}, complete.CoveredTargets)
	assert.Equal(t, int64(1), complete.HighestSequence)
	require.Len(t, candidates, 2)
	assert.ElementsMatch(t, []db.DockerReconciliationCandidateReason{db.DockerReconciliationCandidateExtra, db.DockerReconciliationCandidateMalformed}, []db.DockerReconciliationCandidateReason{candidates[0].Reason, candidates[1].Reason})
}

func TestReduceDockerReconciliationScanMapsDuplicateAndAbsentSafely(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	task := db.DockerReconciliationScanTarget{TargetBoot: "prior", ProjectID: 2, TaskID: 3, Generation: 4, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task-3-g4-prior"}
	helper := db.DockerReconciliationScanTarget{TargetBoot: "prior", ProjectID: 2, TaskID: 3, Generation: 4, Resource: db.DockerReconciliationResourceHelper, ContainerName: "semaphore-task-3-g4-prior-helper"}
	session := db.DockerReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: 5, TargetBoot: "next", ScanTargets: []db.DockerReconciliationScanTarget{task, helper}}
	labels := managedLabels(5, task)
	resources := []ManagedResource{{Kind: ManagedContainer, ID: "one", Name: task.ContainerName, Labels: labels, StateKnown: true}, {Kind: ManagedContainer, ID: "two", Name: task.ContainerName, Labels: labels, StateKnown: true}}
	observations, _, candidates, err := reduceDockerReconciliationScan(session, resources, now)
	require.NoError(t, err)
	assert.Empty(t, candidates)
	require.Len(t, observations, 2)
	byResource := map[db.DockerReconciliationResource]db.DockerReconciliationObservation{}
	for _, observation := range observations {
		byResource[observation.Resource] = observation
	}
	assert.Equal(t, db.DockerReconciliationQuarantine, byResource[db.DockerReconciliationResourceTask].State)
	assert.Equal(t, db.DockerReconciliationQuarantinePending, byResource[db.DockerReconciliationResourceTask].QuarantineStatus)
	assert.Equal(t, db.DockerReconciliationAbsent, byResource[db.DockerReconciliationResourceHelper].State)
}

func TestReduceDockerReconciliationScanAcceptsMoreThanOneHundredManagedResources(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	target := db.DockerReconciliationScanTarget{TargetBoot: "prior", ProjectID: 2, TaskID: 3, Generation: 4, Resource: db.DockerReconciliationResourceTask, ContainerName: "semaphore-task-3-g4-prior", Sequence: 7}
	session := db.DockerReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: 5, TargetBoot: "next", ScanTargets: []db.DockerReconciliationScanTarget{target}}
	resources := []ManagedResource{{Kind: ManagedContainer, ID: "target", Name: target.ContainerName, Labels: managedLabels(5, target), StateKnown: true}}
	for index := 0; index < 101; index++ {
		resources = append(resources, ManagedResource{Kind: ManagedContainer, ID: "extra-" + strconv.Itoa(index), Name: "extra-" + strconv.Itoa(index), Labels: map[string]string{"io.semaphore.managed": "v1"}, StateKnown: true})
	}
	observations, complete, candidates, err := reduceDockerReconciliationScan(session, resources, now)
	require.NoError(t, err)
	require.Len(t, observations, 1)
	assert.Equal(t, int64(7), observations[0].Sequence)
	assert.Equal(t, int64(7), complete.HighestSequence)
	assert.Len(t, candidates, maxDockerReconciliationPageSize)
}

func TestScanDockerReconciliationInspectsOnlyCurrentTargetPage(t *testing.T) {
	first := db.DockerReconciliationScanTarget{TargetBoot: "prior", ProjectID: 2, TaskID: 3, Generation: 4, Resource: db.DockerReconciliationResourceTask, ContainerName: "task-a", Sequence: 1}
	second := first
	second.Resource, second.ContainerName, second.Sequence = db.DockerReconciliationResourceHelper, "task-a-helper", 2
	client := &fakeDockerClient{inspectStates: map[string]ContainerState{
		first.ContainerName:  {Exists: true, ID: "daemon-task-id", Name: first.ContainerName, Labels: managedLabels(5, first)},
		second.ContainerName: {Exists: true, ID: "daemon-helper-id", Name: second.ContainerName, Labels: managedLabels(5, second)},
	}}
	provider := &Provider{client: client, runnerID: 5}
	session := db.DockerReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: 5, TargetBoot: "next", ScanTargets: []db.DockerReconciliationScanTarget{first, second}}
	observations, _, candidates, err := provider.ScanDockerReconciliation(context.Background(), session)
	require.NoError(t, err)
	assert.Len(t, observations, 2)
	byResource := make(map[db.DockerReconciliationResource]db.DockerReconciliationObservation, len(observations))
	for _, observation := range observations {
		byResource[observation.Resource] = observation
	}
	assert.Equal(t, "daemon-task-id", byResource[db.DockerReconciliationResourceTask].ContainerID)
	assert.Empty(t, candidates)
	assert.Equal(t, 2, client.inspectCalls)
	assert.Zero(t, client.listCalls, "exact target readiness must not enumerate daemon inventory")
}

func managedLabels(runnerID int, target db.DockerReconciliationScanTarget) map[string]string {
	return map[string]string{
		"io.semaphore.managed": "v1", labelExecutor: "docker", "io.semaphore.runner-id": strconv.Itoa(runnerID),
		labelTaskID: strconv.Itoa(target.TaskID), labelProjectID: strconv.Itoa(target.ProjectID),
		"io.semaphore.assignment-generation": strconv.Itoa(target.Generation), labelRunnerBoot: target.TargetBoot, labelResource: string(target.Resource),
	}
}
