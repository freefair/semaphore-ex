package dreddhooks

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/db"

	"github.com/stretchr/testify/require"
	"testing"
)

func TestTaskGroupDreddUsesFixtureIdentityAndRevision(t *testing.T) {
	uri, payload, err := TaskGroupRequest(db.TaskGroup{ID: 73, ProjectID: 29, Revision: 4}, "PUT", false)
	require.NoError(t, err)
	require.Equal(t, "/api/project/29/task_groups/73", uri)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &body))
	require.Equal(t, float64(4), body["revision"])
	require.Equal(t, []any{}, body["runner_ids"])
	require.Equal(t, []any{}, body["shared_project_ids"])
}
