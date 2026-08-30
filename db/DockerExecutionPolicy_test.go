package db_test

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const allowedDigestImage = "registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDockerPolicyDefaultsAreFailClosedAndRevisioned(t *testing.T) {
	policy := db.DefaultDockerExecutionPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("default policy must validate: %v", err)
	}
	if policy.Revision < 0 || policy.Hash == "" {
		t.Fatalf("default policy must carry a revision and canonical hash: %#v", policy)
	}
	if policy.AllowPrivileged || policy.AllowHostNamespaces || policy.AllowDevices || policy.AllowBindMounts {
		t.Fatalf("default policy must not grant host privilege: %#v", policy)
	}
}

func TestDockerExecutionPolicyRejectsEveryBroadeningVector(t *testing.T) {
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowedDigestImage}
	require.NoError(t, policy.Canonicalize())
	base := db.DockerExecutionPolicyTestRequest{
		Image: allowedDigestImage, Network: "none", User: "65534:0",
		NanoCPUs: policy.NanoCPUs, MemoryBytes: policy.MemoryBytes, PidsLimit: policy.PidsLimit,
		ReadOnlyRootFS: true,
	}
	assert.True(t, policy.Test(base).Allowed)

	for name, mutate := range map[string]func(*db.DockerExecutionPolicyTestRequest){
		"image": func(r *db.DockerExecutionPolicyTestRequest) {
			r.Image = "registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"network":    func(r *db.DockerExecutionPolicyTestRequest) { r.Network = "host" },
		"privilege":  func(r *db.DockerExecutionPolicyTestRequest) { r.Privileged = true },
		"bind mount": func(r *db.DockerExecutionPolicyTestRequest) { r.HasBindMounts = true },
		"device":     func(r *db.DockerExecutionPolicyTestRequest) { r.HasDevices = true },
		"identity":   func(r *db.DockerExecutionPolicyTestRequest) { r.User = "0" },
		"capability": func(r *db.DockerExecutionPolicyTestRequest) { r.HasCapabilities = true },
		"resource":   func(r *db.DockerExecutionPolicyTestRequest) { r.PidsLimit++ },
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			mutate(&request)
			assert.False(t, policy.Test(request).Allowed)
			assert.NotEmpty(t, policy.Test(request).Rule)
		})
	}
}

func TestDockerExecutionPolicyRequiresSafeHelperNetworkAndFixedIdentity(t *testing.T) {
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowedDigestImage}
	policy.AllowedNetworks = []string{"bridge"}
	policy.Network = "bridge"
	assert.ErrorContains(t, policy.Canonicalize(), "none")

	policy = db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowedDigestImage}
	policy.User = "10001:10001"
	assert.ErrorContains(t, policy.Canonicalize(), "65534:0")

	policy = db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowedDigestImage}
	policy.AllowedNetworks = []string{"none", "host"}
	assert.ErrorContains(t, policy.Canonicalize(), "host")

	policy = db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{allowedDigestImage}
	policy.AllowedNetworks = []string{"none", "container:other"}
	assert.ErrorContains(t, policy.Canonicalize(), "namespace")

	policy = db.DefaultDockerExecutionPolicy()
	policy.RequireDigest = false
	policy.AllowedImages = []string{"registry.example.test/job:latest"}
	assert.ErrorContains(t, policy.Canonicalize(), "immutable image digests")
}
