package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type client struct {
	config          config
	api             kubernetes.Interface
	recordTelemetry func(db.KubernetesTelemetryEvent)
}

func (c *client) recordAPICall(operation db.KubernetesTelemetryOperation, started time.Time, err error) {
	if c.recordTelemetry == nil {
		return
	}
	c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryAPILatency, Operation: operation, DurationMilliseconds: time.Since(started).Milliseconds()})
	if err != nil && !apierrors.IsNotFound(err) {
		var violation db.KubernetesPolicyViolationError
		if errors.As(kubernetesAPIError(err), &violation) {
			c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryDenial, PolicyRule: violation.Rule})
		}
	}
}

func newKubernetesClient(cfg config) (KubernetesClient, error) {
	var restConfig *rest.Config
	var err error
	if cfg.kubeconfigPath == "" {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("loading explicit in-cluster Kubernetes configuration: %w", err)
		}
	} else {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: cfg.kubeconfigPath}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: cfg.context}
		restConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("loading explicit Kubernetes context: %w", err)
		}
	}
	restConfig.UserAgent = "semaphore-kubernetes-executor"
	api, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes API client: %w", err)
	}
	return &client{config: cfg, api: api}, nil
}

func (c *client) CreateBundleSecret(ctx context.Context, spec BundleSecret) (ObjectIdentity, error) {
	started := time.Now()
	immutable := spec.Immutable
	created, err := c.api.CoreV1().Secrets(c.config.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: c.config.namespace, Labels: cloneLabels(spec.Labels)},
		Immutable:  &immutable,
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{bundleArchiveKey: append([]byte(nil), spec.Data...)},
	}, metav1.CreateOptions{})
	if err != nil {
		c.recordAPICall(db.KubernetesTelemetryOperationCreateSecret, started, err)
		return ObjectIdentity{}, kubernetesAPIError(err)
	}
	c.recordAPICall(db.KubernetesTelemetryOperationCreateSecret, started, nil)
	if created.UID == "" {
		return ObjectIdentity{}, fmt.Errorf("created Kubernetes Secret has no UID")
	}
	return ObjectIdentity{Name: created.Name, UID: created.UID}, nil
}

func (c *client) CreateNetworkPolicy(ctx context.Context, policy *networkingv1.NetworkPolicy) (ObjectIdentity, error) {
	if policy == nil || policy.Namespace != c.config.namespace {
		return ObjectIdentity{}, fmt.Errorf("Kubernetes NetworkPolicy namespace does not match runner configuration")
	}
	started := time.Now()
	created, err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Create(ctx, policy, metav1.CreateOptions{})
	if err != nil {
		c.recordAPICall(db.KubernetesTelemetryOperationCreateNetworkPolicy, started, err)
		return ObjectIdentity{}, kubernetesAPIError(err)
	}
	c.recordAPICall(db.KubernetesTelemetryOperationCreateNetworkPolicy, started, nil)
	if created.UID == "" {
		return ObjectIdentity{}, fmt.Errorf("created Kubernetes NetworkPolicy has no UID")
	}
	return ObjectIdentity{Name: created.Name, UID: created.UID}, nil
}

func (c *client) CreateJob(ctx context.Context, job *batchv1.Job) (ObjectIdentity, error) {
	if job == nil || job.Namespace != c.config.namespace {
		return ObjectIdentity{}, fmt.Errorf("Kubernetes Job namespace does not match runner configuration")
	}
	started := time.Now()
	created, err := c.api.BatchV1().Jobs(c.config.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		c.recordAPICall(db.KubernetesTelemetryOperationCreateJob, started, err)
		return ObjectIdentity{}, kubernetesAPIError(err)
	}
	c.recordAPICall(db.KubernetesTelemetryOperationCreateJob, started, nil)
	if created.UID == "" {
		return ObjectIdentity{}, fmt.Errorf("created Kubernetes Job has no UID")
	}
	return ObjectIdentity{Name: created.Name, UID: created.UID}, nil
}

