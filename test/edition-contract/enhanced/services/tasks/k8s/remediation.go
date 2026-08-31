package k8s

import (
	"context"
	"time"

	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

// GarbageCollectKubernetesReconciliation is intentionally a namespaced,
// closed-action deletion path. The server provides the persisted UID tuple;
// every object is read immediately before its UID-preconditioned delete.
func (c *client) GarbageCollectKubernetesReconciliation(ctx context.Context, command db.KubernetesReconciliationRemediationCommand, runnerID int) db.KubernetesReconciliationRemediationResult {
	result := db.KubernetesReconciliationRemediationResult{CommandID: command.CommandID, Status: db.KubernetesReconciliationRemediationBlocked, Evidence: db.KubernetesReconciliationEvidenceUnsafeTarget}
	target := command.Target
	if command.Validate() != nil || command.Action != db.KubernetesReconciliationGarbageCollectExpired || runnerID <= 0 || target.RetentionState != "terminal" || target.RetentionDeadline == nil || !target.RetentionDeadline.Before(time.Now().UTC()) {
		return result
	}
	labels := taskLabels(db.Task{ProjectID: target.ProjectID, ID: target.TaskID, AssignmentGeneration: target.Generation}, runnerID)
	job, jobMissing, safe := c.currentGCJob(ctx, target, labels)
	if !safe {
		return result
	}
	_, podMissing, safe := c.currentGCPod(ctx, target, labels)
	if !safe {
		return result
	}
	if jobMissing != podMissing {
		return result
	}
	removed := false
	if !jobMissing {
		// This is deliberately a second GET: the Pod ownership check above is
		// not the deletion proof. The object can be replaced between those
		// operations, so the UID/label proof must be immediately adjacent.
		job, jobMissing, safe = c.currentGCJob(ctx, target, labels)
		if !safe {
			return result
		}
		if jobMissing {
			if !c.waitForGCPodsAbsent(ctx, target, labels) {
				return result
			}
		} else {
			uid := job.UID
			propagation := metav1.DeletePropagationForeground
			started := time.Now()
			if err := c.api.BatchV1().Jobs(c.config.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &propagation, Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
				c.recordAPICall(db.KubernetesTelemetryOperationDeleteJob, started, err)
				return result
			}
			c.recordAPICall(db.KubernetesTelemetryOperationDeleteJob, started, nil)
			removed = true
			if !c.waitForGCPodsAbsent(ctx, target, labels) {
				return result
			}
		}
	}
	policy, policyMissing, safe := c.currentGCNetworkPolicy(ctx, target, labels)
	if !safe {
		return result
	}
	if !policyMissing {
		uid := policy.UID
		started := time.Now()
		if err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Delete(ctx, policy.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, err)
			return result
		}
		c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, nil)
		removed = true
		if !c.waitForGCNetworkPolicyAbsent(ctx, target, labels) {
			return result
		}
	}
	secret, secretMissing, safe := c.currentGCBundleSecret(ctx, target, labels)
	if !safe {
		return result
	}
	if !secretMissing {
		uid := secret.UID
		started := time.Now()
		if err := c.api.CoreV1().Secrets(c.config.namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
			c.recordAPICall(db.KubernetesTelemetryOperationDeleteSecret, started, err)
			return result
		}
		c.recordAPICall(db.KubernetesTelemetryOperationDeleteSecret, started, nil)
		removed = true
		if !c.waitForGCBundleSecretAbsent(ctx, target, labels) {
			return result
		}
	}
	result.Status = db.KubernetesReconciliationRemediationSucceeded
	if removed {
		result.Evidence = db.KubernetesReconciliationEvidenceRemoved
	} else {
		result.Evidence = db.KubernetesReconciliationEvidenceAlreadyAbsent
	}
	return result
}

func gcIdentityMatches(uid types.UID, labels, expected map[string]string, expectedUID string) bool {
	return string(uid) == expectedUID && labelsExactlyMatch(labels, expected)
}

func (c *client) currentGCJob(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) (*batchv1.Job, bool, bool) {
	started := time.Now()
	job, err := c.api.BatchV1().Jobs(c.config.namespace).Get(ctx, target.JobName, metav1.GetOptions{})
	c.recordAPICall(db.KubernetesTelemetryOperationGetJob, started, err)
	if apierrors.IsNotFound(err) {
		return nil, true, true
	}
	if err != nil || !gcIdentityMatches(job.UID, job.Labels, labels, target.JobUID) {
		return nil, false, false
	}
	return job, false, true
}

func (c *client) currentGCPod(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) (*corev1.Pod, bool, bool) {
	if target.PodUID == "" {
		return nil, true, true
	}
	started := time.Now()
	pod, err := c.api.CoreV1().Pods(c.config.namespace).Get(ctx, target.PodName, metav1.GetOptions{})
	c.recordAPICall(db.KubernetesTelemetryOperationGetPod, started, err)
	if apierrors.IsNotFound(err) {
		return nil, true, true
	}
	if err != nil || !gcIdentityMatches(pod.UID, pod.Labels, labels, target.PodUID) || !ownedBy(pod.OwnerReferences, types.UID(target.JobUID)) {
		return nil, false, false
	}
	return pod, false, true
}

func (c *client) currentGCNetworkPolicy(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) (*networkingv1.NetworkPolicy, bool, bool) {
	started := time.Now()
	policy, err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).Get(ctx, target.NetworkPolicyName, metav1.GetOptions{})
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteNetworkPolicy, started, err)
	if apierrors.IsNotFound(err) {
		return nil, true, true
	}
	if err != nil || !gcIdentityMatches(policy.UID, policy.Labels, labels, target.NetworkPolicyUID) {
		return nil, false, false
	}
	return policy, false, true
}

func (c *client) currentGCBundleSecret(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) (*corev1.Secret, bool, bool) {
	started := time.Now()
	secret, err := c.api.CoreV1().Secrets(c.config.namespace).Get(ctx, target.SecretName, metav1.GetOptions{})
	c.recordAPICall(db.KubernetesTelemetryOperationDeleteSecret, started, err)
	if apierrors.IsNotFound(err) {
		return nil, true, true
	}
	if err != nil || !gcIdentityMatches(secret.UID, secret.Labels, labels, target.SecretUID) {
		return nil, false, false
	}
	return secret, false, true
}

func (c *client) waitForGCPodsAbsent(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) bool {
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		job, jobMissing, safe := c.currentGCJob(ctx, target, labels)
		_ = job
		_, podMissing, podSafe := c.currentGCPod(ctx, target, labels)
		return jobMissing && podMissing, boolError(safe && podSafe)
	}) == nil
}

func (c *client) waitForGCNetworkPolicyAbsent(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) bool {
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		_, missing, safe := c.currentGCNetworkPolicy(ctx, target, labels)
		return missing, boolError(safe)
	}) == nil
}

func (c *client) waitForGCBundleSecretAbsent(ctx context.Context, target db.KubernetesReconciliationTarget, labels map[string]string) bool {
	return wait.PollUntilContextCancel(ctx, c.config.pollInterval, true, func(ctx context.Context) (bool, error) {
		_, missing, safe := c.currentGCBundleSecret(ctx, target, labels)
		return missing, boolError(safe)
	}) == nil
}

func boolError(ok bool) error {
	if ok {
		return nil
	}
	return context.Canceled
}
