package runners

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

const (
	RunnerVersionHeader              = "X-Runner-Version"
	RunnerPlatformHeader             = "X-Runner-Platform"
	RunnerCurrentLoadHeader          = "X-Runner-Current-Load"
	RunnerExecutorTypeHeader         = "X-Runner-Executor-Type"
	RunnerTransportTrustHeader       = "X-Runner-Transport-Trust"
	RunnerSecurityProtocolHeader     = "X-Runner-Security-Protocol"
	RunnerDockerPolicyRevisionHeader = "X-Runner-Docker-Policy-Revision"
	RunnerDockerPolicyHashHeader     = "X-Runner-Docker-Policy-Hash"
	RunnerDockerSessionHeader        = "X-Runner-Docker-Session"
	RunnerDockerFenceHeader          = "X-Runner-Docker-Fence"

	maxRunnerReportTextBytes = 128
	maxRunnerReportedLoad    = 100_000
)

// HealthReport contains optional metadata added by health-aware runners.
// Pointers preserve compatibility with older runners that send no such headers.
type HealthReport struct {
	Version                 *string
	Platform                *string
	CurrentLoad             *int
	ExecutorType            *db.RunnerExecutorType
	TransportTrust          *db.RunnerTransportTrust
	SecurityProtocolVersion *int
	DockerPolicyAck         *db.DockerExecutionPolicyAck
}

// ParseHealthReport validates the bounded metadata attached to a runner poll.
func ParseHealthReport(header http.Header) (HealthReport, error) {
	var report HealthReport
	for name, target := range map[string]**string{
		RunnerVersionHeader:  &report.Version,
		RunnerPlatformHeader: &report.Platform,
	} {
		raw, present := header[name]
		if !present {
			continue
		}
		value := strings.TrimSpace(strings.Join(raw, ","))
		if len(value) > maxRunnerReportTextBytes {
			return HealthReport{}, fmt.Errorf("%s exceeds %d bytes", name, maxRunnerReportTextBytes)
		}
		*target = &value
	}
	if raw, present := header[RunnerCurrentLoadHeader]; present {
		value, err := strconv.Atoi(strings.TrimSpace(strings.Join(raw, ",")))
		if err != nil || value < 0 || value > maxRunnerReportedLoad {
			return HealthReport{}, fmt.Errorf("%s must be between 0 and %d", RunnerCurrentLoadHeader, maxRunnerReportedLoad)
		}
		report.CurrentLoad = &value
	}
	if raw, present := header[RunnerExecutorTypeHeader]; present {
		value, err := db.NormalizeRunnerExecutorType(db.RunnerExecutorType(
			strings.TrimSpace(strings.Join(raw, ",")),
		))
		if err != nil {
			return HealthReport{}, err
		}
		report.ExecutorType = &value
	}
	if raw, present := header[RunnerTransportTrustHeader]; present {
		value := db.RunnerTransportTrust(strings.TrimSpace(strings.Join(raw, ",")))
		switch value {
		case db.RunnerTransportPlaintext, db.RunnerTransportInsecure, db.RunnerTransportSystemCA, db.RunnerTransportCustomCA:
			report.TransportTrust = &value
		default:
			return HealthReport{}, fmt.Errorf("%s contains an unknown trust mode", RunnerTransportTrustHeader)
		}
	}
	if raw, present := header[RunnerSecurityProtocolHeader]; present {
		value, err := strconv.Atoi(strings.TrimSpace(strings.Join(raw, ",")))
		if err != nil || value < 0 || value > db.CurrentSecureRunnerProtocol {
			return HealthReport{}, fmt.Errorf("%s contains an unsupported protocol", RunnerSecurityProtocolHeader)
		}
		report.SecurityProtocolVersion = &value
	}
	policyRevision, revisionPresent := header[RunnerDockerPolicyRevisionHeader]
	policyHash, hashPresent := header[RunnerDockerPolicyHashHeader]
	if revisionPresent != hashPresent {
		return HealthReport{}, fmt.Errorf("Docker policy acknowledgement is incomplete")
	}
	if revisionPresent {
		value, err := strconv.Atoi(strings.TrimSpace(strings.Join(policyRevision, ",")))
		if err != nil || value < 0 {
			return HealthReport{}, fmt.Errorf("%s must be a non-negative integer", RunnerDockerPolicyRevisionHeader)
		}
		hash := strings.TrimSpace(strings.Join(policyHash, ","))
		if len(hash) != 64 {
			return HealthReport{}, fmt.Errorf("%s must be a SHA-256 hash", RunnerDockerPolicyHashHeader)
		}
		for _, char := range hash {
			if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
				return HealthReport{}, fmt.Errorf("%s must be a SHA-256 hash", RunnerDockerPolicyHashHeader)
			}
		}
		report.DockerPolicyAck = &db.DockerExecutionPolicyAck{Revision: value, Hash: hash}
	}
	return report, nil
}

// Apply updates only metadata present in the report.
func (report HealthReport) Apply(runner *db.Runner) {
	if report.Version != nil {
		runner.Version = *report.Version
	}
	if report.Platform != nil {
		runner.Platform = *report.Platform
	}
	if report.CurrentLoad != nil {
		runner.CurrentLoad = *report.CurrentLoad
	}
	if report.DockerPolicyAck != nil {
		runner.DockerPolicyRevision = report.DockerPolicyAck.Revision
		runner.DockerPolicyHash = report.DockerPolicyAck.Hash
	}
	if report.ExecutorType != nil {
		runner.ExecutorType = *report.ExecutorType
	}
	if report.TransportTrust != nil {
		runner.TransportTrust = *report.TransportTrust
	}
	if report.SecurityProtocolVersion != nil {
		runner.SecurityProtocolVersion = *report.SecurityProtocolVersion
	}
}
