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
	"time"
)

const (
	KubernetesPolicyRuleClusterDenied             = "K8S_POLICY_CLUSTER_DENIED"
	KubernetesPolicyRuleNamespaceDenied           = "K8S_POLICY_NAMESPACE_DENIED"
	KubernetesPolicyRuleImageDenied               = "K8S_POLICY_IMAGE_DENIED"
	KubernetesPolicyRuleServiceAccountDenied      = "K8S_POLICY_SERVICE_ACCOUNT_DENIED"
	KubernetesPolicyRuleRuntimeClassDenied        = "K8S_POLICY_RUNTIME_CLASS_DENIED"
	KubernetesPolicyRuleVolumeDenied              = "K8S_POLICY_VOLUME_DENIED"
	KubernetesPolicyRuleNetworkProfileDenied      = "K8S_POLICY_NETWORK_PROFILE_DENIED"
	KubernetesPolicyRuleNetworkProfileUnsupported = "K8S_NETWORK_PROFILE_UNSUPPORTED"
	KubernetesPolicyRuleAdmissionDenied           = "K8S_API_ADMISSION_DENIED"
	KubernetesPolicyRuleQuotaDenied               = "K8S_API_QUOTA_DENIED"
	KubernetesPolicyRuleRBACDenied                = "K8S_API_RBAC_DENIED"
	KubernetesPolicyRuleUnavailable               = "K8S_API_UNAVAILABLE"
	KubernetesPolicyRuleResourceDenied            = "K8S_POLICY_RESOURCE_DENIED"
	KubernetesPolicyRuleRetentionDenied           = "K8S_POLICY_RETENTION_DENIED"
	KubernetesPolicyRuleHostNamespaceDenied       = "K8S_POLICY_HOST_NAMESPACE_DENIED"
	KubernetesPolicyRulePrivilegeDenied           = "K8S_POLICY_PRIVILEGE_DENIED"
	KubernetesPolicyRuleProjectedTokenDenied      = "K8S_POLICY_PROJECTED_TOKEN_DENIED"
	KubernetesPolicyRuleProjectOverrideDenied     = "K8S_POLICY_PROJECT_OVERRIDE_DENIED"
	KubernetesPolicyRuleSecurityProfileDenied     = "K8S_POLICY_SECURITY_PROFILE_DENIED"
)

var ErrKubernetesExecutionPolicyRevisionConflict = errors.New("Kubernetes execution policy revision conflict")

// ValidateKubernetesNamespace keeps runner-reported namespace identity within
// the same bounded DNS label contract as administrator policy entries.
func ValidateKubernetesNamespace(value string) error {
	if !safeDNSLabel(value) {
		return errors.New("invalid Kubernetes namespace")
	}
	return nil
}

// KubernetesVolumeType is the small set of volume sources that the Kubernetes
// executor can construct. Project task input never supplies a volume source.
type KubernetesVolumeType string

const (
	KubernetesVolumeEmptyDir KubernetesVolumeType = "emptyDir"
	KubernetesVolumeSecret   KubernetesVolumeType = "secret"
)

// KubernetesNetworkProfile is the deliberately small set of network shapes
// the executor knows how to construct. It does not claim CNI capabilities.
type KubernetesNetworkProfile string

const KubernetesNetworkProfileDenyAll = "deny-all"

// KubernetesNetworkPolicyEnforcement is an administrator declaration that the
// selected cluster enforces Kubernetes NetworkPolicy resources. The executor
// never probes or infers this from a CNI implementation.
type KubernetesNetworkPolicyEnforcement string

const (
	KubernetesNetworkPolicyEnforcementUnsupported   KubernetesNetworkPolicyEnforcement = "unsupported"
	KubernetesNetworkPolicyEnforcementNetworkPolicy KubernetesNetworkPolicyEnforcement = "network-policy"
)

// KubernetesExecutionResources are policy-owned requests and limits applied
// identically to generated task and helper containers.
type KubernetesExecutionResources struct {
	CPURequestMilli              int64 `json:"cpu_request_milli"`
	CPULimitMilli                int64 `json:"cpu_limit_milli"`
	MemoryRequestBytes           int64 `json:"memory_request_bytes"`
	MemoryLimitBytes             int64 `json:"memory_limit_bytes"`
	EphemeralStorageRequestBytes int64 `json:"ephemeral_storage_request_bytes"`
	EphemeralStorageLimitBytes   int64 `json:"ephemeral_storage_limit_bytes"`
}

