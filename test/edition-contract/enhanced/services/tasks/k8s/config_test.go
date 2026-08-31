package k8s

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testImage = "registry.example.test/semaphore/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestEffectiveConfigRequiresExplicitImmutableExecutionInputs(t *testing.T) {
	cfg, err := effectiveConfig(util.RunnerK8sConfig{
		KubeconfigPath:        "/tmp/kubeconfig",
		Context:               "qa-context",
		ClusterAlias:          "qa-cluster",
		Namespace:             "semaphore-jobs",
		ServiceAccount:        "semaphore-runner",
		Image:                 testImage,
		HelperImage:           testImage,
		PullSecrets:           "registry-a, registry-b",
		PollIntervalSeconds:   2,
		CleanupGraceSeconds:   20,
		ActiveDeadlineSeconds: 900,
	})

	require.NoError(t, err)
	assert.Equal(t, "qa-context", cfg.context)
	assert.Equal(t, "qa-cluster", cfg.clusterAlias)
	assert.Equal(t, "semaphore-jobs", cfg.namespace)
	assert.Equal(t, "semaphore-runner", cfg.serviceAccount)
	assert.Equal(t, []string{"registry-a", "registry-b"}, cfg.pullSecrets)
	assert.Equal(t, 2*time.Second, cfg.pollInterval)
	assert.Equal(t, 20*time.Second, cfg.cleanupGrace)
	assert.Equal(t, int64(900), cfg.activeDeadlineSeconds)
}

func TestEffectiveConfigDefaultsOnlyNonAuthorityValues(t *testing.T) {
	cfg, err := effectiveConfig(util.RunnerK8sConfig{
		ClusterAlias:   "in-cluster",
		ServiceAccount: "semaphore-task",
		Image:          testImage,
		HelperImage:    testImage,
	})

	require.NoError(t, err)
	assert.Equal(t, "semaphore", cfg.namespace)
	assert.Equal(t, "semaphore-task", cfg.serviceAccount)
	assert.Equal(t, 3*time.Second, cfg.pollInterval)
	assert.Equal(t, 30*time.Second, cfg.cleanupGrace)
	assert.Equal(t, int64(3600), cfg.activeDeadlineSeconds)
}

func TestEffectiveConfigRejectsAmbiguousOrMutableInputs(t *testing.T) {
	tests := []struct {
		name   string
		config util.RunnerK8sConfig
		match  string
	}{
		{
			name:   "kubeconfig without context",
			config: util.RunnerK8sConfig{KubeconfigPath: "/tmp/kubeconfig", ClusterAlias: "qa", ServiceAccount: "semaphore-task", Image: testImage, HelperImage: testImage},
			match:  "context",
		},
		{
			name:   "cluster alias omitted",
			config: util.RunnerK8sConfig{ServiceAccount: "semaphore-task", Image: testImage, HelperImage: testImage},
			match:  "cluster alias",
		},
		{
			name:   "unsafe cluster alias",
			config: util.RunnerK8sConfig{ClusterAlias: "qa cluster", ServiceAccount: "semaphore-task", Image: testImage, HelperImage: testImage},
			match:  "cluster alias",
		},
		{
			name:   "mutable image",
			config: util.RunnerK8sConfig{ClusterAlias: "qa", ServiceAccount: "semaphore-task", Image: "registry.example.test/semaphore/job:latest", HelperImage: testImage},
			match:  "immutable",
		},
		{
			name:   "invalid namespace",
			config: util.RunnerK8sConfig{ClusterAlias: "qa", Namespace: "Bad_Namespace", ServiceAccount: "semaphore-task", Image: testImage, HelperImage: testImage},
			match:  "namespace",
		},
		{
			name:   "invalid service account",
			config: util.RunnerK8sConfig{ClusterAlias: "qa", ServiceAccount: "Bad_Service", Image: testImage, HelperImage: testImage},
			match:  "service account",
		},
		{
			name:   "default workload service account",
			config: util.RunnerK8sConfig{ClusterAlias: "qa", ServiceAccount: "default", Image: testImage, HelperImage: testImage},
			match:  "default service account",
		},
		{
			name:   "mutable helper image",
			config: util.RunnerK8sConfig{ClusterAlias: "qa", ServiceAccount: "semaphore-task", Image: testImage, HelperImage: "registry.example.test/semaphore/helper:latest"},
			match:  "immutable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := effectiveConfig(tt.config)
			require.ErrorContains(t, err, tt.match)
		})
	}
}
