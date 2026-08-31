package k8s

import (
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildNetworkPolicy produces the only network shape supported by this
// executor. It is intentionally a namespaced deny-all policy; whether the
// cluster enforces it is an administrator-owned policy declaration.
func buildNetworkPolicy(cfg config, policy db.KubernetesExecutionPolicy, task db.Task, runnerID int) (*networkingv1.NetworkPolicy, error) {
	if policy.NetworkProfile != string(db.KubernetesNetworkProfileDenyAll) ||
		policy.NetworkPolicyEnforcement != db.KubernetesNetworkPolicyEnforcementNetworkPolicy {
		return nil, db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleNetworkProfileUnsupported}
	}
	labels := taskLabels(task, runnerID)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskObjectName("semaphore-network", task),
			Namespace: cfg.namespace,
			Labels:    cloneLabels(labels),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: cloneLabels(labels)},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	}, nil
}

func validateNetworkPolicy(policy *networkingv1.NetworkPolicy, cfg config, task db.Task, runnerID int) error {
	if policy == nil || policy.Namespace != cfg.namespace || policy.Name != taskObjectName("semaphore-network", task) {
		return fmt.Errorf("invalid task NetworkPolicy identity")
	}
	expected := taskLabels(task, runnerID)
	if !labelsExactlyMatch(policy.Labels, expected) || !labelsExactlyMatch(policy.Spec.PodSelector.MatchLabels, expected) ||
		len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 ||
		len(policy.Spec.PolicyTypes) != 2 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress || policy.Spec.PolicyTypes[1] != networkingv1.PolicyTypeEgress {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleNetworkProfileDenied}
	}
	return nil
}
