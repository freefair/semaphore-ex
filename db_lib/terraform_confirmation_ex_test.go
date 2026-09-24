//go:build !windows

package db_lib

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type terraformConfirmationLogger struct {
	task_logger.NopLogger
	listeners []task_logger.StatusListener
	statuses  []task_logger.TaskStatus
	decision  task_logger.TaskStatus
}

func (l *terraformConfirmationLogger) AddStatusListener(listener task_logger.StatusListener) {
	l.listeners = append(l.listeners, listener)
}

func (l *terraformConfirmationLogger) SetStatus(status task_logger.TaskStatus) {
	l.statuses = append(l.statuses, status)
	for _, listener := range l.listeners {
		listener(status)
	}
	if status == task_logger.TaskWaitingConfirmation {
		l.SetStatus(l.decision)
	}
}

func TestTerraformDirectRunConfirmation(t *testing.T) {
	for _, test := range []struct {
		name           string
		params         db.TerraformTaskParams
		template       db.TerraformTemplateParams
		decision       task_logger.TaskStatus
		waits, applies bool
	}{
		{name: "plan only", params: db.TerraformTaskParams{Plan: true}},
		{name: "plan overrides automatic apply", params: db.TerraformTaskParams{Plan: true}, template: db.TerraformTemplateParams{AutoApprove: true}},
		{name: "template auto approval", template: db.TerraformTemplateParams{AutoApprove: true}, applies: true},
		{name: "per run auto approval", params: db.TerraformTaskParams{AutoApprove: true}, template: db.TerraformTemplateParams{AllowAutoApprove: true}, applies: true},
		{name: "manual confirmation", decision: task_logger.TaskConfirmed, waits: true, applies: true},
		{name: "manual rejection", decision: task_logger.TaskRejected, waits: true},
		{name: "manual stop", decision: task_logger.TaskStoppingStatus, waits: true},
		{name: "template prohibits per run auto approval", params: db.TerraformTaskParams{AutoApprove: true}, decision: task_logger.TaskRejected, waits: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, dir := terraformLockingFixture(t, "terraform", `printf '%s\n' "$1" >> stages`)
			logger := &terraformConfirmationLogger{decision: test.decision}
			app.SetLogger(logger)
			require.NoError(t, app.Run(LocalAppRunningArgs{TaskParams: &test.params, TemplateParams: &test.template}))
			stages, err := os.ReadFile(filepath.Join(dir, "stages"))
			require.NoError(t, err)
			expected := "plan\n"
			if test.applies {
				expected += "apply\n"
			}
			assert.Equal(t, expected, string(stages))
			assert.Equal(t, test.waits, slices.Contains(logger.statuses, task_logger.TaskWaitingConfirmation))
			if test.decision == task_logger.TaskRejected {
				assert.Equal(t, task_logger.TaskFailStatus, logger.statuses[len(logger.statuses)-1])
			}
		})
	}
}
