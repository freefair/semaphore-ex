package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const kubernetesPolicyTestImage = "registry.example.test/semaphore/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testKubernetesExecutionPolicy() KubernetesExecutionPolicy {
	return KubernetesExecutionPolicy{
		ClusterAlias:             "qa-cluster",
		Revision:                 3,
		AllowedNamespaces:        []string{"semaphore-jobs"},
		AllowedImages:            []string{kubernetesPolicyTestImage},
		AllowedServiceAccounts:   []string{"semaphore-task"},
		AllowedRuntimeClasses:    []string{""},
		AllowedVolumeTypes:       []KubernetesVolumeType{KubernetesVolumeSecret, KubernetesVolumeEmptyDir},
		AllowedNetworkProfiles:   []string{"deny-all"},
		NetworkProfile:           "deny-all",
		NetworkPolicyEnforcement: KubernetesNetworkPolicyEnforcementNetworkPolicy,
		Resources: KubernetesExecutionResources{
			CPURequestMilli: 100, CPULimitMilli: 500,
			MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20,
			EphemeralStorageRequestBytes: 64 << 20, EphemeralStorageLimitBytes: 256 << 20,
		},
		TerminalRetentionSeconds: 3600,
	}
}

func testKubernetesExecutionRequest(policy KubernetesExecutionPolicy) KubernetesExecutionPolicyTestRequest {
	return KubernetesExecutionPolicyTestRequest{
		ClusterAlias: policy.ClusterAlias, Namespace: "semaphore-jobs", TaskImage: kubernetesPolicyTestImage,
		HelperImage: kubernetesPolicyTestImage, ServiceAccount: "semaphore-task", RuntimeClass: "",
		NetworkProfile: "deny-all", Resources: policy.Resources, TerminalRetentionSeconds: policy.TerminalRetentionSeconds,
		VolumeTypes:                  []KubernetesVolumeType{KubernetesVolumeSecret, KubernetesVolumeEmptyDir},
		HasRestrictedSecurityProfile: true,
	}
}

func TestKubernetesExecutionPolicyCanonicalizesDeterministicallyAndAcknowledges(t *testing.T) {
	left := testKubernetesExecutionPolicy()
	left.AllowedImages = []string{kubernetesPolicyTestImage, kubernetesPolicyTestImage}
	left.AllowedVolumeTypes = []KubernetesVolumeType{KubernetesVolumeSecret, KubernetesVolumeEmptyDir, KubernetesVolumeSecret}
	right := testKubernetesExecutionPolicy()
	require.NoError(t, left.Canonicalize())
	require.NoError(t, right.Canonicalize())
	assert.Equal(t, right.Hash, left.Hash)
	assert.Equal(t, []KubernetesVolumeType{KubernetesVolumeEmptyDir, KubernetesVolumeSecret}, left.AllowedVolumeTypes)
	assert.True(t, left.MatchesAck(left.Acknowledgement()))
	assert.False(t, left.MatchesAck(KubernetesExecutionPolicyAck{ClusterAlias: left.ClusterAlias, Revision: left.Revision, Hash: "wrong"}))
}

func TestKubernetesExecutionPolicyFailsClosedAndReturnsStableDenials(t *testing.T) {
	policy := DefaultKubernetesExecutionPolicy("qa-cluster")
	assert.False(t, policy.Test(KubernetesExecutionPolicyTestRequest{}).Allowed)

	policy = testKubernetesExecutionPolicy()
	require.NoError(t, policy.Canonicalize())
	request := testKubernetesExecutionRequest(policy)
	assert.True(t, policy.Test(request).Allowed)

	tests := []struct {
		name   string
		mutate func(*KubernetesExecutionPolicyTestRequest)
		rule   string
	}{
		{"cluster", func(r *KubernetesExecutionPolicyTestRequest) { r.ClusterAlias = "other" }, KubernetesPolicyRuleClusterDenied},
		{"namespace", func(r *KubernetesExecutionPolicyTestRequest) { r.Namespace = "other" }, KubernetesPolicyRuleNamespaceDenied},
		{"image", func(r *KubernetesExecutionPolicyTestRequest) {
			r.TaskImage = "registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, KubernetesPolicyRuleImageDenied},
		{"service account", func(r *KubernetesExecutionPolicyTestRequest) { r.ServiceAccount = "default" }, KubernetesPolicyRuleServiceAccountDenied},
		{"runtime class", func(r *KubernetesExecutionPolicyTestRequest) { r.RuntimeClass = "sandbox" }, KubernetesPolicyRuleRuntimeClassDenied},
		{"volume", func(r *KubernetesExecutionPolicyTestRequest) { r.VolumeTypes = []KubernetesVolumeType{"hostPath"} }, KubernetesPolicyRuleVolumeDenied},
		{"network", func(r *KubernetesExecutionPolicyTestRequest) { r.NetworkProfile = "open" }, KubernetesPolicyRuleNetworkProfileDenied},
		{"resources", func(r *KubernetesExecutionPolicyTestRequest) { r.Resources.MemoryLimitBytes++ }, KubernetesPolicyRuleResourceDenied},
		{"retention", func(r *KubernetesExecutionPolicyTestRequest) { r.TerminalRetentionSeconds++ }, KubernetesPolicyRuleRetentionDenied},
		{"host namespace", func(r *KubernetesExecutionPolicyTestRequest) { r.HasHostNetwork = true }, KubernetesPolicyRuleHostNamespaceDenied},
		{"privileged", func(r *KubernetesExecutionPolicyTestRequest) { r.HasPrivileged = true }, KubernetesPolicyRulePrivilegeDenied},
		{"projected token", func(r *KubernetesExecutionPolicyTestRequest) { r.HasProjectedServiceToken = true }, KubernetesPolicyRuleProjectedTokenDenied},
		{"project override", func(r *KubernetesExecutionPolicyTestRequest) { r.HasProjectOverride = true }, KubernetesPolicyRuleProjectOverrideDenied},
		{"security profile", func(r *KubernetesExecutionPolicyTestRequest) { r.HasRestrictedSecurityProfile = false }, KubernetesPolicyRuleSecurityProfileDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testRequest := request
			tt.mutate(&testRequest)
			result := policy.Test(testRequest)
			assert.False(t, result.Allowed)
			assert.Equal(t, tt.rule, result.Rule)
			assert.True(t, IsKubernetesPolicyRuleID(result.Rule))
		})
	}
}

func TestDefaultKubernetesExecutionPolicyRequiresExplicitNetworkEnforcement(t *testing.T) {
	policy := DefaultKubernetesExecutionPolicy("qa")
	assert.Equal(t, KubernetesNetworkPolicyEnforcementUnsupported, policy.NetworkPolicyEnforcement)
	policy.NetworkProfile = KubernetesNetworkProfileDenyAll
	result := policy.Test(KubernetesExecutionPolicyTestRequest{NetworkProfile: KubernetesNetworkProfileDenyAll})
	assert.Equal(t, KubernetesPolicyRuleNetworkProfileUnsupported, result.Rule)
}

func TestKubernetesExecutionPolicyRejectsUnsafeAliasesAndUnboundedRetention(t *testing.T) {
	policy := testKubernetesExecutionPolicy()
	policy.ClusterAlias = "qa cluster"
	require.ErrorContains(t, policy.Canonicalize(), "cluster alias")

	policy = testKubernetesExecutionPolicy()
	policy.TerminalRetentionSeconds = 8 * 24 * 60 * 60
	require.ErrorContains(t, policy.Canonicalize(), "terminal retention")
}