func (c *client) DeleteNetworkPolicy(ctx context.Context, policy ObjectIdentity, expectedLabels map[string]string) error {
	if policy.Name == "" || policy.UID == "" || len(expectedLabels) == 0 {
		return fmt.Errorf("Kubernetes NetworkPolicy identity is incomplete")
	}
	started := time.Now()
	current, err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Get(ctx, policy.Name, metav1.GetOptions{})
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, err)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return kubernetesAPIError(err)
	}
	if current.UID != policy.UID || !labelsExactlyMatch(current.Labels, expectedLabels) {
		return fmt.Errorf("Kubernetes NetworkPolicy identity changed before deletion")
	}
	uid := policy.UID
	started = time.Now()
	if err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Delete(ctx, policy.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
		c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, err)
		return kubernetesAPIError(err)
	}
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, nil)
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		started := time.Now()
		current, getErr := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Get(ctx, policy.Name, metav1.GetOptions{})
		c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, getErr)
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}
		if getErr != nil {
			return false, kubernetesAPIError(getErr)
		}
		if current.UID != policy.UID || !labelsExactlyMatch(current.Labels, expectedLabels) {
			return false, fmt.Errorf("Kubernetes NetworkPolicy name was reused during deletion")
		}
		return false, nil
	})
}

func (c *client) WaitForTaskPod(ctx context.Context, job ObjectIdentity, expectedLabels map[string]string) (PodIdentity, error) {
	selector := klabels.Set(expectedLabels).String()
	for {
		started := time.Now()
		list, err := c.api.CoreV1().Pods(c.config.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 10})
		c.recordAPICall(db.KubernetesTelemetryOperationListPods, started, err)
		if err != nil {
			return PodIdentity{}, err
		}
		var owned []corev1.Pod
		for _, pod := range list.Items {
			if ownedBy(pod.OwnerReferences, job.UID) && labelsMatch(pod.Labels, expectedLabels) {
				owned = append(owned, pod)
			}
		}
		if len(owned) > 1 {
			return PodIdentity{}, fmt.Errorf("Kubernetes Job produced more than one attributable Pod")
		}
		if len(owned) == 1 {
			if identity, ready, observeErr := observedPodIdentity(&owned[0]); ready || observeErr != nil {
				return identity, observeErr
			}
		}
		started = time.Now()
		stream, err := c.api.CoreV1().Pods(c.config.namespace).Watch(ctx, metav1.ListOptions{LabelSelector: selector, ResourceVersion: list.ResourceVersion})
		c.recordAPICall(db.KubernetesTelemetryOperationWatchPods, started, err)
		if err != nil {
			return PodIdentity{}, err
		}
		for event := range stream.ResultChan() {
			pod, ok := event.Object.(*corev1.Pod)
			if !ok || !ownedBy(pod.OwnerReferences, job.UID) || !labelsMatch(pod.Labels, expectedLabels) {
				continue
			}
			if event.Type == watch.Deleted {
				stream.Stop()
				return PodIdentity{}, fmt.Errorf("Kubernetes task Pod disappeared before observation")
			}
			if identity, ready, observeErr := observedPodIdentity(pod); ready || observeErr != nil {
				stream.Stop()
				return identity, observeErr
			}
		}
		stream.Stop()
		if ctx.Err() != nil {
			return PodIdentity{}, ctx.Err()
		}
		if c.recordTelemetry != nil {
			c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryWatchReconnect, Operation: db.KubernetesTelemetryOperationWatchPods, Count: 1})
		}
	}
}

func (c *client) OpenPodLogs(ctx context.Context, pod PodIdentity, since *time.Time, follow bool) (io.ReadCloser, error) {
	started := time.Now()
	current, err := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		c.recordAPICall(db.KubernetesTelemetryOperationGetPod, started, err)
		return nil, err
	}
	c.recordAPICall(db.KubernetesTelemetryOperationGetPod, started, nil)
	if current.UID != pod.UID || taskRestartCount(current) != pod.RestartCount {
		return nil, fmt.Errorf("Kubernetes task Pod identity changed before log streaming")
	}
	options := &corev1.PodLogOptions{Container: taskContainerName, Follow: follow, Timestamps: true}
	if since != nil {
		value := metav1.NewTime(since.UTC())
		options.SinceTime = &value
	}
	started = time.Now()
	stream, err := c.api.CoreV1().Pods(c.config.namespace).GetLogs(pod.Name, options).Stream(ctx)
	if err != nil {
		c.recordAPICall(db.KubernetesTelemetryOperationStreamPodLogs, started, err)
		return nil, fmt.Errorf("opening Kubernetes task log stream: %w", err)
	}
	c.recordAPICall(db.KubernetesTelemetryOperationStreamPodLogs, started, nil)
	return stream, nil
}

