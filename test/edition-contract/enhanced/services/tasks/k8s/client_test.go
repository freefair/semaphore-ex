package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestKubernetesAPIErrorUsesOnlyStableNonSensitiveCategories(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		rule string
	}{
		{"secret RBAC", apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "bundle", errors.New("operator detail")), db.KubernetesPolicyRuleRBACDenied},
		{"quota", apierrors.NewForbidden(schema.GroupResource{Resource: "jobs"}, "task", errors.New("exceeded quota with detail")), db.KubernetesPolicyRuleQuotaDenied},
		{"admission", apierrors.NewInvalid(schema.GroupKind{Group: "batch", Kind: "Job"}, "task", nil), db.KubernetesPolicyRuleAdmissionDenied},
		{"transport", errors.New("https://cluster.internal:6443 token=secret"), db.KubernetesPolicyRuleUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var violation db.KubernetesPolicyViolationError
			require.ErrorAs(t, kubernetesAPIError(tt.err), &violation)
			assert.Equal(t, tt.rule, violation.Rule)
			assert.NotContains(t, violation.Rule, "detail")
		})
	}
}

func TestClientTelemetryHooksUseOnlyStableOperationAndDenialEnums(t *testing.T) {
	api := fake.NewSimpleClientset()
	var events []db.KubernetesTelemetryEvent
	client := &client{config: config{namespace: "semaphore-jobs"}, api: api, recordTelemetry: func(event db.KubernetesTelemetryEvent) { events = append(events, event) }}
	_, err := client.CreateBundleSecret(context.Background(), BundleSecret{Name: "bundle", Labels: map[string]string{"managed": "true"}, Data: []byte("bundle"), Immutable: true})
	require.ErrorContains(t, err, "no UID")
	api.PrependReactor("create", "jobs", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "jobs"}, "task-name", errors.New("admission detail token=secret"))
	})
	_, err = client.CreateJob(context.Background(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "task-name", Namespace: "semaphore-jobs"}})
	require.Error(t, err)
	assert.Contains(t, events, db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryAPILatency, Operation: db.KubernetesTelemetryOperationCreateSecret})
	assert.Contains(t, events, db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryDenial, PolicyRule: db.KubernetesPolicyRuleRBACDenied})
	for _, event := range events {
		assert.NotContains(t, event.PolicyRule, "secret")
		assert.NotContains(t, event.PolicyRule, "task-name")
	}
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
