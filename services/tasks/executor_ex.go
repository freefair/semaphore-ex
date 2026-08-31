package tasks

import (
	"context"
	"github.com/semaphoreui/semaphore/db"
)

// ExecutorMetadataProvider is an optional, additive capability implemented by
// remote executors that have a bounded runtime identity worth reporting. The
// runner never serializes executor implementation objects or task inputs.
type ExecutorMetadataProvider interface {
	ExecutorMetadata() db.RunnerExecutorMetadata
}

// RunnerIdentityConsumer receives the authenticated server-side runner ID before
// any task executor is constructed. Remote resource labels can therefore bind to
// the same runner identity used by assignment and progress APIs.
type RunnerIdentityConsumer interface {
	ApplyRunnerIdentity(int) error
}

// DockerExecutionPolicyConsumer is implemented only by Docker-backed providers.
// The job pool uses it to atomically install the central server policy before it
// accepts Docker work; other executor implementations remain unaffected.
type DockerExecutionPolicyConsumer interface {
	ApplyDockerExecutionPolicy(db.DockerExecutionPolicy) error
	DockerExecutionPolicyAcknowledgement() db.DockerExecutionPolicyAck
}

// KubernetesExecutionPolicyConsumer is implemented only by Kubernetes-backed
// providers. The job pool will install a server-owned policy before accepting
// Kubernetes work; Community and local providers remain source-compatible.
type KubernetesExecutionPolicyConsumer interface {
	ApplyKubernetesExecutionPolicy(db.KubernetesExecutionPolicy) error
	KubernetesExecutionPolicyAcknowledgement() db.KubernetesExecutionPolicyAck
}

// KubernetesReconciliationConsumer and Scanner mirror the fenced Docker
// protocol but are deliberately separate: Kubernetes scans use only
// namespaced typed client calls and return no object metadata beyond the
// bounded server contract.
type KubernetesReconciliationConsumer interface {
	ApplyKubernetesReconciliationSession(db.KubernetesReconciliationSession) error
}

type KubernetesReconciliationScanner interface {
	ScanKubernetesReconciliation(context.Context, db.KubernetesReconciliationSession) (db.KubernetesReconciliationScan, error)
}

// KubernetesReconciliationRemediator is runner-local. The server can only
// issue a closed GC command; it never receives Kubernetes credentials.
type KubernetesReconciliationRemediator interface {
	RemediateKubernetesReconciliation(context.Context, db.KubernetesReconciliationRemediationCommand) db.KubernetesReconciliationRemediationResult
}

// KubernetesTelemetryReporter exposes only an in-memory, session-fenced
// batch. Providers without Kubernetes support remain source-compatible.
type KubernetesTelemetryReporter interface {
	PendingKubernetesTelemetry() db.KubernetesTelemetryBatch
	AcknowledgeKubernetesTelemetry(db.KubernetesTelemetryAck)
}

// DockerRunnerIdentityConsumer receives the server-authenticated runner ID
// before Docker work is accepted. It is deliberately separate from the policy
// contract so non-Docker providers remain source-compatible.
type DockerRunnerIdentityConsumer interface {
	ApplyDockerRunnerIdentity(int) error
	ApplyDockerReconciliationSession(db.DockerReconciliationSession) error
	DockerReconciliationSession() db.DockerReconciliationSession
}

// DockerReconciliationScanner is implemented only by Docker providers. It
// receives server-issued tuples and returns no daemon metadata beyond the
// typed reconciliation observations accepted by the API batch boundary.
type DockerReconciliationScanner interface {
	ScanDockerReconciliation(context.Context, db.DockerReconciliationSession) ([]db.DockerReconciliationObservation, db.DockerReconciliationScanComplete, []db.DockerReconciliationOrphanCandidate, error)
}

// DockerReconciliationRemediator is intentionally runner-local: the server
// persists desired commands but never obtains Docker daemon access.
type DockerReconciliationRemediator interface {
	RemediateDockerReconciliation(context.Context, db.DockerReconciliationRemediationCommand) db.DockerReconciliationRemediationResult
}

// DockerTelemetryReporter keeps runner-local telemetry durable in memory until
// the server returns its session-fenced acknowledgement. Non-Docker providers
// do not implement it and retain their existing progress behavior.
type DockerTelemetryReporter interface {
	PendingDockerTelemetry() db.DockerTelemetryBatch
	AcknowledgeDockerTelemetry(db.DockerTelemetryAck)
}

// StopConfirmation is the only evidence the runner may use to turn a Docker
// cancellation into a terminal task result. A context cancellation or a
// successful stop request is not evidence: the daemon must confirm the
// container is no longer running.
type StopConfirmation string

const (
	StopPending     StopConfirmation = "pending"
	StopConfirmed   StopConfirmation = "confirmed"
	StopQuarantined StopConfirmation = "quarantined"
)

// ConfirmedStopper is an optional executor capability. It keeps the legacy
// Job.Kill contract intact for local and Kubernetes executors while letting a
// Docker executor prove (or explicitly quarantine) a cancellation.
type ConfirmedStopper interface {
	ConfirmStop(context.Context) StopConfirmation
}

type DockerReconciliationQuarantineReporter interface {
	DockerCancellationQuarantine() db.DockerReconciliationStopQuarantine
}
