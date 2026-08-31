package runners

import (
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
)

// dockerPolicyRejectionExecutor turns a pre-create policy denial into one
// normal runner failure report. This prevents the server's starting task from
// being redelivered indefinitely while keeping raw task inputs out of logs.
type dockerPolicyRejectionExecutor struct {
	rule   string
	logger task_logger.Logger
	logged atomic.Bool
}

func newDockerPolicyRejectionExecutor(err error) (*dockerPolicyRejectionExecutor, bool) {
	var violation db.DockerPolicyViolationError
	if !errors.As(err, &violation) {
		return nil, false
	}
	return &dockerPolicyRejectionExecutor{rule: violation.Rule, logger: task_logger.NopLogger{}}, true
}

func (e *dockerPolicyRejectionExecutor) Run(string, *string, string) error {
	if e.logged.CompareAndSwap(false, true) {
		e.logger.Log(e.rule)
	}
	return db.DockerPolicyViolationError{Rule: e.rule}
}

func (e *dockerPolicyRejectionExecutor) Kill()                                 {}
func (e *dockerPolicyRejectionExecutor) IsKilled() bool                        { return false }
func (e *dockerPolicyRejectionExecutor) Async() bool                           { return false }
func (e *dockerPolicyRejectionExecutor) Prepare(string, *string, string) error { return nil }
func (e *dockerPolicyRejectionExecutor) Cleanup()                              {}
func (e *dockerPolicyRejectionExecutor) SetStatus(task_logger.TaskStatus)      {}

func (e *dockerPolicyRejectionExecutor) SetLogger(logger task_logger.Logger) {
	if logger != nil {
		e.logger = logger
	}
}

func (e *dockerPolicyRejectionExecutor) ExecutorMetadata() db.RunnerExecutorMetadata {
	return db.RunnerExecutorMetadata{ExecutorType: db.RunnerExecutorDocker, DenialRuleID: e.rule}
}

type kubernetesPolicyRejectionExecutor struct {
	rule     string
	metadata db.RunnerExecutorMetadata
	logger   task_logger.Logger
	logged   atomic.Bool
}

func newKubernetesPolicyRejectionExecutor(err error, policy db.KubernetesExecutionPolicy, cfg util.RunnerK8sConfig, image string) (*kubernetesPolicyRejectionExecutor, bool) {
	var violation db.KubernetesPolicyViolationError
	if !errors.As(err, &violation) {
		return nil, false
	}
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "semaphore"
	}
	deadline := time.Now().UTC().Add(policy.TerminalRetentionDuration())
	return &kubernetesPolicyRejectionExecutor{rule: violation.Rule, logger: task_logger.NopLogger{}, metadata: db.RunnerExecutorMetadata{
		ExecutorType: db.RunnerExecutorK8s, RequestedImage: image, ResolvedImage: image,
		K8sClusterAlias: cfg.ClusterAlias, K8sNamespace: namespace, K8sContainerName: "task",
		K8sLifecycle: "failed", K8sTerminalReason: "PolicyDenied", K8sPolicyRevision: policy.Revision,
		K8sPolicyHash: policy.Hash, K8sDenialRuleID: violation.Rule,
		K8sServiceAccount: cfg.ServiceAccount, K8sResourcePolicyID: strconv.Itoa(policy.Revision), K8sResourcePolicyHash: policy.Hash,
		K8sNetworkProfile: policy.NetworkProfile, K8sNetworkEnforcement: string(policy.NetworkPolicyEnforcement), K8sRetentionDeadline: &deadline, K8sRetentionState: "terminal",
	}}, true
}

func (e *kubernetesPolicyRejectionExecutor) Run(string, *string, string) error {
	if e.logged.CompareAndSwap(false, true) {
		e.logger.Log(e.rule)
	}
	return db.KubernetesPolicyViolationError{Rule: e.rule}
}
func (e *kubernetesPolicyRejectionExecutor) Kill()                                 {}
func (e *kubernetesPolicyRejectionExecutor) IsKilled() bool                        { return false }
func (e *kubernetesPolicyRejectionExecutor) Async() bool                           { return false }
func (e *kubernetesPolicyRejectionExecutor) Prepare(string, *string, string) error { return nil }
func (e *kubernetesPolicyRejectionExecutor) Cleanup()                              {}
func (e *kubernetesPolicyRejectionExecutor) SetStatus(task_logger.TaskStatus)      {}
func (e *kubernetesPolicyRejectionExecutor) SetLogger(logger task_logger.Logger) {
	if logger != nil {
		e.logger = logger
	}
}
func (e *kubernetesPolicyRejectionExecutor) ExecutorMetadata() db.RunnerExecutorMetadata {
	return e.metadata
}
