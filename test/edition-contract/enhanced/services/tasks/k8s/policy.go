package k8s

import (
	"github.com/semaphoreui/semaphore/db"
)

func validateProviderConfiguration(policy db.KubernetesExecutionPolicy, cfg config) error {
	if policy.Hash == "" {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleClusterDenied}
	}
	return policy.ValidateExecution(db.KubernetesExecutionPolicyTestRequest{
		ClusterAlias: cfg.clusterAlias, Namespace: cfg.namespace,
		TaskImage: cfg.image, HelperImage: cfg.helperImage,
		ServiceAccount: cfg.serviceAccount, RuntimeClass: policy.RuntimeClass,
		NetworkProfile: policy.NetworkProfile, Resources: policy.Resources, TerminalRetentionSeconds: policy.TerminalRetentionSeconds,
		VolumeTypes:                  []db.KubernetesVolumeType{db.KubernetesVolumeEmptyDir, db.KubernetesVolumeSecret},
		HasRestrictedSecurityProfile: true,
	})
}

func validateTaskExecution(policy db.KubernetesExecutionPolicy, cfg config, image string) error {
	if policy.Hash == "" {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleClusterDenied}
	}
	return policy.ValidateExecution(db.KubernetesExecutionPolicyTestRequest{
		ClusterAlias: cfg.clusterAlias, Namespace: cfg.namespace,
		TaskImage: image, HelperImage: cfg.helperImage,
		ServiceAccount: cfg.serviceAccount, RuntimeClass: policy.RuntimeClass,
		NetworkProfile: policy.NetworkProfile, Resources: policy.Resources, TerminalRetentionSeconds: policy.TerminalRetentionSeconds,
		VolumeTypes:                  []db.KubernetesVolumeType{db.KubernetesVolumeEmptyDir, db.KubernetesVolumeSecret},
		HasRestrictedSecurityProfile: true,
	})
}