// KubernetesExecutionPolicy is the administrator-owned execution boundary for
// a single configured Kubernetes cluster alias. Empty allow-lists are invalid:
// Kubernetes dispatch must remain fail-closed until policy is supplied.
type KubernetesExecutionPolicy struct {
	ClusterAlias string `db:"cluster_alias" json:"cluster_alias"`
	Revision     int    `db:"revision" json:"revision"`
	Hash         string `db:"policy_hash" json:"hash"`

	AllowedNamespaces        []string                           `json:"allowed_namespaces"`
	AllowedImages            []string                           `json:"allowed_images"`
	AllowedServiceAccounts   []string                           `json:"allowed_service_accounts"`
	AllowedRuntimeClasses    []string                           `json:"allowed_runtime_classes"`
	RuntimeClass             string                             `json:"runtime_class"`
	AllowedVolumeTypes       []KubernetesVolumeType             `json:"allowed_volume_types"`
	AllowedNetworkProfiles   []string                           `json:"allowed_network_profiles"`
	NetworkProfile           string                             `json:"network_profile"`
	NetworkPolicyEnforcement KubernetesNetworkPolicyEnforcement `json:"network_policy_enforcement"`
	Resources                KubernetesExecutionResources       `json:"resources"`
	TerminalRetentionSeconds int                                `json:"terminal_retention_seconds"`
}

// KubernetesExecutionPolicyAck binds a provider to the exact server-owned
// policy revision it has accepted. It contains no Kubernetes credentials.
type KubernetesExecutionPolicyAck struct {
	ClusterAlias string `json:"cluster_alias"`
	Revision     int    `json:"revision"`
	Hash         string `json:"hash"`
}

// KubernetesExecutionPolicyTestRequest is a sanitized final-manifest summary.
// It deliberately cannot carry Kubernetes YAML, labels, commands, or secret data.
type KubernetesExecutionPolicyTestRequest struct {
	ClusterAlias             string                       `json:"cluster_alias"`
	Namespace                string                       `json:"namespace"`
	TaskImage                string                       `json:"task_image"`
	HelperImage              string                       `json:"helper_image"`
	ServiceAccount           string                       `json:"service_account"`
	RuntimeClass             string                       `json:"runtime_class"`
	NetworkProfile           string                       `json:"network_profile"`
	Resources                KubernetesExecutionResources `json:"resources"`
	TerminalRetentionSeconds int                          `json:"terminal_retention_seconds"`
	VolumeTypes              []KubernetesVolumeType       `json:"volume_types"`

	HasHostNetwork               bool `json:"has_host_network"`
	HasHostPID                   bool `json:"has_host_pid"`
	HasHostIPC                   bool `json:"has_host_ipc"`
	HasHostPath                  bool `json:"has_host_path"`
	HasProjectedServiceToken     bool `json:"has_projected_service_token"`
	HasPrivileged                bool `json:"has_privileged"`
	HasProjectOverride           bool `json:"has_project_override"`
	HasRestrictedSecurityProfile bool `json:"has_restricted_security_profile"`
}

type KubernetesExecutionPolicyTestResult struct {
	Allowed bool   `json:"allowed"`
	Rule    string `json:"rule,omitempty"`
}

// KubernetesPolicyViolationError exposes only a stable rule ID so project
// users cannot receive raw admission errors or operator policy values.
type KubernetesPolicyViolationError struct{ Rule string }

func (e KubernetesPolicyViolationError) Error() string { return e.Rule }

func IsKubernetesPolicyRuleID(value string) bool {
	return slices.Contains([]string{
		KubernetesPolicyRuleClusterDenied, KubernetesPolicyRuleNamespaceDenied,
		KubernetesPolicyRuleImageDenied, KubernetesPolicyRuleServiceAccountDenied,
		KubernetesPolicyRuleRuntimeClassDenied, KubernetesPolicyRuleVolumeDenied,
		KubernetesPolicyRuleNetworkProfileDenied, KubernetesPolicyRuleNetworkProfileUnsupported, KubernetesPolicyRuleResourceDenied,
		KubernetesPolicyRuleAdmissionDenied, KubernetesPolicyRuleQuotaDenied, KubernetesPolicyRuleRBACDenied,
		KubernetesPolicyRuleUnavailable,
		KubernetesPolicyRuleRetentionDenied, KubernetesPolicyRuleHostNamespaceDenied,
		KubernetesPolicyRulePrivilegeDenied, KubernetesPolicyRuleProjectedTokenDenied,
		KubernetesPolicyRuleProjectOverrideDenied, KubernetesPolicyRuleSecurityProfileDenied,
	}, value)
}

func IsKubernetesPolicyHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func DefaultKubernetesExecutionPolicy(clusterAlias string) KubernetesExecutionPolicy {
	// The default stays fail-closed until an administrator explicitly declares
	// NetworkPolicy enforcement for the configured cluster.
	return KubernetesExecutionPolicy{ClusterAlias: strings.TrimSpace(clusterAlias), NetworkPolicyEnforcement: KubernetesNetworkPolicyEnforcementUnsupported}
}

func (p *KubernetesExecutionPolicy) Canonicalize() error {
	p.ClusterAlias = strings.TrimSpace(p.ClusterAlias)
	p.AllowedNamespaces = canonicalKubernetesStringSet(p.AllowedNamespaces, false)
	p.AllowedImages = canonicalKubernetesStringSet(p.AllowedImages, false)
	p.AllowedServiceAccounts = canonicalKubernetesStringSet(p.AllowedServiceAccounts, false)
	p.AllowedRuntimeClasses = canonicalKubernetesStringSet(p.AllowedRuntimeClasses, true)
	p.AllowedNetworkProfiles = canonicalKubernetesStringSet(p.AllowedNetworkProfiles, false)
	p.AllowedVolumeTypes = canonicalKubernetesVolumeSet(p.AllowedVolumeTypes)
	p.RuntimeClass = strings.TrimSpace(p.RuntimeClass)
	p.NetworkProfile = strings.TrimSpace(p.NetworkProfile)
	p.NetworkPolicyEnforcement = KubernetesNetworkPolicyEnforcement(strings.TrimSpace(string(p.NetworkPolicyEnforcement)))
	if err := p.Validate(); err != nil {
		return err
	}
	canonical := struct {
		ClusterAlias             string                             `json:"cluster_alias"`
		AllowedNamespaces        []string                           `json:"allowed_namespaces"`
		AllowedImages            []string                           `json:"allowed_images"`
		AllowedServiceAccounts   []string                           `json:"allowed_service_accounts"`
		AllowedRuntimeClasses    []string                           `json:"allowed_runtime_classes"`
		RuntimeClass             string                             `json:"runtime_class"`
		AllowedVolumeTypes       []KubernetesVolumeType             `json:"allowed_volume_types"`
		AllowedNetworkProfiles   []string                           `json:"allowed_network_profiles"`
		NetworkProfile           string                             `json:"network_profile"`
		NetworkPolicyEnforcement KubernetesNetworkPolicyEnforcement `json:"network_policy_enforcement"`
		Resources                KubernetesExecutionResources       `json:"resources"`
		TerminalRetentionSeconds int                                `json:"terminal_retention_seconds"`
	}{
		p.ClusterAlias, p.AllowedNamespaces, p.AllowedImages, p.AllowedServiceAccounts,
		p.AllowedRuntimeClasses, p.RuntimeClass, p.AllowedVolumeTypes,
		p.AllowedNetworkProfiles, p.NetworkProfile, p.NetworkPolicyEnforcement, p.Resources, p.TerminalRetentionSeconds,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	p.Hash = hex.EncodeToString(digest[:])
	return nil
}

func (p KubernetesExecutionPolicy) Validate() error {
	if err := ValidateKubernetesClusterAlias(p.ClusterAlias); err != nil {
		return err
	}
	if p.Revision < 0 {
		return errors.New("Kubernetes policy revision must not be negative")
	}
	if len(p.AllowedNamespaces) == 0 || len(p.AllowedImages) == 0 || len(p.AllowedServiceAccounts) == 0 ||
		len(p.AllowedRuntimeClasses) == 0 || len(p.AllowedVolumeTypes) == 0 || len(p.AllowedNetworkProfiles) == 0 {
		return errors.New("Kubernetes policy requires explicit non-empty allow-lists")
	}
	for _, namespace := range p.AllowedNamespaces {
		if !safeDNSLabel(namespace) {
			return errors.New("Kubernetes policy contains an invalid namespace allow-list entry")
		}
	}
	for _, image := range p.AllowedImages {
		normalized, err := NormalizeExecutorImage(image)
		if err != nil || normalized == nil || *normalized != image || !immutableSHA256Image(image) {
			return errors.New("Kubernetes policy contains an invalid immutable image allow-list entry")
		}
	}
	for _, serviceAccount := range p.AllowedServiceAccounts {
		if !safeDNSLabel(serviceAccount) || serviceAccount == "default" {
			return errors.New("Kubernetes policy contains an invalid workload service-account allow-list entry")
		}
	}
	for _, runtimeClass := range p.AllowedRuntimeClasses {
		if runtimeClass != "" && !safeDNSLabel(runtimeClass) {
			return errors.New("Kubernetes policy contains an invalid runtime-class allow-list entry")
		}
	}
	if !slices.Contains(p.AllowedRuntimeClasses, p.RuntimeClass) {
		return errors.New("Kubernetes policy runtime class must be present in its allow-list")
	}
	for _, volumeType := range p.AllowedVolumeTypes {
		if volumeType != KubernetesVolumeEmptyDir && volumeType != KubernetesVolumeSecret {
			return errors.New("Kubernetes policy contains an unsupported volume allow-list entry")
		}
	}
	if !slices.Contains(p.AllowedVolumeTypes, KubernetesVolumeEmptyDir) || !slices.Contains(p.AllowedVolumeTypes, KubernetesVolumeSecret) {
		return errors.New("Kubernetes policy must explicitly allow the executor emptyDir and Secret volumes")
	}
	for _, profile := range p.AllowedNetworkProfiles {
		if !safeDNSLabel(profile) {
			return errors.New("Kubernetes policy contains an invalid network-profile allow-list entry")
		}
	}
	if !slices.Contains(p.AllowedNetworkProfiles, p.NetworkProfile) {
		return errors.New("Kubernetes policy network profile must be present in its allow-list")
	}
	if p.NetworkProfile != string(KubernetesNetworkProfileDenyAll) {
		return errors.New("Kubernetes policy contains an unsupported network profile")
	}
	if p.NetworkPolicyEnforcement != KubernetesNetworkPolicyEnforcementNetworkPolicy {
		return errors.New("Kubernetes policy requires declared NetworkPolicy enforcement")
	}
	if err := p.Resources.Validate(); err != nil {
		return err
	}
	if p.TerminalRetentionSeconds < 60 || p.TerminalRetentionSeconds > 7*24*60*60 {
		return errors.New("Kubernetes policy terminal retention must be between 1 minute and 7 days")
	}
	return nil
}

func (r KubernetesExecutionResources) Validate() error {
	if r.CPURequestMilli <= 0 || r.CPULimitMilli < r.CPURequestMilli ||
		r.MemoryRequestBytes <= 0 || r.MemoryLimitBytes < r.MemoryRequestBytes ||
		r.EphemeralStorageRequestBytes <= 0 || r.EphemeralStorageLimitBytes < r.EphemeralStorageRequestBytes {
		return errors.New("Kubernetes policy requires positive bounded CPU, memory, and ephemeral-storage resources")
	}
	return nil
}

func (p KubernetesExecutionPolicy) Acknowledgement() KubernetesExecutionPolicyAck {
	return KubernetesExecutionPolicyAck{ClusterAlias: p.ClusterAlias, Revision: p.Revision, Hash: p.Hash}
}

func (p KubernetesExecutionPolicy) MatchesAck(ack KubernetesExecutionPolicyAck) bool {
	return p.ClusterAlias == ack.ClusterAlias && p.Revision == ack.Revision && p.Hash != "" && p.Hash == ack.Hash
}

func (p KubernetesExecutionPolicy) Test(request KubernetesExecutionPolicyTestRequest) KubernetesExecutionPolicyTestResult {
	// Report the network contract first: an operator must never mistake a
	// missing enforcement declaration for a generic resource validation error.
	if request.NetworkProfile == string(KubernetesNetworkProfileDenyAll) &&
		p.NetworkPolicyEnforcement != KubernetesNetworkPolicyEnforcementNetworkPolicy {
		return KubernetesExecutionPolicyTestResult{Rule: KubernetesPolicyRuleNetworkProfileUnsupported}
	}
	if err := p.ValidateExecution(request); err == nil {
		return KubernetesExecutionPolicyTestResult{Allowed: true}
	} else {
		var violation KubernetesPolicyViolationError
		if errors.As(err, &violation) {
			return KubernetesExecutionPolicyTestResult{Rule: violation.Rule}
		}
		return KubernetesExecutionPolicyTestResult{Rule: KubernetesPolicyRuleResourceDenied}
	}
}

func (p KubernetesExecutionPolicy) ValidateExecution(request KubernetesExecutionPolicyTestRequest) error {
	if request.NetworkProfile == string(KubernetesNetworkProfileDenyAll) &&
		p.NetworkPolicyEnforcement != KubernetesNetworkPolicyEnforcementNetworkPolicy {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleNetworkProfileUnsupported}
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if request.ClusterAlias != p.ClusterAlias {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleClusterDenied}
	}
	if !slices.Contains(p.AllowedNamespaces, request.Namespace) {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleNamespaceDenied}
	}
	if !slices.Contains(p.AllowedImages, request.TaskImage) || !slices.Contains(p.AllowedImages, request.HelperImage) ||
		!immutableSHA256Image(request.TaskImage) || !immutableSHA256Image(request.HelperImage) {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleImageDenied}
	}
	if !slices.Contains(p.AllowedServiceAccounts, request.ServiceAccount) {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleServiceAccountDenied}
	}
	if request.RuntimeClass != p.RuntimeClass || !slices.Contains(p.AllowedRuntimeClasses, request.RuntimeClass) {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleRuntimeClassDenied}
	}
	if request.NetworkProfile != p.NetworkProfile || !slices.Contains(p.AllowedNetworkProfiles, request.NetworkProfile) {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleNetworkProfileDenied}
	}
	if request.Resources != p.Resources {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleResourceDenied}
	}
	if request.TerminalRetentionSeconds != p.TerminalRetentionSeconds {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleRetentionDenied}
	}
	if request.HasHostNetwork || request.HasHostPID || request.HasHostIPC || request.HasHostPath {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleHostNamespaceDenied}
	}
	if request.HasProjectedServiceToken {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleProjectedTokenDenied}
	}
	if request.HasPrivileged {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRulePrivilegeDenied}
	}
	if request.HasProjectOverride {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleProjectOverrideDenied}
	}
	if !request.HasRestrictedSecurityProfile {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleSecurityProfileDenied}
	}
	if len(request.VolumeTypes) == 0 {
		return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleVolumeDenied}
	}
	for _, volumeType := range request.VolumeTypes {
		if !slices.Contains(p.AllowedVolumeTypes, volumeType) {
			return KubernetesPolicyViolationError{Rule: KubernetesPolicyRuleVolumeDenied}
		}
	}
	return nil
}

