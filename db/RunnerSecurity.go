package db

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrRunnerSecurityPolicyChangeRequiresReset = errors.New("runner registration policy can only change while the runner is unregistered")

type RunnerSecurityViolationError struct {
	Decision RunnerSecurityDecision
}

func (err RunnerSecurityViolationError) Error() string {
	return err.Decision.Reason
}

type RunnerRegistrationPolicy string
type RunnerRegistrationKind string
type RunnerTransportTrust string

const (
	RunnerRegistrationStandard RunnerRegistrationPolicy = "standard"
	RunnerRegistrationSecure   RunnerRegistrationPolicy = "secure"

	RunnerRegistrationShared            RunnerRegistrationKind = "shared"
	RunnerRegistrationOneTime           RunnerRegistrationKind = "one_time"
	RunnerRegistrationTokenPrefix                              = "smrs_"
	RunnerSecureRegistrationTokenPrefix                        = RunnerRegistrationTokenPrefix + "secure_"

	RunnerTransportPlaintext RunnerTransportTrust = "plaintext"
	RunnerTransportInsecure  RunnerTransportTrust = "insecure_skip_verify"
	RunnerTransportSystemCA  RunnerTransportTrust = "system_ca"
	RunnerTransportCustomCA  RunnerTransportTrust = "custom_ca"

	MinSecureRunnerVersion      = "2.20.0"
	CurrentSecureRunnerProtocol = 1
)

// RunnerSecurityReport contains non-secret, bounded facts declared by the runner.
type RunnerSecurityReport struct {
	RegistrationKind RunnerRegistrationKind `json:"registration_kind"`
	TransportTrust   RunnerTransportTrust   `json:"transport_trust"`
	RunnerVersion    string                 `json:"runner_version"`
	ProtocolVersion  int                    `json:"protocol_version"`
	ExecutorType     RunnerExecutorType     `json:"executor_type"`
	PublicKey        string                 `json:"public_key,omitempty"`
}

// RunnerSecurityDecision is safe for APIs and logs; it never echoes identity material.
type RunnerSecurityDecision struct {
	Policy           RunnerRegistrationPolicy `json:"policy"`
	Compliant        bool                     `json:"compliant"`
	Reason           string                   `json:"reason"`
	Remediation      string                   `json:"remediation,omitempty"`
	AcceptedCriteria []string                 `json:"accepted_criteria"`
	RejectedCriteria []string                 `json:"rejected_criteria"`
}

func NormalizeRunnerRegistrationPolicy(policy RunnerRegistrationPolicy) (RunnerRegistrationPolicy, error) {
	if policy == "" {
		return RunnerRegistrationStandard, nil
	}
	if policy != RunnerRegistrationStandard && policy != RunnerRegistrationSecure {
		return "", fmt.Errorf("unknown runner registration policy %q", policy)
	}
	return policy, nil
}

func ValidateRunnerRegistrationPolicyChange(current Runner, requested RunnerRegistrationPolicy) error {
	normalized, err := NormalizeRunnerRegistrationPolicy(requested)
	if err != nil {
		return err
	}
	currentPolicy, err := NormalizeRunnerRegistrationPolicy(current.RegistrationPolicy)
	if err != nil {
		return err
	}
	if current.IsRegistered() && normalized != currentPolicy {
		return ErrRunnerSecurityPolicyChangeRequiresReset
	}
	return nil
}

// EvaluateRunnerRegistrationPolicy applies one named policy without fallback.
func EvaluateRunnerRegistrationPolicy(
	policy RunnerRegistrationPolicy,
	report RunnerSecurityReport,
) RunnerSecurityDecision {
	decision := RunnerSecurityDecision{
		Policy:           policy,
		AcceptedCriteria: make([]string, 0, 5),
		RejectedCriteria: make([]string, 0, 5),
	}
	if policy == "" {
		policy = RunnerRegistrationStandard
		decision.Policy = policy
	}
	if policy == RunnerRegistrationStandard {
		decision.Compliant = true
		decision.Reason = "standard registration policy accepted"
		return decision
	}
	if policy != RunnerRegistrationSecure {
		decision.Reason = "unknown registration policy"
		decision.Remediation = "Select the standard or secure registration policy."
		decision.RejectedCriteria = append(decision.RejectedCriteria, "policy unsupported")
		return decision
	}

	type requirement struct {
		ok          bool
		accepted    string
		rejected    string
		reason      string
		remediation string
	}
	requirements := []requirement{
		{report.RegistrationKind == RunnerRegistrationOneTime,
			"one-time registration", "shared registration token", "secure mode requires one-time registration",
			"Issue a new one-time registration token for this runner."},
		{report.TransportTrust == RunnerTransportSystemCA || report.TransportTrust == RunnerTransportCustomCA,
			"server identity verified", "server identity not verified", "secure mode requires verified TLS server identity",
			"Use HTTPS with system trust or a configured CA and disable skip-TLS-verification."},
		{secureRunnerVersionSupported(report.RunnerVersion),
			"runner version supported", "runner version unsupported", "runner version is not supported by secure mode",
			"Upgrade the runner to version " + MinSecureRunnerVersion + " or newer."},
		{report.ProtocolVersion == CurrentSecureRunnerProtocol,
			"security protocol supported", "security protocol unsupported", "runner security protocol is not supported",
			fmt.Sprintf("Use runner security protocol version %d.", CurrentSecureRunnerProtocol)},
		{report.ExecutorType == RunnerExecutorLocal || report.ExecutorType == RunnerExecutorDocker || report.ExecutorType == RunnerExecutorK8s,
			"executor capability declared", "executor capability missing", "secure mode requires an explicit executor capability",
			"Configure and declare the runner executor type."},
		{strings.TrimSpace(report.PublicKey) != "",
			"runner identity declared", "runner identity missing", "secure mode requires a runner public identity",
			"Generate a runner identity and register its public key."},
	}
	for _, current := range requirements {
		if current.ok {
			decision.AcceptedCriteria = append(decision.AcceptedCriteria, current.accepted)
			continue
		}
		decision.RejectedCriteria = append(decision.RejectedCriteria, current.rejected)
		if decision.Reason == "" {
			decision.Reason = current.reason
			decision.Remediation = current.remediation
		}
	}
	decision.Compliant = len(decision.RejectedCriteria) == 0
	if decision.Compliant {
		decision.Reason = "secure registration requirements satisfied"
	}
	return decision
}

func secureRunnerVersionSupported(value string) bool {
	parse := func(version string) ([3]int, bool, bool) {
		var result [3]int
		version = strings.TrimPrefix(strings.TrimSpace(version), "v")
		version = strings.SplitN(version, "+", 2)[0]
		core, prerelease, _ := strings.Cut(version, "-")
		version = core
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return result, false, false
		}
		for index := range parts {
			parsed, err := strconv.Atoi(parts[index])
			if err != nil || parsed < 0 {
				return result, false, false
			}
			result[index] = parsed
		}
		return result, prerelease != "", true
	}
	current, prerelease, ok := parse(value)
	if !ok {
		return false
	}
	minimum, _, _ := parse(MinSecureRunnerVersion)
	for index := range current {
		if current[index] != minimum[index] {
			return current[index] > minimum[index]
		}
	}
	return !prerelease
}
