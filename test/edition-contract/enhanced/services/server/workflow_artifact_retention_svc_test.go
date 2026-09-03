package server

import (
	"context"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowArtifactRetentionGovernancePublishesNarrowingPoliciesWithCAS(t *testing.T) {
	fixture := newWorkflowServiceFixture(t)
	t.Cleanup(fixture.store.Close)
	service := NewWorkflowArtifactRetentionGovernanceService(workflowSQL.NewWorkflowFileArtifactStore(fixture.store.GetConnection()))

	state, err := service.GetWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionGlobal, nil)
	require.NoError(t, err)
	assert.Nil(t, state.GlobalPolicy)
	assert.Equal(t, db.DefaultWorkflowArtifactRetentionSnapshot(), state.Effective)

	globalUpdate := pro_interfaces.WorkflowArtifactRetentionUpdate{
		ExpectedRevision: 0, RetentionSeconds: 14 * 24 * 60 * 60,
		MaxArtifactBytes: 32 * 1024 * 1024, MaxRunBytes: 128 * 1024 * 1024,
	}
	state, err = service.PublishWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionGlobal, nil, globalUpdate, fixture.user.ID)
	require.NoError(t, err)
	require.NotNil(t, state.GlobalPolicy)
	assert.Equal(t, 1, state.GlobalPolicy.Revision)
	assert.Equal(t, fixture.user.ID, state.GlobalPolicy.CreatedByUserID)

	projectID := fixture.projectID
	projectUpdate := pro_interfaces.WorkflowArtifactRetentionUpdate{
		ExpectedRevision: 0, RetentionSeconds: 7 * 24 * 60 * 60,
		MaxArtifactBytes: 16 * 1024 * 1024, MaxRunBytes: 64 * 1024 * 1024,
	}
	state, err = service.PublishWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionProject, &projectID, projectUpdate, fixture.user.ID)
	require.NoError(t, err)
	require.NotNil(t, state.ProjectPolicy)
	assert.Equal(t, 1, state.Effective.GlobalRevision)
	assert.Equal(t, 1, state.Effective.ProjectRevision)
	assert.Equal(t, projectUpdate.RetentionSeconds, state.Effective.RetentionSeconds)

	_, err = service.PublishWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionProject, &projectID, projectUpdate, fixture.user.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)
	widening := projectUpdate
	widening.ExpectedRevision = 1
	widening.RetentionSeconds = globalUpdate.RetentionSeconds + 1
	_, err = service.PublishWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionProject, &projectID, widening, fixture.user.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)

	globalBelowProject := globalUpdate
	globalBelowProject.ExpectedRevision = 1
	globalBelowProject.RetentionSeconds = projectUpdate.RetentionSeconds - 1
	_, err = service.PublishWorkflowArtifactRetention(context.Background(), db.WorkflowArtifactRetentionGlobal, nil, globalBelowProject, fixture.user.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowArtifactRetentionConflict)
}
