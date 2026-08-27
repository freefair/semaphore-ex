package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRunnerRegistrationPolicyMatrix(t *testing.T) {
	valid := RunnerSecurityReport{
		RegistrationKind: RunnerRegistrationOneTime,
		TransportTrust:   RunnerTransportSystemCA,
		RunnerVersion:    MinSecureRunnerVersion,
		ProtocolVersion:  CurrentSecureRunnerProtocol,
		ExecutorType:     RunnerExecutorDocker,
		PublicKey:        "public-key",
	}
	tests := []struct {
		name     string
		mutate   func(*RunnerSecurityReport)
		rejected string
	}{
		{name: "compliant"},
		{name: "shared token", mutate: func(r *RunnerSecurityReport) { r.RegistrationKind = RunnerRegistrationShared }, rejected: "shared registration token"},
		{name: "plaintext", mutate: func(r *RunnerSecurityReport) { r.TransportTrust = RunnerTransportPlaintext }, rejected: "server identity not verified"},
		{name: "skip verify", mutate: func(r *RunnerSecurityReport) { r.TransportTrust = RunnerTransportInsecure }, rejected: "server identity not verified"},
		{name: "old version", mutate: func(r *RunnerSecurityReport) { r.RunnerVersion = "2.19.99" }, rejected: "runner version unsupported"},
		{name: "future protocol", mutate: func(r *RunnerSecurityReport) { r.ProtocolVersion++ }, rejected: "security protocol unsupported"},
		{name: "missing executor", mutate: func(r *RunnerSecurityReport) { r.ExecutorType = "" }, rejected: "executor capability missing"},
		{name: "missing identity", mutate: func(r *RunnerSecurityReport) { r.PublicKey = "" }, rejected: "runner identity missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := valid
			if tt.mutate != nil {
				tt.mutate(&report)
			}
			decision := EvaluateRunnerRegistrationPolicy(RunnerRegistrationSecure, report)
			if tt.rejected == "" {
				assert.True(t, decision.Compliant)
				return
			}
			assert.False(t, decision.Compliant)
			assert.Contains(t, decision.RejectedCriteria, tt.rejected)
			require.NotEmpty(t, decision.Reason)
			require.NotEmpty(t, decision.Remediation)
			if report.PublicKey != "" {
				assert.NotContains(t, decision.Reason, report.PublicKey)
			}
		})
	}
}

func TestStandardRunnerPolicyRemainsBackwardCompatible(t *testing.T) {
	decision := EvaluateRunnerRegistrationPolicy(RunnerRegistrationStandard, RunnerSecurityReport{})
	assert.True(t, decision.Compliant)
	assert.Empty(t, decision.RejectedCriteria)
}

func TestSecureRunnerVersionBoundary(t *testing.T) {
	tests := []struct {
		version   string
		supported bool
	}{
		{version: "2.19.99"},
		{version: "2.20.0-beta.1"},
		{version: "2.20.0", supported: true},
		{version: "v2.20.0", supported: true},
		{version: "2.20.0+build.1", supported: true},
		{version: "2.21.0-beta.1", supported: true},
		{version: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			assert.Equal(t, tt.supported, secureRunnerVersionSupported(tt.version))
		})
	}
}

func TestRunnerRegistrationPolicyDowngradeMatrix(t *testing.T) {
	registered := Runner{Token: "token", RegistrationPolicy: RunnerRegistrationSecure}
	assert.ErrorIs(t,
		ValidateRunnerRegistrationPolicyChange(registered, RunnerRegistrationStandard),
		ErrRunnerSecurityPolicyChangeRequiresReset,
	)
	assert.NoError(t, ValidateRunnerRegistrationPolicyChange(registered, RunnerRegistrationSecure))

	unregistered := Runner{RegistrationPolicy: RunnerRegistrationSecure}
	assert.NoError(t, ValidateRunnerRegistrationPolicyChange(unregistered, RunnerRegistrationStandard))
	assert.Error(t, ValidateRunnerRegistrationPolicyChange(unregistered, "best-effort"))
}
