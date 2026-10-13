package k8s

import (
	"github.com/semaphoreui/semaphore/db"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const restrictedWorkloadUID int64 = 65532

const (
	outputsSidecarCPURequestMilli              int64 = 10
	outputsSidecarCPULimitMilli                int64 = 50
	outputsSidecarMemoryRequestBytes           int64 = 16 << 20
	outputsSidecarMemoryLimitBytes             int64 = 64 << 20
	outputsSidecarEphemeralStorageRequestBytes int64 = 8 << 20
	outputsSidecarEphemeralStorageLimitBytes   int64 = 32 << 20
)

func restrictedContainerSecurityContext() *corev1.SecurityContext {
	allowPrivilegeEscalation := false
	readOnlyRootFilesystem := true
	privileged := false
	runAsNonRoot := true
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
		Privileged:               &privileged,
		RunAsNonRoot:             &runAsNonRoot,
		RunAsUser:                int64Pointer(restrictedWorkloadUID),
		RunAsGroup:               int64Pointer(restrictedWorkloadUID),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{corev1.Capability("ALL")}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func restrictedPodSecurityContext() *corev1.PodSecurityContext {
	runAsNonRoot := true
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   &runAsNonRoot,
		RunAsUser:      int64Pointer(restrictedWorkloadUID),
		RunAsGroup:     int64Pointer(restrictedWorkloadUID),
		FSGroup:        int64Pointer(restrictedWorkloadUID),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func policyResources(resources db.KubernetesExecutionResources) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:              *resource.NewMilliQuantity(resources.CPURequestMilli, resource.DecimalSI),
			corev1.ResourceMemory:           *resource.NewQuantity(resources.MemoryRequestBytes, resource.BinarySI),
			corev1.ResourceEphemeralStorage: *resource.NewQuantity(resources.EphemeralStorageRequestBytes, resource.BinarySI),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              *resource.NewMilliQuantity(resources.CPULimitMilli, resource.DecimalSI),
			corev1.ResourceMemory:           *resource.NewQuantity(resources.MemoryLimitBytes, resource.BinarySI),
			corev1.ResourceEphemeralStorage: *resource.NewQuantity(resources.EphemeralStorageLimitBytes, resource.BinarySI),
		},
	}
}

// outputsSidecarResources gives the idle fixed-function sidecar a small,
// bounded envelope. Each value is clamped to the administrator-approved task
// budget, so it cannot exceed the per-container resource policy. The sidecar
// still adds its own bounded request to the Pod's quota footprint.
func outputsSidecarResources(resources db.KubernetesExecutionResources) corev1.ResourceRequirements {
	return policyResources(db.KubernetesExecutionResources{
		CPURequestMilli:              minInt64(resources.CPURequestMilli, outputsSidecarCPURequestMilli),
		CPULimitMilli:                minInt64(resources.CPULimitMilli, outputsSidecarCPULimitMilli),
		MemoryRequestBytes:           minInt64(resources.MemoryRequestBytes, outputsSidecarMemoryRequestBytes),
		MemoryLimitBytes:             minInt64(resources.MemoryLimitBytes, outputsSidecarMemoryLimitBytes),
		EphemeralStorageRequestBytes: minInt64(resources.EphemeralStorageRequestBytes, outputsSidecarEphemeralStorageRequestBytes),
		EphemeralStorageLimitBytes:   minInt64(resources.EphemeralStorageLimitBytes, outputsSidecarEphemeralStorageLimitBytes),
	})
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func policyEmptyDir(resources db.KubernetesExecutionResources) *corev1.EmptyDirVolumeSource {
	return &corev1.EmptyDirVolumeSource{SizeLimit: resource.NewQuantity(resources.EphemeralStorageLimitBytes, resource.BinarySI)}
}

func validateGeneratedJob(job *batchv1.Job, cfg config, policy db.KubernetesExecutionPolicy, taskImage string) error {
	if job == nil || job.Namespace != cfg.namespace || job.Spec.Template.Spec.HostNetwork || job.Spec.Template.Spec.HostPID || job.Spec.Template.Spec.HostIPC {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleHostNamespaceDenied}
	}
	if err := validateTaskExecution(policy, cfg, taskImage); err != nil {
		return err
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != cfg.serviceAccount || pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken ||
		pod.EnableServiceLinks == nil || *pod.EnableServiceLinks {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleServiceAccountDenied}
	}
	if !equalRuntimeClass(pod.RuntimeClassName, policy.RuntimeClass) {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleRuntimeClassDenied}
	}
	volumeTypes, hostPath, projectedToken, unsupportedVolume := generatedVolumeTypes(pod.Volumes)
	if hostPath {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleHostNamespaceDenied}
	}
	if projectedToken {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleProjectedTokenDenied}
	}
	if unsupportedVolume {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleVolumeDenied}
	}
	if err := policy.ValidateExecution(db.KubernetesExecutionPolicyTestRequest{
		ClusterAlias: cfg.clusterAlias, Namespace: cfg.namespace, TaskImage: taskImage, HelperImage: cfg.helperImage,
		ServiceAccount: cfg.serviceAccount, RuntimeClass: policy.RuntimeClass, NetworkProfile: policy.NetworkProfile,
		Resources: policy.Resources, TerminalRetentionSeconds: policy.TerminalRetentionSeconds, VolumeTypes: volumeTypes, HasRestrictedSecurityProfile: true,
	}); err != nil {
		return err
	}
	if !hasRestrictedPodSecurity(pod.SecurityContext) {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleSecurityProfileDenied}
	}
	if len(pod.InitContainers) != 1 || (len(pod.Containers) != 1 && len(pod.Containers) != 2) || pod.InitContainers[0].Image != cfg.helperImage || pod.Containers[0].Image != taskImage {
		return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleProjectOverrideDenied}
	}
	if len(pod.Containers) == 2 {
		outputs := pod.Containers[1]
		if outputs.Name != outputsContainerName || outputs.Image != cfg.helperImage || !hasVolumeMount(outputs.VolumeMounts, "workspace", workspacePath, true) || !hasVolumeMount(outputs.VolumeMounts, "bundle", bundlePath, true) || !hasVolumeMount(outputs.VolumeMounts, "outputs-control", outputsControlPath, false) || hasVolumeMount(pod.Containers[0].VolumeMounts, "outputs-control", outputsControlPath, false) {
			return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleProjectOverrideDenied}
		}
	}
	for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		if !hasRestrictedContainerSecurity(container.SecurityContext) {
			return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleSecurityProfileDenied}
		}
		expectedResources := policy.Resources
		if container.Name == outputsContainerName {
			expectedResources = outputsSidecarPolicyResources(policy.Resources)
		}
		if !equalResourceRequirements(container.Resources, expectedResources) {
			return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleResourceDenied}
		}
		if len(container.Env) != 0 || len(container.EnvFrom) != 0 {
			return db.KubernetesPolicyViolationError{Rule: db.KubernetesPolicyRuleProjectOverrideDenied}
		}
	}
	return nil
}

