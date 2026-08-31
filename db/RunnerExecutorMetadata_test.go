package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const executorMetadataDigest = "registry.example.test/semaphore/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestRunnerExecutorMetadataValidatesBoundedKubernetesProjection(t *testing.T) {
	metadata := RunnerExecutorMetadata{
		ExecutorType:      RunnerExecutorK8s,
		RequestedImage:    executorMetadataDigest,
		ResolvedImage:     executorMetadataDigest,
		K8sClusterAlias:   "qa-cluster",
		K8sNamespace:      "semaphore-jobs",
		K8sJobName:        "semaphore-task-41-3",
		K8sJobUID:         "6f692c50-c01f-4f2e-8592-2fe6c6cf4c7d",
		K8sPodName:        "semaphore-task-41-3-b7d9f",
		K8sPodUID:         "c885a148-5204-495a-ac51-841dfb831091",
		K8sContainerName:  "task",
		K8sLifecycle:      "running",
		K8sPolicyRevision: 1,
		K8sPolicyHash:     strings.Repeat("a", 64),
		K8sServiceAccount: "semaphore-task", K8sResourcePolicyID: "1", K8sResourcePolicyHash: strings.Repeat("a", 64),
		K8sNetworkProfile: string(KubernetesNetworkProfileDenyAll), K8sNetworkEnforcement: string(KubernetesNetworkPolicyEnforcementNetworkPolicy),
		K8sSecretName: "semaphore-bundle-41-3", K8sSecretUID: "secret-uid", K8sNetworkPolicyName: "semaphore-network-41-3", K8sNetworkPolicyUID: "network-policy-uid", K8sRetentionState: "active",
	}

	require.NoError(t, metadata.Validate(RunnerExecutorK8s))
}

func TestRunnerExecutorMetadataRejectsUnsafeKubernetesProjection(t *testing.T) {
	valid := RunnerExecutorMetadata{
		ExecutorType:      RunnerExecutorK8s,
		RequestedImage:    executorMetadataDigest,
		ResolvedImage:     executorMetadataDigest,
		K8sClusterAlias:   "qa-cluster",
		K8sNamespace:      "semaphore-jobs",
		K8sJobName:        "semaphore-task-41-3",
		K8sJobUID:         "6f692c50-c01f-4f2e-8592-2fe6c6cf4c7d",
		K8sPodName:        "semaphore-task-41-3-b7d9f",
		K8sPodUID:         "c885a148-5204-495a-ac51-841dfb831091",
		K8sContainerName:  "task",
		K8sLifecycle:      "running",
		K8sPolicyRevision: 1,
		K8sPolicyHash:     strings.Repeat("a", 64),
		K8sServiceAccount: "semaphore-task", K8sResourcePolicyID: "1", K8sResourcePolicyHash: strings.Repeat("a", 64),
		K8sNetworkProfile: string(KubernetesNetworkProfileDenyAll), K8sNetworkEnforcement: string(KubernetesNetworkPolicyEnforcementNetworkPolicy),
		K8sSecretName: "semaphore-bundle-41-3", K8sSecretUID: "secret-uid", K8sNetworkPolicyName: "semaphore-network-41-3", K8sNetworkPolicyUID: "network-policy-uid", K8sRetentionState: "active",
	}
	tests := []struct {
		name   string
		mutate func(*RunnerExecutorMetadata)
	}{
		{name: "mutable image", mutate: func(value *RunnerExecutorMetadata) {
			value.ResolvedImage = "registry.example.test/semaphore/job:latest"
		}},
		{name: "invalid namespace", mutate: func(value *RunnerExecutorMetadata) { value.K8sNamespace = "Bad_Namespace" }},
		{name: "missing job uid", mutate: func(value *RunnerExecutorMetadata) { value.K8sJobUID = "" }},
		{name: "missing running pod", mutate: func(value *RunnerExecutorMetadata) { value.K8sPodUID = "" }},
		{name: "unknown lifecycle", mutate: func(value *RunnerExecutorMetadata) { value.K8sLifecycle = "evicted-with-secret-message" }},
		{name: "unbounded reason", mutate: func(value *RunnerExecutorMetadata) { value.K8sTerminalReason = strings.Repeat("x", 129) }},
		{name: "raw terminal message", mutate: func(value *RunnerExecutorMetadata) {
			value.K8sLifecycle = "failed"
			value.K8sTerminalReason = "raw Kubernetes message: token=must-not-persist"
		}},
		{name: "docker field", mutate: func(value *RunnerExecutorMetadata) { value.ContainerID = "docker-container" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := valid
			tt.mutate(&metadata)
			require.Error(t, metadata.Validate(RunnerExecutorK8s))
		})
	}
}

func TestRunnerExecutorMetadataAcceptsExactLegacyKubernetesShapeButRejectsPartialProvenance(t *testing.T) {
	legacy := RunnerExecutorMetadata{ExecutorType: RunnerExecutorK8s, RequestedImage: executorMetadataDigest, ResolvedImage: executorMetadataDigest,
		K8sClusterAlias: "qa-cluster", K8sNamespace: "semaphore-jobs", K8sJobName: "semaphore-task-41-3", K8sJobUID: "job-uid",
		K8sPodName: "semaphore-task-41-3-pod", K8sPodUID: "pod-uid", K8sContainerName: "task", K8sLifecycle: "running"}
	require.NoError(t, legacy.Validate(RunnerExecutorK8s))
	legacy.K8sNetworkProfile = string(KubernetesNetworkProfileDenyAll)
	require.Error(t, legacy.Validate(RunnerExecutorK8s))
}