func (c *client) WaitForJob(ctx context.Context, job ObjectIdentity, pod PodIdentity) (JobResult, error) {
	fieldSelector := fields.OneTermEqualSelector("metadata.name", job.Name).String()
	for {
		started := time.Now()
		current, err := c.api.BatchV1().Jobs(c.config.namespace).Get(ctx, job.Name, metav1.GetOptions{})
		c.recordAPICall(db.KubernetesTelemetryOperationGetJob, started, err)
		if err != nil {
			return JobResult{}, err
		}
		if current.UID != job.UID {
			return JobResult{}, fmt.Errorf("Kubernetes Job identity changed during execution")
		}
		if jobTerminal(current) {
			return c.jobResult(ctx, current, job, pod)
		}
		started = time.Now()
		stream, err := c.api.BatchV1().Jobs(c.config.namespace).Watch(ctx, metav1.ListOptions{FieldSelector: fieldSelector, ResourceVersion: current.ResourceVersion})
		c.recordAPICall(db.KubernetesTelemetryOperationWatchJobs, started, err)
		if err != nil {
			return JobResult{}, err
		}
		for event := range stream.ResultChan() {
			observed, ok := event.Object.(*batchv1.Job)
			if !ok || observed.Name != job.Name {
				continue
			}
			if observed.UID != job.UID {
				stream.Stop()
				return JobResult{}, fmt.Errorf("Kubernetes Job identity changed during execution")
			}
			if event.Type == watch.Deleted {
				stream.Stop()
				return JobResult{}, fmt.Errorf("Kubernetes Job disappeared before terminal observation")
			}
			if jobTerminal(observed) {
				stream.Stop()
				return c.jobResult(ctx, observed, job, pod)
			}
		}
		stream.Stop()
		if ctx.Err() != nil {
			return JobResult{}, ctx.Err()
		}
		if c.recordTelemetry != nil {
			c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryWatchReconnect, Operation: db.KubernetesTelemetryOperationWatchJobs, Count: 1})
		}
	}
}

func (c *client) jobResult(ctx context.Context, currentJob *batchv1.Job, job ObjectIdentity, pod PodIdentity) (JobResult, error) {
	currentPod, err := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return JobResult{}, fmt.Errorf("reading terminal Kubernetes task Pod: %w", err)
	}
	if currentPod.UID != pod.UID || !ownedBy(currentPod.OwnerReferences, job.UID) {
		return JobResult{}, fmt.Errorf("terminal Kubernetes task Pod identity changed")
	}
	terminated := taskTermination(currentPod)
	if terminated == nil {
		return JobResult{}, fmt.Errorf("terminal Kubernetes task Pod lacks container termination evidence")
	}
	if jobSucceeded(currentJob) && terminated.ExitCode == 0 {
		return JobResult{Succeeded: true, Lifecycle: "succeeded"}, nil
	}
	return JobResult{Lifecycle: "failed", Reason: terminalReason(currentJob, currentPod, terminated)}, nil
}

func observedPodIdentity(pod *corev1.Pod) (PodIdentity, bool, error) {
	if pod.UID == "" {
		return PodIdentity{}, false, fmt.Errorf("observed Kubernetes task Pod has no UID")
	}
	switch pod.Status.Phase {
	case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
		return PodIdentity{ObjectIdentity: ObjectIdentity{Name: pod.Name, UID: pod.UID}, RestartCount: taskRestartCount(pod)}, true, nil
	default:
		return PodIdentity{}, false, nil
	}
}

