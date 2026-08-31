package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestKubernetesExpiredGarbageCollectionRequiresExactOwnershipProof(t *testing.T) {
	target, labels := expiredGCTarget(t)
	controller := true
	api := fake.NewSimpleClientset(
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: target.JobName, Namespace: "semaphore-jobs", UID: types.UID(target.JobUID), Labels: labels}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: target.PodName, Namespace: "semaphore-jobs", UID: types.UID(target.PodUID), Labels: labels, OwnerReferences: []metav1.OwnerReference{{UID: types.UID(target.JobUID), Controller: &controller}}}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: target.NetworkPolicyName, Namespace: "semaphore-jobs", UID: types.UID(target.NetworkPolicyUID), Labels: labels}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: target.SecretName, Namespace: "semaphore-jobs", UID: types.UID(target.SecretUID), Labels: labels}},
	)
	api.PrependReactor("delete", "jobs", func(action ktesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(ktesting.DeleteAction)
		require.NoError(t, api.Tracker().Delete(schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, "semaphore-jobs", deleteAction.GetName()))
		require.NoError(t, api.Tracker().Delete(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, "semaphore-jobs", target.PodName))
		return true, nil, nil
	})
	client := &client{config: config{namespace: "semaphore-jobs", pollInterval: time.Millisecond}, api: api}
	command := gcCommand(target)

	result := client.GarbageCollectKubernetesReconciliation(context.Background(), command, 19)

	assert.Equal(t, db.KubernetesReconciliationRemediationSucceeded, result.Status)
	assert.Equal(t, db.KubernetesReconciliationEvidenceRemoved, result.Evidence)
	for _, identity := range []struct{ name, resource string }{{target.JobName, "job"}, {target.PodName, "pod"}, {target.NetworkPolicyName, "network policy"}, {target.SecretName, "secret"}} {
		switch identity.resource {
		case "job":
			_, err := api.BatchV1().Jobs("semaphore-jobs").Get(context.Background(), identity.name, metav1.GetOptions{})
			require.Error(t, err)
		case "pod":
			_, err := api.CoreV1().Pods("semaphore-jobs").Get(context.Background(), identity.name, metav1.GetOptions{})
			require.Error(t, err)
		case "network policy":
			_, err := api.NetworkingV1().NetworkPolicies("semaphore-jobs").Get(context.Background(), identity.name, metav1.GetOptions{})
			require.Error(t, err)
		case "secret":
			_, err := api.CoreV1().Secrets("semaphore-jobs").Get(context.Background(), identity.name, metav1.GetOptions{})
			require.Error(t, err)
		}
	}
}

func TestKubernetesExpiredGarbageCollectionRefusesNameReuseOrForeignLabels(t *testing.T) {
	target, labels := expiredGCTarget(t)
	foreign := cloneLabels(labels)
	foreign["io.semaphore.managed"] = "foreign"
	api := fake.NewSimpleClientset(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: target.JobName, Namespace: "semaphore-jobs", UID: "reused-job", Labels: foreign}})
	client := &client{config: config{namespace: "semaphore-jobs", pollInterval: time.Millisecond}, api: api}

	result := client.GarbageCollectKubernetesReconciliation(context.Background(), gcCommand(target), 19)

	assert.Equal(t, db.KubernetesReconciliationRemediationBlocked, result.Status)
	assert.Equal(t, db.KubernetesReconciliationEvidenceUnsafeTarget, result.Evidence)
	stillThere, err := api.BatchV1().Jobs("semaphore-jobs").Get(context.Background(), target.JobName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.UID("reused-job"), stillThere.UID)
}

func expiredGCTarget(t testing.TB) (db.KubernetesReconciliationTarget, map[string]string) {
	t.Helper()
	deadline := time.Now().UTC().Add(-time.Minute)
	target := db.KubernetesReconciliationTarget{ProjectID: 7, TaskID: 41, Generation: 3, JobName: "semaphore-task-41-3", JobUID: "job-uid", PodName: "semaphore-task-41-3-pod", PodUID: "pod-uid", SecretName: "semaphore-bundle-41-3", SecretUID: "secret-uid", NetworkPolicyName: "semaphore-network-41-3", NetworkPolicyUID: "network-uid", RetentionState: "terminal", RetentionDeadline: &deadline}
	return target, taskLabels(db.Task{ProjectID: target.ProjectID, ID: target.TaskID, AssignmentGeneration: target.Generation}, 19)
}

func gcCommand(target db.KubernetesReconciliationTarget) db.KubernetesReconciliationRemediationCommand {
	return db.KubernetesReconciliationRemediationCommand{CommandID: "command", SessionID: "session", Action: db.KubernetesReconciliationGarbageCollectExpired, ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation, Target: target}
}
