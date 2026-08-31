package k8s

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesReconciliationScannerRequiresExactImmutableTuple(t *testing.T) {
	runnerID := 19
	task := db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}
	labels := taskLabels(task, runnerID)
	jobUID := types.UID("job-uid")
	controller := true
	api := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-task-41-3", Namespace: "semaphore-jobs", UID: jobUID, Labels: labels}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-task-41-3-pod", Namespace: "semaphore-jobs", UID: "pod-uid", Labels: labels, OwnerReferences: []metav1.OwnerReference{{UID: jobUID, Controller: &controller}}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-bundle-41-3", Namespace: "semaphore-jobs", UID: "secret-uid", Labels: labels}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-network-41-3", Namespace: "semaphore-jobs", UID: "network-uid", Labels: labels}},
	)
	session := reconciliationSessionFixture(task, runnerID)
	scanner := &client{config: config{clusterAlias: "qa", namespace: "semaphore-jobs"}, api: api}

	scan, err := scanner.ScanKubernetesReconciliation(context.Background(), session, runnerID)

	require.NoError(t, err)
	require.Len(t, scan.Observations, 1)
	assert.Equal(t, db.KubernetesReconciliationObserved, scan.Observations[0].State)
	assert.Empty(t, scan.Candidates)
	assert.Equal(t, session.SessionID, scan.SessionID)
	assert.Equal(t, session.Fence, scan.Fence)
	resources := make([]string, 0, 4)
	for _, action := range api.Actions() {
		if action.GetVerb() == "list" {
			resources = append(resources, action.GetResource().Resource)
		}
	}
	assert.Equal(t, []string{"jobs", "pods", "secrets", "networkpolicies"}, resources, "reconciliation has exactly four namespaced list calls")
}

func TestKubernetesReconciliationScannerQuarantinesMismatchAndReportsForeignObjects(t *testing.T) {
	runnerID := 19
	task := db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}
	labels := taskLabels(task, runnerID)
	foreignLabels := taskLabels(db.Task{ID: 99, ProjectID: 7, AssignmentGeneration: 1}, runnerID)
	api := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "reused-name", Namespace: "semaphore-jobs", UID: "wrong-job", Labels: labels}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-bundle-99-1", Namespace: "semaphore-jobs", UID: "foreign-secret", Labels: foreignLabels}},
	)
	session := reconciliationSessionFixture(task, runnerID)
	scanner := &client{config: config{clusterAlias: "qa", namespace: "semaphore-jobs"}, api: api}

	scan, err := scanner.ScanKubernetesReconciliation(context.Background(), session, runnerID)

	require.NoError(t, err)
	require.Len(t, scan.Observations, 1)
	assert.Equal(t, db.KubernetesReconciliationQuarantined, scan.Observations[0].State)
	assert.Equal(t, kubernetesReconciliationMismatch, scan.Observations[0].Reason)
	assert.Len(t, scan.Candidates, 2)
	for _, candidate := range scan.Candidates {
		assert.Equal(t, kubernetesReconciliationForeign, candidate.Reason)
	}
}

func TestKubernetesReconciliationScannerQuarantinesUnexpiredTerminalLeftoversAsCandidates(t *testing.T) {
	runnerID := 19
	task := db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}
	api := fake.NewSimpleClientset(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "semaphore-task-41-3", Namespace: "semaphore-jobs", UID: "unexpired-job", Labels: taskLabels(task, runnerID)}})
	session := db.KubernetesReconciliationSession{SessionID: "session", Fence: "fence", RunnerID: runnerID, ClusterAlias: "qa", Namespace: "semaphore-jobs"}
	scanner := &client{config: config{clusterAlias: "qa", namespace: "semaphore-jobs"}, api: api}

	scan, err := scanner.ScanKubernetesReconciliation(context.Background(), session, runnerID)

	require.NoError(t, err)
	assert.Empty(t, scan.Observations)
	require.Len(t, scan.Candidates, 1)
	assert.Equal(t, db.KubernetesReconciliationCandidate{Resource: "job", Name: "semaphore-task-41-3", UID: "unexpired-job", Reason: kubernetesReconciliationForeign}, scan.Candidates[0])
}

func TestProviderRejectsUninstalledKubernetesReconciliationSession(t *testing.T) {
	provider := newProviderWithClient(config{clusterAlias: "qa", namespace: "semaphore-jobs"}, &fakeKubernetesClient{})
	require.NoError(t, provider.ApplyRunnerIdentity(19))
	session := reconciliationSessionFixture(db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 3}, 19)

	_, err := provider.ScanKubernetesReconciliation(context.Background(), session)

	require.ErrorContains(t, err, "not installed")
}

func reconciliationSessionFixture(task db.Task, runnerID int) db.KubernetesReconciliationSession {
	return db.KubernetesReconciliationSession{
		SessionID: "session", Fence: "fence", RunnerID: runnerID, ClusterAlias: "qa", Namespace: "semaphore-jobs", Revision: 4,
		Targets: []db.KubernetesReconciliationTarget{{
			ProjectID: task.ProjectID, TaskID: task.ID, Generation: task.AssignmentGeneration,
			JobName: "semaphore-task-41-3", JobUID: "job-uid", PodName: "semaphore-task-41-3-pod", PodUID: "pod-uid",
			SecretName: "semaphore-bundle-41-3", SecretUID: "secret-uid",
			NetworkPolicyName: "semaphore-network-41-3", NetworkPolicyUID: "network-uid", RetentionState: "active",
		}},
	}
}
