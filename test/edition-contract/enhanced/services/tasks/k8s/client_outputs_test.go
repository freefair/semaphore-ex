package k8s

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func TestValidateOutputExecAccessRequiresPodsExecCreatePermission(t *testing.T) {
	for _, tt := range []struct {
		name    string
		allowed bool
		wantErr bool
	}{
		{name: "allowed", allowed: true},
		{name: "denied", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := fake.NewSimpleClientset()
			api.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
				review := action.(ktesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview).DeepCopy()
				assert.Equal(t, "semaphore-jobs", review.Spec.ResourceAttributes.Namespace)
				assert.Equal(t, "create", review.Spec.ResourceAttributes.Verb)
				assert.Equal(t, "pods", review.Spec.ResourceAttributes.Resource)
				assert.Equal(t, "exec", review.Spec.ResourceAttributes.Subresource)
				review.Status.Allowed = tt.allowed
				return true, review, nil
			})
			client := &client{config: config{namespace: "semaphore-jobs"}, api: api}

			err := client.validateOutputExecAccess(context.Background())

			if tt.wantErr {
				require.ErrorIs(t, err, db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied})
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWaitForTaskCompletionRequiresExactPodOwnershipAndLabels(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41", "io.semaphore.runner-id": "3"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}

	tests := []struct {
		name     string
		mutate   func(*corev1.Pod)
		exitCode int32
		wantErr  string
	}{
		{
			name:     "successful task",
			exitCode: 0,
		},
		{
			name:     "failed task",
			exitCode: 17,
		},
		{
			name: "reused pod UID",
			mutate: func(value *corev1.Pod) {
				value.UID = types.UID("replacement-pod-uid")
			},
			wantErr: "identity changed",
		},
		{
			name: "wrong owning job",
			mutate: func(value *corev1.Pod) {
				value.OwnerReferences[0].UID = types.UID("other-job-uid")
			},
			wantErr: "identity changed",
		},
		{
			name: "missing expected label",
			mutate: func(value *corev1.Pod) {
				delete(value.Labels, "io.semaphore.runner-id")
			},
			wantErr: "identity changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := workflowOutputPod(job, pod, labels, tt.exitCode)
			if tt.mutate != nil {
				tt.mutate(current)
			}
			client := &client{config: config{namespace: "semaphore-jobs", pollInterval: time.Millisecond}, api: fake.NewSimpleClientset(current)}

			exitCode, err := client.WaitForTaskCompletion(context.Background(), job, pod, labels)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.exitCode, exitCode)
		})
	}
}

func TestWaitForTaskCompletionHonorsContextCancellation(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}
	current := workflowOutputPod(job, pod, labels, 0)
	current.Status.ContainerStatuses[0].State.Terminated = nil
	client := &client{config: config{namespace: "semaphore-jobs", pollInterval: time.Millisecond}, api: fake.NewSimpleClientset(current)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := client.WaitForTaskCompletion(ctx, job, pod, labels)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecOutputStageTargetsOnlyValidatedOutputsSidecar(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}
	var namespace string
	var podName string
	var container string
	var command []string
	client := &client{
		config:     config{namespace: "semaphore-jobs"},
		api:        fake.NewSimpleClientset(workflowOutputPod(job, pod, labels, 0)),
		restConfig: &rest.Config{Host: "https://cluster.example.test"},
		streamOutputExec: func(_ context.Context, _ *rest.Config, actualNamespace, actualPod, actualContainer string, actualCommand []string, stdout, stderr io.Writer) error {
			namespace = actualNamespace
			podName = actualPod
			container = actualContainer
			command = append([]string(nil), actualCommand...)
			_, err := io.WriteString(stdout, `{"values":{"image":"v1"}}`)
			if err != nil {
				return err
			}
			_, err = io.WriteString(stderr, "sidecar diagnostic")
			return err
		},
	}
	var output bytes.Buffer

	err := client.execOutputStage(context.Background(), job, pod, labels, collectOutputStage, &output)

	require.NoError(t, err)
	assert.Equal(t, "semaphore-jobs", namespace)
	assert.Equal(t, pod.Name, podName)
	assert.Equal(t, outputsContainerName, container)
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "collect-outputs"}, command)
	assert.Equal(t, `{"values":{"image":"v1"}}`, output.String())

	err = client.execOutputStage(context.Background(), job, pod, labels, acknowledgeOutputStage, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/sh", "-c", ": > /semaphore/control/outputs-ack"}, command)
}

