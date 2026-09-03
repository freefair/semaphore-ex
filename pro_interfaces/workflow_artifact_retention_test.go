package pro_interfaces

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowArtifactRetentionUpdateValidatesHardBounds(t *testing.T) {
	update := WorkflowArtifactRetentionUpdate{
		ExpectedRevision: 0,
		RetentionSeconds: db.DefaultWorkflowArtifactRetentionSeconds,
		MaxArtifactBytes: db.MaxWorkflowFileArtifactBytes,
		MaxRunBytes:      db.MaxWorkflowFileArtifactRunBytes,
	}
	require.NoError(t, update.Validate())
	update.MaxRunBytes = update.MaxArtifactBytes - 1
	assert.Error(t, update.Validate())
	update.MaxRunBytes = db.MaxWorkflowFileArtifactRunBytes
	update.RetentionSeconds = db.MinWorkflowArtifactRetentionSeconds - 1
	assert.Error(t, update.Validate())
}

func TestWorkflowArtifactRetentionStateBindsScopeAndEffectiveRevisions(t *testing.T) {
	projectID := 7
	global := db.WorkflowArtifactRetentionPolicy{
		ID: 1, Scope: db.WorkflowArtifactRetentionGlobal, Revision: 2,
		RetentionSeconds: db.DefaultWorkflowArtifactRetentionSeconds,
		MaxArtifactBytes: db.MaxWorkflowFileArtifactBytes, MaxRunBytes: db.MaxWorkflowFileArtifactRunBytes,
		CreatedByUserID: 3, CreatedAt: time.Unix(1, 0).UTC(),
	}
	project := db.WorkflowArtifactRetentionPolicy{
		ID: 2, Scope: db.WorkflowArtifactRetentionProject, ProjectID: &projectID, Revision: 4,
		RetentionSeconds: db.MinWorkflowArtifactRetentionSeconds,
		MaxArtifactBytes: 1024, MaxRunBytes: 2048,
		CreatedByUserID: 5, CreatedAt: time.Unix(2, 0).UTC(),
	}
	effective, err := db.ResolveWorkflowArtifactRetention(global, &project)
	require.NoError(t, err)
	state := WorkflowArtifactRetentionState{GlobalPolicy: &global, ProjectPolicy: &project, Effective: effective}
	require.NoError(t, state.Validate(db.WorkflowArtifactRetentionProject, &projectID))
	assert.Error(t, state.Validate(db.WorkflowArtifactRetentionGlobal, nil))
	state.Effective.MaxRunBytes++
	assert.Error(t, state.Validate(db.WorkflowArtifactRetentionProject, &projectID))
}
