package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	DockerPolicyRuleImageDenied       = "DOCKER_POLICY_IMAGE_DENIED"
	DockerPolicyRuleDigestRequired    = "DOCKER_POLICY_DIGEST_REQUIRED"
	DockerPolicyRuleNetworkDenied     = "DOCKER_POLICY_NETWORK_DENIED"
	DockerPolicyRulePrivilegeDenied   = "DOCKER_POLICY_PRIVILEGE_DENIED"
	DockerPolicyRuleBindMountDenied   = "DOCKER_POLICY_BIND_MOUNT_DENIED"
	DockerPolicyRuleDeviceDenied      = "DOCKER_POLICY_DEVICE_DENIED"
	DockerPolicyRuleResourceDenied    = "DOCKER_POLICY_RESOURCE_DENIED"
	DockerPolicyRuleIdentityDenied    = "DOCKER_POLICY_IDENTITY_DENIED"
	DockerPolicyRuleCapabilityDenied  = "DOCKER_POLICY_CAPABILITY_DENIED"
	DockerPolicyRuleReadonlyRequired  = "DOCKER_POLICY_READONLY_REQUIRED"
	DockerPolicyRuleNamespaceRequired = "DOCKER_POLICY_NAMESPACE_REQUIRED"
)

var ErrDockerExecutionPolicyRevisionConflict = errors.New("Docker execution policy revision conflict")

// DockerExecutionPolicy is the administrator-owned desired execution boundary
// for every Docker runner, regardless of whether its runner row is global or
// project-scoped. It intentionally contains no project-owned inputs.
type DockerExecutionPolicy struct {
	Revision int    `db:"revision" json:"revision"`
	Hash     string `db:"policy_hash" json:"hash"`

	AllowedImages   []string `json:"allowed_images"`
	RequireDigest   bool     `json:"require_digest"`
	AllowedNetworks []string `json:"allowed_networks"`
	Network         string   `json:"network"`

	User                string `json:"user"`
	NanoCPUs            int64  `json:"nano_cpus"`
	MemoryBytes         int64  `json:"memory_bytes"`
	PidsLimit           int64  `json:"pids_limit"`
	PullTimeoutSeconds  int    `json:"pull_timeout_seconds"`
	MaxImageSizeBytes   int64  `json:"max_image_size_bytes"`
	SeccompProfile      string `json:"seccomp_profile"`
	AppArmorProfile     string `json:"apparmor_profile"`
	AllowPrivileged     bool   `json:"allow_privileged"`
	AllowBindMounts     bool   `json:"allow_bind_mounts"`
	AllowDevices        bool   `json:"allow_devices"`
	AllowHostNamespaces bool   `json:"allow_host_namespaces"`
}

// DockerExecutionPolicyAck is the runner's acknowledgement of the exact
// server-owned policy snapshot currently enforced by its provider.
type DockerExecutionPolicyAck struct {
	Revision int    `json:"revision"`
	Hash     string `json:"hash"`
}

// DockerExecutionPolicyTestRequest is a non-secret, admin-only policy test
// vector. The API never accepts a Docker create request from callers.
type DockerExecutionPolicyTestRequest struct {
	Image             string `json:"image"`
	Network           string `json:"network"`
	User              string `json:"user"`
	NanoCPUs          int64  `json:"nano_cpus"`
	MemoryBytes       int64  `json:"memory_bytes"`
	PidsLimit         int64  `json:"pids_limit"`
	Privileged        bool   `json:"privileged"`
	HasBindMounts     bool   `json:"has_bind_mounts"`
	HasDevices        bool   `json:"has_devices"`
	HasHostNamespaces bool   `json:"has_host_namespaces"`
	HasCapabilities   bool   `json:"has_capabilities"`
	ReadOnlyRootFS    bool   `json:"read_only_rootfs"`
}

type DockerExecutionPolicyTestResult struct {
	Allowed bool   `json:"allowed"`
	Rule    string `json:"rule,omitempty"`
}

