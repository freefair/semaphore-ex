package k8s

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func testKubernetesPolicy(t *testing.T, cfg config) db.KubernetesExecutionPolicy {
	t.Helper()
	if cfg.clusterAlias == "" {
		cfg.clusterAlias = "qa"
	}
	if cfg.namespace == "" {
		cfg.namespace = "semaphore"
	}
	if cfg.serviceAccount == "" {
		cfg.serviceAccount = "semaphore-task"
	}
	if cfg.image == "" {
		cfg.image = testImage
	}
	if cfg.helperImage == "" {
		cfg.helperImage = testImage
	}
	policy := db.KubernetesExecutionPolicy{
		ClusterAlias:             cfg.clusterAlias,
		AllowedNamespaces:        []string{cfg.namespace},
		AllowedImages:            []string{cfg.image, cfg.helperImage},
		AllowedServiceAccounts:   []string{cfg.serviceAccount},
		AllowedRuntimeClasses:    []string{""},
		AllowedVolumeTypes:       []db.KubernetesVolumeType{db.KubernetesVolumeSecret, db.KubernetesVolumeEmptyDir},
		AllowedNetworkProfiles:   []string{"deny-all"},
		NetworkProfile:           "deny-all",
		NetworkPolicyEnforcement: db.KubernetesNetworkPolicyEnforcementNetworkPolicy,
		Resources: db.KubernetesExecutionResources{
			CPURequestMilli: 100, CPULimitMilli: 500,
			MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20,
			EphemeralStorageRequestBytes: 64 << 20, EphemeralStorageLimitBytes: 256 << 20,
		},
		TerminalRetentionSeconds: 3600,
	}
	require.NoError(t, policy.Canonicalize())
	return policy
}

func TestKubernetesProviderRequiresPolicyAcknowledgementAndValidatesFinalManifest(t *testing.T) {
	cfg := config{clusterAlias: "qa", namespace: "semaphore-jobs", serviceAccount: "semaphore-task", image: testImage, helperImage: testImage, cleanupGrace: time.Second, activeDeadlineSeconds: 60}
	provider := newProviderWithClient(cfg, &fakeKubernetesClient{})
	provider.ApplyRunnerIdentity(19)
	_, err := provider.NewExecutor(db.Task{}, db.Template{}, db.Inventory{}, db.Repository{}, db.Environment{}, "", "")
	require.ErrorContains(t, err, db.KubernetesPolicyRuleClusterDenied)

	policy := testKubernetesPolicy(t, cfg)
	require.NoError(t, provider.ApplyKubernetesExecutionPolicy(policy))
	assert.True(t, policy.MatchesAck(provider.KubernetesExecutionPolicyAcknowledgement()))
	_, err = provider.NewExecutor(db.Task{}, db.Template{ExecutorImage: stringPointer("registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, db.Inventory{}, db.Repository{}, db.Environment{}, "", "")
	require.ErrorContains(t, err, db.KubernetesPolicyRuleImageDenied)

	job := buildJob(cfg, policy, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 1}, 19, "bundle", testImage, []string{"/bin/true"})
	require.NoError(t, validateGeneratedJob(job, cfg, policy, testImage))
	assert.True(t, *job.Spec.Template.Spec.InitContainers[0].SecurityContext.RunAsNonRoot)
	assert.True(t, *job.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem)
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, job.Spec.Template.Spec.Containers[0].SecurityContext.SeccompProfile.Type)
	assert.Equal(t, int64(500), job.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().MilliValue())
	assert.Equal(t, int64(256<<20), job.Spec.Template.Spec.Containers[0].Resources.Limits.StorageEphemeral().Value())
}

func TestGeneratedManifestRejectsPolicyBroadening(t *testing.T) {
	cfg := config{clusterAlias: "qa", namespace: "semaphore-jobs", serviceAccount: "semaphore-task", image: testImage, helperImage: testImage, cleanupGrace: time.Second, activeDeadlineSeconds: 60}
	policy := testKubernetesPolicy(t, cfg)
	job := buildJob(cfg, policy, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 1}, 19, "bundle", testImage, []string{"/bin/true"})
	job.Spec.Template.Spec.HostNetwork = true
	require.ErrorContains(t, validateGeneratedJob(job, cfg, policy, testImage), db.KubernetesPolicyRuleHostNamespaceDenied)

	job = buildJob(cfg, policy, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 1}, 19, "bundle", testImage, []string{"/bin/true"})
	job.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = boolPointer(true)
	require.ErrorContains(t, validateGeneratedJob(job, cfg, policy, testImage), db.KubernetesPolicyRuleSecurityProfileDenied)

	job = buildJob(cfg, policy, db.Task{ID: 41, ProjectID: 7, AssignmentGeneration: 1}, 19, "bundle", testImage, []string{"/bin/true"})
	job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}})
	require.ErrorContains(t, validateGeneratedJob(job, cfg, policy, testImage), db.KubernetesPolicyRuleHostNamespaceDenied)
}

func boolPointer(value bool) *bool { return &value }

func stringPointer(value string) *string { return &value }
