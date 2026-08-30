package tasks

import "context"

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

// ExecutorProvider is the long-lived factory that produces per-task Executors. One
// Provider is built at runner startup and owns whatever shared, expensive state its
// strategy needs (the K8s clientset for Kubernetes, the AccessKey installer for
// local, a Docker client for the future Docker provider). The JobPool holds a single
// ExecutorProvider regardless of strategy — adding a new executor type means writing
// a new Provider, not threading another field through the pool.
//
// NewExecutor receives only per-task data because everything cross-task is already
// inside the Provider. It returns an Executor or a wiring error; transient runtime
// failures are surfaced later via Executor.Run.
type ExecutorProvider interface {
	NewExecutor(
		task db.Task,
		template db.Template,
		inventory db.Inventory,
		repository db.Repository,
		environment db.Environment,
		jwt string,
	) (Executor, error)
}

// Executor encapsulates the strategy used to run a single task. The job pool dispatches
// every queued task to an Executor, which orchestrates the full lifecycle: preparation
// of the working environment, execution of the underlying tool (ansible-playbook,
// terraform, shell, ...), and cleanup of any resources it allocated.
//
// Today the only implementation is LocalExecutor, which runs the tool as a subprocess
// on the runner host. The interface exists so future implementations (e.g. a Kubernetes
// executor that runs each task in an ephemeral Pod, GitLab-runner-style) can plug into
// the same job pool without changes to TaskRunner / job_pool.
//
// Execution model:
//   - Run is the single entry point used by the job pool. Implementations are free to
//     organize the work internally; LocalExecutor calls Prepare → run-the-app → Cleanup.
//   - Prepare and Cleanup are exposed so that callers that want to observe the lifecycle
//     (status updates, phased progress reporting) can do so. Run remains the default
//     orchestrator and must call them itself when invoked directly.
//   - Kill is invoked from a different goroutine when a stop is requested by the user.
type Executor interface {
	Job

	// Prepare materializes everything required before the underlying tool can start:
	// project tmp dir, repository checkout, inventory file, installed access keys and
	// vault password files. The alias is needed for Terraform tasks (it shapes the
	// TF_HTTP_ADDRESS env var); pass "" for non-Terraform apps. Calling Prepare twice
	// on the same executor is a no-op.
	Prepare(username string, incomingVersion *string, alias string) error

	// Cleanup releases every resource Prepare allocated. It is invoked unconditionally
	// at the end of Run (via defer); implementations must tolerate being called even
	// when Prepare failed partway through.
	Cleanup()

	// SetLogger wires the per-task log sink into the executor. Called by the job
	// pool after the executor is constructed but before Run is invoked. For local
	// execution this also flows the logger into the underlying App (Ansible /
	// Terraform / shell); for K8s execution the logger captures Pod log output.
	SetLogger(logger task_logger.Logger)

	// SetStatus forwards the task status transition into the executor. Most
	// implementations simply re-dispatch to their bound Logger, but exposing it
	// on the interface lets observers downstream (TaskRunner_logging) propagate
	// status without type-asserting back to the concrete executor.
	SetStatus(status task_logger.TaskStatus)
}

// ExecutorMetadataProvider is an optional, additive capability implemented by
// remote executors that have a bounded runtime identity worth reporting. The
// runner never serializes executor implementation objects or task inputs.
type ExecutorMetadataProvider interface {
	ExecutorMetadata() db.RunnerExecutorMetadata
}

// DockerExecutionPolicyConsumer is implemented only by Docker-backed providers.
// The job pool uses it to atomically install the central server policy before it
// accepts Docker work; other executor implementations remain unaffected.
type DockerExecutionPolicyConsumer interface {
	ApplyDockerExecutionPolicy(db.DockerExecutionPolicy) error
	DockerExecutionPolicyAcknowledgement() db.DockerExecutionPolicyAck
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
