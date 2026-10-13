package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/tasks"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	maxKubernetesOutputStdoutBytes = 256 * 1024
	maxKubernetesOutputStderrBytes = 64 * 1024
)

// validateOutputExecAccess fails before creating task-scoped objects when the
// runner cannot open the narrowly fixed outputs-sidecar exec stream.
func (c *client) validateOutputExecAccess(ctx context.Context) error {
	review, err := c.api.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
			Namespace:   c.config.namespace,
			Verb:        "create",
			Resource:    "pods",
			Subresource: "exec",
		}},
	}, metav1.CreateOptions{})
	if err != nil {
		return kubernetesAPIError(err)
	}
	if !review.Status.Allowed {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied}
	}
	return nil
}

// WaitForTaskCompletion waits for Kubernetes to confirm that the fixed task
// container, not a task-controlled marker, has terminated. A reused pod,
// another Job, or a label mismatch can never satisfy this check.
func (c *client) WaitForTaskCompletion(ctx context.Context, job ObjectIdentity, pod PodIdentity, expectedLabels map[string]string) (int32, error) {
	if job.Name == "" || job.UID == "" || pod.Name == "" || pod.UID == "" || len(expectedLabels) == 0 {
		return 0, fmt.Errorf("Kubernetes workflow output identity is incomplete")
	}

	pollInterval := c.config.pollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	var exitCode int32
	err := waitForTaskContainerTermination(ctx, pollInterval, func(ctx context.Context) (*corev1.Pod, error) {
		current, err := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return nil, kubernetesAPIError(err)
		}
		return current, nil
	}, func(current *corev1.Pod) (bool, error) {
		if current.UID != pod.UID || !ownedBy(current.OwnerReferences, job.UID) || !labelsMatch(current.Labels, expectedLabels) {
			return false, fmt.Errorf("Kubernetes workflow output Pod identity changed")
		}
		terminated := taskTermination(current)
		if terminated == nil {
			return false, nil
		}
		exitCode = terminated.ExitCode
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	return exitCode, nil
}

// execOutputStage runs one of the two fixed output operations in the private
// outputs sidecar. It validates ownership immediately before every exec so a
// deleted and recreated Pod cannot receive an operation intended for a prior
// task. Output is never logged by this client.
func (c *client) execOutputStage(ctx context.Context, job ObjectIdentity, pod PodIdentity, expectedLabels map[string]string, stage outputStage, stdout io.Writer) error {
	if job.Name == "" || job.UID == "" || pod.Name == "" || pod.UID == "" || len(expectedLabels) == 0 {
		return fmt.Errorf("Kubernetes workflow output identity is incomplete")
	}
	command, ok := fixedOutputStageCommand(stage)
	if !ok {
		return fmt.Errorf("unsupported Kubernetes workflow output operation")
	}

	current, err := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return kubernetesAPIError(err)
	}
	if current.UID != pod.UID || !ownedBy(current.OwnerReferences, job.UID) || !labelsMatch(current.Labels, expectedLabels) || !podHasContainer(current, outputsContainerName) {
		return fmt.Errorf("Kubernetes workflow output Pod identity changed")
	}

	if stdout == nil {
		stdout = io.Discard
	}
	stdoutLimit := maxKubernetesOutputStdoutBytes
	if stage == terraformOutputStage {
		stdoutLimit = tasks.MaxContainerTerraformRawOutputBytes
	}
	if err := c.streamWorkflowOutputExec(ctx, pod.Name, command,
		&boundedOutputWriter{writer: stdout, limit: stdoutLimit},
		&boundedOutputWriter{writer: io.Discard, limit: maxKubernetesOutputStderrBytes}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("running Kubernetes workflow output operation")
	}
	return nil
}

func fixedOutputStageCommand(stage outputStage) ([]string, bool) {
	switch stage {
	case collectOutputStage:
		return []string{"/bin/sh", "/semaphore/bundle/run.sh", "collect-outputs"}, true
	case terraformOutputStage:
		return []string{"/bin/sh", "/semaphore/bundle/run.sh", "terraform-outputs"}, true
	case acknowledgeOutputStage:
		return []string{"/bin/sh", "-c", ": > /semaphore/control/outputs-ack"}, true
	default:
		return nil, false
	}
}

func (c *client) streamWorkflowOutputExec(ctx context.Context, podName string, command []string, stdout, stderr io.Writer) error {
	if c.streamOutputExec != nil {
		return c.streamOutputExec(ctx, c.restConfig, c.config.namespace, podName, outputsContainerName, command, stdout, stderr)
	}
	request := c.api.CoreV1().RESTClient().Post().Resource("pods").Name(podName).Namespace(c.config.namespace).SubResource("exec")
	request.VersionedParams(&corev1.PodExecOptions{Container: outputsContainerName, Command: command, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("creating Kubernetes workflow output stream")
	}
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: stdout, Stderr: stderr}); err != nil {
		return err
	}
	return nil
}

func waitForTaskContainerTermination(ctx context.Context, interval time.Duration, getPod func(context.Context) (*corev1.Pod, error), examine func(*corev1.Pod) (bool, error)) error {
	return pollUntilContextCancel(ctx, interval, func(ctx context.Context) (bool, error) {
		pod, err := getPod(ctx)
		if err != nil {
			return false, err
		}
		return examine(pod)
	})
}

func pollUntilContextCancel(ctx context.Context, interval time.Duration, condition func(context.Context) (bool, error)) error {
	if done, err := condition(ctx); done || err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, err := condition(ctx)
			if done || err != nil {
				return err
			}
		}
	}
}

func podHasContainer(pod *corev1.Pod, name string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return true
		}
	}
	return false
}

type boundedOutputWriter struct {
	writer  io.Writer
	limit   int
	written int
}

func (w *boundedOutputWriter) Write(value []byte) (int, error) {
	if len(value) > w.limit-w.written {
		return 0, fmt.Errorf("Kubernetes workflow output stream exceeded permitted size")
	}
	n, err := w.writer.Write(value)
	w.written += n
	return n, err
}