func (p KubernetesExecutionPolicy) TerminalRetentionDuration() time.Duration {
	return time.Duration(p.TerminalRetentionSeconds) * time.Second
}

// ValidateKubernetesClusterAlias is shared by provider configuration and
// persisted runner metadata so startup cannot accept an alias progress rejects.
func ValidateKubernetesClusterAlias(value string) error {
	if !safeExecutorIdentity(value, 128, false) {
		return fmt.Errorf("Kubernetes cluster alias must contain 1 to 128 safe identity bytes")
	}
	return nil
}

func canonicalKubernetesStringSet(values []string, keepEmpty bool) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" && !keepEmpty {
			continue
		}
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func canonicalKubernetesVolumeSet(values []KubernetesVolumeType) []KubernetesVolumeType {
	seen := make(map[KubernetesVolumeType]struct{}, len(values))
	for _, value := range values {
		value = KubernetesVolumeType(strings.TrimSpace(string(value)))
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]KubernetesVolumeType, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

type KubernetesExecutionPolicyRepository interface {
	GetKubernetesExecutionPolicy(clusterAlias string) (KubernetesExecutionPolicy, error)
	SaveKubernetesExecutionPolicy(policy KubernetesExecutionPolicy, expectedRevision int) (KubernetesExecutionPolicy, error)
}