func (c *client) DeleteJobForeground(ctx context.Context, job ObjectIdentity, pod PodIdentity, grace time.Duration) error {
	if job.Name == "" || job.UID == "" {
		return fmt.Errorf("Kubernetes Job identity is incomplete")
	}
	uid := job.UID
	graceSeconds := int64(grace.Seconds())
	propagation := metav1.DeletePropagationForeground
	started := time.Now()
	err := c.api.BatchV1().Jobs(c.config.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &graceSeconds,
		PropagationPolicy:  &propagation,
		Preconditions:      &metav1.Preconditions{UID: &uid},
	})
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteJob, started, err)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("foreground deleting Kubernetes Job: %w", err)
	}
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		started := time.Now()
		currentJob, getErr := c.api.BatchV1().Jobs(c.config.namespace).Get(ctx, job.Name, metav1.GetOptions{})
		c.recordAPICall(db.KubernetesTelemetryOperationGetJob, started, getErr)
		if getErr == nil && currentJob.UID != job.UID {
			return false, fmt.Errorf("Kubernetes Job name was reused during deletion")
		}
		if getErr != nil && !apierrors.IsNotFound(getErr) {
			return false, getErr
		}
		jobGone := apierrors.IsNotFound(getErr)
		podGone := pod.Name == ""
		if !podGone {
			started := time.Now()
			currentPod, podErr := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, pod.Name, metav1.GetOptions{})
			c.recordAPICall(db.KubernetesTelemetryOperationGetPod, started, podErr)
			switch {
			case podErr == nil && currentPod.UID != pod.UID:
				return false, fmt.Errorf("Kubernetes Pod name was reused during deletion")
			case podErr == nil:
				podGone = false
			case apierrors.IsNotFound(podErr):
				podGone = true
			default:
				return false, podErr
			}
		}
		return jobGone && podGone, nil
	})
}

func (c *client) DeleteBundleSecret(ctx context.Context, secret ObjectIdentity) error {
	if secret.Name == "" || secret.UID == "" {
		return fmt.Errorf("Kubernetes bundle Secret identity is incomplete")
	}
	uid := secret.UID
	started := time.Now()
	err := c.api.CoreV1().Secrets(c.config.namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteSecret, started, err)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Kubernetes task bundle Secret: %w", err)
	}
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		started := time.Now()
		current, getErr := c.api.CoreV1().Secrets(c.config.namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		c.recordAPICall(db.KubernetesTelemetryOperationDeleteSecret, started, getErr)
		if getErr == nil && current.UID != secret.UID {
			return false, fmt.Errorf("Kubernetes Secret name was reused during deletion")
		}
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}
		return false, getErr
	})
}

func cloneLabels(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func labelsMatch(actual map[string]string, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func labelsExactlyMatch(actual map[string]string, expected map[string]string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return labelsMatch(actual, expected)
}

func kubernetesAPIError(err error) error {
	if apierrors.IsForbidden(err) {
		if strings.Contains(strings.ToLower(err.Error()), "quota") {
			return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleQuotaDenied}
		}
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRBACDenied}
	}
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleAdmissionDenied}
	}
	return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleUnavailable}
}

func ownedBy(references []metav1.OwnerReference, uid types.UID) bool {
	for _, reference := range references {
		if reference.UID == uid && reference.Controller != nil && *reference.Controller {
			return true
		}
	}
	return false
}

func taskRestartCount(pod *corev1.Pod) int32 {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == taskContainerName {
			return status.RestartCount
		}
	}
	return 0
}

func taskTermination(pod *corev1.Pod) *corev1.ContainerStateTerminated {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == taskContainerName {
			return status.State.Terminated
		}
	}
	return nil
}

func jobTerminal(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) {
			return true
		}
	}
	return false
}

func jobSucceeded(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue && condition.Type == batchv1.JobComplete {
			return true
		}
	}
	return false
}

func terminalReason(job *batchv1.Job, pod *corev1.Pod, terminated *corev1.ContainerStateTerminated) string {
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue && condition.Type == batchv1.JobFailed {
			switch condition.Reason {
			case "DeadlineExceeded", "BackoffLimitExceeded", "FailedIndexes", "PodFailurePolicy":
				return condition.Reason
			}
		}
	}
	switch terminated.Reason {
	case "OOMKilled", "Error", "ContainerCannotRun", "DeadlineExceeded":
		return terminated.Reason
	}
	switch pod.Status.Reason {
	case "Evicted", "DeadlineExceeded", "NodeLost", "Shutdown":
		return pod.Status.Reason
	}
	return "NonZeroExit"
}