// DockerPolicyViolationError deliberately exposes only a stable rule ID. It
// avoids echoing image names, mount paths, or daemon configuration into APIs.
type DockerPolicyViolationError struct{ Rule string }

func (e DockerPolicyViolationError) Error() string { return e.Rule }

func IsDockerPolicyRuleID(value string) bool {
	return slices.Contains([]string{DockerPolicyRuleImageDenied, DockerPolicyRuleDigestRequired, DockerPolicyRuleNetworkDenied, DockerPolicyRulePrivilegeDenied, DockerPolicyRuleBindMountDenied, DockerPolicyRuleDeviceDenied, DockerPolicyRuleResourceDenied, DockerPolicyRuleIdentityDenied, DockerPolicyRuleCapabilityDenied, DockerPolicyRuleReadonlyRequired, DockerPolicyRuleNamespaceRequired}, value)
}

func DefaultDockerExecutionPolicy() DockerExecutionPolicy {
	p := DockerExecutionPolicy{
		Revision:           0,
		AllowedImages:      []string{}, // Explicit admin configuration is required before Docker dispatch.
		RequireDigest:      true,
		AllowedNetworks:    []string{"none"},
		Network:            "none",
		User:               "65534:0",
		NanoCPUs:           1_000_000_000,
		MemoryBytes:        512 * 1024 * 1024,
		PidsLimit:          256,
		PullTimeoutSeconds: 300,
		MaxImageSizeBytes:  2 * 1024 * 1024 * 1024,
		SeccompProfile:     "default",
		AppArmorProfile:    "docker-default",
	}
	_ = p.Canonicalize()
	return p
}

func (p *DockerExecutionPolicy) Canonicalize() error {
	if p.Revision < 0 {
		return fmt.Errorf("Docker policy revision must not be negative")
	}
	p.AllowedImages = canonicalStringSet(p.AllowedImages)
	p.AllowedNetworks = canonicalStringSet(p.AllowedNetworks)
	if err := p.Validate(); err != nil {
		return err
	}
	canonical := struct {
		AllowedImages       []string `json:"allowed_images"`
		RequireDigest       bool     `json:"require_digest"`
		AllowedNetworks     []string `json:"allowed_networks"`
		Network             string   `json:"network"`
		User                string   `json:"user"`
		NanoCPUs            int64    `json:"nano_cpus"`
		MemoryBytes         int64    `json:"memory_bytes"`
		PidsLimit           int64    `json:"pids_limit"`
		PullTimeoutSeconds  int      `json:"pull_timeout_seconds"`
		MaxImageSizeBytes   int64    `json:"max_image_size_bytes"`
		SeccompProfile      string   `json:"seccomp_profile"`
		AppArmorProfile     string   `json:"apparmor_profile"`
		AllowPrivileged     bool     `json:"allow_privileged"`
		AllowBindMounts     bool     `json:"allow_bind_mounts"`
		AllowDevices        bool     `json:"allow_devices"`
		AllowHostNamespaces bool     `json:"allow_host_namespaces"`
	}{p.AllowedImages, p.RequireDigest, p.AllowedNetworks, p.Network, p.User, p.NanoCPUs, p.MemoryBytes, p.PidsLimit, p.PullTimeoutSeconds, p.MaxImageSizeBytes, p.SeccompProfile, p.AppArmorProfile, p.AllowPrivileged, p.AllowBindMounts, p.AllowDevices, p.AllowHostNamespaces}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	p.Hash = hex.EncodeToString(digest[:])
	return nil
}

