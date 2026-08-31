package k8s

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

const (
	kubernetesReconciliationMismatch = "identity_or_label_mismatch"
	kubernetesReconciliationForeign  = "foreign_or_duplicate"
)

type reconciliationObject struct {
	resource string
	name     string
	uid      types.UID
	labels   map[string]string
	owners   []metav1.OwnerReference
}

func (c *client) ScanKubernetesReconciliation(ctx context.Context, session db.KubernetesReconciliationSession, runnerID int) (db.KubernetesReconciliationScan, error) {
	if session.ValidateCredentials() != nil || session.RunnerID != runnerID || session.ClusterAlias != c.config.clusterAlias || session.Namespace != c.config.namespace {
		return db.KubernetesReconciliationScan{}, fmt.Errorf("Kubernetes reconciliation session does not match client configuration")
	}
	selector := klabels.Set(map[string]string{
		"app.kubernetes.io/managed-by": "semaphore",
		"io.semaphore.executor":        "k8s",
		"io.semaphore.runner-id":       strconv.Itoa(runnerID),
	}).String()
	options := metav1.ListOptions{LabelSelector: selector, Limit: 100}

	started := time.Now()
	jobs, err := c.api.BatchV1().Jobs(c.config.namespace).List(ctx, options)
	c.recordAPICall(db.KubernetesTelemetryOperationListJobs, started, err)
	if err != nil || jobs.Continue != "" {
		if err == nil {
			err = fmt.Errorf("Kubernetes reconciliation Job list exceeds bounded page")
		}
		return db.KubernetesReconciliationScan{}, kubernetesAPIError(err)
	}
	started = time.Now()
	pods, err := c.api.CoreV1().Pods(c.config.namespace).List(ctx, options)
	c.recordAPICall(db.KubernetesTelemetryOperationListPods, started, err)
	if err != nil || pods.Continue != "" {
		if err == nil {
			err = fmt.Errorf("Kubernetes reconciliation Pod list exceeds bounded page")
		}
		return db.KubernetesReconciliationScan{}, kubernetesAPIError(err)
	}
	started = time.Now()
	secrets, err := c.api.CoreV1().Secrets(c.config.namespace).List(ctx, options)
	c.recordAPICall(db.KubernetesTelemetryOperationListSecrets, started, err)
	if err != nil || secrets.Continue != "" {
		if err == nil {
			err = fmt.Errorf("Kubernetes reconciliation Secret list exceeds bounded page")
		}
		return db.KubernetesReconciliationScan{}, kubernetesAPIError(err)
	}
	started = time.Now()
	networkPolicies, err := c.api.NetworkingV1().NetworkPolicies(c.config.namespace).List(ctx, options)
	c.recordAPICall(db.KubernetesTelemetryOperationListNetworkPolicies, started, err)
	if err != nil || networkPolicies.Continue != "" {
		if err == nil {
			err = fmt.Errorf("Kubernetes reconciliation NetworkPolicy list exceeds bounded page")
		}
		return db.KubernetesReconciliationScan{}, kubernetesAPIError(err)
	}

	objects := reconciliationObjects(jobs.Items, pods.Items, secrets.Items, networkPolicies.Items)
	matched := make(map[string]bool, len(objects))
	scan := db.KubernetesReconciliationScan{SessionID: session.SessionID, Fence: session.Fence, Revision: session.Revision}
	for _, target := range session.Targets {
		observation := db.KubernetesReconciliationObservation{
			ProjectID: target.ProjectID, TaskID: target.TaskID, Generation: target.Generation,
			State: db.KubernetesReconciliationObserved, Revision: session.Revision,
		}
		expectedLabels := taskLabels(db.Task{ProjectID: target.ProjectID, ID: target.TaskID, AssignmentGeneration: target.Generation}, runnerID)
		checks := []struct {
			resource string
			name     string
			uid      string
			ownerUID string
			required bool
		}{
			{"job", target.JobName, target.JobUID, "", true},
			{"secret", target.SecretName, target.SecretUID, "", true},
			{"network_policy", target.NetworkPolicyName, target.NetworkPolicyUID, "", true},
			{"pod", target.PodName, target.PodUID, target.JobUID, target.PodUID != ""},
		}
		matchesExpected := 0
		mismatch := false
		for _, check := range checks {
			if !check.required {
				continue
			}
			matches := objectsWithExactLabels(objects, check.resource, expectedLabels)
			if len(matches) != 1 || matches[0].name != check.name || string(matches[0].uid) != check.uid ||
				(check.ownerUID != "" && !ownedBy(matches[0].owners, types.UID(check.ownerUID))) {
				mismatch = true
				continue
			}
			matchesExpected++
			matched[reconciliationObjectKey(matches[0])] = true
		}
		expiredTerminal := target.RetentionState == "terminal" && target.RetentionDeadline != nil && target.RetentionDeadline.Before(time.Now().UTC())
		if expiredTerminal && matchesExpected == 0 && !mismatch {
			observation.State = db.KubernetesReconciliationAbsent
		} else if mismatch {
			observation.State = db.KubernetesReconciliationQuarantined
			observation.Reason = kubernetesReconciliationMismatch
		}
		scan.Observations = append(scan.Observations, observation)
	}
	for _, object := range objects {
		if matched[reconciliationObjectKey(object)] {
			continue
		}
		if object.name == "" || object.uid == "" {
			return db.KubernetesReconciliationScan{}, fmt.Errorf("Kubernetes reconciliation object identity is incomplete")
		}
		scan.Candidates = append(scan.Candidates, db.KubernetesReconciliationCandidate{
			Resource: object.resource, Name: object.name, UID: string(object.uid),
			Reason: kubernetesReconciliationForeign, Revision: session.Revision,
		})
		if len(scan.Candidates) > 100 {
			return db.KubernetesReconciliationScan{}, fmt.Errorf("Kubernetes reconciliation candidate limit exceeded")
		}
	}
	c.recordReconciliationTelemetry(scan)
	return scan, nil
}

