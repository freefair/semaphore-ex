package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestClientCreatesImmutableTaskScopedBundleSecret(t *testing.T) {
	api := fake.NewSimpleClientset()
	api.PrependReactor("create", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		secret := action.(ktesting.CreateAction).GetObject().(*corev1.Secret).DeepCopy()
		secret.UID = types.UID("secret-uid")
		require.NoError(t, api.Tracker().Create(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, secret, secret.Namespace))
		return true, secret, nil
	})
	client := &client{config: config{namespace: "semaphore-jobs"}, api: api}

	identity, err := client.CreateBundleSecret(context.Background(), BundleSecret{
		Name: "semaphore-bundle-41-3", Labels: map[string]string{"io.semaphore.task-id": "41"},
		Data: []byte("bundle"), Immutable: true,
	})

	require.NoError(t, err)
	assert.Equal(t, "semaphore-bundle-41-3", identity.Name)
	stored, err := api.CoreV1().Secrets("semaphore-jobs").Get(context.Background(), identity.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, stored.Immutable)
	assert.True(t, *stored.Immutable)
	assert.Equal(t, []byte("bundle"), stored.Data[bundleArchiveKey])
	assert.Equal(t, map[string]string{"io.semaphore.task-id": "41"}, stored.Labels)
}

func TestClientRequiresExactJobAndPodTerminationEvidence(t *testing.T) {
	controller := true
	jobUID := types.UID("job-uid")
	podUID := types.UID("pod-uid")
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "semaphore-task-41-3", Namespace: "semaphore-jobs", UID: jobUID, Labels: labels},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "semaphore-task-41-3-pod", Namespace: "semaphore-jobs", UID: podUID, Labels: labels,
			OwnerReferences: []metav1.OwnerReference{{UID: jobUID, Controller: &controller}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  taskContainerName,
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, FinishedAt: metav1.NewTime(time.Now())}},
			}},
		},
	}
	api := fake.NewSimpleClientset(job, pod)
	client := &client{config: config{namespace: "semaphore-jobs", pollInterval: time.Millisecond}, api: api}

	observedPod, err := client.WaitForTaskPod(context.Background(), ObjectIdentity{Name: job.Name, UID: jobUID}, labels)
	require.NoError(t, err)
	assert.Equal(t, podUID, observedPod.UID)
	result, err := client.WaitForJob(context.Background(), ObjectIdentity{Name: job.Name, UID: jobUID}, observedPod)
	require.NoError(t, err)
	assert.Equal(t, JobResult{Succeeded: true, Lifecycle: "succeeded"}, result)

	wrongPod := observedPod
	wrongPod.UID = types.UID("reused-pod")
	_, err = client.WaitForJob(context.Background(), ObjectIdentity{Name: job.Name, UID: jobUID}, wrongPod)
	require.ErrorContains(t, err, "identity changed")
}