func (p DockerExecutionPolicy) Validate() error {
	if p.Revision < 0 || p.NanoCPUs <= 0 || p.MemoryBytes <= 0 || p.PidsLimit <= 0 || p.PullTimeoutSeconds <= 0 || p.MaxImageSizeBytes <= 0 {
		return fmt.Errorf("Docker policy requires positive revision resources")
	}
	if p.User != "65534:0" {
		return fmt.Errorf("Docker policy user must be the fixed non-root identity 65534:0")
	}
	if p.AllowPrivileged || p.AllowBindMounts || p.AllowDevices || p.AllowHostNamespaces {
		return fmt.Errorf("Docker policy may not enable host privilege, bind mounts, devices, or host namespaces")
	}
	for _, image := range p.AllowedImages {
		normalized, err := NormalizeExecutorImage(image)
		if err != nil || normalized == nil || *normalized != image {
			return fmt.Errorf("Docker policy contains an invalid image allow-list entry")
		}
		if p.RequireDigest && !hasImmutableDockerDigest(image) {
			return fmt.Errorf("Docker policy requires digests in image allow-list entries")
		}
	}
	for _, network := range p.AllowedNetworks {
		if network == "" || strings.TrimSpace(network) != network {
			return fmt.Errorf("Docker policy contains an invalid network allow-list entry")
		}
		if network == "host" {
			return fmt.Errorf("Docker policy must not allow the host network")
		}
		if strings.HasPrefix(network, "container:") {
			return fmt.Errorf("Docker policy must not allow a shared container network namespace")
		}
	}
	if !slices.Contains(p.AllowedNetworks, "none") {
		return fmt.Errorf("Docker policy must allow network none for the helper container")
	}
	if !slices.Contains(p.AllowedNetworks, p.Network) {
		return fmt.Errorf("Docker policy network must be present in its allow-list")
	}
	if p.SeccompProfile != "default" || p.AppArmorProfile != "docker-default" {
		return fmt.Errorf("Docker policy supports only default seccomp and docker-default AppArmor profiles")
	}
	return nil
}

func (p DockerExecutionPolicy) MatchesAck(ack DockerExecutionPolicyAck) bool {
	return p.Revision == ack.Revision && p.Hash != "" && p.Hash == ack.Hash
}

func (p DockerExecutionPolicy) Test(request DockerExecutionPolicyTestRequest) DockerExecutionPolicyTestResult {
	err := p.ValidateExecution(request)
	if err == nil {
		return DockerExecutionPolicyTestResult{Allowed: true}
	}
	var violation DockerPolicyViolationError
	if errors.As(err, &violation) {
		return DockerExecutionPolicyTestResult{Rule: violation.Rule}
	}
	return DockerExecutionPolicyTestResult{Rule: DockerPolicyRuleResourceDenied}
}

func (p DockerExecutionPolicy) ValidateExecution(request DockerExecutionPolicyTestRequest) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !slices.Contains(p.AllowedImages, request.Image) {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleImageDenied}
	}
	if p.RequireDigest && !hasImmutableDockerDigest(request.Image) {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleDigestRequired}
	}
	if !slices.Contains(p.AllowedNetworks, request.Network) {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleNetworkDenied}
	}
	if request.Privileged && !p.AllowPrivileged {
		return DockerPolicyViolationError{Rule: DockerPolicyRulePrivilegeDenied}
	}
	if request.HasBindMounts && !p.AllowBindMounts {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleBindMountDenied}
	}
	if request.HasDevices && !p.AllowDevices {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleDeviceDenied}
	}
	if request.HasHostNamespaces && !p.AllowHostNamespaces {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleNamespaceRequired}
	}
	if request.HasCapabilities {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleCapabilityDenied}
	}
	if request.User != p.User {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleIdentityDenied}
	}
	if !request.ReadOnlyRootFS {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleReadonlyRequired}
	}
	if request.NanoCPUs <= 0 || request.MemoryBytes <= 0 || request.PidsLimit <= 0 ||
		request.NanoCPUs > p.NanoCPUs || request.MemoryBytes > p.MemoryBytes || request.PidsLimit > p.PidsLimit {
		return DockerPolicyViolationError{Rule: DockerPolicyRuleResourceDenied}
	}
	return nil
}

func canonicalStringSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func hasImmutableDockerDigest(image string) bool {
	parts := strings.Split(image, "@sha256:")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
		return false
	}
	for _, char := range parts[1] {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

type DockerExecutionPolicyRepository interface {
	GetDockerExecutionPolicy() (DockerExecutionPolicy, error)
	SaveDockerExecutionPolicy(policy DockerExecutionPolicy, expectedRevision int) (DockerExecutionPolicy, error)
}