func (c *client) recordReconciliationTelemetry(scan db.KubernetesReconciliationScan) {
	if c.recordTelemetry == nil {
		return
	}
	var observed, absent, quarantined int64
	for _, observation := range scan.Observations {
		switch observation.State {
		case db.KubernetesReconciliationObserved:
			observed++
		case db.KubernetesReconciliationAbsent:
			absent++
		case db.KubernetesReconciliationQuarantined:
			quarantined++
		}
	}
	if observed > 0 {
		c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryReconciliation, ReconciliationState: db.KubernetesTelemetryReconciliationObserved, Count: observed})
	}
	if absent > 0 {
		c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryReconciliation, ReconciliationState: db.KubernetesTelemetryReconciliationAbsent, Count: absent})
	}
	if quarantined > 0 {
		c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryQuarantine, Count: quarantined})
	}
	if len(scan.Candidates) > 0 {
		c.recordTelemetry(db.KubernetesTelemetryEvent{Kind: db.KubernetesTelemetryOrphan, Count: int64(len(scan.Candidates))})
	}
}

func reconciliationObjects(jobs []batchv1.Job, pods []corev1.Pod, secrets []corev1.Secret, networkPolicies []networkingv1.NetworkPolicy) []reconciliationObject {
	objects := make([]reconciliationObject, 0, len(jobs)+len(pods)+len(secrets)+len(networkPolicies))
	for _, object := range jobs {
		objects = append(objects, reconciliationObject{"job", object.Name, object.UID, object.Labels, object.OwnerReferences})
	}
	for _, object := range pods {
		objects = append(objects, reconciliationObject{"pod", object.Name, object.UID, object.Labels, object.OwnerReferences})
	}
	for _, object := range secrets {
		objects = append(objects, reconciliationObject{"secret", object.Name, object.UID, object.Labels, object.OwnerReferences})
	}
	for _, object := range networkPolicies {
		objects = append(objects, reconciliationObject{"network_policy", object.Name, object.UID, object.Labels, object.OwnerReferences})
	}
	return objects
}

func objectsWithExactLabels(objects []reconciliationObject, resource string, expected map[string]string) []reconciliationObject {
	result := make([]reconciliationObject, 0, 1)
	for _, object := range objects {
		if object.resource == resource && labelsExactlyMatch(object.labels, expected) {
			result = append(result, object)
		}
	}
	return result
}

func reconciliationObjectKey(object reconciliationObject) string {
	return object.resource + "/" + string(object.uid)
}
