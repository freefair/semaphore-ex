package runners

import (
	"errors"
	"sync/atomic"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
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