func outputsSidecarPolicyResources(resources db.KubernetesExecutionResources) db.KubernetesExecutionResources {
	return db.KubernetesExecutionResources{
		CPURequestMilli:              minInt64(resources.CPURequestMilli, outputsSidecarCPURequestMilli),
		CPULimitMilli:                minInt64(resources.CPULimitMilli, outputsSidecarCPULimitMilli),
		MemoryRequestBytes:           minInt64(resources.MemoryRequestBytes, outputsSidecarMemoryRequestBytes),
		MemoryLimitBytes:             minInt64(resources.MemoryLimitBytes, outputsSidecarMemoryLimitBytes),
		EphemeralStorageRequestBytes: minInt64(resources.EphemeralStorageRequestBytes, outputsSidecarEphemeralStorageRequestBytes),
		EphemeralStorageLimitBytes:   minInt64(resources.EphemeralStorageLimitBytes, outputsSidecarEphemeralStorageLimitBytes),
	}
}

func hasVolumeMount(mounts []corev1.VolumeMount, name string, path string, readOnly bool) bool {
	for _, mount := range mounts {
		if mount.Name == name && mount.MountPath == path && mount.ReadOnly == readOnly {
			return true
		}
	}
	return false
}

func generatedVolumeTypes(volumes []corev1.Volume) ([]db.KubernetesVolumeType, bool, bool, bool) {
	types := make([]db.KubernetesVolumeType, 0, len(volumes))
	for _, volume := range volumes {
		switch {
		case volume.Secret != nil:
			types = append(types, db.KubernetesVolumeSecret)
		case volume.EmptyDir != nil:
			types = append(types, db.KubernetesVolumeEmptyDir)
		case volume.HostPath != nil:
			return types, true, false, false
		case volume.Projected != nil:
			for _, source := range volume.Projected.Sources {
				if source.ServiceAccountToken != nil {
					return types, false, true, false
				}
			}
			return types, false, false, true
		default:
			return types, false, false, true
		}
	}
	return types, false, false, false
}

func hasRestrictedPodSecurity(security *corev1.PodSecurityContext) bool {
	return security != nil && security.RunAsNonRoot != nil && *security.RunAsNonRoot && security.RunAsUser != nil && *security.RunAsUser == restrictedWorkloadUID &&
		security.RunAsGroup != nil && *security.RunAsGroup == restrictedWorkloadUID && security.FSGroup != nil && *security.FSGroup == restrictedWorkloadUID &&
		security.SeccompProfile != nil && security.SeccompProfile.Type == corev1.SeccompProfileTypeRuntimeDefault
}

func hasRestrictedContainerSecurity(security *corev1.SecurityContext) bool {
	if security == nil || security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation ||
		security.ReadOnlyRootFilesystem == nil || !*security.ReadOnlyRootFilesystem || security.Privileged == nil || *security.Privileged ||
		security.RunAsNonRoot == nil || !*security.RunAsNonRoot || security.RunAsUser == nil || *security.RunAsUser != restrictedWorkloadUID ||
		security.RunAsGroup == nil || *security.RunAsGroup != restrictedWorkloadUID || security.SeccompProfile == nil || security.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault ||
		security.Capabilities == nil {
		return false
	}
	for _, capability := range security.Capabilities.Drop {
		if capability == corev1.Capability("ALL") {
			return true
		}
	}
	return false
}

func equalResourceRequirements(actual corev1.ResourceRequirements, expected db.KubernetesExecutionResources) bool {
	expectedRequirements := policyResources(expected)
	return actual.Requests.Cpu().MilliValue() == expectedRequirements.Requests.Cpu().MilliValue() &&
		actual.Limits.Cpu().MilliValue() == expectedRequirements.Limits.Cpu().MilliValue() &&
		actual.Requests.Memory().Value() == expectedRequirements.Requests.Memory().Value() &&
		actual.Limits.Memory().Value() == expectedRequirements.Limits.Memory().Value() &&
		actual.Requests.StorageEphemeral().Value() == expectedRequirements.Requests.StorageEphemeral().Value() &&
		actual.Limits.StorageEphemeral().Value() == expectedRequirements.Limits.StorageEphemeral().Value()
}

func equalRuntimeClass(actual *string, expected string) bool {
	if expected == "" {
		return actual == nil || *actual == ""
	}
	return actual != nil && *actual == expected
}

func int64Pointer(value int64) *int64 { return &value }
