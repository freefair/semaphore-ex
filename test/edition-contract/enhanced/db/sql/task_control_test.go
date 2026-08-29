package sql

import (
	"testing"
	"time"

	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskControlStoreFencesExpiredOwnerAndRejectsStaleRelease(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	execution := pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 2, StableID: "runner-job-17-2"}

	first, claimed, err := store.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 1, first.FencingToken)

	blocked, claimed, err := store.ClaimTaskControl(17, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Equal(t, first.OwnerBootID, blocked.OwnerBootID)

	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17)
	require.NoError(t, err)
	second, claimed, err := store.ClaimTaskControl(17, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 2, second.FencingToken)

	current, err := store.IsCurrentTaskControlLease(first)
	require.NoError(t, err)
	assert.False(t, current)
	released, err := store.ReleaseTaskControlLease(first)
	require.NoError(t, err)
	assert.False(t, released)
}

func TestTaskControlStoreRejectsExecutionIdentityChange(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	first := pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 2, StableID: "runner-job-17-2"}
	_, claimed, err := store.ClaimTaskControl(17, first, "boot-a", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)

	_, claimed, err = store.ClaimTaskControl(17, pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 3, StableID: "runner-job-17-3"}, "boot-a", time.Minute)
	assert.False(t, claimed)
	assert.ErrorContains(t, err, "execution identity changed")
}
