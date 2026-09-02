package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowServiceBlocksRunWhenAttachedTaskIsSecurityBlocked(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "credential-revoked-before-dispatch")
	require.NoError(t, err)
	require.Len(t, fixture.enqueuer.tasks, 1)

	blockedTask := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskBlockedStatus, "A required global credential is unavailable. Update the binding or request access, then run again.")
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(blockedTask))

	blockedRun := loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunBlocked, blockedRun.Status)
	assert.Equal(t, db.WorkflowRunNodeBlocked, blockedRun.Nodes[0].Status)
	assert.Equal(t, blockedTask.Message, blockedRun.Nodes[0].Reason)
	assert.NotNil(t, blockedRun.Nodes[0].End)
	assert.Len(t, fixture.enqueuer.tasks, 1, "blocked task must not dispatch a dependent node")

	// A duplicate completion callback must not replace the terminal blocked
	// outcome or re-open the run.
	require.NoError(t, fixture.service.HandleWorkflowTaskCompletion(blockedTask))
	blockedRun = loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunBlocked, blockedRun.Status)
	assert.Equal(t, db.WorkflowRunNodeBlocked, blockedRun.Nodes[0].Status)
}

func TestWorkflowServiceReconciliationBlocksRunFromPersistedSecurityBlockedTask(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	run, err := fixture.service.StartWorkflow(fixture.workflow, &fixture.user, "credential-revoked-during-restart")
	require.NoError(t, err)
	blockedTask := finishWorkflowTask(t, fixture.store, run.Nodes[0], task_logger.TaskBlockedStatus, "A required global credential is unavailable.")

	// This models recovery after dispatch was interrupted: only the terminal task
	// row is durable when reconciliation resumes.
	require.NoError(t, fixture.service.ProgressWorkflowRun(fixture.projectID, run.ID, nil))

	blockedRun := loadWorkflowRun(t, &fixture, run.ID)
	assert.Equal(t, db.WorkflowRunBlocked, blockedRun.Status)
	assert.Equal(t, db.WorkflowRunNodeBlocked, blockedRun.Nodes[0].Status)
	assert.Equal(t, blockedTask.Message, blockedRun.Nodes[0].Reason)
	assert.NotNil(t, blockedRun.Nodes[0].End)
}
