package runners

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerPolicyRejectionExecutorReportsStableRuleOnce(t *testing.T) {
	executor, ok := newDockerPolicyRejectionExecutor(db.DockerPolicyViolationError{Rule: db.DockerPolicyRuleImageDenied})
	require.True(t, ok)
	logger := &policyRejectionLogger{}
	executor.SetLogger(logger)
	require.ErrorContains(t, executor.Run("", nil, ""), db.DockerPolicyRuleImageDenied)
	require.ErrorContains(t, executor.Run("", nil, ""), db.DockerPolicyRuleImageDenied)
	assert.Equal(t, []string{db.DockerPolicyRuleImageDenied}, logger.logs)
	assert.False(t, executor.Async())
}

func TestDockerPolicyRejectionExecutorLeavesNonPolicyErrorsUntouched(t *testing.T) {
	_, ok := newDockerPolicyRejectionExecutor(assert.AnError)
	assert.False(t, ok)
}

type policyRejectionLogger struct {
	task_logger.NopLogger
	logs []string
}

func (l *policyRejectionLogger) Log(message string) { l.logs = append(l.logs, message) }
