package server

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDelayStoreContractDirectMethodsRemainDurableAndScoped(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	defer fixture.store.Close()

	delaySeconds := 60
	workflow, err := fixture.repository.CreateWorkflowTemplate(db.WorkflowTemplate{
		ProjectID: fixture.projectID, Name: "Delay store contract", DefinitionVersion: db.WorkflowDefinitionVersion,
		Nodes: []db.WorkflowNode{
			{ID: -1, Kind: db.WorkflowNodeDelayKind, DisplayName: "Wait", DelaySeconds: &delaySeconds},
			{ID: -2, TemplateID: fixture.first.ID, DisplayName: "Deploy"},
		},
		Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	})
	require.NoError(t, err)

	run, err := fixture.service.StartWorkflow(workflow, &fixture.user, "delay-store-contract")
	require.NoError(t, err)
	waitNode := workflowRunNodeNamed(t, run, "Wait")
	require.NotNil(t, waitNode)

	persisted, err := fixture.repository.GetWorkflowDelay(fixture.projectID, run.ID, waitNode.WorkflowNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowDelayWaiting, persisted.Status)
	assert.Nil(t, persisted.Resolved)
	assert.WithinDuration(t, persisted.Created.Add(time.Minute), persisted.ResumeAt, time.Second)

	createdAgain, err := fixture.repository.CreateWorkflowDelay(persisted)
	require.NoError(t, err)
	assert.Equal(t, persisted.ID, createdAgain.ID, "create remains idempotent for one immutable run node")
	assert.Equal(t, persisted.ResumeAt, createdAgain.ResumeAt)

	all, err := fixture.repository.GetWorkflowDelays(fixture.projectID, run.ID)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, persisted.ID, all[0].ID)
	_, err = fixture.repository.GetWorkflowDelay(fixture.projectID+1, run.ID, waitNode.WorkflowNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound, "project scope must not leak a delay")
	_, err = fixture.repository.GetWorkflowDelay(fixture.projectID, run.ID+1, waitNode.WorkflowNodeID)
	assert.ErrorIs(t, err, db.ErrNotFound, "run scope must not leak a delay")
	otherRunDelays, err := fixture.repository.GetWorkflowDelays(fixture.projectID, run.ID+1)
	require.NoError(t, err)
	assert.Empty(t, otherRunDelays)

	workflow.Nodes[0].DelaySeconds = workflowDelayPointer(3600)
	_, err = fixture.repository.UpdateWorkflowTemplate(workflow)
	require.NoError(t, err)
	reloaded, err := fixture.repository.GetWorkflowDelay(fixture.projectID, run.ID, waitNode.WorkflowNodeID)
	require.NoError(t, err)
	assert.Equal(t, persisted.ResumeAt, reloaded.ResumeAt, "a later workflow definition cannot change the run deadline")

	expired, err := fixture.repository.GetExpiredWorkflowDelays()
	require.NoError(t, err)
	assert.Empty(t, expired)
	_, err = fixture.store.GetConnection().Exec(
		"update project__workflow_delay set resume_at=? where id=?", time.Now().UTC().Add(-time.Second), persisted.ID,
	)
	require.NoError(t, err)
	expired, err = fixture.repository.GetExpiredWorkflowDelays()
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, persisted.ID, expired[0].ID)

	resolution := expired[0]
	resolution.Status = db.WorkflowDelaySuccess
	resolvedAt := time.Now().UTC()
	resolution.Resolved = &resolvedAt
	resolved, err := fixture.repository.ResolveWorkflowDelayIfWaiting(resolution)
	require.NoError(t, err)
	assert.True(t, resolved)
	resolved, err = fixture.repository.ResolveWorkflowDelayIfWaiting(resolution)
	require.NoError(t, err)
	assert.False(t, resolved, "resolution remains idempotent after the terminal transition")

	terminal, err := fixture.repository.GetWorkflowDelay(fixture.projectID, run.ID, waitNode.WorkflowNodeID)
	require.NoError(t, err)
	assert.Equal(t, db.WorkflowDelaySuccess, terminal.Status)
	require.NotNil(t, terminal.Resolved)
	require.NoError(t, fixture.repository.UpdateWorkflowDelay(terminal), "terminal rows support durable idempotent reads")
	mutated := terminal
	mutated.ResumeAt = mutated.ResumeAt.Add(time.Second)
	assert.ErrorContains(t, fixture.repository.UpdateWorkflowDelay(mutated), "immutable")

	expired, err = fixture.repository.GetExpiredWorkflowDelays()
	require.NoError(t, err)
	assert.Empty(t, expired)
}
