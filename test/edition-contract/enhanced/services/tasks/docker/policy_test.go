package docker

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerProviderAppliesOnlyCanonicalPolicyAndRejectsBroadenedConfig(t *testing.T) {
	_, err := effectiveConfig(util.RunnerDockerConfig{Privileged: true, Network: "host"})
	require.ErrorContains(t, err, "privileged")
	cfg, err := effectiveConfig(util.RunnerDockerConfig{Network: "host"})
	require.NoError(t, err)
	provider := &Provider{config: cfg}
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	require.NoError(t, policy.Canonicalize())
	require.NoError(t, provider.ApplyDockerExecutionPolicy(policy))
	ack := provider.DockerExecutionPolicyAcknowledgement()
	assert.True(t, policy.MatchesAck(ack))
	assert.Equal(t, "none", provider.effectivePolicy().Network)
}

func TestDockerProviderRejectsTaskImageOutsideCentralExactAllowList(t *testing.T) {
	allowed := "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg, err := effectiveConfig(util.RunnerDockerConfig{Image: allowed, HelperImage: allowed})
	require.NoError(t, err)
	provider := &Provider{config: cfg, policy: db.DefaultDockerExecutionPolicy()}
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowed}
	require.NoError(t, provider.ApplyDockerExecutionPolicy(policy))
	_, err = provider.NewExecutor(db.Task{}, db.Template{ExecutorImage: stringPointer("registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, db.Inventory{}, db.Repository{}, db.Environment{}, "")
	require.ErrorContains(t, err, db.DockerPolicyRuleImageDenied)
}

func TestDockerHelperAlwaysUsesNoNetworkWhileTaskUsesPolicyNetwork(t *testing.T) {
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{
		"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	policy.AllowedNetworks = []string{"none", "isolated"}
	policy.Network = "isolated"
	require.NoError(t, policy.Canonicalize())
	executor := newDockerExecutorForPlan(&fakeDockerClient{}, config{}, "boot", db.Task{ID: 1}, db.Template{}, &recordingLogger{})
	executor.policy = policy
	assert.Equal(t, "none", executor.containerSpec("helper", policy.AllowedImages[0], "volume", false, "helper").Network)
	assert.Equal(t, "isolated", executor.containerSpec("task", policy.AllowedImages[0], "volume", true, "task").Network)
}

func TestDockerCreateSpecRejectsPrivilegeAndMissingSafeDefaults(t *testing.T) {
	err := validateContainerSpec(ContainerSpec{
		Image: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		User:  "65534:0", Network: "none", NanoCPUs: 1, Memory: 1, PidsLimit: 1,
		Privileged: true, ReadOnlyRootFS: true, NoNewPrivileges: true, DropAllCapabilities: true,
		PrivateNamespaces: true,
	})
	assert.ErrorContains(t, err, db.DockerPolicyRulePrivilegeDenied)

	err = validateContainerSpec(ContainerSpec{
		Image: "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		User:  "65534:0", Network: "none", NanoCPUs: 1, Memory: 1, PidsLimit: 1,
		ReadOnlyRootFS: true, NoNewPrivileges: true, DropAllCapabilities: true, PrivateNamespaces: true,
	})
	assert.NoError(t, err)
}

func TestImageRepositorySeparatesTagsAndImmutableDigests(t *testing.T) {
	assert.Equal(t, "registry.example.test/team/job", imageRepository("registry.example.test/team/job:stable"))
	assert.Equal(t, "registry.example.test/team/job", imageRepository("registry.example.test/team/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
}