func TestExecOutputStageRejectsChangedIdentityAndUnapprovedStages(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}
	tests := []struct {
		name   string
		mutate func(*corev1.Pod)
		stage  outputStage
	}{
		{
			name: "replacement pod UID",
			mutate: func(value *corev1.Pod) {
				value.UID = types.UID("replacement-pod-uid")
			},
			stage: collectOutputStage,
		},
		{
			name: "different job owner",
			mutate: func(value *corev1.Pod) {
				value.OwnerReferences[0].UID = types.UID("different-job-uid")
			},
			stage: collectOutputStage,
		},
		{
			name: "missing required label",
			mutate: func(value *corev1.Pod) {
				delete(value.Labels, "io.semaphore.task-id")
			},
			stage: collectOutputStage,
		},
		{
			name:  "not a fixed stage",
			stage: outputStage("shell"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := workflowOutputPod(job, pod, labels, 0)
			if tt.mutate != nil {
				tt.mutate(current)
			}
			called := false
			client := &client{
				config: config{namespace: "semaphore-jobs"},
				api:    fake.NewSimpleClientset(current),
				streamOutputExec: func(context.Context, *rest.Config, string, string, string, []string, io.Writer, io.Writer) error {
					called = true
					return nil
				},
			}

			err := client.execOutputStage(context.Background(), job, pod, labels, tt.stage, io.Discard)

			require.Error(t, err)
			assert.False(t, called)
		})
	}
}

func TestExecOutputStageBoundsOutputAndHidesStreamFailures(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}
	tests := []struct {
		name   string
		stream func(io.Writer, io.Writer) error
	}{
		{
			name: "stdout exceeds limit",
			stream: func(stdout io.Writer, _ io.Writer) error {
				_, err := io.WriteString(stdout, strings.Repeat("x", maxKubernetesOutputStdoutBytes+1))
				return err
			},
		},
		{
			name: "stderr exceeds limit",
			stream: func(_ io.Writer, stderr io.Writer) error {
				_, err := io.WriteString(stderr, strings.Repeat("x", maxKubernetesOutputStderrBytes+1))
				return err
			},
		},
		{
			name: "stream error is generic",
			stream: func(io.Writer, io.Writer) error {
				return errors.New("untrusted output contains secret-value")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &client{
				config: config{namespace: "semaphore-jobs"},
				api:    fake.NewSimpleClientset(workflowOutputPod(job, pod, labels, 0)),
				streamOutputExec: func(_ context.Context, _ *rest.Config, _ string, _ string, _ string, _ []string, stdout, stderr io.Writer) error {
					return tt.stream(stdout, stderr)
				},
			}
			var output bytes.Buffer

			err := client.execOutputStage(context.Background(), job, pod, labels, collectOutputStage, &output)

			require.ErrorContains(t, err, "running Kubernetes workflow output operation")
			assert.NotContains(t, err.Error(), "secret-value")
			assert.Empty(t, output.String())
		})
	}
}

func TestExecTerraformOutputStageUsesSeparateRawTransportLimit(t *testing.T) {
	labels := map[string]string{"io.semaphore.task-id": "41"}
	job := ObjectIdentity{Name: "semaphore-task-41-3", UID: types.UID("job-uid")}
	pod := PodIdentity{ObjectIdentity: ObjectIdentity{Name: "semaphore-task-41-3-pod", UID: types.UID("pod-uid")}}
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "accepts raw document larger than public output", content: strings.Repeat("x", maxKubernetesOutputStdoutBytes+1)},
		{name: "rejects raw document over transport limit", content: strings.Repeat("secret-marker", tasks.MaxContainerTerraformRawOutputBytes/len("secret-marker")+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &client{
				config: config{namespace: "semaphore-jobs"},
				api:    fake.NewSimpleClientset(workflowOutputPod(job, pod, labels, 0)),
				streamOutputExec: func(_ context.Context, _ *rest.Config, _ string, _ string, _ string, _ []string, stdout, _ io.Writer) error {
					_, err := io.WriteString(stdout, tt.content)
					return err
				},
			}
			var output bytes.Buffer

			err := client.execOutputStage(context.Background(), job, pod, labels, terraformOutputStage, &output)

			if tt.wantErr {
				require.ErrorContains(t, err, "running Kubernetes workflow output operation")
				assert.NotContains(t, err.Error(), "secret-marker")
				assert.Empty(t, output.String())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.content, output.String())
		})
	}
}

func workflowOutputPod(job ObjectIdentity, pod PodIdentity, labels map[string]string, exitCode int32) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            pod.Name,
			Namespace:       "semaphore-jobs",
			UID:             pod.UID,
			Labels:          cloneLabels(labels),
			OwnerReferences: []metav1.OwnerReference{{UID: job.UID, Controller: &controller}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: taskContainerName}, {Name: outputsContainerName}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name:  taskContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode, FinishedAt: metav1.NewTime(time.Now())}},
		}}},
	}
}
